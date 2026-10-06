// Package groups gives what berthd starts the groups its user joined after
// berthd started.
//
// A process's groups are fixed when it starts: they come from its parent,
// and only a login (login, sshd, su) reads the group database again. So when
// a team setup's docker step adds the box's user to the docker group,
// berthd keeps the groups it started with, and so does everything it starts:
// tmux and every terminal, services, scripts. A login shell started by
// berthd (`$SHELL -lc`) is no help, since it inherits them too, and nor is
// restarting berthd under its service manager: a systemd user unit gets the
// user manager's groups, read once when the user's first session began, and
// a launchd agent its own, so the restarted berthd lacks the group as well.
// tmux's server, which every terminal is forked from, could only take the
// group by being restarted, which would end every terminal.
//
// What does work without a password is sg(1): it is setuid, and gives a
// group its caller is listed in by the group database. So berthd compares
// the groups the database gives its user with its own, and when some are
// missing, runs each new terminal, service, script and hook through sg, one
// sg per missing group, then once more for the user's own primary group,
// so files it makes keep their usual group. Nothing that already runs is
// touched; it gets the group when it is next started, or at the next login.
package groups

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Source is where the groups come from; tests replace it.
type Source struct {
	// Database is the group IDs the user has by the group database, its
	// primary group first.
	Database func() (primary string, gids []string, err error)
	// Process is this process's own group IDs.
	Process func() ([]int, error)
	// Name is a group's name.
	Name func(gid string) (string, error)
	// SG is the sg program, or "" when there is none.
	SG func() string
	// GOOS is the system; groups only change this way on Linux.
	GOOS string
	// UID is the process's user: root needs none of this.
	UID int
}

// System reads the real group database and process.
func System() Source {
	return Source{
		Database: func() (string, []string, error) {
			u, err := user.Current()
			if err != nil {
				return "", nil, err
			}
			gids, err := u.GroupIds()
			return u.Gid, gids, err
		},
		Process: func() ([]int, error) {
			gs, err := os.Getgroups()
			return append(gs, os.Getgid()), err
		},
		Name: func(gid string) (string, error) {
			g, err := user.LookupGroupId(gid)
			if err != nil {
				return "", err
			}
			return g.Name, nil
		},
		SG: func() string {
			for _, p := range []string{"/usr/bin/sg", "/bin/sg"} {
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					return p
				}
			}
			p, _ := exec.LookPath("sg")
			return p
		},
		GOOS: runtime.GOOS,
		UID:  os.Getuid(),
	}
}

// Missing is a user's new groups, by name, and the primary group's name.
type Missing struct {
	Groups  []string
	Primary string
	SG      string
}

// Find compares the group database with the process.
func (s Source) Find() Missing {
	if s.GOOS != "linux" || s.UID == 0 {
		return Missing{}
	}
	sg := s.SG()
	if sg == "" {
		return Missing{}
	}
	primary, gids, err := s.Database()
	if err != nil {
		return Missing{}
	}
	have, err := s.Process()
	if err != nil {
		return Missing{}
	}
	got := map[string]bool{}
	for _, g := range have {
		got[strconv.Itoa(g)] = true
	}
	var out Missing
	for _, gid := range gids {
		if got[gid] || gid == primary {
			continue
		}
		name, err := s.Name(gid)
		if err != nil || !safeName(name) {
			continue
		}
		out.Groups = append(out.Groups, name)
	}
	if len(out.Groups) == 0 {
		return Missing{}
	}
	sort.Strings(out.Groups)
	name, err := s.Name(primary)
	if err != nil || !safeName(name) {
		return Missing{}
	}
	out.Primary, out.SG = name, sg
	return out
}

// safeName keeps sg's arguments to group names as useradd makes them.
func safeName(n string) bool {
	if n == "" || len(n) > 64 || strings.HasPrefix(n, "-") {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// Wrap is argv run with the missing groups: argv itself when none are.
func (m Missing) Wrap(argv []string) []string {
	if len(m.Groups) == 0 || len(argv) == 0 {
		return argv
	}
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = quote(a)
	}
	// The innermost sg gives back the user's own primary group, keeping
	// the new ones in the list; each outer sg adds one group.
	cmd := "exec " + strings.Join(quoted, " ")
	cmd = "exec " + quote(m.SG) + " " + quote(m.Primary) + " -c " + quote(cmd)
	for i := len(m.Groups) - 1; i > 0; i-- {
		cmd = "exec " + quote(m.SG) + " " + quote(m.Groups[i]) + " -c " + quote(cmd)
	}
	return []string{m.SG, m.Groups[0], "-c", cmd}
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

var (
	mu     sync.Mutex
	source = System()
	// stamp is /etc/group's size and time when cached was read: a group
	// added since is seen by the very next thing started.
	stamp  string
	cached Missing
)

// Now is the system's missing groups, read again whenever the group
// database changed.
func Now() Missing {
	mu.Lock()
	defer mu.Unlock()
	st := ""
	if fi, err := os.Stat("/etc/group"); err == nil {
		st = fi.ModTime().String() + "/" + strconv.FormatInt(fi.Size(), 10)
	}
	if st == "" || st != stamp {
		cached, stamp = source.Find(), st
	}
	return cached
}

// Forget makes the next Now read the database again.
func Forget() {
	mu.Lock()
	stamp = ""
	mu.Unlock()
}

// Use replaces the system's source, for tests; it returns the old one.
func Use(s Source) Source {
	mu.Lock()
	defer mu.Unlock()
	old := source
	source, stamp = s, ""
	return old
}

// Wrap is argv run with the groups berthd's user joined since it started.
func Wrap(argv []string) []string { return Now().Wrap(argv) }

// CommandContext is exec.CommandContext, run with the user's new groups.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	argv := Wrap(append([]string{name}, args...))
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}
