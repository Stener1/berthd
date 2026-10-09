package box

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/team/teamtest"
)

// Every test here works in a home folder of its own (t.TempDir as HOME),
// with real git: nobody's own clones are looked at.

func adoptGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Acme Dev", "-c", "user.email=dev@acme.test", "-c", "init.defaultBranch=main", "-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoAt makes a git checkout at dir with one commit and origin.
func repoAt(t *testing.T, dir, origin string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	adoptGit(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("acme\n"), 0o644)
	adoptGit(t, dir, "add", ".")
	adoptGit(t, dir, "commit", "-qm", "first")
	if origin != "" {
		adoptGit(t, dir, "remote", "add", "origin", origin)
	}
	return dir
}

func scanHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	return home
}

func resolved(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestScanFindsClonesWhereEngineersKeepThem(t *testing.T) {
	home := scanHome(t)
	outside := t.TempDir()
	h := func(p string) string { return filepath.Join(home, p) }
	repoAt(t, h("work/acme-web"), "https://github.com/acme/web.git")
	// Two levels down in a usual folder.
	repoAt(t, h("work/team/acme-api"), "git@github.com:acme/api.git")
	// One level down in any folder of home's.
	repoAt(t, h("Downloads/x"), "ssh://git@github.com/Acme/Tools")
	// Three levels down is beyond the scan.
	repoAt(t, h("work/a/b/deep"), "https://github.com/acme/deep")
	// Skipped: node_modules and hidden folders.
	repoAt(t, h("work/node_modules/acme-skip"), "https://github.com/acme/skip")
	repoAt(t, h("work/.hidden/acme-hidden"), "https://github.com/acme/hidden")
	// A symlink out of home is not followed; one inside it is.
	repoAt(t, filepath.Join(outside, "repo"), "https://github.com/acme/outside")
	os.Symlink(filepath.Join(outside, "repo"), h("work/outside"))
	repoAt(t, h(".private/inrepo"), "https://github.com/acme/inside")
	os.Symlink(h(".private/inrepo"), h("work/inside"))
	// A bare repository is recognised, and not a checkout to use.
	os.MkdirAll(h("work/bare.git"), 0o755)
	adoptGit(t, h("work/bare.git"), "init", "-q", "--bare")
	adoptGit(t, h("work/bare.git"), "remote", "add", "origin", "https://github.com/acme/bare")
	// A git worktree is not a main checkout: its main checkout is found.
	repoAt(t, h(".private/mainrepo"), "https://github.com/acme/main")
	adoptGit(t, h(".private/mainrepo"), "worktree", "add", "-q", h("work/main-wt"), "-b", "wt")
	// The same folder name, another repository.
	repoAt(t, h("src/web"), "https://github.com/someone-else/web")

	want := map[string]bool{}
	for _, r := range []string{"acme/web", "acme/api", "acme/tools", "acme/deep", "acme/skip", "acme/hidden", "acme/outside", "acme/inside", "acme/bare", "acme/main"} {
		want[r] = true
	}
	s := &cloneScan{Home: home, Want: want}
	found := s.run()
	if s.Truncated {
		t.Fatalf("the scan ran out of budget on a small home")
	}
	got := map[string]string{}
	for slug, paths := range found {
		if len(paths) != 1 {
			t.Fatalf("%s found %d times: %v", slug, len(paths), paths)
		}
		got[slug] = paths[0]
	}
	expect := map[string]string{
		"acme/web":    resolved(t, h("work/acme-web")),
		"acme/api":    resolved(t, h("work/team/acme-api")),
		"acme/tools":  resolved(t, h("Downloads/x")),
		"acme/inside": resolved(t, h(".private/inrepo")),
		"acme/main":   resolved(t, h(".private/mainrepo")),
	}
	for slug, p := range expect {
		if got[slug] != p {
			t.Errorf("%s: found %q, want %q", slug, got[slug], p)
		}
	}
	for _, slug := range []string{"acme/deep", "acme/skip", "acme/hidden", "acme/outside", "acme/bare"} {
		if p, ok := got[slug]; ok {
			t.Errorf("%s should not be found, found at %s", slug, p)
		}
	}
}

func TestScanStopsAtItsBudgets(t *testing.T) {
	home := scanHome(t)
	for i := 0; i < 40; i++ {
		os.MkdirAll(filepath.Join(home, "code", "empty-"+string(rune('a'+i%26))+string(rune('a'+i/26))), 0o755)
	}
	repoAt(t, filepath.Join(home, "work", "acme-web"), "https://github.com/acme/web")
	want := map[string]bool{"acme/web": true}

	// The entries budget: ~/code alone has more entries than it allows.
	s := &cloneScan{Home: home, Want: want, MaxEntries: 10}
	found := s.run()
	if !s.Truncated || s.entries > 10 {
		t.Fatalf("entries budget: truncated %v after %d entries", s.Truncated, s.entries)
	}
	if len(found["acme/web"]) != 0 {
		t.Fatalf("looked past the entries budget: %v", found)
	}

	// The time budget: spent before it starts, so nothing is read.
	s = &cloneScan{Home: home, Want: want, Budget: time.Nanosecond}
	start := time.Now()
	found = s.run()
	if !s.Truncated || len(found) != 0 || time.Since(start) > time.Second {
		t.Fatalf("time budget: truncated %v, found %v in %s", s.Truncated, found, time.Since(start))
	}

	// With room, it is found.
	s = &cloneScan{Home: home, Want: want}
	if found = s.run(); len(found["acme/web"]) != 1 || s.Truncated {
		t.Fatalf("with room: %v truncated %v", found, s.Truncated)
	}
}

func TestOriginsMatchInEveryFormAndOnlyTheirRepo(t *testing.T) {
	dir := t.TempDir()
	for _, form := range []string{
		"https://github.com/acme/web",
		"https://github.com/acme/web.git",
		"https://github.com/Acme/Web.git/",
		"ssh://git@github.com/acme/web.git",
		"git@github.com:acme/web.git",
		"git@github.com:acme/web",
	} {
		cfg := filepath.Join(dir, "config")
		// Another remote first, and the origin's URL quoted or not.
		os.WriteFile(cfg, []byte("[core]\n\tbare = false\n[remote \"upstream\"]\n\turl = https://github.com/acme/other\n[remote \"origin\"]\n\turl = \""+form+"\"\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"), 0o644)
		got := originInConfig(cfg)
		if got != form {
			t.Errorf("origin read as %q, want %q", got, form)
		}
		if s := slugOf(got); !strings.EqualFold(s, "acme/web") {
			t.Errorf("%s is %q, not acme/web", form, s)
		}
	}
	for _, other := range []string{"https://github.com/someone-else/web", "git@github.com:acme/web-legacy.git", ""} {
		if strings.EqualFold(slugOf(other), "acme/web") {
			t.Errorf("%q matched acme/web", other)
		}
	}
}

// adoptFixture is a team fixture whose bundle runs no steps, only its
// projects, with the repositories on the fake GitHub.
func adoptBundle(f *teamFixture, projects ...TeamProjectPlan) TeamBundle {
	tb := f.bundle()
	tb.Steps, tb.Script, tb.GitHub = nil, "", false
	tb.Files["box/init-env.sh"] = b64(envInit)
	tb.Projects = projects
	return tb
}

// envInit is an init as teams write them: .env from .env.example only when
// there is none, and only empty or example values filled in.
const envInit = `#!/bin/sh
set -e
[ -f .env ] || cp .env.example .env
v=$(sed -n 's/^SECRET=//p' .env)
if [ -z "$v" ] || [ "$v" = example ]; then
  grep -v '^SECRET=' .env > .env.tmp || true
  echo SECRET=generated >> .env.tmp
  mv .env.tmp .env
fi
echo ran >> "$MARKS/init-ran"
pwd >> "$MARKS/init-ran"
`

func (f *teamFixture) done() TeamStatus {
	f.t.Helper()
	return f.waitFor("the projects", func(st TeamStatus) bool { return st.Phase == "done" || st.Phase == "failed" })
}

func projectOf(st TeamStatus, id string) TeamProjectStatus {
	for _, p := range st.Projects {
		if p.ID == id {
			return p
		}
	}
	return TeamProjectStatus{}
}

func TestTeamSetupUsesALocationAlreadyOnTheBoxAtAnotherPath(t *testing.T) {
	f := newTeamFixture(t)
	f.bundle()
	// The engineer cloned acme/web to ~/elsewhere/web and added it as
	// "myweb", with a key of its own; team.json says ~/code/web.
	mine := filepath.Join(f.home, "elsewhere", "web")
	adoptGit(t, f.home, "clone", "-q", "https://github.com/acme/web", mine)
	if _, err := f.b.Locations.Add(context.Background(), "myweb", mine); err != nil {
		t.Fatal(err)
	}
	f.b.mergeLocalEnv("myweb", map[string]string{"MINE": "kept"})
	tb := adoptBundle(f, TeamProjectPlan{ID: "web", Repo: "acme/web", Source: "repo", TrustHash: sha(teamRepoConfig), Env: map[string]string{"MAIL_KEY": "SG.mine"}})
	if _, err := f.b.StartTeam(context.Background(), tb); err != nil {
		t.Fatal(err)
	}
	st := f.done()
	web := projectOf(st, "web")
	if st.Phase != "done" || !web.Adopted || web.Location != "myweb" || web.Path != evalHome(mine) {
		t.Fatalf("web: %+v (%s)", web, st.Error)
	}
	if _, err := os.Stat(filepath.Join(f.home, "code", "web")); err == nil {
		t.Fatal("a second clone was made at ~/code/web")
	}
	all, _ := f.b.Locations.List(context.Background())
	if len(all) != 1 {
		t.Fatalf("locations: %+v", all)
	}
	saved, _ := f.b.Locations.saved("myweb")
	if saved.Config.Env["MINE"] != "kept" || saved.Config.Env["MAIL_KEY"] != "SG.mine" || saved.RepoTrust != sha(teamRepoConfig) {
		t.Fatalf("myweb's settings: %+v trust %q", saved.Config, saved.RepoTrust)
	}
	// Keys are found where the project is: its adopted location.
	if !strings.Contains(strings.Join(st.KeysSet, ","), "web/MAIL_KEY") {
		t.Fatalf("keys set: %v", st.KeysSet)
	}

	// Two locations of the same repo: the one at the team.json path wins,
	// and the status says so.
	adoptGit(t, f.home, "clone", "-q", "https://github.com/acme/web", filepath.Join(f.home, "code", "web"))
	f.b.Locations.Add(context.Background(), "web", filepath.Join(f.home, "code", "web"))
	if _, err := f.b.StartTeam(context.Background(), tb); err != nil {
		t.Fatal(err)
	}
	st = f.done()
	web = projectOf(st, "web")
	if web.Location != "web" || !strings.Contains(web.Note, "at the path team.json gives it") {
		t.Fatalf("with two: %+v", web)
	}
}

func TestAChosenCloneWhoseOriginChangedIsRefused(t *testing.T) {
	f := newTeamFixture(t)
	f.bundle()
	mine := filepath.Join(f.home, "work", "acme-web")
	adoptGit(t, f.home, "clone", "-q", "https://github.com/acme/web", mine)
	tb := adoptBundle(f, TeamProjectPlan{ID: "web", Repo: "acme/web", Source: "none", Use: "~/work/acme-web"})
	// Between the scan and the setup, it became a clone of another repo.
	adoptGit(t, mine, "remote", "set-url", "origin", "https://github.com/acme/other")
	if _, err := f.b.StartTeam(context.Background(), tb); err != nil {
		t.Fatal(err)
	}
	st := f.done()
	web := projectOf(st, "web")
	if st.Phase != "failed" || web.State != TeamFailed || !strings.Contains(web.Error, "no longer a clone of acme/web") {
		t.Fatalf("web: %+v", web)
	}
	if _, err := os.Stat(filepath.Join(f.home, "code", "web")); err == nil {
		t.Fatal("it cloned instead")
	}
	if all, _ := f.b.Locations.List(context.Background()); len(all) != 0 {
		t.Fatalf("it was added: %+v", all)
	}
	// A git worktree chosen as if it were a main checkout is refused too.
	adoptGit(t, mine, "remote", "set-url", "origin", "https://github.com/acme/web")
	adoptGit(t, mine, "worktree", "add", "-q", filepath.Join(f.home, "work", "acme-web-wt"), "-b", "wt")
	if _, err := verifyClone("~/work/acme-web-wt", "acme/web"); err == nil || !strings.Contains(err.Error(), "git worktree") {
		t.Fatalf("a worktree as a main checkout: %v", err)
	}
}

// snapshot is everything about a checkout adopting must leave alone.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("branch " + adoptGit(t, dir, "symbolic-ref", "--short", "HEAD") + "\n")
	b.WriteString("head " + adoptGit(t, dir, "rev-parse", "HEAD") + "\n")
	b.WriteString("status\n" + adoptGit(t, dir, "status", "--porcelain", "--untracked-files=all") + "\n")
	b.WriteString("stash " + adoptGit(t, dir, "stash", "list") + "\n")
	var files []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			raw, _ := os.ReadFile(p)
			sum := sha256.Sum256(raw)
			rel, _ := filepath.Rel(dir, p)
			files = append(files, rel+" "+hex.EncodeToString(sum[:]))
		}
		return nil
	})
	sort.Strings(files)
	b.WriteString(strings.Join(files, "\n"))
	return b.String()
}

// dirtyAPI clones acme/api to ~/work/acme-api on a branch of its own, with
// uncommitted changes and an .env of the engineer's.
func dirtyAPI(t *testing.T, f *teamFixture) string {
	f.gh.Repo("acme/api", map[string]string{"main.go": "package main\n", ".env.example": "SECRET=example\nPORT=3000\n"}, teamtest.Repo{})
	dir := filepath.Join(f.home, "work", "acme-api")
	adoptGit(t, f.home, "clone", "-q", "https://github.com/acme/api", dir)
	adoptGit(t, dir, "checkout", "-qb", "feat/x")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\n// half done\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("todo\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=mine\nPORT=4000\n"), 0o600)
	return dir
}

func apiPlan(use string) TeamProjectPlan {
	return TeamProjectPlan{ID: "api", Repo: "acme/api", Source: "kit", Init: "box/init-env.sh", Use: use,
		Kit: &KitInstall{Kit: Kit{ID: "acme-api", Name: "Acme API", Config: RepoConfig{Setup: `echo "$BERTH_WORKTREE_NAME" >> "$MARKS/worktree-setup"`}}, Hash: "abc123"}}
}

func TestAdoptingADirtyCheckoutLeavesItExactlyAsItWas(t *testing.T) {
	f := newTeamFixture(t)
	dir := dirtyAPI(t, f)
	before := snapshot(t, dir)
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if _, err := f.b.StartTeam(context.Background(), adoptBundle(f, apiPlan("~/work/acme-api"))); err != nil {
		t.Fatal(err)
	}
	st := f.done()
	api := projectOf(st, "api")
	if st.Phase != "done" || !api.Adopted || api.Path != evalHome(dir) {
		t.Fatalf("api: %+v (%s)", api, st.Error)
	}
	// The init ran, in the existing checkout.
	ran, _ := os.ReadFile(filepath.Join(f.marks, "init-ran"))
	if !strings.Contains(string(ran), "work/acme-api") {
		t.Fatalf("init ran where? %q", ran)
	}
	// Its branch, commit, working tree, untracked files and .env are byte
	// for byte what they were; nothing was stashed.
	if after := snapshot(t, dir); after != before {
		t.Fatalf("the checkout changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".env")); string(after) != string(env) || !strings.Contains(string(after), "SECRET=mine") {
		t.Fatalf(".env changed: %q", after)
	}
	if _, err := os.Stat(filepath.Join(f.home, "code", "api")); err == nil {
		t.Fatal("a second clone was made")
	}
}

func TestWorktreesOfAnAdoptedRepoAreSetUpOnFirstOpen(t *testing.T) {
	f := newTeamFixture(t)
	dir := dirtyAPI(t, f)
	wt := filepath.Join(f.home, "work", "acme-api-review")
	adoptGit(t, dir, "worktree", "add", "-q", wt, "-b", "review")
	os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("mine\n"), 0o644)
	before := snapshot(t, wt)
	if _, err := f.b.StartTeam(context.Background(), adoptBundle(f, apiPlan("~/work/acme-api"))); err != nil {
		t.Fatal(err)
	}
	st := f.done()
	api := projectOf(st, "api")
	if api.FirstOpen != 1 {
		t.Fatalf("api: %+v", api)
	}
	loc, err := f.b.Locations.Get(context.Background(), api.Location)
	if err != nil || len(loc.Worktrees) != 2 {
		t.Fatalf("worktrees: %+v %v", loc.Worktrees, err)
	}
	var w Worktree
	for _, x := range loc.Worktrees {
		if !x.Main {
			w = x
		}
	}
	if !w.SetupOnOpen || loc.Worktrees[0].SetupOnOpen {
		t.Fatalf("set up on first open: %+v", loc.Worktrees)
	}
	// Adopting didn't set it up, or touch its files.
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(f.marks, "worktree-setup")); err == nil {
		t.Fatal("the worktree was set up during adoption")
	}
	if after := snapshot(t, wt); after != before {
		t.Fatalf("the worktree changed:\n%s\n%s", before, after)
	}
	// The first time a session starts there, its setup runs, once.
	f.b.setUpOnFirstOpen("", w.Path)
	f.b.setUpOnFirstOpen("", w.Path)
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, _ := os.ReadFile(filepath.Join(f.marks, "worktree-setup"))
		if strings.TrimSpace(string(raw)) == w.Name {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("setup on first open: %q", raw)
		}
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if raw, _ := os.ReadFile(filepath.Join(f.marks, "worktree-setup")); strings.Count(string(raw), "\n") != 1 {
		t.Fatalf("set up more than once: %q", raw)
	}
	loc, _ = f.b.Locations.Get(context.Background(), api.Location)
	for _, x := range loc.Worktrees {
		if x.SetupOnOpen {
			t.Fatalf("still waiting for its first open: %+v", x)
		}
	}
}

func TestExistingListsCandidatesForThePage(t *testing.T) {
	f := newTeamFixture(t)
	dir := dirtyAPI(t, f)
	repoAt(t, filepath.Join(f.home, "Downloads", "web-copy"), "git@github.com:acme/web.git")
	r := httptest.NewRequest("GET", "/v1/team/acme/existing?project=api:acme/api:~/code/api&project=web:acme/web:", nil)
	r.SetPathValue("id", "acme")
	w := httptest.NewRecorder()
	if err := f.b.getExisting(w, r); err != nil {
		t.Fatal(err)
	}
	var res ExistingResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || len(res.Projects) != 2 {
		t.Fatalf("%s %v", w.Body, err)
	}
	api := res.Projects[0]
	if len(api.Clones) != 1 {
		t.Fatalf("api: %+v", api)
	}
	c := api.Clones[0]
	if c.Path != evalHome(dir) || c.Display != "~/work/acme-api" || c.Branch != "feat/x" || c.Dirty != 3 || c.Location != "" || c.LastCommit == nil {
		t.Fatalf("api's clone: %+v", c)
	}
	if web := res.Projects[1]; len(web.Clones) != 1 || web.Clones[0].Display != "~/Downloads/web-copy" || web.Clones[0].Dirty != 0 {
		t.Fatalf("web: %+v", web)
	}
	// Looking changed nothing: git's optional locks were off.
	if out := adoptGit(t, dir, "status", "--porcelain"); strings.Count(out, "\n") != 2 {
		t.Fatalf("status: %q", out)
	}
	// Once a Shipyard location, it says which.
	f.b.Locations.Add(context.Background(), "my-api", dir)
	w = httptest.NewRecorder()
	f.b.getExisting(w, r)
	res = ExistingResult{}
	json.Unmarshal(w.Body.Bytes(), &res)
	if got := res.Projects[0].Clones; len(got) != 1 || got[0].Location != "my-api" || got[0].Used == nil {
		t.Fatalf("as a location: %+v", got)
	}
	// A bad project is refused.
	bad := httptest.NewRequest("GET", "/v1/team/acme/existing?project=api:not-a-repo:~/x", nil)
	bad.SetPathValue("id", "acme")
	if err := f.b.getExisting(httptest.NewRecorder(), bad); err == nil {
		t.Fatal("a bad project was accepted")
	}
}
