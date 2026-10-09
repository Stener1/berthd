package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/team"
	"github.com/cosscom/shipyard/internal/team/teamtest"
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
 "box":{"script":"box/setup.sh","settings":{"PG_VERSION":"16","$why":"a comment"},"steps":[{"id":"tools","title":"Tools","detail":"jq","sudo":true},{"id":"db","title":"Database"}]},
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
	}, teamtest.Repo{Private: true, Author: "dana@acme.test"})
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
	if v.Commit == nil || len(v.Commit.SHA) != 40 || v.Commit.Short != v.Commit.SHA[:7] || v.Commit.Author != "dana" {
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
	// The plan has the team's steps, then Shipyard's own GitHub sign-in on the
	// box and, since the shared keys are op:// references, its 1Password
	// sign-in, each with its exact commands.
	if len(v.Steps) != 4 || v.Steps[0].ID != "tools" || !v.Steps[0].Sudo || v.Steps[2].ID != "github" || !v.Steps[2].Berth ||
		v.Steps[3].ID != "1password" || !v.Steps[3].Berth || !strings.Contains(strings.Join(v.Steps[3].Commands, " "), "berthd secret signin") {
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
	r, err := readTeam(ctx, gh, acmeSrc, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.accept(r, "devbox"); err != nil {
		t.Fatal(err)
	}
	acc, _ := a.accepted(acmeSrc)
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
	if acc2, _ := a.accepted(acmeSrc); acc2.Commit != acc.Commit {
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
		got = box.TeamBundle{}
		json.Unmarshal(raw, &got)
		json.NewEncoder(w).Encode(box.TeamStatus{ID: got.ID, Phase: "steps", Steps: []box.TeamStepStatus{}, Projects: []box.TeamProjectStatus{}, KeysSet: []string{}})
	}))
	var retried string
	var noTmux atomic.Bool
	b.server.Handle("POST /v1/team/{id}/retry", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if noTmux.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "tmux is not installed on this box, and Shipyard runs the team setup's steps in a terminal there (tmux): install it with `sudo apt install tmux`, then set up again", "code": "tmux_missing"})
			return
		}
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
	if got.ID != "acme" || got.Commit != v.Commit.SHA || got.Script != "box/setup.sh" || len(got.Steps) != 2 || !got.GitHub || !got.OnePassword || len(got.Files) != 6 {
		t.Fatalf("bundle: %+v", got)
	}
	// box.settings go to the box (its comments don't).
	if len(got.Settings) != 1 || got.Settings["PG_VERSION"] != "16" {
		t.Fatalf("settings: %v", got.Settings)
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
	if acc, _ := (&Agent{cfg: Config{Dir: a.dir}}).accepted(acmeSrc); acc == nil || acc.Commit != v.Commit.SHA || acc.Box != boxName {
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
	g.Repo("northwind/shop.app", map[string]string{"README.md": "shop\n"}, teamtest.Repo{})
	code, body = uiPost(t, a, "/v1/team/northwind/setup", tok, TeamSetupRequest{Box: boxName, Repos: []string{"northwind/web", "northwind/shop.app"}})
	if code != 200 || got.ID != "northwind" || len(got.Projects) != 2 || got.Projects[0].Source != "repo" || got.Projects[0].TrustHash == "" || len(got.Steps) != 0 || !got.GitHub || got.OnePassword {
		t.Fatalf("picker: %d %s %+v", code, body, got)
	}
	// A repository whose name can't be part of a URL gets a project id
	// that can, and keeps its folder's name.
	if p := got.Projects[1]; p.ID != "shop-app" || p.Path != "~/code/shop.app" || p.Repo != "northwind/shop.app" {
		t.Fatalf("shop.app: %+v", p)
	}
	// A box without tmux says so with its code, so the app can say how to
	// install it.
	noTmux.Store(true)
	code, body = uiPost(t, a, "/v1/team/acme/retry", tok, map[string]string{"from": "db"})
	if code != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"tmux_missing"`) || !strings.Contains(body, "sudo apt install tmux") {
		t.Fatalf("tmux missing: %d %s", code, body)
	}
}

var acmeSrc = team.Source{Owner: "acme", Repo: team.Repo}

// A team setup read from a link: another repository, a branch and a
// folder, before the org publishes its own .berth.
func TestATeamSetupLoadsFromADirectLink(t *testing.T) {
	g, _ := acmeGitHub(t)
	g.Repo("sean/kits", map[string]string{"README.md": "kits\n"}, teamtest.Repo{})
	draft := strings.Replace(acmeTeamJSON("./kits/web"), `"name":"Acme"`, `"name":"Acme (draft)"`, 1)
	commit := g.Repo("sean/kits", map[string]string{
		"team/team.json":         draft,
		"team/box/setup.sh":      acmeSetupSh,
		"team/box/init-api.sh":   "#!/bin/sh\n",
		"team/kits/web/kit.json": `{"id":"acme-web","name":"Acme web","config":{}}`,
		"team/kits/api/kit.json": `{"id":"acme-api","name":"Acme API","config":{}}`,
	}, teamtest.Repo{Branch: "team-setup", Author: "sean@example.test"})
	a := testAgent(t)
	ctx := context.Background()
	link := "https://github.com/sean/kits/tree/team-setup/team"
	v, err := a.teamView(ctx, link, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "found" || v.Source.Kind != "link" || v.Source.Repo != "sean/kits" || v.Source.RefKind != "branch" ||
		v.Source.Label != "sean/kits · team-setup branch · team/" || v.Source.Key != "github.com/sean/kits/tree/team-setup/team" {
		t.Fatalf("source: %+v", v.Source)
	}
	// The card names the repository it came from, not the org it is for.
	if v.Org.Login != "sean" || v.Setup.Org != "acme" || v.Setup.Name != "Acme (draft)" || v.Commit.SHA != commit || v.Commit.Author != "sean" {
		t.Fatalf("org %+v setup %s commit %+v", v.Org, v.Setup.Org, v.Commit)
	}
	if v.Access.Readable != 3 || v.Files[0].Path != "team.json" || v.Steps[0].ID != "tools" {
		t.Fatalf("access %+v files %+v", v.Access, v.Files)
	}
	// Accepting it pins the branch's commit; a newer one is an update.
	gh, _ := newGH()
	src, _ := team.ParseSource(link)
	r, err := readTeam(ctx, gh, src, commit[:7])
	if err != nil || r.commit.SHA != commit {
		t.Fatal(err)
	}
	if err := a.accept(r, "devbox"); err != nil {
		t.Fatal(err)
	}
	g.Repo("sean/kits", map[string]string{"team/box/setup.sh": acmeSetupSh + "# newer\n"}, teamtest.Repo{Branch: "team-setup"})
	acc, _ := a.accepted(src)
	if acc == nil || acc.Source != src.String() {
		t.Fatalf("accepted %+v", acc)
	}
	u, _, err := teamUpdate(ctx, gh, acc)
	if err != nil || u == nil || len(u.Changes) != 1 || u.Changes[0].ID != "box/setup.sh" {
		t.Fatalf("update %+v %v", u, err)
	}
	// Other forms of link, and one that can't be read.
	for _, l := range []string{"github.com/sean/kits@team-setup", "berth://team?src=github.com%2Fsean%2Fkits%2Ftree%2Fteam-setup%2Fteam"} {
		if _, err := team.ParseSource(l); err != nil {
			t.Fatal(l, err)
		}
	}
	v, err = a.teamView(ctx, "github.com/sean/nothing-here", "", false)
	if err != nil || v.State != "unreadable" || len(v.Repos) != 0 {
		t.Fatalf("unreadable link: %+v %v", v, err)
	}
	// The org's own .berth is still checked to be the org's.
	v, _ = a.teamView(ctx, "acme", "", false)
	if v.Source.Kind != "org" || v.Source.Label != "acme/.berth" {
		t.Fatalf("org source %+v", v.Source)
	}
}

func TestSetupFromALinkThroughTheRoutes(t *testing.T) {
	g, _ := acmeGitHub(t)
	g.Repo("sean/kits", map[string]string{"README.md": "x"}, teamtest.Repo{})
	g.Repo("sean/kits", map[string]string{"team.json": acmeTeamJSON("./kits/web"), "box/setup.sh": acmeSetupSh, "box/init-api.sh": "#!/bin/sh\n",
		"kits/web/kit.json": `{"id":"acme-web","name":"W","config":{}}`, "kits/api/kit.json": `{"id":"acme-api","name":"A","config":{}}`}, teamtest.Repo{Branch: "draft"})
	b := newBox(t)
	var got box.TeamBundle
	b.server.Handle("POST /v1/team", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = box.TeamBundle{}
		json.Unmarshal(raw, &got)
		json.NewEncoder(w).Encode(box.TeamStatus{ID: got.ID, Phase: "steps"})
	}))
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	st, _ := a.client.Status(context.Background())
	key := url.PathEscape("github.com/sean/kits@draft")
	resp, body := uiCall(t, a, http.MethodGet, "/v1/team/"+key, tok)
	var v TeamView
	if json.Unmarshal([]byte(body), &v); resp.StatusCode != 200 || v.Source.Label != "sean/kits · draft branch" {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	code, body := uiPost(t, a, "/v1/team/"+key+"/setup", tok, TeamSetupRequest{Box: st.Boxes[0].Name, Commit: v.Commit.SHA})
	if code != 200 || got.ID != "acme" || got.Org != "acme" || got.Commit != v.Commit.SHA {
		t.Fatalf("%d %s %+v", code, body, got)
	}
	_, body = uiCall(t, a, http.MethodGet, "/v1/team", tok)
	if !strings.Contains(body, `"key":"github.com/sean/kits@draft"`) {
		t.Fatalf("accepted: %s", body)
	}
}

// Skipping 1Password: no op step, no op:// reference in any project's
// config (so nothing on the box ever calls op for them), the shared keys
// typed instead where they were, and the rest left for later.
func TestSkippingOnePasswordNeverReachesOp(t *testing.T) {
	g, kitCommit := acmeGitHub(t)
	b := newBox(t)
	var got box.TeamBundle
	b.server.Handle("POST /v1/team", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = box.TeamBundle{}
		json.Unmarshal(raw, &got)
		json.NewEncoder(w).Encode(box.TeamStatus{ID: got.ID, Phase: "steps", Steps: []box.TeamStepStatus{}, Projects: []box.TeamProjectStatus{}, KeysSet: []string{}})
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
	// The page can ask for the 1Password keys by name; the team lets you skip.
	if len(v.Keys.OnePassword) != 2 || v.Keys.OnePassword[0].Key != "DAILY" || v.Keys.OnePassword[1].Key != "STRIPE_KEY" || v.Keys.OnePasswordRequired {
		t.Fatalf("1Password keys: %+v", v.Keys)
	}
	code, body := uiPost(t, a, "/v1/team/acme/setup", tok, TeamSetupRequest{Box: boxName, Commit: v.Commit.Short, SkipOnePassword: true,
		Keys: map[string]map[string]string{"web": {"STRIPE_KEY": "sk_test_typed", "DAILY": "", "MAIL_KEY": ""}}})
	if code != 200 {
		t.Fatalf("setup: %d %s", code, body)
	}
	if got.OnePassword || !got.OnePasswordSkipped {
		t.Fatalf("the 1Password step is still there: %+v", got)
	}
	for _, p := range got.Projects {
		for k, val := range p.Env {
			if strings.HasPrefix(val, "op://") {
				t.Fatalf("%s/%s is still a 1Password reference: %s", p.ID, k, val)
			}
		}
	}
	var web box.TeamProjectPlan
	for _, p := range got.Projects {
		if p.ID == "web" {
			web = p
		}
	}
	if web.Env["STRIPE_KEY"] != "sk_test_typed" || len(web.Env) != 1 {
		t.Fatalf("web env: %v", web.Env)
	}
	// The references wait aside for Use 1Password; every key is listed, so
	// the box can say which are missing.
	if web.Deferred["STRIPE_KEY"] != "op://Dev/Stripe/key" || web.Deferred["DAILY"] != "op://Dev/Daily/key" || strings.Join(web.Keys, ",") != "DAILY,MAIL_KEY,STRIPE_KEY" {
		t.Fatalf("web: deferred %v keys %v", web.Deferred, web.Keys)
	}

	// A team that requires 1Password: skipping is refused, and says why.
	g.Repo("acme/.berth", map[string]string{"team.json": strings.Replace(acmeTeamJSON("https://github.com/acme/kits/tree/"+kitCommit[:7]+"/tools"), `"contact":"#onboarding",`, `"contact":"#onboarding","onepassword":"required",`, 1)}, teamtest.Repo{})
	_, body = uiCall(t, a, http.MethodGet, "/v1/team/acme", tok)
	v = TeamView{}
	json.Unmarshal([]byte(body), &v)
	if !v.Keys.OnePasswordRequired {
		t.Fatalf("required not shown: %+v", v.Keys)
	}
	code, body = uiPost(t, a, "/v1/team/acme/setup", tok, TeamSetupRequest{Box: boxName, Commit: v.Commit.Short, SkipOnePassword: true})
	if code == 200 || !strings.Contains(body, "needs 1Password") {
		t.Fatalf("skipping a required 1Password: %d %s", code, body)
	}
}

func TestThePageSeesClonesTheBoxHasAndChoosesWhichToUse(t *testing.T) {
	acmeGitHub(t)
	b := newBox(t)
	var asked url.Values
	b.server.Handle("GET /v1/team/{id}/existing", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query()
		json.NewEncoder(w).Encode(box.ExistingResult{Looked: []string{"~/code", "~/work"}, Projects: []box.ExistingProject{
			{ID: "web", Repo: "acme/web", Path: "~/code/web", Clones: []box.ExistingClone{{Path: "/home/dev/work/acme-web", Display: "~/work/acme-web", Branch: "feat/x", Dirty: 3}}},
			{ID: "api", Repo: "acme/api", Path: "~/code/api", Clones: []box.ExistingClone{}},
		}})
	}))
	var got box.TeamBundle
	b.server.Handle("POST /v1/team", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = box.TeamBundle{}
		json.Unmarshal(raw, &got)
		json.NewEncoder(w).Encode(box.TeamStatus{ID: got.ID, Phase: "steps", Steps: []box.TeamStepStatus{}, Projects: []box.TeamProjectStatus{}, KeysSet: []string{}})
	}))
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	st, _ := a.client.Status(context.Background())
	boxName := st.Boxes[0].Name

	_, body := uiCall(t, a, http.MethodGet, "/v1/team/acme?box="+url.QueryEscape(boxName), tok)
	var v TeamView
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.Commit == nil {
		t.Fatalf("view: %s", body)
	}
	// The box was asked about the projects this account can read.
	if p := strings.Join(asked["project"], " "); !strings.Contains(p, "web:acme/web:~/code/web") || strings.Contains(p, "acme/secret") {
		t.Fatalf("asked the box about %q", p)
	}
	var web TeamProjectView
	for _, p := range v.Projects {
		if p.ID == "web" {
			web = p
		}
	}
	if len(web.Existing) != 1 || web.Existing[0].Display != "~/work/acme-web" || web.Existing[0].Dirty != 3 || v.Scanned == nil || len(v.Scanned.Looked) != 2 {
		t.Fatalf("web's clones: %+v scanned %+v", web.Existing, v.Scanned)
	}
	// The choice goes to the box with the setup: web uses the clone, api
	// is cloned fresh, tools is left to the box.
	code, body := uiPost(t, a, "/v1/team/acme/setup", tok, TeamSetupRequest{Box: boxName, Commit: v.Commit.Short, Use: map[string]string{"web": "~/work/acme-web", "api": ""}})
	if code != 200 {
		t.Fatalf("setup: %d %s", code, body)
	}
	plans := map[string]box.TeamProjectPlan{}
	for _, p := range got.Projects {
		plans[p.ID] = p
	}
	if plans["web"].Use != "~/work/acme-web" || plans["web"].Fresh || !plans["api"].Fresh || plans["api"].Use != "" || plans["tools"].Fresh || plans["tools"].Use != "" {
		t.Fatalf("plans: %+v", plans)
	}
}
