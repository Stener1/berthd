package box

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Finding the clones an engineer already has, so Team setup can use them
// rather than clone a second copy. The scan is bounded: a fixed set of
// folders under the home folder, two levels deep (one under the home
// folder's other children), a time budget and an entries budget. Of each
// git repository it meets it reads one thing, remote.origin.url from its
// .git/config, and nothing else; only the ones whose origin is a team
// project are looked at further (their branch and status), and only by
// git itself, without taking its locks.

// scanFolders are the usual homes of clones, under the home folder.
var scanFolders = []string{"code", "work", "src", "projects", "dev", "repos", "git"}

// skipFolders are never looked into.
var skipFolders = map[string]bool{"node_modules": true, ".cache": true, ".git": true}

// cloneScan finds main checkouts whose origin is one of Want.
type cloneScan struct {
	// Home is the home folder; symlinks are followed only inside it.
	Home string
	// Extra are more folders to look in, two levels deep: the parents of
	// the paths team.json gives its projects.
	Extra []string
	// Want are the repositories looked for, "owner/name" in lower case.
	Want map[string]bool
	// Budget and MaxEntries bound the scan; zero is the default.
	Budget     time.Duration
	MaxEntries int

	home     string
	deadline time.Time
	entries  int
	seen     map[string]bool
	uid      int
	found    map[string][]string
	// Truncated says a budget ran out before every folder was looked in.
	Truncated bool
}

const (
	defaultScanBudget  = 2 * time.Second
	defaultScanEntries = 20000
)

// run scans and returns the checkouts found, by repository ("owner/name"
// in lower case), as absolute paths with symlinks resolved.
func (s *cloneScan) run() map[string][]string {
	s.found = map[string][]string{}
	s.seen = map[string]bool{}
	s.uid = os.Getuid()
	if s.Budget <= 0 {
		s.Budget = defaultScanBudget
	}
	if s.MaxEntries <= 0 {
		s.MaxEntries = defaultScanEntries
	}
	s.deadline = time.Now().Add(s.Budget)
	home, err := filepath.EvalSymlinks(s.Home)
	if err != nil {
		return s.found
	}
	s.home = home
	// The usual folders and the team's own first, two levels deep; then
	// every other folder in home, one level deep (~/Downloads/acme-web).
	for _, f := range scanFolders {
		s.visit(filepath.Join(s.Home, f), 0, 2)
	}
	for _, f := range s.Extra {
		// The home folder itself is the rule below, not two levels of it.
		if r, err := filepath.EvalSymlinks(f); err == nil && r == home {
			continue
		}
		s.visit(f, 0, 2)
	}
	if ents, ok := s.readDir(s.Home); ok {
		for _, e := range ents {
			if hiddenOrSkipped(e.Name()) {
				continue
			}
			s.visit(filepath.Join(s.Home, e.Name()), 0, 1)
		}
	}
	return s.found
}

func hiddenOrSkipped(name string) bool {
	return skipFolders[name] || strings.HasPrefix(name, ".")
}

// spent says a budget ran out; it is checked before each folder.
func (s *cloneScan) spent() bool {
	if s.entries >= s.MaxEntries || time.Now().After(s.deadline) {
		s.Truncated = true
		return true
	}
	return false
}

// readDir lists a folder, counting its entries against the budget.
func (s *cloneScan) readDir(dir string) ([]os.DirEntry, bool) {
	if s.spent() {
		return nil, false
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	left := s.MaxEntries - s.entries
	ents, err := f.ReadDir(left)
	s.entries += len(ents)
	if err != nil && err != io.EOF && len(ents) == 0 {
		return nil, false
	}
	return ents, true
}

// inside reports whether p is the home folder or under it.
func (s *cloneScan) inside(p string) bool {
	return p == s.home || strings.HasPrefix(p, s.home+string(filepath.Separator))
}

// owned reports whether info belongs to the user the scan runs as.
func (s *cloneScan) owned(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == s.uid
}

// visit looks at dir, depth levels below where it started, and into it
// while depth < max. Hidden folders are skipped as entries, so a folder the
// scan starts from is looked in even when hidden: it was asked for by name.
func (s *cloneScan) visit(dir string, depth, max int) {
	if s.spent() {
		return
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return
	}
	real := dir
	if info.Mode()&os.ModeSymlink != 0 {
		// A symlink is followed only while it stays in the home folder.
		if real, err = filepath.EvalSymlinks(dir); err != nil || !s.inside(real) {
			return
		}
		if info, err = os.Stat(real); err != nil {
			return
		}
	} else if r, err := filepath.EvalSymlinks(dir); err == nil {
		real = r
	}
	if !info.IsDir() || !s.owned(info) || s.seen[real] {
		return
	}
	s.seen[real] = true
	switch kind, main := gitKind(real); kind {
	case "checkout":
		s.match(real, filepath.Join(real, ".git", "config"))
		return
	case "worktree":
		// A git worktree is not a main checkout: its main checkout is the
		// one offered, when it is in the home folder and the user's.
		if main != "" && s.inside(main) {
			if mi, err := os.Stat(main); err == nil && s.owned(mi) {
				if k, _ := gitKind(main); k == "checkout" {
					s.match(main, filepath.Join(main, ".git", "config"))
				}
			}
		}
		return
	case "bare":
		// A bare repository has no checkout to work in: recognised, and
		// not looked into.
		return
	}
	if depth >= max {
		return
	}
	ents, ok := s.readDir(real)
	if !ok {
		return
	}
	for _, e := range ents {
		if hiddenOrSkipped(e.Name()) {
			continue
		}
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		s.visit(filepath.Join(real, e.Name()), depth+1, max)
	}
}

// match records path when the origin in its git config is wanted.
func (s *cloneScan) match(path, config string) {
	slug := strings.ToLower(slugOf(originInConfig(config)))
	if slug == "" || !s.Want[slug] {
		return
	}
	for _, p := range s.found[slug] {
		if p == path {
			return
		}
	}
	s.found[slug] = append(s.found[slug], path)
}

// gitKind says what dir is to git: "checkout" (a .git folder), "worktree"
// (a .git file pointing into another repository, whose main checkout is
// main), "bare" (a bare repository), or "".
func gitKind(dir string) (kind, main string) {
	g := filepath.Join(dir, ".git")
	if info, err := os.Lstat(g); err == nil {
		if info.IsDir() {
			return "checkout", ""
		}
		if info.Mode().IsRegular() {
			return "worktree", worktreeMain(dir, g)
		}
		return "", ""
	}
	if isFile(filepath.Join(dir, "HEAD")) && isFile(filepath.Join(dir, "config")) && isDir(filepath.Join(dir, "objects")) && isDir(filepath.Join(dir, "refs")) {
		return "bare", ""
	}
	return "", ""
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// worktreeMain reads a worktree's .git file ("gitdir: <repo>/.git/worktrees/
// <name>") and its commondir, to the main checkout's folder.
func worktreeMain(dir, dotgit string) string {
	raw, err := readHead(dotgit, 4096)
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	common := gitdir
	if c, err := readHead(filepath.Join(gitdir, "commondir"), 4096); err == nil {
		common = strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		return ""
	}
	main := filepath.Dir(common)
	if r, err := filepath.EvalSymlinks(main); err == nil {
		main = r
	}
	return main
}

func readHead(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

// originInConfig reads remote.origin.url from a git config file, as
// written: url.*.insteadOf rewrites are not applied, as `git config --get
// remote.origin.url` doesn't apply them.
func originInConfig(path string) string {
	raw, err := readHead(path, 256<<10)
	if err != nil {
		return ""
	}
	inOrigin := false
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				inOrigin = false
				continue
			}
			head := strings.TrimSpace(line[1:end])
			name, sub, _ := strings.Cut(head, " ")
			inOrigin = strings.EqualFold(name, "remote") && strings.TrimSpace(sub) == `"origin"`
			// [remote.origin] is the old way to write it.
			if !inOrigin && strings.EqualFold(head, "remote.origin") {
				inOrigin = true
			}
			line = strings.TrimSpace(line[end+1:])
			if line == "" {
				continue
			}
		}
		if !inOrigin {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		return val
	}
	return ""
}
