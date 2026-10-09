package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/statefile"
	"github.com/cosscom/shipyard/internal/team"
)

// Just the kit: a team setup's kit for one project, taken on its own for a
// project the person already has on a box, without the setup's box steps,
// sudo, clone or 1Password. It is read from <org>/.berth at the commit
// shown, kept on this laptop as a kit whose source says which team setup
// and commit it comes from, and installed on the project like any kit
// (internal/box/teamkit.go). A newer commit of .berth that changes it is
// kept here too, so the project's kit shows it has an update to review.
// The project's shared keys and its init are each an opt-in.

// TeamKitPlan is what the sheet shows before the kit is used.
type TeamKitPlan struct {
	Org      TeamOrg `json:"org"`
	Name     string  `json:"name"`
	Commit   string  `json:"commit"`
	Short    string  `json:"short"`
	Box      string  `json:"box"`
	Location string  `json:"location"`
	Project  string  `json:"project"`
	Repo     string  `json:"repo"`
	Kit      struct {
		Ref         string `json:"ref"`
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Hash        string `json:"hash"`
	} `json:"kit"`
	// SetsUp is what the kit brings each worktree, in a few words.
	SetsUp []string `json:"sets_up"`
	// Requires are the kit's tools, each checked on the box (Found is nil
	// when the box didn't say).
	Requires []TeamKitTool `json:"requires"`
	// Keys are the project's shared keys, each with what in the kit reads
	// it; Init the project's init. Both are off unless chosen.
	Keys []TeamKitKey    `json:"keys"`
	Init *TeamKitInitRef `json:"init,omitempty"`
	// Replaces names the kit the project has now, when it is another one;
	// Applied says the project has this kit already, as it is now.
	Replaces  string   `json:"replaces,omitempty"`
	Applied   bool     `json:"applied,omitempty"`
	Worktrees int      `json:"worktrees"`
	Warnings  []string `json:"warnings"`
}

type TeamKitTool struct {
	Tool  string `json:"tool"`
	Hint  string `json:"hint,omitempty"`
	Found *bool  `json:"found,omitempty"`
}

type TeamKitKey struct {
	Name        string   `json:"name"`
	OnePassword bool     `json:"onepassword"`
	UsedBy      []string `json:"used_by"`
}

type TeamKitInitRef struct {
	Script string `json:"script"`
	Detail string `json:"detail,omitempty"`
}

// teamKitRead is a team kit as read for a plan or to apply.
type teamKitRead struct {
	r       teamRead
	project team.Project
	kit     teamKit
	loc     box.Location
}

// readTeamKitFor reads the team setup at commit ("" for its newest) and
// the kit it declares for location's repository on boxName.
func (a *Agent) readTeamKitFor(ctx context.Context, g ghCLI, key, commit, boxName, location string) (teamKitRead, error) {
	src, err := team.ParseSource(key)
	if err != nil {
		return teamKitRead{}, err
	}
	var locs []box.Location
	if err := a.postToBox(ctx, boxName, http.MethodGet, "/v1/locations", nil, &locs); err != nil {
		return teamKitRead{}, err
	}
	var loc box.Location
	for _, l := range locs {
		if l.Name == location {
			loc = l
		}
	}
	if loc.Name == "" {
		return teamKitRead{}, fmt.Errorf("%s has no project called %s", boxName, location)
	}
	slug := box.GitHubSlug(loc.Remote)
	r, err := readTeam(ctx, g, src, commit)
	if err != nil {
		return teamKitRead{}, err
	}
	if r.state != "found" {
		return teamKitRead{}, fmt.Errorf("there is no team setup at %s that this account can read", src.String())
	}
	var p team.Project
	for _, x := range r.setup.Projects {
		if slug != "" && strings.EqualFold(x.Repo, slug) {
			p = x
		}
	}
	if p.ID == "" {
		return teamKitRead{}, fmt.Errorf("%s doesn't list %s", src.Slug(), firstNonEmpty(slug, location))
	}
	if p.Kit == "" {
		return teamKitRead{}, fmt.Errorf("%s has no kit for %s: its setup comes from the repository's own .berth/config.json", src.Slug(), p.Repo)
	}
	ref, err := team.ParseKitRef(p.Kit)
	if err != nil {
		return teamKitRead{}, err
	}
	k, err := a.readTeamKit(ctx, g, r, ref)
	if err != nil {
		return teamKitRead{}, err
	}
	return teamKitRead{r: r, project: p, kit: k, loc: loc}, nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// teamKitLink is where a team kit can be read again: the folder of
// <org>/.berth at its commit, or the kit's own pinned link.
func teamKitLink(owner, commit, ref string) string {
	kr, err := team.ParseKitRef(ref)
	if err != nil {
		return ref
	}
	switch {
	case kr.Path != "":
		return "https://github.com/" + owner + "/" + team.Repo + "/tree/" + commit + "/" + kr.Path
	case kr.Owner != "":
		l := "https://github.com/" + kr.Owner + "/" + kr.Name + "/tree/" + kr.Ref
		if kr.Sub != "" {
			l += "/" + kr.Sub
		}
		return l
	}
	return kr.Link + "@" + kr.Ref
}

// keyUsers says what in a kit reads a key: its setup, teardown, services
// or files.
func keyUsers(k KitInfo, name string) []string {
	var out []string
	c := k.Config
	if strings.Contains(c.Setup, name) {
		out = append(out, "setup")
	}
	if strings.Contains(c.Archive, name) {
		out = append(out, "teardown")
	}
	for _, s := range c.Services {
		if strings.Contains(s.Run, name) {
			out = append(out, "the "+serviceTitle(s)+" service")
		}
	}
	for _, v := range c.Env {
		if strings.Contains(v, name) {
			out = append(out, "its environment")
			break
		}
	}
	for _, f := range k.FileList {
		if f.Path != kitManifest && strings.Contains(f.Text, name) {
			out = append(out, f.Path)
		}
	}
	return out
}

func (a *Agent) teamKitPlan(ctx context.Context, key, boxName, location string) (TeamKitPlan, error) {
	g, err := newGH()
	if err != nil {
		return TeamKitPlan{}, err
	}
	tk, err := a.readTeamKitFor(ctx, g, key, "", boxName, location)
	if err != nil {
		return TeamKitPlan{}, err
	}
	defer os.RemoveAll(tk.kit.info.Path)
	info := tk.kit.info
	p := TeamKitPlan{Org: tk.r.org, Name: tk.r.setup.Name, Commit: tk.r.commit.SHA, Short: tk.r.commit.Short, Box: boxName, Location: location,
		Project: tk.project.ID, Repo: tk.project.Repo, SetsUp: configWords(info.Config), Requires: []TeamKitTool{}, Keys: []TeamKitKey{}, Warnings: append([]string{}, tk.r.warnings...)}
	if p.SetsUp == nil {
		p.SetsUp = []string{}
	}
	p.Kit.Ref, p.Kit.ID, p.Kit.Name, p.Kit.Description, p.Kit.Hash = tk.project.Kit, info.ID, info.Name, info.Description, info.Hash
	for _, w := range tk.loc.Worktrees {
		if !w.Main {
			p.Worktrees++
		}
	}
	// The kit's tools, checked on the box as its login shell finds them.
	if len(info.Requires) > 0 {
		q := url.Values{}
		for _, r := range info.Requires {
			q.Add("tool", r.Tool)
		}
		var req box.Requirements
		found := map[string]bool{}
		asked := a.postToBox(ctx, boxName, http.MethodGet, "/v1/requirements?"+q.Encode(), nil, &req) == nil && req.Tools != nil
		for _, t := range req.Tools {
			found[t.Tool] = t.Found
		}
		for _, r := range info.Requires {
			t := TeamKitTool{Tool: r.Tool, Hint: r.Hint}
			if asked {
				f := found[r.Tool]
				t.Found = &f
			}
			p.Requires = append(p.Requires, t)
		}
	}
	if k, ok := tk.r.setup.Keys[tk.project.ID]; ok {
		for _, name := range sortedNames(k.Shared) {
			users := keyUsers(info, name)
			if users == nil {
				users = []string{}
			}
			p.Keys = append(p.Keys, TeamKitKey{Name: name, OnePassword: team.IsOnePasswordRef(k.Shared[name]), UsedBy: users})
		}
	}
	if tk.project.Init != "" {
		p.Init = &TeamKitInitRef{Script: tk.project.Init, Detail: tk.project.InitDetail}
	}
	var cfg box.Config
	if a.postToBox(ctx, boxName, http.MethodGet, "/v1/locations/"+url.PathEscape(location)+"/config", nil, &cfg) == nil && cfg.Kit != nil {
		if cfg.Kit.ID != info.ID {
			p.Replaces = cfg.Kit.Name
		} else if cfg.Kit.Hash == info.Hash {
			p.Applied = true
		}
	}
	return p, nil
}

// TeamKitApply is the sheet's choice.
type TeamKitApply struct {
	Box      string `json:"box"`
	Location string `json:"location"`
	// Commit and Hash are what the sheet showed: a setup or kit that has
	// changed since is refused, to be reviewed again.
	Commit string `json:"commit"`
	Hash   string `json:"hash"`
	Keys   bool   `json:"keys,omitempty"`
	Init   bool   `json:"init,omitempty"`
}

func (a *Agent) applyTeamKit(ctx context.Context, key string, req TeamKitApply) (box.TeamKitResult, error) {
	if req.Commit == "" || req.Hash == "" {
		return box.TeamKitResult{}, errors.New("say which commit and kit you reviewed")
	}
	g, err := newGH()
	if err != nil {
		return box.TeamKitResult{}, err
	}
	tk, err := a.readTeamKitFor(ctx, g, key, req.Commit, req.Box, req.Location)
	if err != nil {
		return box.TeamKitResult{}, err
	}
	defer os.RemoveAll(tk.kit.info.Path)
	if tk.kit.info.Hash != req.Hash {
		return box.TeamKitResult{}, errors.New(errKitChanged)
	}
	t := &box.KitTeam{Org: tk.r.org.Login, Commit: tk.r.commit.SHA, Project: tk.project.ID}
	kept, err := a.keepTeamKit(tk.kit.info, teamKitLink(tk.r.org.Login, tk.r.commit.SHA, tk.project.Kit), tk.kit.commit, t)
	if err != nil {
		return box.TeamKitResult{}, err
	}
	in, err := kitInstall(kept)
	if err != nil {
		return box.TeamKitResult{}, err
	}
	body := box.TeamKitRequest{Kit: in}
	if k, ok := tk.r.setup.Keys[tk.project.ID]; ok && req.Keys && len(k.Shared) > 0 {
		body.Env = map[string]string{}
		for name, ref := range k.Shared {
			body.Env[name] = ref
		}
	}
	if req.Init && tk.project.Init != "" {
		files := map[string]string{}
		for p, b := range tk.r.files {
			files[p] = base64.StdEncoding.EncodeToString(b)
		}
		body.Init = &box.TeamKitInit{Team: tk.r.setup.ID, Project: tk.project.ID, Script: cleanInside(tk.project.Init), Files: files, Settings: tk.r.setup.Box.Settings}
	}
	var res box.TeamKitResult
	if err := a.postToBox(ctx, req.Box, http.MethodPost, "/v1/locations/"+url.PathEscape(req.Location)+"/team-kit", body, &res); err != nil {
		return res, err
	}
	a.publish(Event{Type: "kit.added", Data: map[string]any{"kit": kept.ID, "source": kept.Source.Src, "team": t.Org, "commit": t.Commit}})
	a.markKitTeam(req.Box, req.Location, t.Org)
	return res, nil
}

// keepTeamKit keeps a team kit on this laptop, its source saying which
// team setup, commit and project it comes from.
func (a *Agent) keepTeamKit(info KitInfo, src, commit string, t *box.KitTeam) (KitInfo, error) {
	kept, err := a.keepKit(info.Path, src, commit)
	if err != nil {
		return KitInfo{}, err
	}
	s, _ := json.MarshalIndent(KitSource{Src: src, Commit: commit, Fetched: time.Now().UTC(), Team: t}, "", "  ")
	if err := statefile.Write(filepath.Join(kept.Path, "source.json"), s); err != nil {
		return KitInfo{}, err
	}
	k, err := readKit(kept.Path, false)
	k.Origin = "user"
	return k, err
}

// markKitTeam notes at once that a project follows a team's kit, for the
// suggestions, before the next round reads the boxes again.
func (a *Agent) markKitTeam(boxName, location, org string) {
	s := &a.suggest
	s.mu.Lock()
	for i, l := range s.locs[boxName] {
		if l.Name == location {
			s.locs[boxName][i].KitTeam = org
		}
	}
	s.mu.Unlock()
	a.rebuildSuggestions(nil)
}

// updateTeamKit reads a kept team kit again at its team setup's newest
// commit and keeps it when it changed; nothing is installed here.
func (a *Agent) updateTeamKit(ctx context.Context, g ghCLI, k KitInfo) (KitInfo, bool, error) {
	t := k.Source.Team
	c, err := g.commit(ctx, t.Org+"/"+team.Repo, "HEAD")
	if err != nil {
		return k, false, err
	}
	if c.SHA == t.Commit {
		return k, false, nil
	}
	r, err := readTeam(ctx, g, team.Source{Owner: t.Org, Repo: team.Repo}, c.SHA)
	if err != nil {
		return k, false, err
	}
	if r.state != "found" {
		return k, false, fmt.Errorf("%s/.berth can't be read any more", t.Org)
	}
	p, ok := r.setup.Project(t.Project)
	if !ok || p.Kit == "" {
		return k, false, fmt.Errorf("%s/.berth has no kit for %s any more", t.Org, t.Project)
	}
	ref, err := team.ParseKitRef(p.Kit)
	if err != nil {
		return k, false, err
	}
	tk, err := a.readTeamKit(ctx, g, r, ref)
	if err != nil {
		return k, false, err
	}
	defer os.RemoveAll(tk.info.Path)
	nk, err := a.keepTeamKit(tk.info, teamKitLink(t.Org, c.SHA, p.Kit), tk.commit, &box.KitTeam{Org: t.Org, Commit: c.SHA, Project: t.Project})
	if err != nil {
		return k, false, err
	}
	return nk, nk.Hash != k.Hash, nil
}

// checkTeamKits looks for newer commits of the team setups whose kits this
// laptop keeps, and says when one changed a kit: the projects that have it
// show an update to review.
func (a *Agent) checkTeamKits(ctx context.Context, g ghCLI) {
	kits := a.kits()
	sort.Slice(kits, func(i, j int) bool { return kits[i].ID < kits[j].ID })
	for _, k := range kits {
		if k.Origin != "user" || k.Source == nil || k.Source.Team == nil {
			continue
		}
		from := k.Source.Team.Commit
		nk, changed, err := a.updateTeamKit(ctx, g, k)
		if err != nil || !changed {
			continue
		}
		a.publish(Event{Type: "kit.added", Data: map[string]any{"kit": nk.ID, "source": nk.Source.Src, "team": nk.Source.Team.Org, "from": shortSHA(from), "to": shortSHA(nk.Source.Team.Commit)}})
	}
}

func (a *Agent) teamKitRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/team/{org}/kit", func(w http.ResponseWriter, r *http.Request) {
		if !a.githubReady(w, r) {
			return
		}
		a.sync()
		q := r.URL.Query()
		p, err := a.teamKitPlan(r.Context(), r.PathValue("org"), q.Get("box"), q.Get("location"))
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("POST /v1/team/{org}/kit", func(w http.ResponseWriter, r *http.Request) {
		if !a.githubReady(w, r) {
			return
		}
		var req TeamKitApply
		if !decodeBody(w, r, &req) {
			return
		}
		a.sync()
		res, err := a.applyTeamKit(r.Context(), r.PathValue("org"), req)
		if err != nil {
			status := http.StatusBadGateway
			if err.Error() == errKitChanged {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
}
