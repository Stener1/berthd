package box

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/events"
	"github.com/cosscom/shipyard/internal/proxy"
	"github.com/cosscom/shipyard/internal/prreview"
	"github.com/cosscom/shipyard/internal/statefile"
)

// The "Review in Shipyard" button. GitHub strips berth:// links from PR
// descriptions, so a review link goes through the site: a badge that links
// to https://berthd.app/review?repo=…&pr=…, a static page that checks the
// parameters and opens the berth:// link (site/review/index.html).
//
// When a project turns it on ("review_button": true in the box's own config,
// the default branch's trusted .berth/config.json, its kit, or the team's
// team.json), berthd adds the button once to the description of each pull
// request opened from one of the project's worktrees: between two markers,
// appended after everything else, which it never touches. Later it only
// replaces what is between the markers, when the worktree's as and path are
// set (`berthd review-button --as EMAIL --path /x`). A PR from a fork, or by
// someone outside the org, gets nothing, as a review would refuse it.

const (
	// ReviewButtonMarker starts the button's block in a PR's description,
	// and reviewButtonEnd ends it.
	ReviewButtonMarker = "<!-- shipyard-review-button -->"
	reviewButtonEnd    = "<!-- /shipyard-review-button -->"
	// ReviewSite is where the bounce page and the badge are served.
	ReviewSite = "https://berthd.app"
)

// reviewButtonEvery is how often a worktree's PR is looked for again until
// it has its button; reviewButtonMaxCalls bounds gh calls per poll.
const (
	reviewButtonEvery    = 2 * time.Minute
	reviewButtonMaxCalls = 20
)

// ReviewButtons keeps, per worktree, what berthd did about its PR's button,
// and the as and path its button should carry.
type ReviewButtons struct {
	Path string
	// GH is the gh to run; empty is the one on PATH. Tests give a fake.
	GH string
	mu sync.Mutex
	// cursor is where the next poll starts, so with more worktrees than
	// calls each still gets its turn.
	cursor int
}

// ReviewButtonMark is one worktree's button.
type ReviewButtonMark struct {
	Branch string `json:"branch"`
	// As and Path are what the button should open with; set by
	// `berthd review-button`. WantSet says they were set at all, so a
	// button someone wrote by hand is left alone until then.
	As       string `json:"as,omitempty"`
	PagePath string `json:"path,omitempty"`
	WantSet  bool   `json:"want_set,omitempty"`
	// Repo and PR are the pull request once found.
	Repo string `json:"repo,omitempty"`
	PR   int    `json:"pr,omitempty"`
	// Done says nothing is left to do with the as and path above: the button
	// is there (placed by berthd or by hand), or the PR gets none (Skipped
	// says why).
	Done     bool   `json:"done,omitempty"`
	DoneAs   string `json:"done_as,omitempty"`
	DonePath string `json:"done_path,omitempty"`
	Skipped  string `json:"skipped,omitempty"`
	// Checked is the last look at GitHub.
	Checked time.Time `json:"checked,omitzero"`
	// NoUIAt is the PR's head commit when its files showed nothing a
	// browser shows: it is looked at again once the PR moves on.
	NoUIAt string `json:"no_ui_at,omitempty"`
}

// uiFile says whether a changed file can change what a page shows: the
// button is for pull requests a reviewer looks at in a browser.
func uiFile(name string) bool {
	n := strings.ToLower(name)
	switch path.Ext(n) {
	case ".tsx", ".jsx", ".vue", ".svelte", ".astro", ".html", ".htm", ".css", ".scss", ".sass", ".less", ".styl", ".mdx",
		".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico", ".woff", ".woff2":
		return true
	}
	for _, dir := range []string{"components/", "pages/", "views/", "layouts/", "styles/", "public/", "locales/", "i18n/"} {
		if strings.HasPrefix(n, dir) || strings.Contains(n, "/"+dir) {
			return true
		}
	}
	return false
}

func (s *ReviewButtons) read() map[string]*ReviewButtonMark {
	out := map[string]*ReviewButtonMark{}
	if s == nil || s.Path == "" {
		return out
	}
	if b, err := os.ReadFile(s.Path); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

func (s *ReviewButtons) write(m map[string]*ReviewButtonMark) error {
	if s == nil || s.Path == "" {
		return nil
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(s.Path, append(b, '\n'))
}

// update changes one worktree's mark under the lock.
func (s *ReviewButtons) update(path string, fn func(m *ReviewButtonMark)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.read()
	m := all[path]
	if m == nil {
		m = &ReviewButtonMark{}
		all[path] = m
	}
	fn(m)
	return s.write(all)
}

func (s *ReviewButtons) get(path string) ReviewButtonMark {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.read()[path]; m != nil {
		return *m
	}
	return ReviewButtonMark{}
}

// ReviewButtonLink is the bounce page's address for repo's pr, opening
// logged in as as, at path (either may be ""). The site rebuilds the
// berth:// link from these alone.
func ReviewButtonLink(repo string, pr int, as, path string) string {
	l := ReviewSite + "/review?repo=" + repo + "&pr=" + strconv.Itoa(pr)
	if as != "" {
		l += "&as=" + queryEscape(as)
	}
	if path != "" {
		l += "&path=" + queryEscape(path)
	}
	return l
}

// queryEscape keeps / and @ readable and escapes what a query value needs
// to, as the review link does.
func queryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("-._~/@", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ReviewButtonBlock is the button as it goes in a PR's description,
// markers included.
func ReviewButtonBlock(repo string, pr int, as, path string, nl string) string {
	return ReviewButtonMarker + nl +
		"[![Review in Shipyard](" + ReviewSite + "/badges/review.svg)](" + ReviewButtonLink(repo, pr, as, path) + ")" + nl +
		reviewButtonEnd
}

// maxReviewPath bounds a button's path, as a review link does.
const maxReviewPath = 512

// validButtonPath is a page on the worktree's own host, as a review link's
// path must be: one the login route's next takes unchanged, with no space.
func validButtonPath(p string) bool {
	return p != "" && len(p) <= maxReviewPath && !strings.Contains(p, " ") && proxy.SafeNext(p) == p
}

// withReviewButton is body with block in it: appended after everything
// else when there is no button, or put in place of the marked one when
// replace is set. ok is false when body is to be left as it is: it already
// has the block, a marked button that is not to be replaced, a marked one
// with no end, or a review button someone wrote by hand. Nothing outside the
// marked block ever changes.
func withReviewButton(body, block string, replace bool) (string, bool) {
	if start := strings.Index(body, ReviewButtonMarker); start >= 0 {
		rel := strings.Index(body[start:], reviewButtonEnd)
		if rel < 0 || !replace {
			return body, false
		}
		end := start + rel + len(reviewButtonEnd)
		if body[start:end] == block {
			return body, false
		}
		return body[:start] + block + body[end:], true
	}
	if strings.Contains(body, ReviewSite+"/review?") || strings.Contains(body, "berth://review?") {
		return body, false
	}
	nl := "\n"
	if strings.Contains(body, "\r\n") {
		nl = "\r\n"
	}
	switch {
	case body == "":
		return block, true
	case strings.HasSuffix(body, nl+nl):
		return body + block, true
	case strings.HasSuffix(body, nl):
		return body + nl + block, true
	}
	return body + nl + nl + block, true
}

// reviewButtonState says whether a project's PRs get the button, and where
// that comes from: the box's own config, the repository's trusted config,
// the kit, the team's team.json, or "" (off by default).
type ReviewButtonState struct {
	On   bool   `json:"on"`
	From string `json:"from,omitempty"`
	// Inherited is what the project has without the box's own setting.
	Inherited     bool   `json:"inherited"`
	InheritedFrom string `json:"inherited_from,omitempty"`
}

// reviewButtonFor is a location's setting.
func (b *Box) reviewButtonFor(name string) ReviewButtonState {
	saved, err := b.Locations.saved(name)
	if err != nil {
		return ReviewButtonState{}
	}
	var st ReviewButtonState
	if b.teamReviewButton(saved.Path) {
		st.Inherited, st.InheritedFrom = true, "team"
	}
	if repo, _, err := repoLayer(saved); err == nil && repo.ReviewButton != nil {
		st.Inherited, st.InheritedFrom = *repo.ReviewButton, "repo"
	}
	if saved.Kit != nil && saved.Kit.Config.ReviewButton != nil {
		st.Inherited, st.InheritedFrom = *saved.Kit.Config.ReviewButton, "kit"
	}
	st.On, st.From = st.Inherited, st.InheritedFrom
	if saved.Config != nil && saved.Config.ReviewButton != nil {
		st.On, st.From = *saved.Config.ReviewButton, "box"
	}
	return st
}

// teamReviewButton says a team setup on the box turns the button on for
// the project cloned at path.
func (b *Box) teamReviewButton(path string) bool {
	if b.Team == nil || b.Team.Dir == "" {
		return false
	}
	entries, _ := os.ReadDir(b.Team.Dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tb, err := b.Team.readBundle(e.Name())
		if err != nil {
			continue
		}
		for _, p := range tb.Projects {
			dest, err := filepath.Abs(expandHome(p.Path))
			if err == nil && samePath(dest, path) && p.ReviewButton {
				return true
			}
		}
	}
	return false
}

// ghPull is what GitHub's REST API says about a pull request.
type ghPull struct {
	Number      int     `json:"number"`
	State       string  `json:"state"`
	Body        *string `json:"body"`
	Association string  `json:"author_association"`
	Head        struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
}

func (p ghPull) body() string {
	if p.Body == nil {
		return ""
	}
	return *p.Body
}

// gh runs the box's gh in dir with stdin, and decodes its JSON into out
// (when not nil).
func (s *ReviewButtons) gh(ctx context.Context, dir string, stdin []byte, out any, args ...string) error {
	bin := s.GH
	if bin == "" {
		p, err := toolPath("gh")
		if err != nil {
			return err
		}
		bin = p
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gh %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

// errBodyMoving is a description that changed under berthd twice in a row:
// it waits for the next look.
var errBodyMoving = errors.New("the PR's description changed while Shipyard was adding its button")

// placeReviewButton adds block to the PR's description, or replaces the
// marked one. GitHub has no compare-and-set for a description, so it reads
// the description again just before writing, and when that differs from what
// it worked from, works from the new one, once.
func (s *ReviewButtons) placeReviewButton(ctx context.Context, dir, repo string, pr int, body, block string, replace bool) (bool, error) {
	cur := body
	for attempt := 0; attempt < 2; attempt++ {
		next, ok := withReviewButton(cur, block, replace)
		if !ok {
			return false, nil
		}
		var fresh ghPull
		if err := s.gh(ctx, dir, nil, &fresh, "api", fmt.Sprintf("repos/%s/pulls/%d", repo, pr)); err != nil {
			return false, err
		}
		if fresh.body() != cur {
			cur = fresh.body()
			continue
		}
		payload, _ := json.Marshal(map[string]string{"body": next})
		if err := s.gh(ctx, dir, payload, nil, "api", "-X", "PATCH", fmt.Sprintf("repos/%s/pulls/%d", repo, pr), "--input", "-"); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, errBodyMoving
}

// RunReviewButtons looks for new PRs every 30 seconds until ctx ends.
func (b *Box) RunReviewButtons(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.PollReviewButtons(ctx, time.Now())
		}
	}
}

type buttonTarget struct {
	loc Location
	wt  Worktree
}

// PollReviewButtons gives each PR opened from a worktree of a project that
// has the button on its button, once, and puts a changed as or path in
// place. It returns how many descriptions it edited.
func (b *Box) PollReviewButtons(ctx context.Context, now time.Time) int {
	s := b.ReviewButtons
	if s == nil {
		return 0
	}
	if s.GH == "" {
		if _, err := toolPath("gh"); err != nil {
			return 0
		}
	}
	locs, err := b.Locations.List(ctx)
	if err != nil {
		return 0
	}
	var targets []buttonTarget
	for _, l := range locs {
		if !l.Repo || !b.reviewButtonFor(l.Name).On {
			continue
		}
		for _, w := range l.Worktrees {
			// A review worktree is someone else's PR; the main checkout has
			// none of its own.
			if w.Main || w.Review != nil || w.Branch == "" || w.Branch == l.DefaultBranch {
				continue
			}
			targets = append(targets, buttonTarget{l, w})
		}
	}
	if len(targets) == 0 {
		return 0
	}
	s.mu.Lock()
	start := s.cursor
	s.mu.Unlock()
	if start >= len(targets) {
		start = 0
	}
	calls, edited, next := 0, 0, 0
	for i := range targets {
		j := (start + i) % len(targets)
		if calls >= reviewButtonMaxCalls {
			next = j
			break
		}
		n, e := b.reviewButtonOne(ctx, targets[j], now)
		calls += n
		if e {
			edited++
		}
	}
	s.mu.Lock()
	s.cursor = next
	s.mu.Unlock()
	return edited
}

// reviewButtonOne does what is due for one worktree: the gh calls it made,
// and whether it edited the description.
func (b *Box) reviewButtonOne(ctx context.Context, tg buttonTarget, now time.Time) (calls int, edited bool) {
	s := b.ReviewButtons
	m := s.get(tg.wt.Path)
	if m.Branch != tg.wt.Branch {
		// A new worktree, or one switched to another branch: start again,
		// keeping nothing of the old branch's PR.
		m = ReviewButtonMark{Branch: tg.wt.Branch}
		s.update(tg.wt.Path, func(x *ReviewButtonMark) { *x = m })
	}
	if m.Done && (m.Skipped != "" || (m.DoneAs == m.As && m.DonePath == m.PagePath)) {
		return 0, false
	}
	if !m.Checked.IsZero() && now.Sub(m.Checked) < reviewButtonEvery && !(m.Done && m.WantSet) {
		return 0, false
	}
	repo := originSlug(ctx, tg.loc.Path)
	if repo == "" {
		repo = tg.loc.Slug
	}
	if !prreview.ValidRepo(repo) {
		return 0, false
	}
	owner, _, _ := strings.Cut(repo, "/")
	set := func(fn func(x *ReviewButtonMark)) {
		s.update(tg.wt.Path, func(x *ReviewButtonMark) {
			x.Checked = now
			fn(x)
		})
	}
	// The branch's most recent PR in the repository itself: a fork's
	// branch has another owner, so it is never asked for.
	q := url.Values{"head": {owner + ":" + tg.wt.Branch}, "state": {"all"}, "per_page": {"5"}}
	var pulls []ghPull
	calls++
	if err := s.gh(ctx, tg.wt.Path, nil, &pulls, "api", "repos/"+repo+"/pulls?"+q.Encode()); err != nil {
		set(func(*ReviewButtonMark) {})
		return calls, false
	}
	var pr *ghPull
	for i := range pulls {
		if pulls[i].Head.Ref == tg.wt.Branch {
			pr = &pulls[i]
			break
		}
	}
	if pr == nil || pr.Number <= 0 {
		set(func(*ReviewButtonMark) {})
		return calls, false
	}
	skip := ""
	switch {
	case !strings.EqualFold(pr.State, "open"):
		skip = "closed"
	case pr.Head.Repo == nil || !strings.EqualFold(pr.Head.Repo.FullName, repo):
		skip = prreview.CodeFork
	case !prreview.TrustedAssociation(pr.Association):
		skip = prreview.CodeOutsider
	}
	if skip != "" {
		set(func(x *ReviewButtonMark) { x.Repo, x.PR, x.Done, x.Skipped = repo, pr.Number, true, skip })
		return calls, false
	}
	// Only for UI work, unless someone asked for this PR's button
	// (berthd review-button). A PR is looked at again when it moves on.
	if !m.WantSet {
		if pr.Head.SHA != "" && pr.Head.SHA == m.NoUIAt {
			set(func(*ReviewButtonMark) {})
			return calls, false
		}
		var files []struct {
			Filename string `json:"filename"`
		}
		calls++
		if err := s.gh(ctx, tg.wt.Path, nil, &files, "api", fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", repo, pr.Number)); err != nil {
			set(func(x *ReviewButtonMark) { x.Repo, x.PR = repo, pr.Number })
			return calls, false
		}
		ui := false
		for _, f := range files {
			if uiFile(f.Filename) {
				ui = true
				break
			}
		}
		if !ui {
			set(func(x *ReviewButtonMark) { x.Repo, x.PR, x.NoUIAt = repo, pr.Number, pr.Head.SHA })
			return calls, false
		}
	}
	nl := "\n"
	if strings.Contains(pr.body(), "\r\n") {
		nl = "\r\n"
	}
	block := ReviewButtonBlock(repo, pr.Number, m.As, m.PagePath, nl)
	calls += 2
	changed, err := s.placeReviewButton(ctx, tg.wt.Path, repo, pr.Number, pr.body(), block, m.WantSet)
	if err != nil {
		set(func(x *ReviewButtonMark) { x.Repo, x.PR = repo, pr.Number })
		return calls, false
	}
	set(func(x *ReviewButtonMark) {
		x.Repo, x.PR, x.Done, x.Skipped, x.DoneAs, x.DonePath = repo, pr.Number, true, "", m.As, m.PagePath
	})
	if changed {
		data := map[string]any{"repo": repo, "pr": pr.Number, "location": tg.loc.Name, "worktree": tg.wt.Name}
		if m.As != "" {
			data["as"] = m.As
		}
		if m.PagePath != "" {
			data["path"] = m.PagePath
		}
		b.Events.Publish(events.Event{Type: "review.button", Box: b.Name, Origin: "berthd", Time: now, Data: data})
	}
	return calls, changed
}

// ReviewButtonInfo is a worktree's button as the CLI and the app show it.
type ReviewButtonInfo struct {
	Location string `json:"location"`
	Worktree string `json:"worktree"`
	// Enabled says the project has the button on, and From where.
	Enabled bool   `json:"enabled"`
	From    string `json:"from,omitempty"`
	As      string `json:"as,omitempty"`
	Path    string `json:"path,omitempty"`
	Repo    string `json:"repo,omitempty"`
	PR      int    `json:"pr,omitempty"`
	// State is waiting (no PR yet), placed, skipped (Skipped says why) or
	// off.
	State   string `json:"state"`
	Skipped string `json:"skipped,omitempty"`
	// Link is the button's link once its PR is known.
	Link string `json:"link,omitempty"`
}

func (b *Box) reviewButtonInfo(loc Location, wt Worktree) ReviewButtonInfo {
	st := b.reviewButtonFor(loc.Name)
	info := ReviewButtonInfo{Location: loc.Name, Worktree: wt.Name, Enabled: st.On, From: st.From, State: "off"}
	if b.ReviewButtons == nil {
		return info
	}
	m := b.ReviewButtons.get(wt.Path)
	if m.Branch != "" && m.Branch != wt.Branch {
		m = ReviewButtonMark{}
	}
	info.As, info.Path, info.Repo, info.PR, info.Skipped = m.As, m.PagePath, m.Repo, m.PR, m.Skipped
	switch {
	case !st.On:
	case m.Skipped != "":
		info.State = "skipped"
	case m.Done:
		info.State = "placed"
	default:
		info.State = "waiting"
	}
	if m.PR > 0 && m.Skipped == "" {
		info.Link = ReviewButtonLink(m.Repo, m.PR, m.As, m.PagePath)
	}
	return info
}

func (b *Box) worktreeByName(ctx context.Context, loc, wt string) (Location, Worktree, error) {
	l, err := b.Locations.Get(ctx, loc)
	if err != nil {
		return Location{}, Worktree{}, err
	}
	for _, w := range l.Worktrees {
		if w.Name == wt {
			return l, w, nil
		}
	}
	return Location{}, Worktree{}, ErrUnknownWorktree
}

// getReviewButton is GET /v1/worktrees/{loc}/{wt}/review-button.
func (b *Box) getReviewButton(w http.ResponseWriter, r *http.Request) error {
	l, wt, err := b.worktreeByName(r.Context(), r.PathValue("loc"), r.PathValue("wt"))
	if err != nil {
		return err
	}
	writeJSON(w, b.reviewButtonInfo(l, wt))
	return nil
}

// putReviewButton is PUT /v1/worktrees/{loc}/{wt}/review-button {as, path}:
// what the worktree's button opens with. The next look puts it in place.
func (b *Box) putReviewButton(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		As   string `json:"as"`
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if b.ReviewButtons == nil {
		return httpError{http.StatusNotImplemented, "this box keeps no review buttons"}
	}
	l, wt, err := b.worktreeByName(r.Context(), r.PathValue("loc"), r.PathValue("wt"))
	if err != nil {
		return err
	}
	if wt.Main || wt.Review != nil {
		return badRequest("%s/%s opens no PR of its own, so it has no review button", l.Name, wt.Name)
	}
	if req.As != "" {
		if !ValidLoginEmail(req.As) {
			return badRequest("as %q is not a plain email address", clip(req.As, 80))
		}
		src, err := b.Locations.loginFor(l.Name)
		if err != nil {
			return err
		}
		if src == nil {
			return badRequest("%s has no login users; leave --as out", l.Name)
		}
		email, ok := src.cfg.allows(req.As)
		if !ok {
			return badRequest("%s isn't one of %s's login users (berthd login users)", req.As, l.Name)
		}
		req.As = email
	}
	if req.Path != "" && !validButtonPath(req.Path) {
		return badRequest("path %q is not a page on the worktree's own address, such as /settings", clip(req.Path, 80))
	}
	err = b.ReviewButtons.update(wt.Path, func(m *ReviewButtonMark) {
		if m.Branch != wt.Branch {
			*m = ReviewButtonMark{Branch: wt.Branch}
		}
		m.As, m.PagePath, m.WantSet = req.As, req.Path, true
	})
	if err != nil {
		return err
	}
	writeJSON(w, b.reviewButtonInfo(l, wt))
	return nil
}
