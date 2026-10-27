package box

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/box/runs"
	"github.com/cosscom/shipyard/internal/events"
)

// prCheckout is a clone whose origin reads as remote, while its pushes go
// to a bare repository here (pushInsteadOf), with the branch committed.
// hook, when set, is the bare repository's post-receive: what a forge
// says back on a push.
func prCheckout(t *testing.T, remote, branch, hook string) (dir, bare string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	bare, dir = filepath.Join(root, "shop.git"), filepath.Join(root, "shop")
	run := func(in string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = in
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run(root, "init", "-q", "--bare", "-b", "main", bare)
	if hook != "" {
		os.WriteFile(filepath.Join(bare, "hooks", "post-receive"), []byte("#!/bin/sh\n"+hook), 0o755)
	}
	os.MkdirAll(dir, 0o755)
	run(dir, "init", "-q", "-b", "main")
	run(dir, "remote", "add", "origin", remote)
	run(dir, "config", "url."+bare+".pushInsteadOf", remote)
	os.WriteFile(filepath.Join(dir, "refunds.go"), []byte("package shop\n"), 0o644)
	run(dir, "add", ".")
	run(dir, "commit", "-q", "-m", "feat: partial refunds")
	run(dir, "checkout", "-q", "-b", branch)
	return dir, bare
}

// fakeGHCalls puts a gh on PATH that logs its arguments and answers
// `auth status` with authExit and `pr create` with prOut and prExit;
// `pr view` finds no pull request.
func fakeGHCalls(t *testing.T, authExit, prExit int, prOut string) func() []string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$*" >> "` + log + `"
case "$1 $2" in
  "auth status") exit ` + strconv.Itoa(authExit) + ` ;;
  "pr view") echo 'no pull requests found' >&2; exit 1 ;;
  "pr create") cat <<'OUT'
` + prOut + `
OUT
    exit ` + strconv.Itoa(prExit) + ` ;;
esac
exit 1
`
	os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(log)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func prHostBox(t *testing.T) *runHost {
	b := &Box{Name: "devbox", Locations: NewLocations(filepath.Join(t.TempDir(), "locations.json")), Events: &events.Bus{}}
	return &runHost{b: b, env: map[string]*runEnv{}}
}

func prStep(dir, branch string) *runs.StepCtx {
	return &runs.StepCtx{Run: runs.Summary{ID: "r_pr", Template: "attempts"}, Path: "3.t.0", Attempt: 1,
		Step: runs.Step{ID: "open", Kind: "pr", Draft: true, Title: "refunds", Text: "Picked from 2 attempts."},
		Vars: map[string]string{"pick.path": dir, "pick.branch": branch}}
}

// #138: on a Gitea remote the pr step never runs gh pr create. It pushes
// the branch, passes on the link Gitea printed, and is a skip that says
// why, which the run goes on from.
func TestThePRStepOnGiteaPushesAndSkips(t *testing.T) {
	calls := fakeGHCalls(t, 1, 1, "should not be called")
	dir, bare := prCheckout(t, "https://gitea.acme.test/acme/shop.git", "refunds-a2",
		`echo
echo "Create a new pull request for 'refunds-a2':"
echo "  https://gitea.acme.test/acme/shop/compare/main...refunds-a2"
echo`)
	res := prHostBox(t).pullRequest(context.Background(), prStep(dir, "refunds-a2"))
	t.Logf("status %s: %s", res.Status, res.Out)
	if res.Status != runs.Skipped || res.Err != "" {
		t.Fatalf("status %s, err %q", res.Status, res.Err)
	}
	for _, want := range []string{"This repository is on gitea.acme.test, not GitHub, so Shipyard didn't open a pull request.",
		"The branch refunds-a2 is pushed to origin; open one there: https://gitea.acme.test/acme/shop/compare/main...refunds-a2"} {
		if !strings.Contains(res.Out, want) {
			t.Fatalf("out %q lacks %q", res.Out, want)
		}
	}
	if res.Set["pr.skipped"] != "1" || res.Set["pr.link"] != "https://gitea.acme.test/acme/shop/compare/main...refunds-a2" {
		t.Fatalf("set %v", res.Set)
	}
	if out, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "refs/heads/refunds-a2").CombinedOutput(); err != nil {
		t.Fatalf("the branch was not pushed: %s", out)
	}
	got := calls()
	if len(got) != 1 || got[0] != "auth status --hostname gitea.acme.test" {
		t.Fatalf("gh calls %q", got)
	}
}

// On GitHub, a gh pr create that fails is the step failing (the engine
// makes it a publishing failure a person retries or finishes past), with
// gh's words kept and no credentials in them.
func TestThePRStepOnGitHubFailsWithGhsWords(t *testing.T) {
	calls := fakeGHCalls(t, 1, 1, "HTTP 401: Bad credentials (https://x-access-token:ghp_SECRET@github.com/acme/shop)")
	dir, _ := prCheckout(t, "https://github.com/acme/shop.git", "refunds-a2", "")
	res := prHostBox(t).pullRequest(context.Background(), prStep(dir, "refunds-a2"))
	t.Logf("status %s, err %q: %s", res.Status, res.Err, res.Out)
	if res.Status != runs.Failed || res.Err != "gh pr create failed" || !strings.Contains(res.Out, "HTTP 401: Bad credentials") {
		t.Fatalf("%+v", res)
	}
	if strings.Contains(res.Out, "ghp_SECRET") {
		t.Fatalf("a credential was kept: %q", res.Out)
	}
	var created bool
	for _, c := range calls() {
		if strings.HasPrefix(c, "pr create --head refunds-a2") && strings.HasSuffix(c, "--draft") {
			created = true
		}
		if strings.HasPrefix(c, "auth") {
			t.Fatalf("asked gh about github.com: %q", c)
		}
	}
	if !created {
		t.Fatalf("gh calls %q", calls())
	}
}

// A host gh is signed in to is GitHub Enterprise: the pull request opens
// there as on github.com.
func TestThePRStepOpensOnASignedInEnterpriseHost(t *testing.T) {
	fakeGHCalls(t, 0, 0, "https://ghe.acme.test/acme/shop/pull/12")
	dir, _ := prCheckout(t, "git@ghe.acme.test:acme/shop.git", "refunds-a2", "")
	res := prHostBox(t).pullRequest(context.Background(), prStep(dir, "refunds-a2"))
	if res.Status != runs.Succeeded || res.Set["pr.url"] != "https://ghe.acme.test/acme/shop/pull/12" {
		t.Fatalf("%+v", res)
	}
}

func TestRemoteHostsAndPushLinks(t *testing.T) {
	for in, want := range map[string]string{
		"https://gitea.acme.test/acme/shop.git":    "gitea.acme.test",
		"git@Gitea.Acme.test:acme/shop.git":        "gitea.acme.test",
		"ssh://git@forge.acme.test:2222/acme/shop": "forge.acme.test",
		"/srv/git/shop.git":                        "",
		"file:///srv/git/shop.git":                 "",
	} {
		if got, _ := remoteHost(in); got != want {
			t.Errorf("remoteHost(%q) = %q, want %q", in, got, want)
		}
	}
	gitlab := "remote: \nremote: To create a merge request for refunds, visit:\nremote:   https://gitlab.acme.test/acme/shop/-/merge_requests/new?merge_request%5Bsource_branch%5D=refunds\nremote: \nTo gitlab.acme.test:acme/shop.git\n * [new branch]      refunds -> refunds\n"
	if got := pushLink(gitlab); got != "https://gitlab.acme.test/acme/shop/-/merge_requests/new?merge_request%5Bsource_branch%5D=refunds" {
		t.Errorf("gitlab link %q", got)
	}
	if got := pushLink("remote:   https://me:tok@forge.acme.test/acme/shop/compare/main...x\n"); got != "https://forge.acme.test/acme/shop/compare/main...x" {
		t.Errorf("link with a credential %q", got)
	}
	if got := pushLink("To https://forge.acme.test/acme/shop\n * [new branch] x -> x\n"); got != "" {
		t.Errorf("a link from git itself %q", got)
	}
	if got := noPRText(forge{Local: true}, "x", ""); !strings.Contains(got, "origin is a folder on this box") {
		t.Errorf("local text %q", got)
	}
}
