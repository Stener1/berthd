package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/proxy"
	"github.com/cosscom/shipyard/internal/prreview"
	"github.com/cosscom/shipyard/internal/team"
)

// Review a PR in one click. A review link (berth://review?repo=…&pr=…), a
// paste or `berth review OWNER/NAME#N` opens a review sheet: what the pull
// request is, who wrote it, the exact commit, which box it would use, what
// would run there and which keys it would get, and what in it changes
// setup. Reading it touches nothing on any box: GitHub is read with this
// laptop's gh, as the reviewer, and the boxes are asked what they would do.
// Only Review (or `berth review --yes`) makes the worktree, at the commit
// shown, after every check has been made again.
//
// Who may be reviewed in one click is decided here (internal/prreview):
// a repository the team's setup lists, or one already on a box; an open
// pull request from a branch in that repository, by a member, owner or
// collaborator. Anything else is refused with what to do instead.

// ReviewSheet is what the review sheet shows.
type ReviewSheet struct {
	Repo     string            `json:"repo"`
	PR       int               `json:"pr"`
	Link     string            `json:"link"`
	URL      string            `json:"url,omitempty"`
	Title    string            `json:"title,omitempty"`
	State    string            `json:"state,omitempty"`
	Draft    bool              `json:"draft,omitempty"`
	Author   *ReviewAuthor     `json:"author,omitempty"`
	Head     *ReviewHead       `json:"head,omitempty"`
	Base     *ReviewBase       `json:"base,omitempty"`
	Reviewer string            `json:"reviewer,omitempty"`
	Org      string            `json:"org"`
	Team     *ReviewTeam       `json:"team,omitempty"`
	Hint     *ReviewHint       `json:"hint,omitempty"`
	Verdict  prreview.Verdict  `json:"verdict"`
	Boxes    []ReviewBox       `json:"boxes"`
	Box      string            `json:"box,omitempty"`
	Changes  []prreview.Change `json:"changes"`
	Files    int               `json:"files"`
	// Login is how the link asks to open the review: as a dev user, at a
	// page. Each box says whether its project's login allows it.
	Login    *ReviewLoginAsk `json:"login,omitempty"`
	ask      []string
	boxRepos []string
}

// ReviewLoginAsk is a link's as and path.
type ReviewLoginAsk struct {
	As   string `json:"as,omitempty"`
	Path string `json:"path,omitempty"`
}

// ReviewBoxLogin is whether a box's project lets the review open logged
// in as the link asks, and if not why: it opens anyway, without logging in.
type ReviewBoxLogin struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

type ReviewAuthor struct {
	Login       string `json:"login"`
	Association string `json:"association"`
}

type ReviewHead struct {
	Branch string `json:"branch"`
	SHA    string `json:"sha"`
	Repo   string `json:"repo,omitempty"`
	Cross  bool   `json:"cross"`
}

type ReviewBase struct {
	Branch string `json:"branch"`
}

type ReviewTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Org  string `json:"org"`
}

// ReviewHint is the commit the link named, and whether it is the head.
type ReviewHint struct {
	SHA     string `json:"sha"`
	Matches bool   `json:"matches"`
}

// ReviewBox is a box with the project: where a review would go, what it
// would run and which keys it would get.
type ReviewBox struct {
	Box      string            `json:"box"`
	Location string            `json:"location"`
	Setup    ReviewBoxSetup    `json:"setup"`
	Secrets  box.ReviewSecrets `json:"secrets"`
	IdleDays int               `json:"idle_days"`
	Existing *ReviewExisting   `json:"existing,omitempty"`
	Login    *ReviewBoxLogin   `json:"login,omitempty"`
	watch    []string
	login    *box.ReviewLogin
}

type ReviewBoxSetup struct {
	From           string              `json:"from"`
	Kit            *box.ReviewKit      `json:"kit,omitempty"`
	RepoConfig     string              `json:"repo_config"`
	DefaultBranch  string              `json:"default_branch,omitempty"`
	MatchesDefault *bool               `json:"matches_default,omitempty"`
	Script         string              `json:"script,omitempty"`
	Archive        string              `json:"archive,omitempty"`
	Services       []box.ReviewService `json:"services"`
	Hooks          int                 `json:"hooks"`
	Ports          int                 `json:"ports"`
}

type ReviewExisting struct {
	Worktree string `json:"worktree"`
	SHA      string `json:"sha"`
}

// ReviewOpenRequest is what the reviewer confirmed: the pull request, the
// commit they were shown, and the box.
type ReviewOpenRequest struct {
	Repo string `json:"repo"`
	PR   int    `json:"pr"`
	SHA  string `json:"sha"`
	Box  string `json:"box"`
	// As and Path, from the link, say how to open it once ready; they
	// change nothing the box fetches, sets up or trusts.
	As   string `json:"as,omitempty"`
	Path string `json:"path,omitempty"`
}

// ReviewUpdateRequest moves a review to the head the reviewer was shown.
type ReviewUpdateRequest struct {
	Box      string `json:"box"`
	Location string `json:"location"`
	Worktree string `json:"worktree"`
	SHA      string `json:"sha"`
}

// ReviewOpened is a review worktree made or moved.
type ReviewOpened struct {
	Box      string          `json:"box"`
	Location string          `json:"location"`
	Worktree box.Worktree    `json:"worktree"`
	Review   *box.ReviewMark `json:"review"`
	// Open is the address to open it at: through the login route when the
	// project's login allows the link's user, else the page alone.
	Open string `json:"open,omitempty"`
}

// ReviewStatus is how a review worktree stands against its PR now.
type ReviewStatus struct {
	Head       string `json:"head"`
	NewCommits int    `json:"new_commits"`
	State      string `json:"state"`
	Moved      bool   `json:"moved"`
	// Forced: the PR's branch was rewritten; the commit reviewed is no
	// longer in it.
	Forced bool `json:"forced,omitempty"`
}

// pullRequest reads a pull request with gh, as the reviewer.
func (g ghCLI) pullRequest(ctx context.Context, repo string, n int) (prreview.PR, error) {
	stdout, stderr, err := g.run(ctx, "pr", "view", strconv.Itoa(n), "-R", repo, "--json",
		"number,title,state,isDraft,url,author,authorAssociation,isCrossRepository,headRefName,headRefOid,headRepository,headRepositoryOwner,baseRefName")
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		// A gh without authorAssociation in pr view: the REST API has it.
		if strings.Contains(msg, "Unknown JSON field") {
			return g.pullRequestREST(ctx, repo, n)
		}
		if unreadable(msg) {
			return prreview.PR{}, errGHNotFound
		}
		if msg == "" {
			msg = err.Error()
		}
		return prreview.PR{}, fmt.Errorf("gh pr view: %s", strings.TrimPrefix(msg, "gh: "))
	}
	var v struct {
		Number              int                     `json:"number"`
		Title               string                  `json:"title"`
		State               string                  `json:"state"`
		IsDraft             bool                    `json:"isDraft"`
		URL                 string                  `json:"url"`
		AuthorAssociation   string                  `json:"authorAssociation"`
		IsCrossRepository   bool                    `json:"isCrossRepository"`
		HeadRefName         string                  `json:"headRefName"`
		HeadRefOid          string                  `json:"headRefOid"`
		BaseRefName         string                  `json:"baseRefName"`
		Author              struct{ Login string }  `json:"author"`
		HeadRepository      *struct{ Name string }  `json:"headRepository"`
		HeadRepositoryOwner *struct{ Login string } `json:"headRepositoryOwner"`
	}
	if err := json.Unmarshal(stdout, &v); err != nil {
		return prreview.PR{}, fmt.Errorf("gh pr view: %w", err)
	}
	pr := prreview.PR{Number: v.Number, Title: v.Title, State: strings.ToUpper(v.State), Draft: v.IsDraft, URL: v.URL, Author: v.Author.Login,
		Association: strings.ToUpper(v.AuthorAssociation), HeadBranch: v.HeadRefName, HeadSHA: strings.ToLower(v.HeadRefOid), Cross: v.IsCrossRepository, BaseBranch: v.BaseRefName}
	if v.HeadRepository != nil && v.HeadRepositoryOwner != nil && v.HeadRepository.Name != "" {
		pr.HeadRepo = v.HeadRepositoryOwner.Login + "/" + v.HeadRepository.Name
	}
	return pr, nil
}

func unreadable(msg string) bool {
	return strings.Contains(msg, "HTTP 404") || strings.Contains(msg, "Could not resolve to a") || strings.Contains(msg, "no pull requests found")
}

// pullRequestREST reads a pull request from the REST API.
func (g ghCLI) pullRequestREST(ctx context.Context, repo string, n int) (prreview.PR, error) {
	var v struct {
		Number            int                    `json:"number"`
		Title             string                 `json:"title"`
		State             string                 `json:"state"`
		Merged            bool                   `json:"merged"`
		Draft             bool                   `json:"draft"`
		HTMLURL           string                 `json:"html_url"`
		AuthorAssociation string                 `json:"author_association"`
		User              struct{ Login string } `json:"user"`
		Head              struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo *struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := g.api(ctx, "repos/"+repo+"/pulls/"+strconv.Itoa(n), &v); err != nil {
		return prreview.PR{}, err
	}
	state := strings.ToUpper(v.State)
	if v.Merged {
		state = prreview.StateMerged
	}
	pr := prreview.PR{Number: v.Number, Title: v.Title, State: state, Draft: v.Draft, URL: v.HTMLURL, Author: v.User.Login,
		Association: strings.ToUpper(v.AuthorAssociation), HeadBranch: v.Head.Ref, HeadSHA: strings.ToLower(v.Head.SHA), BaseBranch: v.Base.Ref}
	if v.Head.Repo != nil {
		pr.HeadRepo = v.Head.Repo.FullName
		pr.Cross = !strings.EqualFold(pr.HeadRepo, repo)
	} else {
		// The fork was deleted: not this repository's branch.
		pr.Cross = true
	}
	return pr, nil
}

// prFiles reads the files a pull request changes, with their lines where
// the diff can be read.
func (g ghCLI) prFiles(ctx context.Context, repo string, n int) ([]prreview.FileDiff, error) {
	stdout, _, err := g.run(ctx, "pr", "diff", strconv.Itoa(n), "-R", repo, "--color", "never")
	if err == nil {
		return prreview.ParseDiff(string(stdout)), nil
	}
	// Too large a diff for GitHub to send: the names, without their lines.
	stdout, stderr, err := g.run(ctx, "pr", "view", strconv.Itoa(n), "-R", repo, "--json", "files")
	if err != nil {
		return nil, fmt.Errorf("gh pr view: %s", strings.TrimSpace(string(stderr)))
	}
	var v struct {
		Files []struct{ Path string } `json:"files"`
	}
	if err := json.Unmarshal(stdout, &v); err != nil {
		return nil, err
	}
	out := make([]prreview.FileDiff, 0, len(v.Files))
	for _, f := range v.Files {
		out = append(out, prreview.FileDiff{Path: f.Path, NoPatch: true})
	}
	return out, nil
}

// reviewTeam is the team setup that lists repo, if one this laptop
// accepted does, with the keys it asks each engineer for there.
type reviewTeamInfo struct {
	team  ReviewTeam
	ask   []string
	repos []string
}

func (a *Agent) reviewTeams(repo string) (match *reviewTeamInfo, all []string) {
	for _, acc := range a.allAccepted() {
		var s team.Setup
		if json.Unmarshal(acc.Setup, &s) != nil {
			continue
		}
		for _, p := range s.Projects {
			all = append(all, p.Repo)
			if match == nil && strings.EqualFold(p.Repo, repo) {
				match = &reviewTeamInfo{team: ReviewTeam{ID: s.ID, Name: s.Name, Org: s.Org}, ask: append([]string{}, s.Keys[p.ID].Ask...)}
			}
		}
	}
	return match, all
}

// reviewBoxes asks each online box whether it has repo and what a review
// there would run.
func (a *Agent) reviewBoxes(ctx context.Context, repo string, pr int, ask []string) (boxes []ReviewBox, repos []string) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, st := range a.status().Boxes {
		if st.State != StateOnline {
			continue
		}
		c, ok := a.client(st.Name)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(name string, bc *box.Client) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			locs, err := bc.Locations(cctx)
			if err != nil {
				return
			}
			for _, l := range locs {
				mu.Lock()
				if l.Slug != "" {
					repos = append(repos, l.Slug)
				}
				mu.Unlock()
				if !strings.EqualFold(l.Slug, repo) {
					continue
				}
				q := url.Values{}
				if len(ask) > 0 {
					q.Set("withhold", strings.Join(ask, ","))
				}
				var s box.ReviewSetup
				if err := bc.Call(cctx, http.MethodGet, "/v1/locations/"+url.PathEscape(l.Name)+"/review-setup?"+q.Encode(), nil, &s); err != nil {
					continue
				}
				rb := ReviewBox{Box: name, Location: l.Name, Secrets: s.Secrets, IdleDays: s.IdleDays, watch: s.Watch, login: s.Login,
					Setup: ReviewBoxSetup{From: s.From, Kit: s.Kit, RepoConfig: s.RepoConfig, DefaultBranch: s.DefaultBranch, MatchesDefault: s.MatchesDefault,
						Script: s.Script, Archive: s.Archive, Services: s.Services, Hooks: s.Hooks, Ports: s.Ports}}
				for _, e := range s.Existing {
					if strings.EqualFold(e.Review.Repo, repo) && e.Review.PR == pr && rb.Existing == nil {
						rb.Existing = &ReviewExisting{Worktree: e.Worktree, SHA: e.Review.SHA}
					}
				}
				mu.Lock()
				boxes = append(boxes, rb)
				mu.Unlock()
			}
		}(st.Name, box.NewClient(c))
	}
	wg.Wait()
	sort.Slice(boxes, func(i, j int) bool {
		if boxes[i].Box != boxes[j].Box {
			return boxes[i].Box < boxes[j].Box
		}
		return boxes[i].Location < boxes[j].Location
	})
	return boxes, repos
}

// reviewSheet reads everything the sheet shows. Nothing is fetched to a
// box and nothing runs.
func (a *Agent) reviewSheet(ctx context.Context, g ghCLI, ref prreview.Ref, pick string) (ReviewSheet, error) {
	repo := ref.Repo()
	sh := ReviewSheet{Repo: repo, PR: ref.PR, Link: ref.Link(), URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, ref.PR), Boxes: []ReviewBox{}, Changes: []prreview.Change{}}
	if ref.As != "" || ref.Path != "" {
		sh.Link = ref.ShareLink()
		sh.Login = &ReviewLoginAsk{As: ref.As, Path: ref.Path}
	}
	owner, _, _ := strings.Cut(repo, "/")
	sh.Org = owner
	tm, teamRepos := a.reviewTeams(repo)
	if tm != nil {
		sh.Team, sh.ask = &tm.team, tm.ask
		sh.Org = tm.team.Org
	}
	boxes, boxRepos := a.reviewBoxes(ctx, repo, ref.PR, sh.ask)
	for i := range boxes {
		if ref.As == "" {
			continue
		}
		var l *prreview.Login
		if boxes[i].login != nil {
			l = &prreview.Login{Users: boxes[i].login.Users, Any: boxes[i].login.Any}
		}
		ok, why := prreview.LoginCheck(repo, ref.As, l)
		boxes[i].Login = &ReviewBoxLogin{Allowed: ok, Reason: why}
	}
	sh.Boxes, sh.boxRepos = boxes, boxRepos
	policy := prreview.Policy{TeamRepos: teamRepos, BoxRepos: boxRepos, Org: sh.Org}
	// A repository nobody listed is refused before GitHub is asked anything.
	if v := prreview.CheckRepo(repo, policy); !v.Allowed {
		sh.Verdict = v
		return sh, nil
	}
	pr, err := g.pullRequest(ctx, repo, ref.PR)
	if errors.Is(err, errGHNotFound) {
		sh.Verdict = prreview.Verdict{Code: prreview.CodeUnreadable, Reason: fmt.Sprintf("You can't see %s#%d with your GitHub account, or it doesn't exist", repo, ref.PR)}
		return sh, nil
	}
	if err != nil {
		return sh, err
	}
	sh.Title, sh.State, sh.Draft = pr.Title, pr.State, pr.Draft
	if pr.URL != "" {
		sh.URL = pr.URL
	}
	sh.Author = &ReviewAuthor{Login: pr.Author, Association: pr.Association}
	sh.Head = &ReviewHead{Branch: pr.HeadBranch, SHA: pr.HeadSHA, Repo: pr.HeadRepo, Cross: pr.Cross}
	sh.Base = &ReviewBase{Branch: pr.BaseBranch}
	if ref.SHA != "" {
		sh.Hint = &ReviewHint{SHA: ref.SHA, Matches: strings.HasPrefix(pr.HeadSHA, ref.SHA)}
	}
	sh.Verdict = prreview.Authorize(repo, pr, policy)
	if !sh.Verdict.Allowed {
		return sh, nil
	}
	if len(sh.Boxes) == 0 {
		sh.Verdict = prreview.Verdict{Code: prreview.CodeNoBox, Reason: fmt.Sprintf("No box of yours has %s yet. Set it up from Team setup first", repo)}
		return sh, nil
	}
	sh.Box = sh.Boxes[0].Box
	for _, b := range sh.Boxes {
		if b.Box == pick {
			sh.Box = pick
		}
	}
	var watch []string
	for _, b := range sh.Boxes {
		watch = append(watch, b.watch...)
	}
	files, err := g.prFiles(ctx, repo, ref.PR)
	if err != nil {
		return sh, err
	}
	sh.Files = len(files)
	sh.Changes = prreview.SetupChanges(files, watch)
	if st := githubState(ctx); st.State == "ready" {
		sh.Reviewer = st.Login
	}
	return sh, nil
}

// boxCall calls a box and keeps its status and error code.
func (a *Agent) boxCall(ctx context.Context, boxName, method, path string, in, out any) (int, string, error) {
	c, ok := a.client(boxName)
	if !ok {
		return http.StatusNotFound, "not_found", fmt.Errorf("no paired box named %s", boxName)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	resp, err := c.DoWithHeader(ctx, method, path, body, http.Header{"Content-Type": {"application/json"}, box.OriginHeader: {"app"}})
	if err != nil {
		return http.StatusBadGateway, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error, Code string }
		json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
		if e.Error == "" {
			e.Error = "the box replied " + resp.Status
		}
		return resp.StatusCode, e.Code, errors.New(e.Error)
	}
	if out != nil {
		return resp.StatusCode, "", json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, "", nil
}

// reviewReady answers when gh can't read GitHub as the reviewer.
func reviewReady(w http.ResponseWriter, r *http.Request) (ghCLI, bool) {
	st := githubState(r.Context())
	switch st.State {
	case "missing":
		writeCoded(w, http.StatusPreconditionFailed, "Install the GitHub CLI (gh) first: "+st.Install.Command, "gh_missing")
		return ghCLI{}, false
	case "signed-out":
		writeCoded(w, http.StatusPreconditionFailed, "Connect GitHub first: run gh auth login", "gh_signed_out")
		return ghCLI{}, false
	}
	g, err := newGH()
	if err != nil {
		writeCoded(w, http.StatusPreconditionFailed, err.Error(), "gh_missing")
		return ghCLI{}, false
	}
	return g, true
}

// confirmed checks again, at Review, everything the sheet showed: the pull
// request is still allowed, its head is still the commit shown, and the box
// has the project.
func (a *Agent) confirmed(w http.ResponseWriter, r *http.Request, g ghCLI, ref prreview.Ref, sha, boxName string) (ReviewSheet, *ReviewBox, bool) {
	sh, err := a.reviewSheet(r.Context(), g, ref, boxName)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return sh, nil, false
	}
	if !sh.Verdict.Allowed {
		writeCoded(w, http.StatusForbidden, sh.Verdict.Reason, sh.Verdict.Code)
		return sh, nil, false
	}
	if sh.Head == nil || sh.Head.SHA != sha {
		got := ""
		if sh.Head != nil {
			got = sh.Head.SHA
		}
		writeCoded(w, http.StatusConflict, fmt.Sprintf("PR #%d moved on since you opened it: its head is %s now, not %s. Look at it again", ref.PR, shortSHA(got), shortSHA(sha)), "moved")
		return sh, nil, false
	}
	for i := range sh.Boxes {
		if sh.Boxes[i].Box == boxName {
			return sh, &sh.Boxes[i], true
		}
	}
	writeCoded(w, http.StatusBadRequest, fmt.Sprintf("%s has no %s", boxName, ref.Repo()), "no_box")
	return sh, nil, false
}

func (a *Agent) prReviewRoutes(mux *http.ServeMux) {
	// The sheet: read-only, on every box and on GitHub.
	mux.HandleFunc("POST /v1/pr-review/plan", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Link string `json:"link"`
			Repo string `json:"repo"`
			PR   int    `json:"pr"`
			Box  string `json:"box"`
			As   string `json:"as"`
			Path string `json:"path"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		var ref prreview.Ref
		var err error
		if req.Link != "" {
			ref, err = prreview.ParseArg(req.Link)
		} else {
			ref, err = prreview.ParseArg(req.Repo + "#" + strconv.Itoa(req.PR))
		}
		// as and path given beside a link or OWNER/NAME#N are read as the
		// link reads them.
		if err == nil && (req.As != "" || req.Path != "") {
			ref, err = prreview.ParseLink(prreview.LinkFor(ref.Repo(), ref.PR, firstOf(req.As, ref.As), firstOf(req.Path, ref.Path)))
		}
		if err != nil {
			writeCoded(w, http.StatusBadRequest, err.Error(), "bad_link")
			return
		}
		g, ok := reviewReady(w, r)
		if !ok {
			return
		}
		a.sync()
		sh, err := a.reviewSheet(r.Context(), g, ref, req.Box)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sh)
	})
	// Review: everything checked again, then the box makes the worktree at
	// the commit the reviewer was shown.
	mux.HandleFunc("POST /v1/pr-review/open", func(w http.ResponseWriter, r *http.Request) {
		var req ReviewOpenRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.SHA = strings.ToLower(req.SHA)
		ref, err := prreview.ParseLink(prreview.LinkFor(req.Repo, req.PR, req.As, req.Path))
		if err != nil || !prreview.ValidSHA(req.SHA) || req.Box == "" {
			writeCoded(w, http.StatusBadRequest, "say the repo, the PR, the full commit you were shown and the box", "bad_request")
			return
		}
		g, ok := reviewReady(w, r)
		if !ok {
			return
		}
		a.sync()
		sh, rb, ok := a.confirmed(w, r, g, ref, req.SHA, req.Box)
		if !ok {
			return
		}
		open := box.ReviewOpen{Location: rb.Location, Repo: sh.Repo, PR: sh.PR, SHA: req.SHA, Title: sh.Title, Author: sh.Author.Login,
			Association: sh.Author.Association, HeadBranch: sh.Head.Branch, URL: sh.URL, Reviewer: sh.Reviewer, Withhold: sh.ask}
		var wt box.Worktree
		if code, ecode, err := a.boxCall(r.Context(), req.Box, http.MethodPost, "/v1/reviews", open, &wt); err != nil {
			writeCoded(w, code, err.Error(), ecode)
			return
		}
		writeJSON(w, http.StatusOK, ReviewOpened{Box: req.Box, Location: rb.Location, Worktree: wt, Review: wt.Review, Open: a.reviewOpenURL(req.Box, rb, wt, ref)})
	})
	// Update to latest: the sheet again, confirmed again.
	mux.HandleFunc("POST /v1/pr-review/update", func(w http.ResponseWriter, r *http.Request) {
		var req ReviewUpdateRequest
		if !decodeBody(w, r, &req) {
			return
		}
		req.SHA = strings.ToLower(req.SHA)
		if !prreview.ValidSHA(req.SHA) || req.Box == "" || req.Location == "" || req.Worktree == "" {
			writeCoded(w, http.StatusBadRequest, "say the box, the project, the review and the full commit you were shown", "bad_request")
			return
		}
		g, ok := reviewReady(w, r)
		if !ok {
			return
		}
		a.sync()
		mark, err := a.reviewMark(r.Context(), req.Box, req.Location, req.Worktree)
		if err != nil {
			writeCoded(w, http.StatusNotFound, err.Error(), "not_found")
			return
		}
		ref := prreview.Ref{PR: mark.PR}
		ref.Owner, ref.Name, _ = strings.Cut(mark.Repo, "/")
		sh, rb, ok := a.confirmed(w, r, g, ref, req.SHA, req.Box)
		if !ok {
			return
		}
		up := box.ReviewUpdate{Location: req.Location, Worktree: req.Worktree, SHA: req.SHA, Title: sh.Title, Author: sh.Author.Login,
			Association: sh.Author.Association, Reviewer: sh.Reviewer, Withhold: sh.ask}
		var wt box.Worktree
		if code, ecode, err := a.boxCall(r.Context(), req.Box, http.MethodPost, "/v1/reviews/update", up, &wt); err != nil {
			writeCoded(w, code, err.Error(), ecode)
			return
		}
		writeJSON(w, http.StatusOK, ReviewOpened{Box: req.Box, Location: rb.Location, Worktree: wt, Review: wt.Review})
	})
	// How a review stands: new commits since it was opened.
	mux.HandleFunc("GET /v1/pr-review/status", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		g, ok := reviewReady(w, r)
		if !ok {
			return
		}
		a.sync()
		mark, err := a.reviewMark(r.Context(), q.Get("box"), q.Get("location"), q.Get("worktree"))
		if err != nil {
			writeCoded(w, http.StatusNotFound, err.Error(), "not_found")
			return
		}
		st, err := reviewStatus(r.Context(), g, *mark)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// reviewOpenURL is where a ready review opens: its private URL at the
// link's page, through the login route when the project's login lets the
// link's user in. "" when its names can't be a host.
func (a *Agent) reviewOpenURL(boxName string, rb *ReviewBox, wt box.Worktree, ref prreview.Ref) string {
	labels := []string{strings.ToLower(wt.Name), strings.ToLower(rb.Location), strings.ToLower(boxName)}
	for _, l := range labels {
		if !hostLabel.MatchString(l) {
			return ""
		}
	}
	origin := "http://" + strings.Join(labels, ".") + ".localhost"
	if p := a.status().Proxy.URLPort; p != 0 && p != 80 {
		origin += ":" + strconv.Itoa(p)
	}
	if rb.Login != nil && rb.Login.Allowed {
		return proxy.LoginURL(origin, ref.As, ref.Path)
	}
	return origin + proxy.SafeNext(ref.Path)
}

// reviewMark finds a review worktree's mark on its box.
func (a *Agent) reviewMark(ctx context.Context, boxName, location, worktree string) (*box.ReviewMark, error) {
	c, ok := a.client(boxName)
	if !ok {
		return nil, fmt.Errorf("no paired box named %s", boxName)
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	locs, err := box.NewClient(c).Locations(cctx)
	if err != nil {
		return nil, err
	}
	for _, l := range locs {
		if l.Name != location {
			continue
		}
		for _, wt := range l.Worktrees {
			if wt.Name == worktree && wt.Review != nil {
				return wt.Review, nil
			}
		}
	}
	return nil, fmt.Errorf("%s has no review %s/%s", boxName, location, worktree)
}

// reviewStatus compares a review's commit with its PR's head now.
func reviewStatus(ctx context.Context, g ghCLI, mark box.ReviewMark) (ReviewStatus, error) {
	pr, err := g.pullRequest(ctx, mark.Repo, mark.PR)
	if err != nil {
		return ReviewStatus{}, err
	}
	st := ReviewStatus{Head: pr.HeadSHA, State: pr.State, Moved: pr.HeadSHA != mark.SHA}
	if !st.Moved {
		return st, nil
	}
	var cmp struct {
		TotalCommits int    `json:"total_commits"`
		AheadBy      int    `json:"ahead_by"`
		Status       string `json:"status"`
	}
	if err := g.api(ctx, "repos/"+mark.Repo+"/compare/"+mark.SHA+"..."+pr.HeadSHA, &cmp); err != nil {
		st.Forced = true
		return st, nil
	}
	st.NewCommits = cmp.TotalCommits
	if cmp.AheadBy > 0 {
		st.NewCommits = cmp.AheadBy
	}
	st.Forced = cmp.Status == "diverged" || cmp.Status == "behind"
	return st, nil
}
