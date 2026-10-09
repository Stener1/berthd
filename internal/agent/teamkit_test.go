package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/team"
	"github.com/cosscom/shipyard/internal/team/teamtest"
	"github.com/cosscom/shipyard/internal/wire"
)

const kitTeamJSON = `{"schema":"berth.team/v1","id":"acme","name":"Acme","org":"acme",
 "box":{"script":"box/setup.sh","steps":[{"id":"tools","title":"Tools","sudo":true}]},
 "projects":[{"id":"api","repo":"acme/api","kit":"./kits/api","init":"box/init-api.sh","init_detail":"makes .env from .env.example"}],
 "keys":{"api":{"shared":{"STRIPE_KEY":"op://Dev/Stripe/key"},"ask":["MAIL_KEY"]}}}`

const kitAPIManifest = `{"id":"acme-api","name":"Acme API","requires":[{"tool":"git"},{"tool":"pg_dump_nowhere","hint":"the full team setup installs it"}],
 "config":{"setup":"./setup.sh","services":[{"name":"api","run":"go run ."}]}}`

// kitGitHub is acme with a team setup whose api project has a kit, keys
// and an init.
func kitGitHub(t *testing.T) *teamtest.GitHub {
	g := teamtest.New(t)
	g.SignIn("engineer")
	g.Org("acme", "Acme", true)
	g.Repo("acme/.berth", map[string]string{
		"team.json":          kitTeamJSON,
		"box/setup.sh":       "#!/bin/sh\nstep_tools() {\n  sudo true\n}\n",
		"box/init-api.sh":    "#!/bin/sh\ntouch .env\n",
		"kits/api/kit.json":  kitAPIManifest,
		"kits/api/setup.sh":  "#!/bin/sh\n[ -n \"$STRIPE_KEY\" ] || echo no stripe\n",
		"kits/api/README.md": "acme api kit\n",
	}, teamtest.Repo{Private: true})
	g.Repo("acme/api", map[string]string{"main.go": "package main\n"}, teamtest.Repo{})
	return g
}

// kitBox is a real box with acme/api added by hand, and a worktree of it,
// behind a test box server.
func kitBox(t *testing.T) (*box.Box, *testBox) {
	dir := t.TempDir()
	bx := &box.Box{Name: "devbox", Locations: box.NewLocations(filepath.Join(dir, "locations.json")), Events: &events.Bus{}, KitsDir: filepath.Join(dir, "kits")}
	repo := agentGitRepo(t)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("remote", "add", "origin", "https://github.com/acme/api.git")
	run("worktree", "add", "-q", repo+"-review", "-b", "review")
	if _, err := bx.Locations.Add(context.Background(), "api", repo); err != nil {
		t.Fatal(err)
	}
	b := newBoxWith(t, func(s *wire.Server) {
		s.Handle("GET /v1/locations", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			locs, _ := bx.Locations.List(r.Context())
			json.NewEncoder(w).Encode(locs)
		}))
		s.Handle("GET /v1/locations/{name}/config", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg, err := bx.Locations.Config(r.Context(), r.PathValue("name"))
			if err != nil {
				http.Error(w, err.Error(), 404)
				return
			}
			json.NewEncoder(w).Encode(cfg)
		}))
		s.Handle("GET /v1/kits", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			out := []box.InstalledKitAt{}
			if cfg, err := bx.Locations.Config(r.Context(), "api"); err == nil && cfg.Kit != nil {
				out = append(out, box.InstalledKitAt{Location: "api", Slug: "acme/api", Kit: *cfg.Kit})
			}
			json.NewEncoder(w).Encode(out)
		}))
		// The box's requirements check, for the tools a kit asks for: git
		// is here, nothing else is.
		s.Handle("GET /v1/requirements", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := box.Requirements{}
			for _, tool := range r.URL.Query()["tool"] {
				req.Tools = append(req.Tools, box.NamedTool{Tool: tool, Found: tool == "git"})
			}
			json.NewEncoder(w).Encode(req)
		}))
		s.Handle("POST /v1/locations/{name}/team-kit", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req box.TeamKitRequest
			json.NewDecoder(r.Body).Decode(&req)
			res, err := bx.ApplyTeamKit(r.Context(), r.PathValue("name"), req)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			json.NewEncoder(w).Encode(res)
		}))
	})
	return bx, b
}

func teamSourceOf(org string) team.Source { return team.Source{Owner: org, Repo: team.Repo} }

func mustKitRef(t *testing.T, s string) team.KitRef {
	r, err := team.ParseKitRef(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func kitPlan(t *testing.T, a *runningAgent) TeamKitPlan {
	var p TeamKitPlan
	if err := a.client.Call(context.Background(), "GET", "/v1/team/acme/kit?box=devbox&location=api", nil, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestJustTheKitPlanListsRequirementsKeysAndInit(t *testing.T) {
	kitGitHub(t)
	_, b := kitBox(t)
	a := startAgent(t, b.pairLaptop())
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	p := kitPlan(t, a)
	if p.Project != "api" || p.Repo != "acme/api" || p.Kit.ID != "acme-api" || p.Kit.Hash == "" || len(p.Commit) != 40 || p.Worktrees != 1 || p.Applied {
		t.Fatalf("plan: %+v", p)
	}
	if !reflect.DeepEqual(p.SetsUp, []string{"per-worktree setup", "services"}) {
		t.Fatalf("sets up: %v", p.SetsUp)
	}
	// Its tools, checked on the box.
	if len(p.Requires) != 2 || p.Requires[0].Found == nil || !*p.Requires[0].Found || *p.Requires[1].Found || p.Requires[1].Hint != "the full team setup installs it" {
		t.Fatalf("requires: %+v", p.Requires)
	}
	// Its shared keys, with what reads them; its init.
	if len(p.Keys) != 1 || p.Keys[0].Name != "STRIPE_KEY" || !p.Keys[0].OnePassword || !reflect.DeepEqual(p.Keys[0].UsedBy, []string{"setup.sh"}) {
		t.Fatalf("keys: %+v", p.Keys)
	}
	if p.Init == nil || p.Init.Script != "box/init-api.sh" || p.Init.Detail != "makes .env from .env.example" {
		t.Fatalf("init: %+v", p.Init)
	}
}

func TestJustTheKitAppliesOnlyTheKitAndFollowsTheTeam(t *testing.T) {
	g := kitGitHub(t)
	bx, b := kitBox(t)
	dir := b.pairLaptop()
	a := startAgent(t, dir)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	p := kitPlan(t, a)
	ctx := context.Background()

	// A kit that changed since the sheet showed it is refused.
	err := a.client.Call(ctx, "POST", "/v1/team/acme/kit", TeamKitApply{Box: "devbox", Location: "api", Commit: p.Commit, Hash: "000000000000"}, nil)
	if err == nil || !strings.Contains(err.Error(), "changed since it was reviewed") {
		t.Fatalf("a stale hash: %v", err)
	}

	// The defaults: no keys, no init.
	var res box.TeamKitResult
	if err := a.client.Call(ctx, "POST", "/v1/team/acme/kit", TeamKitApply{Box: "devbox", Location: "api", Commit: p.Commit, Hash: p.Kit.Hash}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Kit.ID != "acme-api" || res.Kit.Team == nil || res.Kit.Team.Org != "acme" || res.Kit.Team.Commit != p.Commit || res.FirstOpen != 1 || res.Session != "" {
		t.Fatalf("applied: %+v", res)
	}
	cfg, _ := bx.Locations.Config(ctx, "api")
	if cfg.Kit == nil || cfg.Kit.Hash != p.Kit.Hash || len(cfg.Local.Env) != 0 {
		t.Fatalf("the project: kit %+v, its own env %v", cfg.Kit, cfg.Local.Env)
	}
	// Nothing of the box steps: .berth's script and init were never sent.
	if _, err := os.Stat(filepath.Join(bx.KitsDir, "api", "acme-api", "box")); err == nil {
		t.Fatal("box steps came along with the kit")
	}
	// Kept here, saying where it comes from.
	raw, err := os.ReadFile(filepath.Join(dir, "user", "kits", "acme-api", "source.json"))
	if err != nil || !strings.Contains(string(raw), `"org": "acme"`) || !strings.Contains(string(raw), "github.com/acme/.berth/tree/"+p.Commit+"/kits/api") {
		t.Fatalf("source.json: %s %v", raw, err)
	}
	if p2 := kitPlan(t, a); !p2.Applied {
		t.Fatalf("after: %+v", p2)
	}

	// The team changes the kit: the project's kit has an update.
	g.Repo("acme/.berth", map[string]string{"kits/api/setup.sh": "#!/bin/sh\nmake db\n"}, teamtest.Repo{Private: true})
	var upd struct {
		Kit     KitInfo `json:"kit"`
		Changed bool    `json:"changed"`
	}
	if err := a.client.Call(ctx, "POST", "/v1/kits/acme-api/update", nil, &upd); err != nil {
		t.Fatal(err)
	}
	if !upd.Changed || upd.Kit.Source == nil || upd.Kit.Source.Team == nil || upd.Kit.Source.Team.Commit == p.Commit {
		t.Fatalf("update: %+v", upd)
	}
	var installed []InstalledKitOn
	if err := a.client.Call(ctx, "GET", "/v1/kits/installed", nil, &installed); err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || !installed[0].Outdated || installed[0].Location != "api" {
		t.Fatalf("installed: %+v", installed)
	}
	// Nothing ran the setup's steps: no sudo, no team run.
	for _, c := range g.Calls() {
		if strings.Contains(c, "auth login") {
			t.Fatalf("signed the box in: %s", c)
		}
	}
}

func TestJustTheKitAddsKeysWhenAsked(t *testing.T) {
	kitGitHub(t)
	bx, b := kitBox(t)
	a := startAgent(t, b.pairLaptop())
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	p := kitPlan(t, a)
	if err := a.client.Call(context.Background(), "POST", "/v1/team/acme/kit", TeamKitApply{Box: "devbox", Location: "api", Commit: p.Commit, Hash: p.Kit.Hash, Keys: true}, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ := bx.Locations.Config(context.Background(), "api")
	// The shared reference only: asked keys are each engineer's own.
	if !reflect.DeepEqual(cfg.Local.Env, map[string]string{"STRIPE_KEY": "op://Dev/Stripe/key"}) {
		t.Fatalf("env: %v", cfg.Local.Env)
	}
}

func TestSuggestionSaysWhenAProjectFollowsTheTeamsKit(t *testing.T) {
	acmeGitHub(t)
	api := loc("devbox", "api", "https://github.com/acme/api", false)
	api.KitTeam = "acme"
	a := newSuggestAgent(t, map[string][]suggestLoc{"devbox": {api, loc("devbox", "tools-x", "https://github.com/acme/secret", false)}})
	a.suggestRound(context.Background())
	got := a.TeamSuggestions()
	if len(got) != 1 {
		t.Fatalf("suggestions: %+v", got)
	}
	for _, p := range got[0].Projects {
		switch p.Location {
		case "api":
			if !p.Kit || !p.KitApplied {
				t.Fatalf("api: %+v", p)
			}
		case "tools-x":
			if p.Kit || p.KitApplied {
				t.Fatalf("a repo without a team kit: %+v", p)
			}
		}
	}
}

func TestANewerTeamCommitThatChangesAKeptKitIsAnnounced(t *testing.T) {
	g := kitGitHub(t)
	a := testAgent(t)
	a.cfg.Now = time.Now
	ctx := context.Background()
	gh, _ := newGH()
	r, err := readTeam(ctx, gh, teamSourceOf("acme"), "")
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.readTeamKit(ctx, gh, r, mustKitRef(t, "./kits/api"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.keepTeamKit(tk.info, teamKitLink("acme", r.commit.SHA, "./kits/api"), tk.commit, &box.KitTeam{Org: "acme", Commit: r.commit.SHA, Project: "api"}); err != nil {
		t.Fatal(err)
	}
	ch, stop := a.bus.Subscribe()
	defer stop()
	// No newer commit: nothing to say, and only one request.
	a.checkTeamKits(ctx, gh)
	// A newer commit that leaves the kit alone: nothing either.
	g.Repo("acme/.berth", map[string]string{"README.md": "hello\n"}, teamtest.Repo{Private: true})
	a.checkTeamKits(ctx, gh)
	select {
	case e := <-ch:
		t.Fatalf("announced %+v", e)
	default:
	}
	// One that changes it is announced once, and kept; "from" is the last
	// commit read, the one that left the kit alone.
	g.Repo("acme/.berth", map[string]string{"kits/api/setup.sh": "#!/bin/sh\nmake db\n"}, teamtest.Repo{Private: true})
	a.checkTeamKits(ctx, gh)
	select {
	case e := <-ch:
		if e.Type != "kit.added" || e.Data["kit"] != "acme-api" || e.Data["team"] != "acme" || e.Data["from"] == shortSHA(r.commit.SHA) || e.Data["from"] == e.Data["to"] {
			t.Fatalf("event: %+v", e)
		}
	default:
		t.Fatal("no event for the changed kit")
	}
	a.checkTeamKits(ctx, gh)
	select {
	case e := <-ch:
		t.Fatalf("announced twice: %+v", e)
	default:
	}
}
