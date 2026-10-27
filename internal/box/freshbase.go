package box

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A new branch from the default branch starts from what origin has now, not
// from whenever the main checkout was last pulled. Before the branch is
// made, berthd fetches the default branch and fast-forwards the local one to
// it, so `git diff main...HEAD` in the new worktree compares with current
// history too. It only ever fast-forwards: a main checkout with changes, or
// a local branch with commits origin doesn't have, is left as it is, and a
// fetch that fails (offline, no access) leaves everything as it was.
// "keep_main": false in a layer of the project's config turns it off.

// BaseRefresh is what keeping the default branch current did, reported in
// worktree.created as "main".
type BaseRefresh struct {
	Branch string `json:"branch"`
	// Status is "current" (already up to date), "updated" (fast-forwarded),
	// "changes" (its checkout has uncommitted changes), "ahead" (it has
	// commits origin doesn't), or "failed" (the fetch or the move failed).
	Status string `json:"status"`
	// Behind is how many commits it was behind origin.
	Behind int    `json:"behind,omitempty"`
	Error  string `json:"error,omitempty"`
}

const (
	baseFetchTimeout = 20 * time.Second
	baseMoveTimeout  = 30 * time.Second
)

// baseLocks keeps two creates in one repository from moving its default
// branch at once: they would trip over each other's index.lock.
var baseLocks sync.Map

func lockBase(repo string) func() {
	m, _ := baseLocks.LoadOrStore(repo, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// freshBase brings the default branch up to date when base is it (or names
// nothing, so the main checkout's branch, when that is it), and says what
// to branch from: base as it was, or origin's branch when the local one
// couldn't move. It returns no refresh when there was nothing to keep
// current.
func freshBase(ctx context.Context, loc Location, base string) (string, *BaseRefresh) {
	if !loc.keepMain {
		return base, nil
	}
	branch := strings.TrimPrefix(base, "origin/")
	if base == "" {
		for _, w := range loc.Worktrees {
			if w.Main {
				branch = w.Branch
			}
		}
	}
	if branch == "" || branch != originDefault(ctx, loc.Path) {
		return base, nil
	}
	repo := loc.Path
	defer lockBase(repo)()

	fetchCtx, cancel := context.WithTimeout(ctx, baseFetchTimeout)
	out, err := git(fetchCtx, "-C", repo, "fetch", "--quiet", "--no-tags", "origin", branch)
	cancel()
	if err != nil {
		return base, &BaseRefresh{Branch: branch, Status: "failed", Error: "git fetch: " + strings.TrimSpace(string(out))}
	}
	if !branchExists(ctx, repo, "refs/heads/"+branch) {
		// No local branch to keep current: start from origin's.
		return "origin/" + branch, nil
	}
	r := fastForward(ctx, loc, branch)
	if (r.Status == "changes" || r.Status == "failed") && r.Behind > 0 {
		// The local branch is only behind and stayed there; origin's has
		// all of it and more.
		return "origin/" + branch, r
	}
	if base == "" {
		base = branch
	}
	return base, r
}

// originDefault is the branch origin/HEAD points at, or main or master when
// the clone never learned it, as long as origin has it.
func originDefault(ctx context.Context, repo string) string {
	if out, err := git(ctx, "-C", repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if branchExists(ctx, repo, "refs/remotes/origin/"+b) {
			return b
		}
	}
	return ""
}

// fastForward moves the local branch to origin's when that only adds
// commits. A branch nobody has checked out moves with a compare-and-swap
// update-ref; a checked-out one with `merge --ff-only` in its checkout,
// which refuses, under git's own lock, to touch a file that was changed.
func fastForward(ctx context.Context, loc Location, branch string) *BaseRefresh {
	repo := loc.Path
	r := &BaseRefresh{Branch: branch}
	fail := func(what string, out []byte) *BaseRefresh {
		r.Status, r.Error = "failed", strings.TrimSpace(what+": "+strings.TrimSpace(string(out)))
		return r
	}
	remote, err := revParse(ctx, repo, "refs/remotes/origin/"+branch)
	if err != nil {
		return fail("no origin/"+branch, nil)
	}
	local, err := revParse(ctx, repo, "refs/heads/"+branch)
	if err != nil {
		return fail("no "+branch, nil)
	}
	if local == remote {
		r.Status = "current"
		return r
	}
	out, err := git(ctx, "-C", repo, "rev-list", "--left-right", "--count", local+"..."+remote)
	if err != nil {
		return fail("git rev-list", out)
	}
	ahead, behind, ok := aheadBehind(string(out))
	if !ok {
		return fail("git rev-list", out)
	}
	r.Behind = behind
	if ahead > 0 {
		r.Status = "ahead"
		return r
	}
	owner := ""
	if out, err := git(ctx, "-C", repo, "worktree", "list", "--porcelain"); err == nil {
		for _, w := range parseWorktrees(out, repo) {
			if w.Branch == branch {
				owner = w.Path
			}
		}
	}
	// The move runs to the end even if whoever asked goes away: a merge
	// killed halfway leaves the checkout locked.
	moveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), baseMoveTimeout)
	defer cancel()
	if owner == "" {
		if out, err := git(moveCtx, "-C", repo, "update-ref", "-m", "shipyard: fast-forward to origin/"+branch, "refs/heads/"+branch, remote, local); err != nil {
			return fail("git update-ref", out)
		}
		r.Status = "updated"
		return r
	}
	// A read that took index.lock in someone's checkout would fail their
	// own git.
	if out, err := git(ctx, "-C", owner, "--no-optional-locks", "status", "--porcelain", "--untracked-files=no"); err != nil {
		return fail("git status", out)
	} else if len(strings.TrimSpace(string(out))) > 0 {
		r.Status = "changes"
		return r
	}
	if out, err := git(ctx, "-C", owner, "symbolic-ref", "-q", "HEAD"); err != nil || strings.TrimSpace(string(out)) != "refs/heads/"+branch {
		return fail(branch+" is no longer checked out in "+owner, nil)
	}
	if strings.Contains(branch, "=") {
		// -c branch.<name>.mergeOptions= can't name it.
		return fail("can't fast-forward a branch named "+branch, nil)
	}
	// A plain fast-forward whatever the person's git config says: no hooks,
	// autostash, gc or merge options, and no overwriting an ignored file
	// (a .env) where the new commits add one.
	out, err = git(moveCtx, "-C", owner,
		"-c", "core.hooksPath=/dev/null", "-c", "gc.auto=0", "-c", "maintenance.auto=false",
		"-c", "merge.autoStash=false", "-c", "branch."+branch+".mergeOptions=",
		"merge", "--ff-only", "--no-verify-signatures", "--no-overwrite-ignore", "--no-stat", "-q", remote)
	if err != nil {
		text := string(out)
		if strings.Contains(text, "would be overwritten") || strings.Contains(text, "untracked working tree files") {
			r.Status = "changes"
			return r
		}
		// Someone else (their own pull) may have got there first.
		if _, err := git(ctx, "-C", repo, "merge-base", "--is-ancestor", remote, "refs/heads/"+branch); err == nil {
			r.Status = "updated"
			return r
		}
		return fail("git merge --ff-only", out)
	}
	if now, err := revParse(ctx, repo, "refs/heads/"+branch); err != nil || now != remote {
		return fail(fmt.Sprintf("%s didn't end up at origin/%s", branch, branch), nil)
	}
	r.Status = "updated"
	return r
}

func revParse(ctx context.Context, repo, ref string) (string, error) {
	out, err := git(ctx, "-C", repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// aheadBehind reads `rev-list --left-right --count a...b`: commits only in
// a, then only in b.
func aheadBehind(out string) (ahead, behind int, ok bool) {
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, false
	}
	a, err1 := strconv.Atoi(f[0])
	b, err2 := strconv.Atoi(f[1])
	return a, b, err1 == nil && err2 == nil
}
