package box

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Every Locations.List and Get describes each location with git: its
// worktrees, origin's URL and the default branch. The app, plugins, the
// MCP and the worktree watcher ask many times a second, across every
// location, and the answers almost never change. So each answer is kept
// with a stamp of the files in .git that git read to give it, and git runs
// again only once one of them has changed: a commit, a worktree added or
// removed, a lock, a remote's new URL. gitFactsMaxAge bounds what a stamp
// can't see (a global url.*.insteadOf, an included config file).
var gitFactsMaxAge = time.Minute

var gitFacts = &factCache{m: map[string]fact{}}

type factCache struct {
	mu sync.Mutex
	m  map[string]fact
}

type fact struct {
	stamp string
	at    time.Time
	val   any
}

// maxFacts keeps the cache from growing without bound on a box that adds
// and removes many repositories; it starts over when it gets there.
const maxFacts = 4096

// cachedGit is what run gives for repo, from the last time it ran if
// stamp is as it was then. run's ok=false (git failed, or was cancelled)
// is not kept, so the next call tries again. A folder with no .git of its
// own is asked every time, as before.
func cachedGit[T any](repo, kind string, stamp func(g gitDirs) string, run func() (T, bool)) T {
	g, ok := gitDirsOf(repo)
	if !ok {
		v, _ := run()
		return v
	}
	key := kind + "\x00" + repo
	// The stamp comes before git runs: a change made in between is seen
	// as a change next time, rather than the old answer kept under the
	// new stamp.
	s := stamp(g)
	gitFacts.mu.Lock()
	if f, ok := gitFacts.m[key]; ok && f.stamp == s && time.Since(f.at) < gitFactsMaxAge {
		gitFacts.mu.Unlock()
		return f.val.(T)
	}
	gitFacts.mu.Unlock()
	v, ok := run()
	if !ok {
		return v
	}
	gitFacts.mu.Lock()
	if len(gitFacts.m) >= maxFacts {
		gitFacts.m = map[string]fact{}
	}
	gitFacts.m[key] = fact{stamp: s, at: time.Now(), val: v}
	gitFacts.mu.Unlock()
	return v
}

// gitDirs are a checkout's own git directory and the one its repository
// shares between worktrees: the same folder for a main checkout, and
// .git/worktrees/<id> and .git for a linked one.
type gitDirs struct{ git, common string }

func gitDirsOf(repo string) (gitDirs, bool) {
	dot := filepath.Join(repo, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return gitDirs{}, false
	}
	if fi.IsDir() {
		return gitDirs{dot, dot}, true
	}
	b, err := os.ReadFile(dot)
	if err != nil {
		return gitDirs{}, false
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return gitDirs{}, false
	}
	gd = relativeTo(repo, strings.TrimSpace(gd))
	common := gd
	if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
		common = relativeTo(gd, strings.TrimSpace(string(c)))
	}
	return gitDirs{gd, common}, true
}

func relativeTo(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

// stampOf names one version of each file: there or not, and its inode,
// size and modification time. Git replaces refs, HEAD and config by
// renaming a new file over the old, so each change brings a new inode.
func stampOf(paths ...string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(p)
		fi, err := os.Stat(p)
		if err != nil {
			b.WriteString(" -\n")
			continue
		}
		b.WriteByte(' ')
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			b.WriteString(strconv.FormatUint(uint64(st.Dev), 10) + ":" + strconv.FormatUint(st.Ino, 10) + " ")
		}
		b.WriteString(strconv.FormatInt(fi.Size(), 10) + " " + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + "\n")
	}
	return b.String()
}

// headFiles are a HEAD file and, when it names a branch, that branch's
// loose ref: what git reads to tell where HEAD points.
func headFiles(common, head string) []string {
	files := []string{head}
	if b, err := os.ReadFile(head); err == nil {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: "); ok {
			files = append(files, filepath.Join(common, filepath.FromSlash(ref)))
		}
	}
	return files
}

// refStores are where refs live besides their loose files.
func refStores(common string) []string {
	return []string{filepath.Join(common, "packed-refs"), filepath.Join(common, "reftable", "tables.list")}
}

// worktreeStamp covers what `git worktree list --porcelain` reports: the
// worktrees, each one's HEAD and branch, and its lock.
func worktreeStamp(g gitDirs) string {
	files := append(refStores(g.common), filepath.Join(g.common, "config"), filepath.Join(g.common, "worktrees"))
	files = append(files, headFiles(g.common, filepath.Join(g.common, "HEAD"))...)
	ents, _ := os.ReadDir(filepath.Join(g.common, "worktrees"))
	for _, e := range ents {
		dir := filepath.Join(g.common, "worktrees", e.Name())
		// A reftable repository keeps a linked worktree's HEAD in a
		// reftable of its own.
		files = append(files, filepath.Join(dir, "gitdir"), filepath.Join(dir, "locked"), filepath.Join(dir, "reftable", "tables.list"))
		files = append(files, headFiles(g.common, filepath.Join(dir, "HEAD"))...)
	}
	return stampOf(files...)
}

// configStamp covers what `git remote get-url` reads: the repository's
// config and the user's.
func configStamp(g gitDirs) string {
	files := []string{filepath.Join(g.common, "config"), filepath.Join(g.git, "config.worktree")}
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".gitconfig"))
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		if home, err := os.UserHomeDir(); err == nil {
			xdg = filepath.Join(home, ".config")
		}
	}
	if xdg != "" {
		files = append(files, filepath.Join(xdg, "git", "config"))
	}
	return stampOf(files...)
}

// defaultBranchStamp covers origin's HEAD and the checkout's own.
func defaultBranchStamp(g gitDirs) string {
	files := append(refStores(g.common), filepath.Join(g.common, "refs", "remotes", "origin", "HEAD"), filepath.Join(g.git, "HEAD"), filepath.Join(g.git, "reftable", "tables.list"))
	return stampOf(files...)
}

// gitAnswered says a failed git still gave an answer worth keeping (no
// origin, a HEAD that names no branch): it ran and exited with a status,
// rather than being killed by a timeout or not starting at all.
func gitAnswered(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() >= 0
}

// worktreeList is `git worktree list --porcelain` for repo.
func worktreeList(ctx context.Context, repo string) ([]byte, error) {
	type result struct {
		out []byte
		err error
	}
	r := cachedGit(repo, "worktrees", worktreeStamp, func() (result, bool) {
		out, err := git(ctx, "-C", repo, "worktree", "list", "--porcelain")
		return result{out, err}, err == nil
	})
	return r.out, r.err
}
