package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sean-brydon/berthd/internal/box"
	"github.com/sean-brydon/berthd/internal/team/teamtest"
)

const acmeSetupSh = `#!/bin/sh
set -eu
step_tools() {
  sudo apt-get install -y jq
}
step_db() {
  docker run -d --name acme-db postgres:16
}
case "${1:-all}" in
  check) exit 1 ;;
  plan) echo tools sudo; echo db ;;
  *) "step_$1" ;;
esac
`

const webConfig = `{"setup":"pnpm install","services":[{"name":"web","run":"pnpm dev","title":"Next.js"}]}`

func acmeTeamJSON(kitLink string) string {
	return `{"schema":"berth.team/v1","id":"acme","name":"Acme","org":"acme","contact":"#onboarding",
 "box":{"script":"box/setup.sh","steps":[{"id":"tools","title":"Tools","detail":"jq","sudo":true},{"id":"db","title":"Database"}]},
 "projects":[
  {"id":"web","repo":"acme/web","required":true,"kit":"./kits/web","first_task":"Fix a bug"},
  {"id":"api","repo":"acme/api","kit":"./kits/api","init":"box/init-api.sh"},
  {"id":"tools","repo":"acme/tools","kit":"` + kitLink + `"},
  {"id":"secret","repo":"acme/secret"}],
 "keys":{"web":{"from":".env.example","shared":{"STRIPE_KEY":"op://Dev/Stripe/key","DAILY":"op://Dev/Daily/key"},"ask":["MAIL_KEY"]}}}`
}

// acmeGitHub is a fake GitHub with the acme org: a team setup, three
// repositories the engineer can read and one they can't, and a kit kept in
// a repository of its own.
func acmeGitHub(t *testing.T) (*teamtest.GitHub, string) {
	g := teamtest.New(t)
	g.SignIn("engineer")
	g.Org("acme", "Acme Inc", true)
	kitCommit := g.Repo("acme/kits", map[string]string{
		"tools/kit.json":  `{"id":"acme-tools","name":"Acme tools","config":{"setup":"make","services":[{"name":"docs","run":"make docs"}]}}`,
		"tools/README.md": "the tools kit",
	}, teamtest.Repo{})
	g.Repo("acme/.berth", map[string]string{
		"team.json":         acmeTeamJSON("https://github.com/acme/kits/tree/" + kitCommit[:7] + "/tools"),
		"box/setup.sh":      acmeSetupSh,
		"box/init-api.sh":   "#!/bin/sh\nmake db\n",
		"kits/web/kit.json": `{"id":"acme-web","name":"Acme web","config":{"setup":"pnpm i"}}`,
		"kits/api/kit.json": `{"id":"acme-api","name":"Acme API","config":{"services":[{"name":"api","run":"go run ."}]}}`,
		"README.md":         "# acme/.berth\n",
	}, teamtest.Repo{Private: true, Author: "keith@acme.test"})
	g.Repo("acme/web", map[string]string{".berth/config.json": webConfig, ".env.example": "STRIPE_KEY=\nDAILY=\nMAIL_KEY=\nPORT=3000\n"}, teamtest.Repo{Private: true})
	g.Repo("acme/api", map[string]string{"main.go": "package main\n"}, teamtest.Repo{})
	g.Repo("acme/tools", map[string]string{"Makefile": "all:\n"}, teamtest.Repo{})
	g.Repo("acme/secret", map[string]string{"x": "y"}, teamtest.Repo{NoAccess: true})
	return g, kitCommit
}

func testAgent(t *testing.T) *Agent {
	return &Agent{cfg: Config{Dir: t.TempDir(), UserDir: t.TempDir()}}
}

func TestGitHubStateFollowsTheLaptopsGH(t *testing.T) {
	g := teamtest.New(t)
	if st := githubState(context.Background()); st.State != "signed-out" {
		t.Fatalf("signed out: %+v", st)
	}
	g.SignIn("engineer")
	st := githubState(context.Background())
	if st.State != "ready" || st.Login != "engineer" {
		t.Fatalf("signed in: %+v", st)
	}
	// Without gh at all, it says how to install it.
	t.Setenv("PATH", t.TempDir())
	old := ghDirs
	ghDirs = nil
	t.Cleanup(func() { ghDirs = old })
	t.Setenv("HOME", t.TempDir())
	st = githubState(context.Background())
	if st.State != "missing" || st.Install == nil || st.Install.Command == "" {
		t.Fatalf("missing: %+v", st)
	}
}

func TestConnectGitHubOpensATerminalRunningGHAuthLogin(t *testing.T) {
	teamtest.New(t)
	var opened string
	old := openTerminal
	openTerminal = func(script string) error { opened = script; return nil }
	t.Cleanup(func() { openTerminal = old })
	a := testAgent(t)
	ok, err := a.openGHLogin()
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	b, _ := os.ReadFile(opened)
	if !strings.Contains(string(b), "auth login --hostname github.com") {
		t.Fatalf("the terminal runs:\n%s", b)
	}
}

func TestTeamViewReadsTheSetupAndChecksAccessPerRepo(t *testing.T) {
	g, kitCommit := acmeGitHub(t)
	a := testAgent(t)
	v, err := a.teamView(context.Background(), "acme", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "found" || v.Org.Name != "Acme Inc" || !v.Org.Verified || v.Repo == nil || !v.Repo.Private {
		t.Fatalf("view: %+v", v)
	}
	if v.Commit == nil || len(v.Commit.SHA) != 40 || v.Commit.Short != v.Commit.SHA[:7] || v.Commit.Author != "keith" {
		t.Fatalf("commit: %+v", v.Commit)
	}
	// The access check is reading each repository: no membership check.
	if v.Access.Total != 4 || v.Access.Readable != 3 || len(v.Access.Missing) != 1 || v.Access.Missing[0] != "acme/secret" {
		t.Fatalf("access: %+v", v.Access)
	}
	for _, call := range g.Calls() {
		if strings.Contains(call, "members") || strings.Contains(call, "memberships") {
			t.Fatalf("checked membership: %s", call)
		}
	}
	byID := map[string]TeamProjectView{}
	for _, p := range v.Projects {
		byID[p.ID] = p
	}
	web := byID["web"]
	sum := sha256.Sum256([]byte(webConfig))
	if web.Source != "repo" || web.ConfigHash != hex.EncodeToString(sum[:]) || web.Kit != nil || len(web.Services) != 1 || web.Services[0] != "Next.js" {
		t.Fatalf("web, whose own config wins over the team kit: %+v", web)
	}
	if web.Keys == nil || web.Keys.Listed != 4 || len(web.Keys.Shared) != 2 || web.Keys.Ask[0] != "MAIL_KEY" {
		t.Fatalf("web keys: %+v", web.Keys)
	}
	api := byID["api"]
	if api.Source != "kit" || api.Kit == nil || api.Kit.ID != "acme-api" || api.Kit.Hash == "" || api.Services[0] != "api" || api.Init != "box/init-api.sh" {
		t.Fatalf("api, from a kit kept in .berth: %+v %+v", api, api.Kit)
	}
	tools := byID["tools"]
	if tools.Source != "kit" || tools.Kit == nil || tools.Kit.ID != "acme-tools" || tools.Kit.Commit != kitCommit[:7] {
		t.Fatalf("tools, from a pinned kit link read with gh: %+v %+v", tools, tools.Kit)
	}
	if s := byID["secret"]; s.Access || s.Error == "" {
		t.Fatalf("secret: %+v", s)
	}
	if v.Keys.Shared != 2 || len(v.Keys.Ask) != 1 || v.Keys.Ask[0].Key != "MAIL_KEY" {
		t.Fatalf("keys: %+v", v.Keys)
	}
	// The plan has the team's steps, then Berth's own GitHub sign-in on the
	// box, each with its exact commands.
	if len(v.Steps) != 3 || v.Steps[0].ID != "tools" || !v.Steps[0].Sudo || v.Steps[2].ID != "github" || !v.Steps[2].Berth {
		t.Fatalf("steps: %+v", v.Steps)
	}
	if !strings.Contains(strings.Join(v.Steps[0].Commands, "\n"), "sudo apt-get install -y jq") {
		t.Fatalf("tools' commands: %v", v.Steps[0].Commands)
	}
	if v.Files[0].Path != "team.json" || len(v.Files) != 6 {
		t.Fatalf("files: %+v", v.Files)
	}
	// Nothing of it is left behind.
	if ents, _ := os.ReadDir(filepath.Join(a.cfg.Dir, "team", "tmp")); len(ents) != 0 {
		t.Fatalf("left %d folders in tmp", len(ents))
	}
}

func TestTeamViewWithoutBerthRepoListsReposToPick(t *testing.T) {
	g := teamtest.New(t)
	g.SignIn("engineer")
	g.Org("northwind", "Northwind Labs", false)
	g.Repo("northwind/web", map[string]string{".berth/config.json": `{"ports":2}`}, teamtest.Repo{})
	g.Repo("northwind/site", map[string]string{"index.html": "hi"}, teamtest.Repo{})
	g.Repo("northwind/hidden", map[string]string{"x": "y"}, teamtest.Repo{NoAccess: true})
	a := testAgent(t)
	v, err := a.teamView(context.Background(), "northwind", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "none" || len(v.Repos) != 2 || v.Repos[0].FullName != "northwind/web" || !v.Repos[0].HasBerth || v.Repos[1].HasBerth {
		t.Fatalf("none: %+v", v)
	}
	// From a team link, a .berth that can't be read is "unreadable".
	v, _ = a.teamView(context.Background(), "northwind", "", true)
	if v.State != "unreadable" || len(v.Repos) != 0 {
		t.Fatalf("unreadable: %+v", v)
	}
	v, _ = a.teamView(context.Background(), "nobody", "", false)
	if v.State != "no-org" {
		t.Fatalf("no org: %+v", v)
	}
}

func TestATeamSetupThatBreaksTheRulesIsRefused(t *testing.T) {
	g := teamtest.New(t)
	g.SignIn("engineer")
	g.Org("acme", "Acme", false)
	g.Repo("acme/.berth", map[string]string{"team.json": strings.Replace(acmeTeamJSON("./kits/web"), `"op://Dev/Stripe/key"`, `"sk_live_abc"`, 1)}, teamtest.Repo{})
	_, err := testAgent(t).teamView(context.Background(), "acme", "", false)
	if err == nil || !strings.Contains(err.Error(), "never a value") {
		t.Fatalf("a secret in git: %v", err)
	}
	g.Repo("acme/.berth", map[string]string{"team.json": strings.Replace(acmeTeamJSON("./kits/web"), `"org":"acme"`, `"org":"other"`, 1)}, teamtest.Repo{})
	if _, err := testAgent(t).teamView(context.Background(), "acme", "", false); err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("another org's setup: %v", err)
	}
	g.Repo("acme/.berth", map[string]string{"team.json": acmeTeamJSON("./kits/web")}, teamtest.Repo{})
	if _, err := testAgent(t).teamView(context.Background(), "acme", "", false); err == nil || !strings.Contains(err.Error(), "box/setup.sh") {
		t.Fatalf("a script it doesn't carry: %v", err)
	}
}

func TestANewerCommitIsAnUpdateToReviewNeverRun(t *testing.T) {
	g, _ := acmeGitHub(t)
	a := testAgent(t)
	ctx := context.Background()
	gh, _ := newGH()
	r, err := readTeam(ctx, gh, "acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.accept(r, "devbox"); err != nil {
		t.Fatal(err)
	}
	acc, _ := a.accepted("acme")
	if u, _, err := teamUpdate(ctx, gh, acc); err != nil || u != nil {
		t.Fatalf("no update yet: %+v %v", u, err)
	}
	// The team adds a step that needs sudo and a repository.
	newJSON := strings.Replace(acmeTeamJSON("./kits/web"), `{"id":"db","title":"Database"}`, `{"id":"db","title":"Database"},{"id":"node","title":"Node 22","sudo":true}`, 1)
	newJSON = strings.Replace(newJSON, `{"id":"secret","repo":"acme/secret"}`, `{"id":"secret","repo":"acme/secret"},{"id":"video","repo":"acme/video"}`, 1)
	to := g.Repo("acme/.berth", map[string]string{"team.json": newJSON, "box/setup.sh": acmeSetupSh + "step_node() {\n  sudo true\n}\n"}, teamtest.Repo{})
	u, _, err := teamUpdate(ctx, gh, acc)
	if err != nil || u == nil {
		t.Fatalf("update: %+v %v", u, err)
	}
	if u.To != to[:7] || u.From != acc.Commit[:7] || u.Commits != 1 || len(u.Sudo) != 1 || u.Sudo[0] != "Node 22" {
		t.Fatalf("update: %+v", u)
	}
	got := map[string]bool{}
	for _, c := range u.Changes {
		got[c.Kind+" "+c.Area+" "+c.ID] = true
	}
	for _, want := range []string{"add step node", "add project video", "change file box/setup.sh", "change project tools"} {
		if !got[want] {
			t.Errorf("missing %s in %+v", want, u.Changes)
		}
	}
	// The page shows the same diff, and the watcher announces it once.
	v, err := a.teamView(ctx, "acme", "", false)
	if err != nil || v.Update == nil || v.Update.To != u.To || v.Accepted == nil || v.Accepted.Box != "devbox" {
		t.Fatalf("view: %+v %v", v.Update, err)
	}
	a.cfg.Now = time.Now
	ch, cancel := a.bus.Subscribe()
	defer cancel()
	a.checkTeamUpdates(ctx)
	a.checkTeamUpdates(ctx)
	var events []Event
	for len(ch) > 0 {
		events = append(events, <-ch)
	}
	if len(events) != 1 || events[0].Type != "team.update" {
		t.Fatalf("events: %+v", events)
	}
	// Nothing was accepted by itself.
	if acc2, _ := a.accepted("acme"); acc2.Commit != acc.Commit {
		t.Fatal("the update was applied by itself")
	}
}

func TestTeamRoutesNeedGitHubConnected(t *testing.T) {
	g := teamtest.New(t)
	b := newBox(t)
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	resp, body := uiCall(t, a, http.MethodGet, "/v1/team/acme", tok)
	if resp.StatusCode != http.StatusPreconditionFailed || !strings.Contains(body, "gh_signed_out") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	resp, body = uiCall(t, a, http.MethodGet, "/v1/github", tok)
	var st GitHubState
	json.Unmarshal([]byte(body), &st)
	if resp.StatusCode != 200 || st.State != "signed-out" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	g.SignIn("engineer")
	g.Org("acme", "Acme", false)
	resp, body = uiCall(t, a, http.MethodGet, "/v1/team/acme", tok)
	if resp.StatusCode != 200 || !strings.Contains(body, `"state":"none"`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

func uiPost(t *testing.T, a *runningAgent, path, token string, body any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, "http://"+a.ui+path, strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func TestSetupSendsTheReviewedCommitToTheBoxAndTrustsAtIt(t *testing.T) {
	g, _ := acmeGitHub(t)
	b := newBox(t)
	var got box.TeamBundle
	b.server.Handle("POST /v1/team", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &got)
		json.NewEncoder(w).Encode(box.TeamStatus{ID: got.ID, Phase: "steps", Steps: []box.TeamStepStatus{}, Projects: []box.TeamProjectStatus{}, KeysSet: []string{}})
	}))
	var retried string
	b.server.Handle("POST /v1/team/{id}/retry", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		retried = r.PathValue("id") + " " + string(raw)
		json.NewEncoder(w).Encode(box.TeamStatus{ID: r.PathValue("id"), Phase: "steps"})
	}))
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	st, _ := a.client.Status(context.Background())
	boxName := st.Boxes[0].Name

	_, body := uiCall(t, a, http.MethodGet, "/v1/team/acme", tok)
	var v TeamView
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.Commit == nil {
		t.Fatalf("view: %s", body)
	}
	code, body := uiPost(t, a, "/v1/team/acme/setup", tok, TeamSetupRequest{Box: boxName, Commit: v.Commit.Short,
		Keys: map[string]map[string]string{"web": {"MAIL_KEY": "SG.mine", "STRIPE_KEY": "typed-but-shared"}}})
	if code != 200 {
		t.Fatalf("setup: %d %s", code, body)
	}
	if got.ID != "acme" || got.Commit != v.Commit.SHA || got.Script != "box/setup.sh" || len(got.Steps) != 2 || !got.GitHub || len(got.Files) != 6 {
		t.Fatalf("bundle: %+v", got)
	}
	plans := map[string]box.TeamProjectPlan{}
	for _, p := range got.Projects {
		plans[p.ID] = p
	}
	if _, ok := plans["secret"]; ok || len(plans) != 3 {
		t.Fatalf("a repository nobody can read was sent: %v", plans)
	}
	web := plans["web"]
	if web.Source != "repo" || web.TrustHash != v.Projects[0].ConfigHash || web.Kit != nil || web.URL != "https://github.com/acme/web.git" || web.Path != "~/code/web" {
		t.Fatalf("web: %+v", web)
	}
	// Shared keys are references; only asked keys take what was typed.
	if web.Env["STRIPE_KEY"] != "op://Dev/Stripe/key" || web.Env["MAIL_KEY"] != "SG.mine" || len(web.Env) != 3 {
		t.Fatalf("web env: %v", web.Env)
	}
	api := plans["api"]
	if api.Kit == nil || api.Kit.Kit.ID != "acme-api" || api.Kit.Hash != v.Projects[1].Kit.Hash || api.Init != "box/init-api.sh" {
		t.Fatalf("api: %+v", api)
	}
	if acc, _ := (&Agent{cfg: Config{Dir: a.dir}}).accepted("acme"); acc == nil || acc.Commit != v.Commit.SHA || acc.Box != boxName {
		t.Fatalf("accepted: %+v", acc)
	}
	// A commit nobody reviewed is refused.
	g.Repo("acme/.berth", map[string]string{"README.md": "changed\n"}, teamtest.Repo{})
	code, body = uiPost(t, a, "/v1/team/acme/setup", tok, TeamSetupRequest{Box: boxName, Commit: "0000000"})
	if code == 200 {
		t.Fatalf("an unreviewed commit ran: %s", body)
	}
	code, body = uiPost(t, a, "/v1/team/acme/retry", tok, map[string]string{"from": "db"})
	if code != 200 || !strings.HasPrefix(retried, `acme {"from":"db"}`) {
		t.Fatalf("retry: %d %s %q", code, body, retried)
	}
	// The picker: an org without .berth sets up the repos chosen.
	g.Org("northwind", "Northwind", false)
	g.Repo("northwind/web", map[string]string{".berth/config.json": `{"ports":2}`}, teamtest.Repo{})
	code, body = uiPost(t, a, "/v1/team/northwind/setup", tok, TeamSetupRequest{Box: boxName, Repos: []string{"northwind/web"}})
	if code != 200 || got.ID != "northwind" || len(got.Projects) != 1 || got.Projects[0].Source != "repo" || got.Projects[0].TrustHash == "" || len(got.Steps) != 0 || !got.GitHub {
		t.Fatalf("picker: %d %s %+v", code, body, got)
	}
}
