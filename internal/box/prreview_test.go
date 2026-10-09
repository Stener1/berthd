package box

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

// reviewFixture is acme/shop on a box: a bare repository standing in for
// GitHub (its pull requests under refs/pull/N/head), a clone of it as the
// location "shop" whose origin says https://github.com/acme/shop.git, and
// a box with a team setup that names MAIL_API_KEY as each engineer's own.
type reviewFixture struct {
	t       *testing.T
	root    string
	bare    string
	repo    string
	marks   string
	main    string
	b       *Box
	bus     *events.Bus
	journal *events.Journal
	gh      string
}

// The default branch's .berth/config.json: what every worktree of the
// project runs, trusted on the box.
const mainConfig = `{
  "setup": "echo from-main > .setup-ran; echo \"$MAIL_API_KEY|$STRIPE_KEY|$BERTH_REVIEW\" > .setup-env",
  "archive": "echo \"$BERTH_WORKTREE_NAME\" >> \"$BERTH_ROOT_PATH/../archived\"",
  "services": [{"name": "web", "run": "yarn dev", "title": "Next.js"}]
}`

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("SHELL", "/bin/sh")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &reviewFixture{t: t, root: root, bare: filepath.Join(root, "github", "acme", "shop.git"), repo: filepath.Join(root, "code", "shop"), marks: filepath.Join(root, "marks")}
	os.MkdirAll(f.marks, 0o755)
	os.MkdirAll(filepath.Dir(f.bare), 0o755)
	runGit(t, root, "init", "-q", "--bare", "-b", "main", f.bare)
	work := filepath.Join(root, "work")
	os.MkdirAll(work, 0o755)
	runGit(t, work, "init", "-q", "-b", "main")
	f.write(work, map[string]string{".berth/config.json": mainConfig, "src/checkout.ts": "export const total = (n: number) => Math.round(n)\n", "README.md": "shop\n", ".gitignore": ".setup-*\n"})
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-q", "-m", "shop")
	runGit(t, work, "push", "-q", f.bare, "HEAD:refs/heads/main")
	f.main = runGit(t, work, "rev-parse", "HEAD")

	// The engineer's clone: origin is GitHub as written, fetched from the
	// bare repository; its hooks live in its own tree, as husky sets up.
	os.MkdirAll(filepath.Dir(f.repo), 0o755)
	runGit(t, root, "clone", "-q", f.bare, f.repo)
	runGit(t, f.repo, "remote", "set-url", "origin", "https://github.com/acme/shop.git")
	runGit(t, f.repo, "config", "url."+f.bare+".insteadOf", "https://github.com/acme/shop.git")
	runGit(t, f.repo, "config", "core.hooksPath", ".githooks")

	j, err := events.OpenJournal(filepath.Join(root, "journal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	f.journal = j
	f.bus = &events.Bus{Journal: j}
	f.gh = filepath.Join(root, "bin", "gh")
	os.MkdirAll(filepath.Dir(f.gh), 0o755)
	// A gh that answers a PR's state from a file, as the box's own gh
	// would from GitHub.
	os.WriteFile(f.gh, []byte("#!/bin/sh\n# gh pr view N -R REPO --json state\ns=$(cat \""+root+"/state-$3\" 2>/dev/null || echo OPEN)\necho \"{\\\"state\\\":\\\"$s\\\"}\"\n"), 0o755)
	f.b = &Box{Name: "devbox", Locations: NewLocations(filepath.Join(root, "box", "locations.json")), Events: f.bus, LogDir: filepath.Join(root, "logs"),
		Reviews: &ReviewStore{Path: filepath.Join(root, "box", "reviews.json"), GH: f.gh}, Team: &TeamRunner{Dir: filepath.Join(root, "box", "team")}}
	ctx := context.Background()
	if _, err := f.b.Locations.Add(ctx, "shop", f.repo); err != nil {
		t.Fatal(err)
	}
	trustRepo(t, f.b.Locations, "shop")
	// The team setup laid its keys into the project's config on the box:
	// the shared one a 1Password reference, the engineer's own a value.
	if err := f.b.mergeLocalEnv("shop", map[string]string{"STRIPE_KEY": "op://dev/stripe/key", "MAIL_API_KEY": "SG.mine"}); err != nil {
		t.Fatal(err)
	}
	f.bundle(TeamBundle{ID: "acme", Name: "Acme", Org: "acme", Projects: []TeamProjectPlan{{ID: "shop", Repo: "acme/shop", Path: f.repo,
		Env: map[string]string{"STRIPE_KEY": "op://dev/stripe/key", "MAIL_API_KEY": "SG.mine"}, Keys: []string{"MAIL_API_KEY", "STRIPE_KEY"}, Ask: []string{"MAIL_API_KEY"}}}})
	return f
}

func (f *reviewFixture) bundle(tb TeamBundle) {
	dir := filepath.Join(f.b.Team.Dir, tb.ID)
	os.MkdirAll(dir, 0o755)
	b, _ := json.Marshal(tb)
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), b, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *reviewFixture) write(dir string, files map[string]string) {
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		mode := os.FileMode(0o644)
		if strings.HasPrefix(c, "#!") {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(c), mode); err != nil {
			f.t.Fatal(err)
		}
	}
}

// pr pushes a commit on top of base (main when "") as refs/pull/N/head and
// returns it.
func (f *reviewFixture) pr(n int, base string, files map[string]string) string {
	f.t.Helper()
	work := filepath.Join(f.root, "pr-"+strconv.Itoa(n)+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	runGit(f.t, f.root, "clone", "-q", f.bare, work)
	if base != "" {
		runGit(f.t, work, "fetch", "-q", "origin", "refs/pull/"+strconv.Itoa(n)+"/head")
		runGit(f.t, work, "checkout", "-q", base)
	}
	f.write(work, files)
	runGit(f.t, work, "add", "-A")
	runGit(f.t, work, "commit", "-q", "-m", "PR "+strconv.Itoa(n))
	runGit(f.t, work, "push", "-q", "-f", "origin", "HEAD:refs/pull/"+strconv.Itoa(n)+"/head")
	return runGit(f.t, work, "rev-parse", "HEAD")
}

func (f *reviewFixture) open(pr int, sha string) (Worktree, error) {
	return f.b.OpenReview(context.Background(), "app", ReviewOpen{Location: "shop", Repo: "acme/shop", PR: pr, SHA: sha,
		Title: "Fix checkout rounding", Author: "dana-acme", Association: "MEMBER", HeadBranch: "fix-rounding", Reviewer: "sean-acme",
		URL: "https://github.com/acme/shop/pull/" + strconv.Itoa(pr)})
}

func (f *reviewFixture) journaled(typ string) []events.Event {
	f.journal.Sync()
	var out []events.Event
	it := f.journal.Iter(0, 0)
	defer it.Close()
	for {
		e, ok := it.Next()
		if !ok {
			return out
		}
		if e.Type == typ {
			out = append(out, e)
		}
	}
}

func headOf(t *testing.T, dir string) string { return runGit(t, dir, "rev-parse", "HEAD") }

func codeOf(err error) string {
	var rc reviewCode
	if errors.As(err, &rc) {
		return rc.code
	}
	return ""
}

func TestAReviewIsMadeAtTheConfirmedCommitAndNothingElse(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr(42, "", map[string]string{"src/checkout.ts": "export const total = (n: number) => Math.round(n * 100) / 100\n"})
	ch, stop := f.bus.Subscribe()
	defer stop()

	// A commit that is not the PR's head is refused, and nothing is made.
	for _, wrong := range []string{f.main, strings.Repeat("ab", 20)} {
		_, err := f.open(42, wrong)
		if codeOf(err) != "moved" || statusFor(err) != 409 {
			t.Fatalf("open at %s: %v (%s)", wrong, err, codeOf(err))
		}
	}
	if loc, _ := f.b.Locations.Get(context.Background(), "shop"); len(loc.Worktrees) != 1 {
		t.Fatalf("a worktree was made for a commit nobody confirmed: %+v", loc.Worktrees)
	}

	wt, err := f.open(42, sha)
	if err != nil {
		t.Fatal(err)
	}
	if got := headOf(t, wt.Path); got != sha {
		t.Fatalf("worktree at %s, confirmed %s", got, sha)
	}
	if got := runGit(t, f.repo, "rev-parse", "refs/berth/review/42"); got != sha {
		t.Fatalf("pinned ref %s", got)
	}
	if wt.Name != "review-42" || wt.Branch != "review/review-42" || wt.Title != "Review: #42 Fix checkout rounding" || wt.Review == nil || wt.Review.SHA != sha {
		t.Fatalf("worktree: %+v", wt)
	}
	waitFor(t, ch, "worktree.setup.finished")

	// The listing says it is a review, with its title.
	loc, _ := f.b.Locations.Get(context.Background(), "shop")
	var listed *Worktree
	for i := range loc.Worktrees {
		if loc.Worktrees[i].Path == wt.Path {
			listed = &loc.Worktrees[i]
		}
	}
	if listed == nil || listed.Review == nil || listed.Review.PR != 42 || listed.Title != "Review: #42 Fix checkout rounding" {
		t.Fatalf("listed: %+v", listed)
	}

	// The PR moves on: the review stays where it was.
	newer := f.pr(42, sha, map[string]string{"src/coupons.ts": "export {}\n"})
	if got := headOf(t, wt.Path); got != sha {
		t.Fatalf("the review moved by itself to %s", got)
	}
	// Updating to a commit the reviewer was not shown is refused.
	if _, err := f.b.UpdateReview(context.Background(), "app", ReviewUpdate{Location: "shop", Worktree: "review-42", SHA: sha[:39] + "0"}); codeOf(err) != "moved" {
		t.Fatalf("update to an unseen commit: %v", err)
	}
	// With uncommitted changes it stays put.
	os.WriteFile(filepath.Join(wt.Path, "notes.txt"), []byte("my review notes"), 0o644)
	if _, err := f.b.UpdateReview(context.Background(), "app", ReviewUpdate{Location: "shop", Worktree: "review-42", SHA: newer}); codeOf(err) != "dirty" {
		t.Fatalf("update with changes: %v", err)
	}
	if got := headOf(t, wt.Path); got != sha {
		t.Fatalf("a dirty review moved to %s", got)
	}
	os.Remove(filepath.Join(wt.Path, "notes.txt"))
	up, err := f.b.UpdateReview(context.Background(), "app", ReviewUpdate{Location: "shop", Worktree: "review-42", SHA: newer, Title: "Fix checkout rounding and coupons", Author: "dana-acme", Association: "MEMBER", Reviewer: "sean-acme"})
	if err != nil {
		t.Fatal(err)
	}
	if got := headOf(t, wt.Path); got != newer || up.Review.SHA != newer || up.Title != "Review: #42 Fix checkout rounding and coupons" {
		t.Fatalf("updated to %s: %+v", got, up)
	}
	// Opening the same PR again is the same worktree.
	again, err := f.open(42, newer)
	if err != nil || again.Path != wt.Path {
		t.Fatalf("again: %+v %v", again, err)
	}
}

func TestAReviewLinkCannotNameAnythingButARepoAndANumber(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr(42, "", map[string]string{"a.txt": "a\n"})
	for name, req := range map[string]ReviewOpen{
		"injected repo":    {Location: "shop", Repo: "acme/shop;touch /tmp/x", PR: 42, SHA: sha},
		"path repo":        {Location: "shop", Repo: "../../etc", PR: 42, SHA: sha},
		"zero pr":          {Location: "shop", Repo: "acme/shop", PR: 0, SHA: sha},
		"short sha":        {Location: "shop", Repo: "acme/shop", PR: 42, SHA: sha[:7]},
		"ref, not a sha":   {Location: "shop", Repo: "acme/shop", PR: 42, SHA: "refs/heads/main"},
		"another repo":     {Location: "shop", Repo: "acme/other", PR: 42, SHA: sha},
		"odd author":       {Location: "shop", Repo: "acme/shop", PR: 42, SHA: sha, Author: "dana; rm -rf ~"},
		"odd key to keep":  {Location: "shop", Repo: "acme/shop", PR: 42, SHA: sha, Withhold: []string{"A=B"}},
		"unknown location": {Location: "nope", Repo: "acme/shop", PR: 42, SHA: sha},
	} {
		if _, err := f.b.OpenReview(context.Background(), "app", req); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	if loc, _ := f.b.Locations.Get(context.Background(), "shop"); len(loc.Worktrees) != 1 {
		t.Fatalf("made %+v", loc.Worktrees)
	}
}

func TestReviewSetupComesFromTheDefaultBranchNeverThePRHead(t *testing.T) {
	f := newReviewFixture(t)
	pwned := func(what string) string { return filepath.Join(f.marks, "pwned-"+what) }
	// The PR rewrites .berth/config.json to run its own setup, service and
	// hook, and adds a post-checkout git hook.
	evil := `{"setup": "touch ` + pwned("setup") + `", "services": [{"name": "evil", "run": "touch ` + pwned("service") + `", "autostart": true}],
 "hooks": [{"on": "worktree.created", "run": "touch ` + pwned("hook") + `"}], "env": {"MAIL_API_KEY": "stolen"}}`
	sha := f.pr(66, "", map[string]string{
		".berth/config.json":      evil,
		".githooks/post-checkout": "#!/bin/sh\ntouch " + pwned("git-hook") + "\n",
		"src/checkout.ts":         "export const total = () => 0\n",
	})
	ch, stop := f.bus.Subscribe()
	defer stop()
	wt, err := f.open(66, sha)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "worktree.setup.finished")
	// The checkout is the PR's, config file and all...
	if b, _ := os.ReadFile(filepath.Join(wt.Path, ".berth", "config.json")); !strings.Contains(string(b), "pwned-setup") {
		t.Fatalf("the worktree is not the PR's head: %s", b)
	}
	// ...and what ran is the default branch's setup.
	if b, err := os.ReadFile(filepath.Join(wt.Path, ".setup-ran")); err != nil || strings.TrimSpace(string(b)) != "from-main" {
		t.Fatalf("main's setup did not run: %q %v", b, err)
	}
	for _, what := range []string{"setup", "service", "hook", "git-hook"} {
		if _, err := os.Stat(pwned(what)); err == nil {
			t.Fatalf("the PR's %s ran", what)
		}
	}
	cfg, err := f.b.Locations.Config(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Effective.Services) != 1 || cfg.Effective.Services[0].Name != "web" || len(cfg.Effective.Hooks) != 0 || cfg.Effective.Env["MAIL_API_KEY"] == "stolen" {
		t.Fatalf("the location's config took the PR's: %+v", cfg.Effective)
	}
	if hs, _ := f.b.repoHooks(context.Background(), map[string]any{"path": wt.Path}); len(hs) != 0 {
		t.Fatalf("the PR's hooks would run: %+v", hs)
	}
	setup, err := f.b.ReviewSetupFor(context.Background(), "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if setup.From != "repo" || setup.RepoConfig != RepoTrustTrusted || setup.MatchesDefault == nil || !*setup.MatchesDefault || setup.DefaultBranch != "main" ||
		len(setup.Services) != 1 || setup.Services[0].Title != "Next.js" || !strings.HasPrefix(setup.Script, "echo from-main") {
		t.Fatalf("setup: %+v", setup)
	}
	// Updating it to a newer head of the PR runs none of them either.
	newer := f.pr(66, sha, map[string]string{"src/more.ts": "export {}\n"})
	if _, err := f.b.UpdateReview(context.Background(), "app", ReviewUpdate{Location: "shop", Worktree: wt.Name, SHA: newer}); err != nil {
		t.Fatal(err)
	}
	for _, what := range []string{"setup", "service", "hook", "git-hook"} {
		if _, err := os.Stat(pwned(what)); err == nil {
			t.Fatalf("the PR's %s ran on update", what)
		}
	}
	// A main checkout whose config is not the default branch's is said so.
	os.WriteFile(filepath.Join(f.repo, ".berth", "config.json"), []byte(`{"ports": 2}`), 0o644)
	if s, _ := f.b.ReviewSetupFor(context.Background(), "shop", nil); s.MatchesDefault == nil || *s.MatchesDefault {
		t.Fatalf("a main checkout off the default branch's config: %+v", s.MatchesDefault)
	}
	// The git hook is real: an ordinary checkout of the PR in a worktree
	// runs it.
	plain := filepath.Join(f.root, "plain")
	runGit(t, f.repo, "worktree", "add", "-q", "--detach", plain, "main")
	runGit(t, plain, "checkout", "-q", sha)
	if _, err := os.Stat(pwned("git-hook")); err != nil {
		t.Fatal("control: the PR's post-checkout hook did not run in an ordinary checkout, so the test proves nothing")
	}
}

func TestAReviewGetsTheTeamsSharedKeysNeverTheReviewersOwn(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr(42, "", map[string]string{"a.txt": "a\n"})
	ch, stop := f.bus.Subscribe()
	defer stop()
	wt, err := f.open(42, sha)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "worktree.setup.finished")
	if len(wt.Review.Withheld) != 1 || wt.Review.Withheld[0] != "MAIL_API_KEY" {
		t.Fatalf("withheld %v", wt.Review.Withheld)
	}
	review, err := f.b.worktreeEnv(context.Background(), "shop", wt)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(review.env, "\n")
	if strings.Contains(env, "MAIL_API_KEY=") || strings.Contains(env, "SG.mine") {
		t.Fatalf("the review got the reviewer's own key:\n%s", env)
	}
	if review.refs["STRIPE_KEY"] != "op://dev/stripe/key" {
		t.Fatalf("the review lacks the team's shared key: %v", review.refs)
	}
	for _, want := range []string{"BERTH_REVIEW=42", "BERTH_REVIEW_REPO=acme/shop", "BERTH_WITHHELD=MAIL_API_KEY", "BERTH_REVIEW_SHA=" + sha} {
		if !strings.Contains(env, want) {
			t.Fatalf("no %s in\n%s", want, env)
		}
	}
	// Its setup ran without it too.
	if b, _ := os.ReadFile(filepath.Join(wt.Path, ".setup-env")); !strings.HasPrefix(string(b), "|") || !strings.HasSuffix(strings.TrimSpace(string(b)), "|42") {
		t.Fatalf("setup saw %q", b)
	}
	// An ordinary worktree of the project gets both, as before.
	plain, err := f.b.Locations.CreateWorktree(context.Background(), "shop", "plain", "", "")
	if err != nil {
		t.Fatal(err)
	}
	own, _ := f.b.worktreeEnv(context.Background(), "shop", plain)
	if !strings.Contains(strings.Join(own.env, "\n"), "MAIL_API_KEY=SG.mine") || own.refs["STRIPE_KEY"] == "" {
		t.Fatalf("an ordinary worktree lost its keys: %v %v", own.env, own.refs)
	}
	// The sheet says the same, by name.
	s, err := f.b.ReviewSetupFor(context.Background(), "shop", []string{"OTHER_OWN"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(s.Secrets.Shared, ",") != "STRIPE_KEY" || strings.Join(s.Secrets.Withheld, ",") != "MAIL_API_KEY" {
		t.Fatalf("secrets: %+v", s.Secrets)
	}
}

func TestATeamBundleFromBeforeAskStillWithholdsTheEngineersKeys(t *testing.T) {
	f := newReviewFixture(t)
	f.bundle(TeamBundle{ID: "acme", Name: "Acme", Org: "acme", Projects: []TeamProjectPlan{{ID: "shop", Repo: "acme/shop", Path: f.repo,
		Env: map[string]string{"STRIPE_KEY": "op://dev/stripe/key", "MAIL_API_KEY": "SG.mine"}, Keys: []string{"MAIL_API_KEY", "STRIPE_KEY"}}}})
	if got := f.b.teamAskKeys(f.repo); strings.Join(got, ",") != "MAIL_API_KEY" {
		t.Fatalf("asked keys %v", got)
	}
}

func TestReviewsAreCleanedUpWhenMergedClosedOrIdleButNeverWithChanges(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	ch, stop := f.bus.Subscribe()
	defer stop()
	open := func(n int) Worktree {
		sha := f.pr(n, "", map[string]string{"pr.txt": strconv.Itoa(n)})
		wt, err := f.open(n, sha)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, ch, "worktree.setup.finished")
		return wt
	}
	merged, closed, idle, busy, fresh := open(51), open(52), open(53), open(54), open(55)
	os.WriteFile(filepath.Join(f.root, "state-51"), []byte("MERGED"), 0o644)
	os.WriteFile(filepath.Join(f.root, "state-52"), []byte("CLOSED"), 0o644)
	os.WriteFile(filepath.Join(f.root, "state-54"), []byte("MERGED"), 0o644)
	// busy has work in it nobody committed.
	os.WriteFile(filepath.Join(busy.Path, "my-notes.md"), []byte("half a review"), 0o644)
	// idle was last touched eight days ago; fresh yesterday.
	for wt, ago := range map[Worktree]time.Duration{idle: 8 * 24 * time.Hour, fresh: 24 * time.Hour} {
		_, m := f.b.Locations.reviewAt(wt.Path)
		m.Opened = time.Now().Add(-ago)
		f.b.Locations.setReview("shop", wt.Path, m)
	}
	got := map[string]ReviewSweep{}
	for _, s := range f.b.SweepReviews(ctx, time.Now()) {
		got[s.Worktree] = s
	}
	want := map[string]ReviewSweep{
		merged.Name: {Location: "shop", Worktree: merged.Name, Reason: "merged", Action: "removed"},
		closed.Name: {Location: "shop", Worktree: closed.Name, Reason: "closed", Action: "removed"},
		idle.Name:   {Location: "shop", Worktree: idle.Name, Reason: "idle", Action: "removed"},
		busy.Name:   {Location: "shop", Worktree: busy.Name, Reason: "merged", Action: "waiting"},
	}
	if len(got) != len(want) {
		t.Fatalf("sweep: %+v", got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: %+v; want %+v", k, got[k], w)
		}
	}
	for _, wt := range []Worktree{merged, closed, idle} {
		if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
			t.Errorf("%s is still there", wt.Name)
		}
		if branchExists(ctx, f.repo, "refs/heads/"+wt.Branch) {
			t.Errorf("%s's branch is still there", wt.Name)
		}
	}
	// Each went through the archive script, which drops its database.
	archived, _ := os.ReadFile(filepath.Join(filepath.Dir(f.repo), "archived"))
	for _, wt := range []Worktree{merged, closed, idle} {
		if !strings.Contains(string(archived), wt.Name) {
			t.Errorf("%s was removed without its archive script: %q", wt.Name, archived)
		}
	}
	// The one with changes is there, its notes too, waiting for the
	// reviewer; fresh is untouched.
	if b, err := os.ReadFile(filepath.Join(busy.Path, "my-notes.md")); err != nil || string(b) != "half a review" {
		t.Fatal("uncommitted work was lost")
	}
	if _, m := f.b.Locations.reviewAt(busy.Path); m == nil || m.Cleanup != "merged" {
		t.Fatalf("busy: %+v", m)
	}
	if _, err := os.Stat(fresh.Path); err != nil {
		t.Fatal("a review in use was removed")
	}
	// A second pass asks once, not again.
	f.b.SweepReviews(ctx, time.Now())
	if n := len(f.journaled("review.waiting")); n != 1 {
		t.Fatalf("%d review.waiting events", n)
	}
	reasons := map[string]string{}
	for _, e := range f.journaled("review.removed") {
		reasons[e.Data["name"].(string)] = e.Data["reason"].(string)
	}
	if reasons[merged.Name] != "merged" || reasons[closed.Name] != "closed" || reasons[idle.Name] != "idle" {
		t.Fatalf("removed: %v", reasons)
	}
	// Idle clean-up can be turned off per box.
	if err := f.b.Reviews.setIdleDays(0); err != nil {
		t.Fatal(err)
	}
	_, m := f.b.Locations.reviewAt(fresh.Path)
	m.Opened = time.Now().Add(-90 * 24 * time.Hour)
	f.b.Locations.setReview("shop", fresh.Path, m)
	if s := f.b.SweepReviews(ctx, time.Now()); len(s) != 1 || s[0].Worktree != busy.Name {
		t.Fatalf("with idle clean-up off: %+v", s)
	}
}

func TestReviewsAreInTheJournal(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr(42, "", map[string]string{"a.txt": "a\n"})
	ch, stop := f.bus.Subscribe()
	defer stop()
	wt, err := f.open(42, sha)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "worktree.setup.finished")
	newer := f.pr(42, sha, map[string]string{"b.txt": "b\n"})
	if _, err := f.b.UpdateReview(context.Background(), "app", ReviewUpdate{Location: "shop", Worktree: wt.Name, SHA: newer, Author: "dana-acme", Association: "MEMBER", Reviewer: "sean-acme"}); err != nil {
		t.Fatal(err)
	}
	loc, _ := f.b.Locations.Get(context.Background(), "shop")
	done := make(chan error, 1)
	if _, err := f.b.dropWorktree("app", loc, wt.Name, wt.Path, "", false, "removed", func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	opened := f.journaled("review.opened")
	if len(opened) != 1 {
		t.Fatalf("review.opened: %+v", opened)
	}
	d := opened[0].Data
	for k, v := range map[string]any{"repo": "acme/shop", "sha": sha, "author": "dana-acme", "association": "MEMBER", "box": "devbox", "reviewer": "sean-acme", "secrets": "shared", "location": "shop", "name": "review-42"} {
		if d[k] != v {
			t.Errorf("review.opened %s = %v; want %v", k, d[k], v)
		}
	}
	if d["pr"] != float64(42) || opened[0].Origin != "app" {
		t.Errorf("review.opened: %+v", opened[0])
	}
	if w, _ := d["withheld"].([]any); len(w) != 1 || w[0] != "MAIL_API_KEY" {
		t.Errorf("withheld: %v", d["withheld"])
	}
	if strings.Contains(string(mustJSON(opened[0])), "SG.mine") {
		t.Fatal("a key's value is in the journal")
	}
	updated := f.journaled("review.updated")
	if len(updated) != 1 || updated[0].Data["from"] != sha || updated[0].Data["sha"] != newer || updated[0].Data["reviewer"] != "sean-acme" {
		t.Fatalf("review.updated: %+v", updated)
	}
	removed := f.journaled("review.removed")
	if len(removed) != 1 || removed[0].Data["reason"] != "removed" || removed[0].Data["pr"] != float64(42) {
		t.Fatalf("review.removed: %+v", removed)
	}
	// The mark went with the worktree, and so did its branch and pin.
	if _, m := f.b.Locations.reviewAt(wt.Path); m != nil {
		t.Fatal("the mark outlived the worktree")
	}
	if branchExists(context.Background(), f.repo, "refs/heads/review/review-42") || branchExists(context.Background(), f.repo, "refs/berth/review/42") {
		t.Fatal("the review's branch or pin is still there")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestReviewRoutesOverTheWire(t *testing.T) {
	f := newReviewFixture(t)
	sha := f.pr(42, "", map[string]string{"a.txt": "a\n"})
	c, _ := servedBox(t, func(b *Box) {
		b.Locations, b.Reviews, b.Team, b.LogDir = f.b.Locations, f.b.Reviews, f.b.Team, f.b.LogDir
	})
	var setup ReviewSetup
	if code := call(t, c, "GET", "/v1/locations/shop/review-setup?withhold=OTHER", "app", nil, &setup); code != 200 || setup.IdleDays != 7 || setup.From != "repo" {
		t.Fatalf("setup: %d %+v", code, setup)
	}
	var errBody map[string]string
	if code := call(t, c, "POST", "/v1/reviews", "app", ReviewOpen{Location: "shop", Repo: "acme/shop", PR: 42, SHA: f.main}, &errBody); code != 409 || errBody["code"] != "moved" {
		t.Fatalf("moved: %d %v", code, errBody)
	}
	var wt Worktree
	if code := call(t, c, "POST", "/v1/reviews", "app", ReviewOpen{Location: "shop", Repo: "acme/shop", PR: 42, SHA: sha, Title: "Fix"}, &wt); code != 200 || wt.Review == nil {
		t.Fatalf("open: %d %+v", code, wt)
	}
	var list []ReviewEntry
	if code := call(t, c, "GET", "/v1/reviews", "", nil, &list); code != 200 || len(list) != 1 || list[0].Review.SHA != sha {
		t.Fatalf("list: %d %+v", code, list)
	}
	var settings map[string]int
	if code := call(t, c, "PUT", "/v1/reviews/settings", "", map[string]int{"idle_days": 3}, &settings); code != 200 || settings["idle_days"] != 3 {
		t.Fatalf("settings: %d %v", code, settings)
	}
	if code := call(t, c, "PUT", "/v1/reviews/settings", "", map[string]int{"idle_days": -1}, nil); code != 400 {
		t.Fatalf("negative idle days: %d", code)
	}
}

func TestReviewSetupSaysWhoAReviewCanLogInAsFromTrustedConfigOnly(t *testing.T) {
	f := newReviewFixture(t)
	ctx := context.Background()
	if s, err := f.b.ReviewSetupFor(ctx, "shop", nil); err != nil || s.Login != nil {
		t.Fatalf("no login: %+v %v", s.Login, err)
	}
	saved, err := f.b.Locations.saved("shop")
	if err != nil {
		t.Fatal(err)
	}
	c := *saved.Config
	c.Login = &LoginConfig{Script: "scripts/login.sh", Users: []LoginUser{{Email: "pro@acme.test", Label: "Pro"}}}
	if err := f.b.Locations.SetLocalConfig("shop", c); err != nil {
		t.Fatal(err)
	}
	// A PR that adds a user of its own changes nothing: its config is never read.
	sha := f.pr(70, "", map[string]string{".berth/config.json": `{"login": {"script": "x.sh", "users": ["evil@acme.test"], "any": true}}`})
	ch, stop := f.bus.Subscribe()
	defer stop()
	if _, err := f.open(70, sha); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ch, "worktree.setup.finished")
	s, err := f.b.ReviewSetupFor(ctx, "shop", nil)
	if err != nil || s.Login == nil || s.Login.Any || strings.Join(s.Login.Users, ",") != "pro@acme.test" {
		t.Fatalf("login: %+v %v", s.Login, err)
	}
}
