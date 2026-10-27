package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/team/teamtest"
)

// suggestAgent is an agent whose boxes hold locs, on a network, with a
// clock the test moves.
type suggestAgent struct {
	*Agent
	mu     sync.Mutex
	clock  time.Time
	locs   map[string][]suggestLoc
	online bool
}

func newSuggestAgent(t *testing.T, locs map[string][]suggestLoc) *suggestAgent {
	s := &suggestAgent{Agent: testAgent(t), clock: time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC), locs: locs, online: true}
	s.cfg.Now = func() time.Time {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.clock
	}
	s.suggest.locate = func(context.Context) map[string][]suggestLoc {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := map[string][]suggestLoc{}
		for b, l := range s.locs {
			out[b] = append([]suggestLoc{}, l...)
		}
		return out
	}
	s.suggest.online = func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.online
	}
	return s
}

func (s *suggestAgent) later(d time.Duration) {
	s.mu.Lock()
	s.clock = s.clock.Add(d)
	s.mu.Unlock()
}

// asked lists the owners the fake gh was asked about, in order.
func asked(g *teamtest.GitHub) []string {
	var out []string
	for _, c := range g.Calls() {
		if !strings.HasPrefix(c, "api graphql") {
			continue
		}
		if _, owner, ok := strings.Cut(c, "owner="); ok {
			out = append(out, owner)
		}
	}
	return out
}

func loc(boxName, name, remote string, own bool) suggestLoc {
	l := suggestLocs(boxName, []box.Location{{Name: name, Repo: true, Remote: remote, RepoTrust: map[bool]string{true: "trusted", false: "none"}[own]}})
	if len(l) == 0 {
		return suggestLoc{}
	}
	return l[0]
}

func TestSuggestCollectsOwnersFromGitHubRemotesOnly(t *testing.T) {
	locs := suggestLocs("devbox", []box.Location{
		{Name: "web", Repo: true, Remote: "https://github.com/acme/web.git"},
		{Name: "api", Repo: true, Remote: "git@github.com:Acme/api.git"},
		{Name: "tools", Repo: true, Remote: "ssh://git@ssh.github.com:443/northwind/tools.git"},
		{Name: "notes", Repo: true, Remote: "git@github.com-personal:jo/notes.git"},
		{Name: "ghe", Repo: true, Remote: "https://github.acme.example/acme/internal.git"},
		{Name: "lab", Repo: true, Remote: "git@gitlab.com:acme/lab.git"},
		{Name: "scratch", Repo: true},
		{Name: "folder", Repo: false, Remote: "https://github.com/acme/folder.git"},
	})
	var got []string
	for _, l := range locs {
		got = append(got, l.Name+"="+l.Repo)
	}
	want := []string{"web=acme/web", "api=Acme/api", "tools=northwind/tools", "notes=jo/notes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projects:\n got %v\nwant %v", got, want)
	}
	a := newSuggestAgent(t, map[string][]suggestLoc{"devbox": locs})
	a.suggest.mu.Lock()
	a.suggestLoadLocked()
	a.suggest.locs = a.suggest.locate(context.Background())
	due := a.dueOwnersLocked(a.now(), nil)
	a.suggest.mu.Unlock()
	// One request per owner, whatever the case of its name.
	if !reflect.DeepEqual(due, []string{"acme", "jo", "northwind"}) {
		t.Fatalf("owners: %v", due)
	}
}

func TestSuggestFindsAnOrgsSetupWithOneRequestAndSaysWhatItBrings(t *testing.T) {
	g, _ := acmeGitHub(t)
	g.Org("northwind", "Northwind", false)
	g.Repo("northwind/shop", map[string]string{"x": "y"}, teamtest.Repo{})
	a := newSuggestAgent(t, map[string][]suggestLoc{
		"devbox": {
			loc("devbox", "api", "git@github.com:acme/api.git", false),
			// Its own .berth/config.json wins over the team's kit.
			loc("devbox", "web", "https://github.com/acme/web", true),
			loc("devbox", "old", "https://github.com/acme/old-thing", false),
			loc("devbox", "shop", "https://github.com/northwind/shop", false),
		},
	})
	a.suggestRound(context.Background())
	got := a.TeamSuggestions()
	if len(got) != 1 || got[0].Org != "acme" || got[0].Name != "Acme" || got[0].Repo != "acme/.berth" {
		t.Fatalf("suggestions: %+v", got)
	}
	byName := map[string]TeamSuggestProject{}
	for _, p := range got[0].Projects {
		byName[p.Location] = p
	}
	if p := byName["api"]; !p.Listed || p.Box != "devbox" || p.Repo != "acme/api" || !reflect.DeepEqual(p.SetsUp, []string{"services", "a first-time setup"}) {
		t.Fatalf("api: %+v", p)
	}
	if p := byName["web"]; !p.Listed || !reflect.DeepEqual(p.SetsUp, []string{"its keys"}) {
		t.Fatalf("web: %+v", p)
	}
	if p := byName["old"]; p.Listed || len(p.SetsUp) != 0 {
		t.Fatalf("a repo the setup doesn't list: %+v", p)
	}
	if q := asked(g); !reflect.DeepEqual(q, []string{"acme", "northwind"}) {
		t.Fatalf("asked about %v", q)
	}
	// The kits of the person's own repositories only: web's and api's, not
	// tools'; nothing else of the org is read.
	var kits []string
	for _, c := range g.Calls() {
		if strings.Contains(c, "kit.json") {
			kits = append(kits, c)
		}
		if strings.Contains(c, "orgs/") || strings.Contains(c, "/repos") || strings.Contains(c, "members") {
			t.Fatalf("read more than it needs: %s", c)
		}
	}
	if len(kits) != 2 || strings.Contains(strings.Join(kits, " "), "acme/kits") {
		t.Fatalf("kits read: %v", kits)
	}
	// The status carries them.
	if st := a.status(); len(st.TeamSuggestions) != 1 || st.TeamSuggestions[0].Org != "acme" {
		t.Fatalf("status: %+v", st.TeamSuggestions)
	}
	b, _ := json.Marshal(a.status())
	if !strings.Contains(string(b), `"team_suggestions":[{"org":"acme"`) {
		t.Fatalf("status JSON: %s", b)
	}
}

func TestSuggestAsksAnOrgAtMostDailyAndAPersonWeekly(t *testing.T) {
	g, _ := acmeGitHub(t)
	g.User("jo")
	g.Repo("jo/notes", map[string]string{"x": "y"}, teamtest.Repo{})
	a := newSuggestAgent(t, map[string][]suggestLoc{"devbox": {
		loc("devbox", "web", "https://github.com/acme/web", false),
		loc("devbox", "notes", "git@github.com:jo/notes.git", false),
	}})
	ctx := context.Background()
	a.suggestRound(ctx)
	a.later(time.Hour)
	a.suggestRound(ctx)
	a.later(22 * time.Hour)
	a.suggestRound(ctx)
	if q := asked(g); !reflect.DeepEqual(q, []string{"acme", "jo"}) {
		t.Fatalf("within a day: %v", q)
	}
	a.later(2 * time.Hour) // a day and an hour since the first
	a.suggestRound(ctx)
	if q := asked(g); !reflect.DeepEqual(q, []string{"acme", "jo", "acme"}) {
		t.Fatalf("after a day, the org only: %v", q)
	}
	a.later(6 * 24 * time.Hour)
	a.suggestRound(ctx)
	if q := asked(g); !reflect.DeepEqual(q, []string{"acme", "jo", "acme", "acme", "jo"}) {
		t.Fatalf("after a week, the person too: %v", q)
	}
	// What it learnt survives a restart: a new agent on the same state asks
	// no one.
	b := newSuggestAgent(t, a.locs)
	b.cfg.Dir = a.cfg.Dir
	b.clock = a.clock
	b.suggestRound(ctx)
	if q := asked(g); len(q) != 5 {
		t.Fatalf("after a restart: %v", q)
	}
	if got := b.TeamSuggestions(); len(got) != 1 || got[0].Org != "acme" {
		t.Fatalf("after a restart: %+v", got)
	}
}

func TestSuggestAsksAboutANewOwnerAtOnce(t *testing.T) {
	g, _ := acmeGitHub(t)
	g.Org("northwind", "Northwind", false)
	a := newSuggestAgent(t, map[string][]suggestLoc{"devbox": {loc("devbox", "web", "https://github.com/acme/web", false)}})
	ctx := context.Background()
	a.suggestRound(ctx)
	a.later(10 * time.Minute)
	a.mu.Lock()
	a.locs["devbox"] = append(a.locs["devbox"], loc("devbox", "shop", "https://github.com/northwind/shop", false), loc("devbox", "api", "https://github.com/acme/api", false))
	a.mu.Unlock()
	a.suggestRound(ctx)
	if q := asked(g); !reflect.DeepEqual(q, []string{"acme", "northwind"}) {
		t.Fatalf("asked %v", q)
	}
	if got := a.TeamSuggestions(); len(got) != 1 || len(got[0].Projects) != 2 {
		t.Fatalf("suggestions: %+v", got)
	}
}

func TestSuggestSkipsAcceptedAndDismissedOrgs(t *testing.T) {
	g, _ := acmeGitHub(t)
	g.Org("northwind", "Northwind", false)
	g.Repo("northwind/.berth", map[string]string{"team.json": `{"schema":"berth.team/v1","id":"northwind","name":"Northwind","org":"northwind","projects":[{"id":"shop","repo":"northwind/shop","review_button":true}]}`}, teamtest.Repo{})
	a := newSuggestAgent(t, map[string][]suggestLoc{"devbox": {
		loc("devbox", "web", "https://github.com/acme/web", false),
		loc("devbox", "shop", "https://github.com/northwind/shop", false),
	}})
	if err := a.saveAccepted(teamAccepted{Org: "acme", ID: "acme", Name: "Acme", Commit: "abc", Box: "devbox"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a.suggestRound(ctx)
	got := a.TeamSuggestions()
	if len(got) != 1 || got[0].Org != "northwind" || !reflect.DeepEqual(got[0].Projects[0].SetsUp, []string{"review links"}) {
		t.Fatalf("suggestions: %+v", got)
	}
	if q := asked(g); !reflect.DeepEqual(q, []string{"northwind"}) {
		t.Fatalf("asked about an accepted org: %v", q)
	}

	// Not for me, through the app's route: gone, and never asked again.
	mux := http.NewServeMux()
	a.teamSuggestRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/team-suggestions/Northwind/dismiss", nil))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	a.later(30 * 24 * time.Hour)
	a.suggestRound(ctx)
	if q := asked(g); len(q) != 1 || len(a.TeamSuggestions()) != 0 {
		t.Fatalf("after dismissing: asked %v, suggestions %+v", q, a.TeamSuggestions())
	}
	// Undo brings it back from what is known, without asking.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/v1/team-suggestions/northwind/dismiss", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"org":"northwind"`) {
		t.Fatalf("undo: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/team-suggestions/not%20an%20org/dismiss", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a bad org: %d", rec.Code)
	}
}

func TestSuggestStaysQuietOfflineAndOnErrorsAndTriesLater(t *testing.T) {
	g, _ := acmeGitHub(t)
	a := newSuggestAgent(t, map[string][]suggestLoc{"devbox": {loc("devbox", "web", "https://github.com/acme/web", false)}})
	ctx := context.Background()

	// No network: gh isn't run at all.
	a.online = false
	a.suggestRound(ctx)
	if c := g.Calls(); len(c) != 1 || c[0] != "" {
		t.Fatalf("offline, gh ran: %v", c)
	}

	// A network, but GitHub can't be reached: nothing to show, no error.
	a.online = true
	g.Fail("error connecting to api.github.com")
	a.suggestRound(ctx)
	if q := asked(g); len(q) != 1 || len(a.TeamSuggestions()) != 0 {
		t.Fatalf("on error: asked %v, suggestions %+v", q, a.TeamSuggestions())
	}
	// It waits before trying again, longer each time.
	a.later(5 * time.Minute)
	a.suggestRound(ctx)
	if q := asked(g); len(q) != 1 {
		t.Fatalf("tried again at once: %v", q)
	}
	g.Fail("")
	a.later(20 * time.Minute)
	a.suggestRound(ctx)
	if q := asked(g); len(q) != 2 || len(a.TeamSuggestions()) != 1 {
		t.Fatalf("after the wait: asked %v, suggestions %+v", q, a.TeamSuggestions())
	}

	// Signed out of gh is an error like any other: silent.
	g.SignIn("")
	b := newSuggestAgent(t, a.locs)
	g.Fail("gh: Requires authentication (HTTP 401)")
	b.suggestRound(ctx)
	if len(b.TeamSuggestions()) != 0 {
		t.Fatalf("signed out: %+v", b.TeamSuggestions())
	}
}

func TestSuggestIgnoresASetupThatNamesAnotherOrg(t *testing.T) {
	g := teamtest.New(t)
	g.SignIn("engineer")
	g.Org("acme", "Acme", false)
	g.Repo("acme/.berth", map[string]string{"team.json": `{"schema":"berth.team/v1","id":"x","name":"X","org":"someone-else","projects":[]}`}, teamtest.Repo{})
	a := newSuggestAgent(t, map[string][]suggestLoc{"devbox": {loc("devbox", "web", "https://github.com/acme/web", false)}})
	a.suggestRound(context.Background())
	if got := a.TeamSuggestions(); len(got) != 0 {
		t.Fatalf("suggested: %+v", got)
	}
}

func TestConfigWordsAreGeneric(t *testing.T) {
	on := true
	for _, tc := range []struct {
		c    box.RepoConfig
		want []string
	}{
		{box.RepoConfig{Setup: "pnpm i", Env: map[string]string{"DATABASE_URL": "postgres://localhost/$BERTH_WORKTREE_SLUG"}}, []string{"per-worktree databases"}},
		{box.RepoConfig{Setup: "pnpm i && pnpm db:migrate"}, []string{"per-worktree databases"}},
		{box.RepoConfig{Setup: "make"}, []string{"per-worktree setup"}},
		{box.RepoConfig{Services: []box.WorktreeService{{Name: "web"}}, Login: &box.LoginConfig{Script: "login.sh"}, ReviewButton: &on}, []string{"services", "Log in as…", "review links"}},
		{box.RepoConfig{}, nil},
	} {
		if got := configWords(tc.c); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("configWords(%+v) = %v, want %v", tc.c, got, tc.want)
		}
	}
}
