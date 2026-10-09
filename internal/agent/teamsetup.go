package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/team"
)

// TeamSetupRequest starts a team setup on a box: the commit the engineer
// reviewed, which projects (all they can read when empty), and the keys
// they typed. Repos instead sets up an org's repositories without a
// .berth (the picker).
type TeamSetupRequest struct {
	Box      string                       `json:"box"`
	Commit   string                       `json:"commit"`
	Projects []string                     `json:"projects,omitempty"`
	Keys     map[string]map[string]string `json:"keys,omitempty"`
	Repos    []string                     `json:"repos,omitempty"`
	// SkipOnePassword skips 1Password for the shared keys, where the team
	// allows it: op is never signed in or called for them; Keys holds the
	// values typed for them instead, and those left blank are missing.
	SkipOnePassword bool `json:"skip_onepassword,omitempty"`
	// Use says, per project (its id, or its repo for Repos), which clone
	// already on the box to use instead of cloning: a folder there, or ""
	// for a fresh clone. A project left out gets the box's default: a
	// Shipyard location of the same repo if there is one, else a clone.
	Use map[string]string `json:"use,omitempty"`
}

// applyUse sets what the engineer chose for a project's clone.
func applyUse(plan *box.TeamProjectPlan, use map[string]string) {
	path, ok := use[plan.ID]
	if !ok {
		path, ok = use[plan.Repo]
	}
	if !ok {
		return
	}
	if path == "" {
		plan.Fresh = true
		return
	}
	plan.Use = path
}

var errTeamChanged = errors.New("the team setup changed since you reviewed it; review it again")

// teamBundle builds what the box runs, from the commit the engineer
// reviewed: the box gets .berth's files as read here, each project's kit as
// reviewed, and the hash of each repository's own config to trust.
func (a *Agent) teamBundle(ctx context.Context, g ghCLI, src team.Source, req TeamSetupRequest) (box.TeamBundle, *teamRead, func(), error) {
	cleanup := func() {}
	if len(req.Repos) > 0 {
		if src.Link {
			return box.TeamBundle{}, nil, cleanup, errors.New("pick repos from an org's name, not a link")
		}
		tb, err := a.repoBundle(ctx, g, src.Owner, req.Repos)
		for i := range tb.Projects {
			applyUse(&tb.Projects[i], req.Use)
		}
		return tb, nil, cleanup, err
	}
	if req.Commit == "" {
		return box.TeamBundle{}, nil, cleanup, errors.New("say which commit of the team setup you reviewed")
	}
	r, err := readTeam(ctx, g, src, req.Commit)
	if err != nil {
		return box.TeamBundle{}, nil, cleanup, err
	}
	if r.state != "found" {
		return box.TeamBundle{}, nil, cleanup, fmt.Errorf("%s has no team setup this account can read", src.String())
	}
	if !strings.HasPrefix(r.commit.SHA, req.Commit) {
		return box.TeamBundle{}, nil, cleanup, errTeamChanged
	}
	skipOP := req.SkipOnePassword && r.setup.UsesOnePassword()
	if skipOP && r.setup.RequiresOnePassword() {
		return box.TeamBundle{}, nil, cleanup, fmt.Errorf("%s's team setup needs 1Password for its shared keys (\"onepassword\": \"required\" in team.json)", r.setup.Name)
	}
	projects := a.checkProjects(ctx, g, r, true)
	var kitDirs []string
	cleanup = func() {
		for _, d := range kitDirs {
			os.RemoveAll(d)
		}
	}
	tb := box.TeamBundle{ID: r.setup.ID, Name: r.setup.Name, Org: r.setup.Org, Commit: r.commit.SHA, Script: r.setup.Box.Script,
		Steps: r.setup.Box.Steps, Settings: r.setup.Box.Settings, Files: map[string]string{}, Projects: []box.TeamProjectPlan{}}
	if tb.Steps == nil {
		tb.Steps = []team.Step{}
	}
	for p, b := range r.files {
		tb.Files[p] = base64.StdEncoding.EncodeToString(b)
	}
	want := map[string]bool{}
	for _, id := range req.Projects {
		want[id] = true
	}
	for _, tp := range projects {
		if tp.kit != nil {
			kitDirs = append(kitDirs, tp.kit.info.Path)
		}
		v := tp.view
		if !v.Access || (len(want) > 0 && !want[v.ID] && !v.Required) {
			continue
		}
		if v.Error != "" {
			return tb, &r, cleanup, fmt.Errorf("%s: %s", v.Repo, v.Error)
		}
		p, _ := r.setup.Project(v.ID)
		plan := box.TeamProjectPlan{ID: v.ID, Repo: v.Repo, URL: "https://github.com/" + v.Repo + ".git", Path: v.Path, Required: v.Required,
			Source: v.Source, TrustHash: v.ConfigHash, FirstTask: p.FirstTask}
		if p.Init != "" {
			plan.Init = cleanInside(p.Init)
		}
		applyUse(&plan, req.Use)
		if v.Source == "kit" && tp.kit != nil {
			in, err := kitInstall(tp.kit.info)
			if err != nil {
				return tb, &r, cleanup, err
			}
			plan.Kit = &in
		}
		if k, ok := r.setup.Keys[v.ID]; ok {
			plan.Env = map[string]string{}
			typed := req.Keys[v.ID]
			for name, ref := range k.Shared {
				plan.Keys = append(plan.Keys, name)
				if skipOP && team.IsOnePasswordRef(ref) {
					// Skipped: never op. What was typed, if anything; the
					// reference waits aside for Use 1Password.
					if plan.Deferred == nil {
						plan.Deferred = map[string]string{}
					}
					plan.Deferred[name] = ref
					if val := typed[name]; val != "" {
						plan.Env[name] = val
					}
					continue
				}
				plan.Env[name] = ref
			}
			for _, name := range k.Ask {
				plan.Keys = append(plan.Keys, name)
				plan.Ask = append(plan.Ask, name)
				if val := typed[name]; val != "" {
					plan.Env[name] = val
				}
			}
			sort.Strings(plan.Keys)
		}
		tb.Projects = append(tb.Projects, plan)
	}
	tb.GitHub = len(tb.Projects) > 0
	// The agent CLIs the team names, installed by Shipyard's own step.
	tb.Agents = r.setup.Agents
	// The box reads the shared keys' op:// references with its own op,
	// which Shipyard signs in as a step, so nothing asks in a service's
	// terminal later.
	tb.OnePassword = tb.GitHub && r.setup.UsesOnePassword() && !skipOP
	tb.OnePasswordSkipped = tb.GitHub && skipOP
	return tb, &r, cleanup, nil
}

var nonTeamID = regexp.MustCompile(`[^a-z0-9-]+`)

// repoTeamID is the team id of an org's repos set up without a .berth.
func repoTeamID(org string) string {
	return strings.Trim(nonTeamID.ReplaceAllString(strings.ToLower(org), "-"), "-")
}

// repoProjectID is a picked repo's project id.
func repoProjectID(name string) string { return team.URLSafeName(name) }

// repoBundle sets up repositories an org has without a .berth: no box
// steps but the box's GitHub sign-in, and each repository's own config,
// trusted as shown in the picker.
func (a *Agent) repoBundle(ctx context.Context, g ghCLI, org string, repos []string) (box.TeamBundle, error) {
	id := repoTeamID(org)
	if id == "" {
		return box.TeamBundle{}, fmt.Errorf("%q is not a GitHub org name", org)
	}
	tb := box.TeamBundle{ID: id, Name: org, Org: org, Steps: []team.Step{}, Files: map[string]string{}, GitHub: true}
	var o struct {
		Name string `json:"name"`
	}
	if g.api(ctx, "orgs/"+org, &o) == nil && o.Name != "" {
		tb.Name = o.Name
	}
	seen := map[string]bool{}
	for _, slug := range repos {
		if !team.ValidRepo(slug) || !strings.EqualFold(strings.Split(slug, "/")[0], org) {
			return tb, fmt.Errorf("%q is not one of %s's repositories", slug, org)
		}
		r, err := g.repo(ctx, slug)
		if err != nil {
			if errors.Is(err, errGHNotFound) {
				return tb, fmt.Errorf("you can't read %s", slug)
			}
			return tb, err
		}
		name := strings.SplitN(r.FullName, "/", 2)[1]
		// The project's id names it on the box, in every worktree's URL:
		// acme.com is acme-com there, cloned to ~/code/acme.com all the same.
		id := repoProjectID(name)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		plan := box.TeamProjectPlan{ID: id, Repo: r.FullName, URL: "https://github.com/" + r.FullName + ".git", Path: "~/code/" + name, Source: "none"}
		if raw, _, err := g.file(ctx, r.FullName, box.RepoConfigFile, ""); err == nil {
			plan.Source, plan.TrustHash = "repo", sha256Hex(raw)
		}
		tb.Projects = append(tb.Projects, plan)
	}
	return tb, nil
}

// postToBox sends a request to a paired box.
func (a *Agent) postToBox(ctx context.Context, boxName, method, path string, in, out any) error {
	c, ok := a.client(boxName)
	if !ok {
		return fmt.Errorf("no paired box named %s", boxName)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return box.NewClient(c).Call(ctx, method, path, in, out)
}

func (a *Agent) teamSetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/team/{org}/setup", func(w http.ResponseWriter, r *http.Request) {
		var req TeamSetupRequest
		if !decodeBody(w, r, &req) {
			return
		}
		if !a.githubReady(w, r) {
			return
		}
		if req.Box == "" {
			writeError(w, http.StatusBadRequest, "say which box to set up")
			return
		}
		a.sync()
		g, _ := newGH()
		org := r.PathValue("org")
		src, err := team.ParseSource(org)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		tb, read, cleanup, err := a.teamBundle(r.Context(), g, src, req)
		defer cleanup()
		if errors.Is(err, errTeamChanged) {
			writeCoded(w, http.StatusConflict, err.Error(), "team_changed")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		var st box.TeamStatus
		if err := a.postToBox(r.Context(), req.Box, http.MethodPost, "/v1/team", tb, &st); err != nil {
			writeBoxError(w, err)
			return
		}
		if read != nil {
			if err := a.accept(*read, req.Box); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		a.publish(Event{Type: "team.accepted", Data: map[string]any{"org": org, "team": tb.ID, "box": req.Box, "commit": tb.Commit}})
		writeJSON(w, http.StatusOK, st)
	})
	// pull fast-forwards a clone the page found that is behind: a separate
	// button, never part of adopting it.
	mux.HandleFunc("POST /v1/team/{org}/pull", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Box  string `json:"box"`
			Repo string `json:"repo"`
			Path string `json:"path"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		src, err := team.ParseSource(r.PathValue("org"))
		if err != nil || req.Box == "" {
			writeError(w, http.StatusBadRequest, "say which box, repo and folder")
			return
		}
		id := repoTeamID(src.Owner)
		if acc, _ := a.accepted(src); acc != nil {
			id = acc.ID
		}
		a.sync()
		var c box.ExistingClone
		if err := a.postToBox(r.Context(), req.Box, http.MethodPost, "/v1/team/"+url.PathEscape(id)+"/pull", map[string]string{"repo": req.Repo, "path": req.Path}, &c); err != nil {
			writeBoxError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("POST /v1/team/{org}/retry", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Box  string `json:"box"`
			From string `json:"from"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		org := r.PathValue("org")
		src, err := team.ParseSource(org)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		id := repoTeamID(src.Owner)
		if acc, _ := a.accepted(src); acc != nil {
			id = acc.ID
			if req.Box == "" {
				req.Box = acc.Box
			}
		}
		a.sync()
		var st box.TeamStatus
		if err := a.postToBox(r.Context(), req.Box, http.MethodPost, "/v1/team/"+url.PathEscape(id)+"/retry", map[string]string{"from": req.From}, &st); err != nil {
			writeBoxError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
}

// writeBoxError passes on a box's refusal. A box without tmux keeps its
// code, so the app shows how to install it rather than the bare error.
func writeBoxError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "tmux is not installed") {
		writeCoded(w, http.StatusServiceUnavailable, err.Error(), box.CodeTmuxMissing)
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
