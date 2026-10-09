package box

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

func TestSlugsComeFromEveryKindOfRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:calcom/cal.com.git":       "calcom/cal.com",
		"https://github.com/calcom/cal.git":       "calcom/cal",
		"ssh://git@gitlab.com/group/sub/proj.git": "sub/proj",
		"https://gitlab.example.com/a/b/":         "a/b",
		"":                                        "",
	} {
		if got := slugOf(in); got != want {
			t.Errorf("slugOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGitHubSlugIsOnlyForGitHubDotCom(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:acme/web.git":               "acme/web",
		"https://github.com/acme/web":               "acme/web",
		"https://token@github.com/acme/web.git":     "acme/web",
		"ssh://git@ssh.github.com:443/acme/api.git": "acme/api",
		"git@github.com-work:acme/tools.git":        "acme/tools",
		"https://github.acme.example/acme/web.git":  "",
		"git@gitlab.com:acme/web.git":               "",
		"/srv/git/web.git":                          "",
		"https://github.com/acme":                   "",
		"":                                          "",
	} {
		if got := GitHubSlug(in); got != want {
			t.Errorf("GitHubSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveTurnsInputIntoAWorktree(t *testing.T) {
	ctx := context.Background()
	repo := gitRepo(t)
	gitIn(t, repo, "branch", "feat/qa-app")
	for _, tc := range []struct {
		input, kind string
		want        Resolution
	}{
		{"Fix the login loop!", "", Resolution{Kind: "name", Name: "fix-the-login-loop", Branch: "fix-the-login-loop"}},
		{"sean/fix-billing", "", Resolution{Kind: "name", Name: "fix-billing", Branch: "sean/fix-billing"}},
		{"feat/qa-app", "", Resolution{Kind: "branch", Name: "qa-app", Branch: "feat/qa-app", Exists: true}},
		{"https://github.com/calcom/cal/tree/feat/qa-app", "", Resolution{Kind: "branch", Name: "qa-app", Branch: "feat/qa-app", Exists: true}},
		{"https://acme.atlassian.net/browse/ENG-1234", "", Resolution{Kind: "issue", Name: "eng-1234", Branch: "eng-1234", Title: "ENG-1234", URL: "https://acme.atlassian.net/browse/ENG-1234"}},
		{"https://gitlab.com/acme/app/-/merge_requests/42", "", Resolution{Kind: "pr", PR: 42, Name: "mr-42", Branch: "mr-42", Ref: "merge-requests/42/head", URL: "https://gitlab.com/acme/app/-/merge_requests/42"}},
		{"Whatever Name", "name", Resolution{Kind: "name", Name: "whatever-name", Branch: "whatever-name"}},
	} {
		got, err := resolveInput(ctx, repo, tc.input, tc.kind)
		if err != nil {
			t.Fatalf("%q: %v", tc.input, err)
		}
		if got != tc.want {
			t.Errorf("%q:\n got %+v\nwant %+v", tc.input, got, tc.want)
		}
	}
	// A number without gh falls back to the PR's head ref.
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir())
	got, _ := resolveInput(ctx, repo, "#1234", "")
	if got.Kind != "pr" || got.Branch != "pr-1234" || got.Ref != "pull/1234/head" || got.Note == "" {
		t.Fatalf("#1234 without gh = %+v", got)
	}
}

func TestAWorktreeCanCheckOutAPullRequestsHead(t *testing.T) {
	ctx := context.Background()
	origin := gitRepo(t)
	// A PR's head lives only under refs/pull, as on GitHub.
	gitIn(t, origin, "checkout", "-q", "-b", "contributor-fix")
	os.WriteFile(filepath.Join(origin, "FIX"), []byte("fixed"), 0o644)
	gitIn(t, origin, "add", "FIX")
	gitIn(t, origin, "commit", "-q", "-m", "fix")
	gitIn(t, origin, "update-ref", "refs/pull/7/head", "HEAD")
	gitIn(t, origin, "checkout", "-q", "main")
	gitIn(t, origin, "branch", "-D", "contributor-fix")

	clone := filepath.Join(t.TempDir(), "app")
	gitIn(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	l := NewLocations(filepath.Join(t.TempDir(), "locations.json"))
	if _, err := l.Add(ctx, "app", clone); err != nil {
		t.Fatal(err)
	}
	wt, err := l.CreateWorktreeFrom(ctx, "app", WorktreeRequest{Name: "pr-7", Branch: "pr-7", PR: 7})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(wt.Path, "FIX")); err != nil || string(b) != "fixed" {
		t.Fatalf("the PR's change is not in the worktree: %v %q", err, b)
	}
}

// allowFileClones lets a test clone from a folder, which a box refuses.
func allowFileClones(t *testing.T) {
	old := GitProtocols
	GitProtocols += ":file"
	t.Cleanup(func() { GitProtocols = old })
}

// A clone link only gets network transports: not ext:: (a command) nor a
// path or file:// on the box (security audit L-8).
func TestCloneRefusesLocalAndCommandTransports(t *testing.T) {
	c, _ := servedBox(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := gitRepo(t)
	marker := filepath.Join(home, "ext-ran")
	for i, link := range []string{src, "file://" + src, "ext::sh -c touch% " + marker} {
		resp, err := c.Do(context.Background(), "POST", "/v1/locations/clone", strings.NewReader(`{"url":"`+link+`","parent":"~/work`+strconv.Itoa(i)+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		body := readAll(t, resp)
		if !strings.Contains(body, `"error"`) {
			t.Errorf("clone of %s: %s", link, body)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ext:: ran a command")
	}
}

func TestProjectsAreBrowsedClonedAndCreatedOverTheAPI(t *testing.T) {
	allowFileClones(t)
	c, _ := servedBox(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := gitRepo(t)
	work := filepath.Join(home, "work")
	os.MkdirAll(filepath.Join(work, "notes"), 0o755)
	os.MkdirAll(filepath.Join(work, ".hidden"), 0o755)

	// Clone, streaming progress, then it is a location.
	resp, err := c.Do(context.Background(), "POST", "/v1/locations/clone", strings.NewReader(`{"url":"`+src+`","parent":"~/work"}`))
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if !strings.Contains(body, `"done":true`) || strings.Contains(body, `"error"`) || !strings.Contains(body, `"name":"cal"`) {
		t.Fatalf("clone streamed %s", body)
	}

	var made Location
	if status := call(t, c, "POST", "/v1/locations/new", "", map[string]string{"name": "my-app"}, &made); status != 200 || !made.Repo || made.DefaultBranch != "main" {
		t.Fatalf("new: %d %+v", status, made)
	}
	if status := call(t, c, "POST", "/v1/locations/new", "", map[string]string{"name": "../escape"}, nil); status != 400 {
		t.Fatalf("a path as a name gave %d", status)
	}
	// The sample project: its files, committed, in ~/work/hello.
	var sample Location
	if status := call(t, c, "POST", "/v1/locations/new", "", map[string]string{"sample": "hello", "parent": "~/samples"}, &sample); status != 200 || sample.Name != "hello" || !sample.Repo {
		t.Fatalf("sample: %d %+v", status, sample)
	}
	for _, name := range []string{"README.md", "package.json", "greet.js", "greet.test.js", "server.js", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(sample.Path, name)); err != nil {
			t.Errorf("the sample has no %s: %v", name, err)
		}
	}
	if out, _ := exec.Command("git", "-C", sample.Path, "status", "--porcelain").CombinedOutput(); len(out) != 0 {
		t.Errorf("the sample is not committed: %s", out)
	}
	if status := call(t, c, "POST", "/v1/locations/new", "", map[string]string{"sample": "nope"}, nil); status != 400 {
		t.Fatalf("an unknown sample gave %d", status)
	}

	var f Folder
	call(t, c, "GET", "/v1/fs", "", nil, &f)
	names := []string{}
	for _, e := range f.Entries {
		names = append(names, e.Name)
		if e.Name == "cal" && !e.Git {
			t.Fatal("the clone is not marked as a git repository")
		}
	}
	if f.Path != work || strings.Join(names, ",") != "cal,my-app,notes" {
		t.Fatalf("listing %s = %v", f.Path, names)
	}

	var res Resolution
	call(t, c, "POST", "/v1/locations/my-app/resolve", "", map[string]string{"input": "add login page"}, &res)
	if res.Name != "add-login-page" || res.Base != "main" {
		t.Fatalf("resolve = %+v", res)
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
