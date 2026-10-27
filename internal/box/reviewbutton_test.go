package box

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// buttonFixture is the review fixture's acme/shop with a worktree on the
// branch feat-x, and a gh that answers GitHub's pulls API from files: each
// PR's fields in pr-N.json, its description in body-N.json (what a PATCH
// wrote), and which PR a branch has in head-BRANCH. A race-N.json takes the
// place of the description at the next read of that PR, as a person editing
// it at that moment would.
type buttonFixture struct {
	*reviewFixture
	ghDir string
	wt    string
	now   time.Time
}

func newButtonFixture(t *testing.T) *buttonFixture {
	t.Helper()
	f := &buttonFixture{reviewFixture: newReviewFixture(t), now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	f.ghDir = filepath.Join(f.root, "github-api")
	os.MkdirAll(f.ghDir, 0o755)
	gh := filepath.Join(f.root, "bin", "gh-api")
	script := `#!/bin/sh
d="` + f.ghDir + `"
echo "$*" >> "$d/calls"
pr() {
  [ -f "$d/race-$1.json" ] && mv "$d/race-$1.json" "$d/body-$1.json"
  printf '%s' "$(cat "$d/pr-$1.json")"
  tail -c +2 "$d/body-$1.json"
}
[ "$1" = api ] || exit 2
if [ "$2" = -X ] && [ "$3" = PATCH ]; then
  n=${4##*/}
  cat > "$d/body-$n.json"
  echo '{}'
  exit 0
fi
case "$2" in
  *"/pulls?"*)
    h=$(echo "$2" | sed -n 's/.*head=acme%3A\([^&]*\).*/\1/p')
    n=$(cat "$d/head-$h" 2>/dev/null)
    if [ -z "$n" ]; then echo '[]'; exit 0; fi
    printf '['; pr "$n"; printf ']\n' ;;
  */pulls/*/files*)
    n=$(echo "$2" | sed -n 's/.*pulls\/\([0-9]*\)\/files.*/\1/p')
    if [ -f "$d/files-$n.json" ]; then cat "$d/files-$n.json"; else echo '[{"filename":"app/billing/page.tsx"}]'; fi ;;
  */pulls/*) pr "${2##*/}" ;;
  *) exit 3 ;;
esac
`
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.b.ReviewButtons = &ReviewButtons{Path: filepath.Join(f.root, "box", "review-buttons.json"), GH: gh}
	f.wt = filepath.Join(f.root, "code", "shop-feat-x")
	runGit(t, f.repo, "worktree", "add", "-q", "-b", "feat-x", f.wt)
	return f
}

// githubPR puts PR n, from branch feat-x, on the fake GitHub.
func (f *buttonFixture) githubPR(n int, body *string, assoc, headRepo string) {
	f.t.Helper()
	head := `null`
	if headRepo != "" {
		head = `{"full_name":"` + headRepo + `"}`
	}
	fields := `{"number":` + itoa(n) + `,"state":"open","author_association":"` + assoc + `","head":{"ref":"feat-x","repo":` + head + `},`
	os.WriteFile(filepath.Join(f.ghDir, "pr-"+itoa(n)+".json"), []byte(fields), 0o644)
	f.setBody(n, body)
	os.WriteFile(filepath.Join(f.ghDir, "head-feat-x"), []byte(itoa(n)), 0o644)
}

func (f *buttonFixture) setBody(n int, body *string) {
	b, _ := json.Marshal(map[string]*string{"body": body})
	os.WriteFile(filepath.Join(f.ghDir, "body-"+itoa(n)+".json"), b, 0o644)
}

func (f *buttonFixture) body(n int) string {
	f.t.Helper()
	var v struct {
		Body *string `json:"body"`
	}
	b, _ := os.ReadFile(filepath.Join(f.ghDir, "body-"+itoa(n)+".json"))
	if err := json.Unmarshal(b, &v); err != nil {
		f.t.Fatalf("body-%d: %v (%s)", n, err, b)
	}
	if v.Body == nil {
		return ""
	}
	return *v.Body
}

// patches counts the edits made to PR descriptions; calls counts every gh
// call.
func (f *buttonFixture) patches() int { return strings.Count(f.calls(), "-X PATCH") }

func (f *buttonFixture) calls() string {
	b, _ := os.ReadFile(filepath.Join(f.ghDir, "calls"))
	return string(b)
}

// poll looks, as berthd does every 30 seconds; each look is later than the
// last by more than a worktree's wait.
func (f *buttonFixture) poll() int {
	f.now = f.now.Add(5 * time.Minute)
	return f.b.PollReviewButtons(context.Background(), f.now)
}

func (f *buttonFixture) setLocal(c RepoConfig) {
	f.t.Helper()
	if err := f.b.Locations.SetLocalConfig("shop", c); err != nil {
		f.t.Fatal(err)
	}
}

func boolp(v bool) *bool { return &v }

func strp(s string) *string { return &s }

const plainBlock = ReviewButtonMarker + "\n[![Review in Shipyard](https://berthd.app/badges/review.svg)](https://berthd.app/review?repo=acme/shop&pr=12)\n" + reviewButtonEnd

func TestTheReviewButtonIsAddedOnceAndTheRestOfTheBodyStays(t *testing.T) {
	for _, tc := range []struct {
		name string
		body *string
		want string
	}{
		{"prose", strp("Fixes checkout rounding.\n\n- [x] tests"), "Fixes checkout rounding.\n\n- [x] tests\n\n" + plainBlock},
		{"trailing newline", strp("Fixes it.\n"), "Fixes it.\n\n" + plainBlock},
		{"blank line already", strp("Fixes it.\n\n"), "Fixes it.\n\n" + plainBlock},
		{"crlf", strp("Fixes it.\r\n\r\n- [x] tests  "), "Fixes it.\r\n\r\n- [x] tests  \r\n\r\n" + strings.ReplaceAll(plainBlock, "\n", "\r\n")},
		{"empty", strp(""), plainBlock},
		{"none", nil, plainBlock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newButtonFixture(t)
			f.setLocal(RepoConfig{ReviewButton: boolp(true)})
			// No PR yet: nothing to edit, and it looks again later.
			if n := f.poll(); n != 0 || f.patches() != 0 {
				t.Fatalf("edited %d with no PR", n)
			}
			f.githubPR(12, tc.body, "MEMBER", "acme/shop")
			if n := f.poll(); n != 1 {
				t.Fatalf("edited %d, want 1; calls:\n%s", n, f.calls())
			}
			got := f.body(12)
			if got != tc.want {
				t.Fatalf("body = %q\nwant   %q", got, tc.want)
			}
			if tc.body != nil && !strings.HasPrefix(got, *tc.body) {
				t.Fatalf("the description before the button changed: %q", got)
			}
			// Later looks change nothing, and ask GitHub nothing more.
			before := f.calls()
			for range 3 {
				if n := f.poll(); n != 0 {
					t.Fatalf("a later look edited %d", n)
				}
			}
			if f.patches() != 1 || f.calls() != before {
				t.Fatalf("later looks called gh:\n%s", strings.TrimPrefix(f.calls(), before))
			}
			if strings.Count(f.body(12), ReviewButtonMarker) != 1 {
				t.Fatalf("button twice: %q", f.body(12))
			}
			ev := f.journaled("review.button")
			if len(ev) != 1 || ev[0].Data["repo"] != "acme/shop" || ev[0].Data["pr"] != float64(12) {
				t.Fatalf("journal = %+v", ev)
			}
		})
	}
}

func TestTheReviewButtonIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *buttonFixture)
		want  bool
		from  string
	}{
		{"off by default", func(f *buttonFixture) {}, false, ""},
		{"the box's own config", func(f *buttonFixture) { f.setLocal(RepoConfig{ReviewButton: boolp(true)}) }, true, "box"},
		{"the team's team.json", func(f *buttonFixture) { f.teamButton(true) }, true, "team"},
		{"the box turns the team's off", func(f *buttonFixture) {
			f.teamButton(true)
			f.setLocal(RepoConfig{ReviewButton: boolp(false)})
		}, false, "box"},
		{"the default branch's trusted config", func(f *buttonFixture) {
			f.commitConfig(`{"review_button": true}`)
			trustRepo(f.t, f.b.Locations, "shop")
		}, true, "repo"},
		{"an untrusted config does nothing", func(f *buttonFixture) { f.commitConfig(`{"review_button": true, "setup": "true"}`) }, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newButtonFixture(t)
			tc.setup(f)
			body := "Fixes it."
			f.githubPR(12, &body, "MEMBER", "acme/shop")
			st := f.b.reviewButtonFor("shop")
			if st.On != tc.want || st.From != tc.from {
				t.Fatalf("state = %+v, want on %v from %q", st, tc.want, tc.from)
			}
			f.poll()
			if tc.want != (f.patches() == 1) {
				t.Fatalf("patches = %d, want on %v; calls:\n%s", f.patches(), tc.want, f.calls())
			}
			if !tc.want {
				if f.calls() != "" || f.body(12) != body {
					t.Fatalf("off, yet it asked GitHub or edited: %q\n%s", f.body(12), f.calls())
				}
			}
		})
	}
}

// teamButton writes the team setup's bundle with the project's
// review_button.
func (f *buttonFixture) teamButton(v bool) {
	f.bundle(TeamBundle{ID: "acme", Name: "Acme", Org: "acme", Projects: []TeamProjectPlan{{ID: "shop", Repo: "acme/shop", Path: f.repo, ReviewButton: v}}})
}

// commitConfig changes the main checkout's .berth/config.json, as a merge
// to the default branch would.
func (f *buttonFixture) commitConfig(c string) {
	f.write(f.repo, map[string]string{".berth/config.json": c})
	runGit(f.t, f.repo, "-c", "core.hooksPath=/dev/null", "commit", "-q", "-am", "config")
}

func TestForksAndOutsidersGetNoButton(t *testing.T) {
	for _, tc := range []struct{ name, assoc, head, skipped string }{
		{"fork", "MEMBER", "someone/shop", "fork"},
		{"deleted fork", "MEMBER", "", "fork"},
		{"outsider", "CONTRIBUTOR", "acme/shop", "outsider"},
		{"first-timer", "FIRST_TIME_CONTRIBUTOR", "acme/shop", "outsider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newButtonFixture(t)
			f.setLocal(RepoConfig{ReviewButton: boolp(true)})
			f.githubPR(12, strp("From outside."), tc.assoc, tc.head)
			f.poll()
			f.poll()
			if f.patches() != 0 || f.body(12) != "From outside." {
				t.Fatalf("edited a %s's PR: %q", tc.name, f.body(12))
			}
			loc, _ := f.b.Locations.Get(context.Background(), "shop")
			for _, w := range loc.Worktrees {
				if w.Path == f.wt {
					if info := f.b.reviewButtonInfo(loc, w); info.State != "skipped" || info.Skipped != tc.skipped || info.Link != "" {
						t.Fatalf("info = %+v", info)
					}
				}
			}
			if len(f.journaled("review.button")) != 0 {
				t.Fatal("journaled a button that was not added")
			}
		})
	}
}

func TestAButtonSomeoneWroteIsLeftAlone(t *testing.T) {
	for _, body := range []string{
		"Fixes it.\n\n" + plainBlock + "\n\nMore below.",
		"Fixes it.\n\n[![Review in Shipyard](https://berthd.app/badges/review.svg)](https://berthd.app/review?repo=acme/shop&pr=12&as=pro@acme.test&path=/billing)",
		"Fixes it.\n\nReview it: berth://review?repo=acme/shop&pr=12",
		// A marker with no end: never guessed at.
		"Fixes it.\n\n" + ReviewButtonMarker + "\n[broken",
	} {
		f := newButtonFixture(t)
		f.setLocal(RepoConfig{ReviewButton: boolp(true)})
		f.githubPR(12, &body, "MEMBER", "acme/shop")
		f.poll()
		if f.patches() != 0 || f.body(12) != body {
			t.Fatalf("edited %q into %q", body, f.body(12))
		}
	}
}

func (f *buttonFixture) putButton(as, path string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"as": as, "path": path})
	r := httptest.NewRequest(http.MethodPut, "/v1/worktrees/shop/feat-x/review-button", strings.NewReader(string(b)))
	r.SetPathValue("loc", "shop")
	r.SetPathValue("wt", "feat-x")
	w := httptest.NewRecorder()
	if err := f.b.putReviewButton(w, r); err != nil {
		writeErr(w, err)
	}
	return w
}

func TestSettingAsAndPathReplacesTheButtonInPlace(t *testing.T) {
	f := newButtonFixture(t)
	f.setLocal(RepoConfig{ReviewButton: boolp(true), Login: &LoginConfig{Script: "login.sh", Users: []LoginUser{{Email: "pro@acme.test"}, {Email: "admin@acme.test", Label: "Team admin"}}}})
	f.githubPR(12, strp("Fixes it.\n\n- [x] tests"), "MEMBER", "acme/shop")
	f.poll()
	// Someone writes below the button afterwards.
	f.setBody(12, strp(f.body(12)+"\n\n## Notes\nLooks good"))

	for _, bad := range []struct{ as, path string }{
		{"not-an-email", ""},
		{"x@y.z\n; rm -rf /", ""},
		{"stranger@acme.test", ""}, // not one of the project's users
		{strings.Repeat("a", 250) + "@acme.test", ""},
		{"", "//evil.example"},
		{"", "https://evil.example/"},
		{"", "/%2F%2Fevil.example"},
		{"", "/\\evil"},
		{"", "/a b"},
		{"", "/" + strings.Repeat("a", 512)},
	} {
		if w := f.putButton(bad.as, bad.path); w.Code != http.StatusBadRequest {
			t.Fatalf("as %q path %q: %d %s", bad.as, bad.path, w.Code, w.Body)
		}
	}
	f.poll()
	if f.patches() != 1 {
		t.Fatalf("a refused setting edited the PR")
	}

	if w := f.putButton("Admin@acme.test", "/billing?tab=plans"); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if n := f.poll(); n != 1 {
		t.Fatalf("edited %d after as and path were set", n)
	}
	want := "Fixes it.\n\n- [x] tests\n\n" + ReviewButtonMarker +
		"\n[![Review in Shipyard](https://berthd.app/badges/review.svg)](https://berthd.app/review?repo=acme/shop&pr=12&as=admin@acme.test&path=/billing%3Ftab%3Dplans)\n" +
		reviewButtonEnd + "\n\n## Notes\nLooks good"
	if got := f.body(12); got != want {
		t.Fatalf("body = %q\nwant   %q", got, want)
	}
	f.poll()
	if f.patches() != 2 {
		t.Fatalf("patches = %d, want 2", f.patches())
	}
	if ev := f.journaled("review.button"); len(ev) != 2 || ev[1].Data["as"] != "admin@acme.test" || ev[1].Data["path"] != "/billing?tab=plans" {
		t.Fatalf("journal = %+v", ev)
	}

	// Clearing them goes back to the plain button, in the same place.
	f.putButton("", "")
	f.poll()
	if got := f.body(12); got != "Fixes it.\n\n- [x] tests\n\n"+plainBlock+"\n\n## Notes\nLooks good" {
		t.Fatalf("after clearing: %q", got)
	}
}

func TestAnEditWhileAddingIsReadAgainOnce(t *testing.T) {
	f := newButtonFixture(t)
	f.setLocal(RepoConfig{ReviewButton: boolp(true)})
	f.githubPR(12, strp("First draft."), "MEMBER", "acme/shop")
	// The author edits the description just as berthd reads it.
	b, _ := json.Marshal(map[string]string{"body": "Second draft, with more."})
	os.WriteFile(filepath.Join(f.ghDir, "race-12.json"), b, 0o644)
	if n := f.poll(); n != 1 {
		t.Fatalf("edited %d; calls:\n%s", n, f.calls())
	}
	if got := f.body(12); got != "Second draft, with more.\n\n"+plainBlock {
		t.Fatalf("body = %q", got)
	}
	if f.patches() != 1 {
		t.Fatalf("patches = %d", f.patches())
	}
}

func TestWithReviewButton(t *testing.T) {
	block := plainBlock
	if _, ok := withReviewButton("x\n\n"+block, block, true); ok {
		t.Fatal("the same block was written again")
	}
	other := strings.Replace(block, "pr=12", "pr=12&path=/x", 1)
	got, ok := withReviewButton("a\n"+block+"\nb", other, true)
	if !ok || got != "a\n"+other+"\nb" {
		t.Fatalf("replace = %q, %v", got, ok)
	}
	if got, ok := withReviewButton("a\n"+block+"\nb", other, false); ok || got != "a\n"+block+"\nb" {
		t.Fatalf("replaced without being asked: %q", got)
	}
	if l := ReviewButtonLink("acme/shop", 7, "a+b@acme.test", "/x y&z#w"); l != "https://berthd.app/review?repo=acme/shop&pr=7&as=a%2Bb@acme.test&path=/x%20y%26z%23w" {
		t.Fatalf("link = %s", l)
	}
}

// The button is for UI work: a PR whose files show nothing in a browser gets
// none, is asked about again only when it moves on, and gets one once it
// touches a page; a button someone asked for (berthd review-button) goes on
// regardless.
func TestOnlyUIWorkGetsAButton(t *testing.T) {
	f := newButtonFixture(t)
	f.setLocal(RepoConfig{ReviewButton: boolp(true), Login: &LoginConfig{Script: "login.sh", Users: []LoginUser{{Email: "pro@acme.test"}}}})
	f.githubPR(12, strp("Moves the webhook queue."), "MEMBER", "acme/shop")
	atHead := func(sha string) {
		p := filepath.Join(f.ghDir, "pr-12.json")
		b, _ := os.ReadFile(p)
		s := regexp.MustCompile(`"ref":"feat-x"(,"sha":"[a-z0-9]*")?`).ReplaceAllString(string(b), `"ref":"feat-x","sha":"`+sha+`"`)
		os.WriteFile(p, []byte(s), 0o644)
	}
	files := func(names ...string) {
		var b strings.Builder
		b.WriteString("[")
		for i, n := range names {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"filename":"` + n + `"}`)
		}
		b.WriteString("]")
		os.WriteFile(filepath.Join(f.ghDir, "files-12.json"), []byte(b.String()), 0o644)
	}
	asked := func() int { return strings.Count(f.calls(), "/pulls/12/files") }

	atHead("aaa")
	files("internal/queue/webhook.go", "internal/queue/webhook_test.go", "docs/queue.md")
	if n := f.poll(); n != 0 || f.patches() != 0 {
		t.Fatalf("a backend-only PR got a button")
	}
	f.poll()
	if asked() != 1 {
		t.Fatalf("its files were asked for %d times at one commit, want 1", asked())
	}

	atHead("bbb")
	files("internal/queue/webhook.go", "apps/web/components/Billing.tsx")
	if n := f.poll(); n != 1 || !strings.Contains(f.body(12), ReviewButtonMarker) {
		t.Fatalf("a PR that now touches a component got no button; calls:\n%s", f.calls())
	}

	// Asked for by hand, a backend PR gets one too.
	g := newButtonFixture(t)
	g.setLocal(RepoConfig{ReviewButton: boolp(true), Login: &LoginConfig{Script: "login.sh", Users: []LoginUser{{Email: "pro@acme.test"}}}})
	g.githubPR(12, strp("Moves the webhook queue."), "MEMBER", "acme/shop")
	os.WriteFile(filepath.Join(g.ghDir, "files-12.json"), []byte(`[{"filename":"internal/queue/webhook.go"}]`), 0o644)
	if w := g.putButton("pro@acme.test", "/settings/webhooks"); w.Code != http.StatusOK {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	if n := g.poll(); n != 1 {
		t.Fatalf("a button someone asked for wasn't added")
	}
}

func TestUIFile(t *testing.T) {
	for name, want := range map[string]bool{
		"apps/web/app/billing/page.tsx": true, "src/Button.jsx": true, "styles/main.css": true, "x/y.module.scss": true,
		"packages/ui/components/Card.ts": true, "public/logo.png": true, "apps/web/public/static/locales/en/common.json": true,
		"web/views/home.html": true, "src/App.vue": true,
		"internal/queue/webhook.go": false, "api/v2/bookings.ts": false, "README.md": false, "prisma/schema.prisma": false,
		"package.json": false, ".github/workflows/ci.yml": false, "src/lib/date.test.ts": false,
	} {
		if got := uiFile(name); got != want {
			t.Errorf("uiFile(%q) = %v, want %v", name, got, want)
		}
	}
}
