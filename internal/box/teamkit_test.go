package box

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

// teamKit is acme's kit for api, as "Just the kit" sends it.
func teamKit() TeamKitRequest {
	plan := apiPlan("")
	in := *plan.Kit
	in.Team = &KitTeam{Org: "acme", Commit: "4e1c9a2aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Project: "api"}
	in.Source = "https://github.com/acme/.berth/tree/4e1c9a2aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/kits/api"
	return TeamKitRequest{Kit: in}
}

// apiLocation is acme/api, added to Shipyard by hand, with a worktree of
// its own.
func apiLocation(t *testing.T, f *teamFixture) (dir, wt string) {
	dir = dirtyAPI(t, f)
	if _, err := f.b.Locations.Add(context.Background(), "api", dir); err != nil {
		t.Fatal(err)
	}
	wt = filepath.Join(f.home, "work", "acme-api-review")
	adoptGit(t, dir, "worktree", "add", "-q", wt, "-b", "review")
	return dir, wt
}

func TestJustTheKitInstallsOnlyTheKit(t *testing.T) {
	f := newTeamFixture(t)
	dir, wt := apiLocation(t, f)
	before := snapshot(t, dir)
	res, err := f.b.ApplyTeamKit(context.Background(), "api", teamKit())
	if err != nil {
		t.Fatal(err)
	}
	if res.Kit.ID != "acme-api" || res.Kit.Team == nil || res.Kit.Team.Org != "acme" || res.FirstOpen != 1 || res.Session != "" {
		t.Fatalf("result: %+v", res)
	}
	// No box step, no team run, no terminal, no init.
	if _, err := f.b.Team.readState("acme"); err == nil {
		t.Fatal("a team setup run was recorded")
	}
	if list, _ := f.b.Sessions.List(context.Background()); len(list) != 0 {
		t.Fatalf("terminals started: %+v", list)
	}
	time.Sleep(150 * time.Millisecond)
	for _, m := range []string{"init-ran", "init-api", "worktree-setup"} {
		if _, err := os.Stat(filepath.Join(f.marks, m)); err == nil {
			t.Fatalf("%s ran", m)
		}
	}
	// The checkout as it was; the keys not added.
	if after := snapshot(t, dir); after != before {
		t.Fatalf("the checkout changed:\n%s\n%s", before, after)
	}
	saved, _ := f.b.Locations.saved("api")
	if saved.Kit == nil || saved.Kit.Team == nil || (saved.Config != nil && len(saved.Config.Env) > 0) {
		t.Fatalf("saved: kit %+v config %+v", saved.Kit, saved.Config)
	}
	loc, _ := f.b.Locations.Get(context.Background(), "api")
	if loc.KitTeam != "acme" {
		t.Fatalf("location: %+v", loc)
	}
	// The worktree already there is set up on its first open.
	var w Worktree
	for _, x := range loc.Worktrees {
		if samePath(x.Path, wt) {
			w = x
		}
	}
	if !w.SetupOnOpen {
		t.Fatalf("worktrees: %+v", loc.Worktrees)
	}
}

func TestJustTheKitAddsKeysAndRunsTheInitOnlyWhenAsked(t *testing.T) {
	f := newTeamFixture(t)
	dir, _ := apiLocation(t, f)
	req := teamKit()
	req.Env = map[string]string{"STRIPE_KEY": "op://Dev/Stripe/key"}
	req.Init = &TeamKitInit{Team: "acme", Project: "api", Script: "box/init-env.sh", Files: map[string]string{"box/init-env.sh": b64(envInit)}}
	res, err := f.b.ApplyTeamKit(context.Background(), "api", req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Session != "team-acme-api" {
		t.Fatalf("result: %+v", res)
	}
	saved, _ := f.b.Locations.saved("api")
	if saved.Config == nil || saved.Config.Env["STRIPE_KEY"] != "op://Dev/Stripe/key" {
		t.Fatalf("keys: %+v", saved.Config)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, _ := os.ReadFile(filepath.Join(f.marks, "init-ran"))
		if strings.Contains(string(raw), "acme-api") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the init didn't run in the checkout: %q", raw)
		}
		time.Sleep(30 * time.Millisecond)
	}
	// It ran in the checkout, leaving its own .env value.
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.Contains(string(env), "SECRET=mine") {
		t.Fatalf(".env: %s", env)
	}
}

func TestAFullSetupLaterKeepsTheKitAndTheProjectAsTheyAre(t *testing.T) {
	f := newTeamFixture(t)
	apiLocation(t, f)
	first, err := f.b.ApplyTeamKit(context.Background(), "api", teamKit())
	if err != nil {
		t.Fatal(err)
	}
	var installs atomic.Int32
	stop := f.bus.Observe(func(e events.Event) {
		if e.Type == "kit.installed" {
			installs.Add(1)
		}
	})
	defer stop()
	if _, err := f.b.StartTeam(context.Background(), adoptBundle(f, apiPlan(""))); err != nil {
		t.Fatal(err)
	}
	st := f.done()
	api := projectOf(st, "api")
	if st.Phase != "done" || !api.Adopted || api.Location != "api" {
		t.Fatalf("api: %+v (%s)", api, st.Error)
	}
	all, _ := f.b.Locations.List(context.Background())
	if len(all) != 1 {
		t.Fatalf("locations: %+v", all)
	}
	saved, _ := f.b.Locations.saved("api")
	if saved.Kit == nil || !saved.Kit.InstalledAt.Equal(first.Kit.InstalledAt) {
		t.Fatalf("the kit was applied again: %+v", saved.Kit)
	}
	if n := installs.Load(); n != 0 {
		t.Fatalf("kit.installed %d times", n)
	}
}

func TestRequirementsCheckToolsAskedFor(t *testing.T) {
	f := newTeamFixture(t)
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "pg_fake_tool"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	w := httptest.NewRecorder()
	if err := f.b.requirements(w, httptest.NewRequest("GET", "/v1/requirements?tool=pg_fake_tool&tool=no_such_tool_here", nil)); err != nil {
		t.Fatal(err)
	}
	rec := w.Body.String()
	if !strings.Contains(rec, `{"tool":"pg_fake_tool","found":true`) || !strings.Contains(rec, `{"tool":"no_such_tool_here","found":false}`) {
		t.Fatalf("requirements: %s", rec)
	}
}
