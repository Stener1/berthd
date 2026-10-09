package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cosscom/shipyard/internal/agentcli"
	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/statefile"
	"github.com/cosscom/shipyard/internal/team"
)

// Team setups on the laptop: read <org>/.berth with the person's gh, check
// they can read each repository it lists, and keep which commit they
// accepted, so a newer one is offered as an update to review, never run by
// itself. The box does the setting up (internal/box/team.go).

// TeamOrg is the org (or user) that publishes a team setup.
type TeamOrg struct {
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
	// Verified is GitHub's own mark that the org verified a domain.
	Verified bool   `json:"verified"`
	Type     string `json:"type"`
	HTMLURL  string `json:"html_url"`
}

// TeamFile is one file of .berth, for "Read every command".
type TeamFile struct {
	Path string `json:"path"`
	Size int    `json:"size"`
	Text string `json:"text,omitempty"`
}

// TeamStepView is a box step as the plan shows it, with its commands.
type TeamStepView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Sudo   bool   `json:"sudo"`
	// Berth marks the steps Shipyard adds: the box's own GitHub sign-in, and
	// 1Password's when the keys need it.
	Berth    bool     `json:"berth,omitempty"`
	Commands []string `json:"commands"`
}

// TeamProjectView is a repository as the plan shows it, after the access
// check.
type TeamProjectView struct {
	ID       string `json:"id"`
	Repo     string `json:"repo"`
	Path     string `json:"path"`
	Required bool   `json:"required"`
	// Access is whether this laptop's gh can read the repository.
	Access        bool   `json:"access"`
	Private       bool   `json:"private"`
	SizeKB        int64  `json:"size_kb,omitempty"`
	Description   string `json:"description,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	// Source is where its setup comes from: "repo" (its own committed
	// .berth/config.json), "kit" (the team setup's), or "none".
	Source string `json:"source"`
	// ConfigHash is the sha256 of the repository's .berth/config.json as
	// shown here; accepting the team setup trusts that exact file.
	ConfigHash string           `json:"config_hash,omitempty"`
	Kit        *TeamKitView     `json:"kit,omitempty"`
	Services   []string         `json:"services"`
	Init       string           `json:"init,omitempty"`
	InitDetail string           `json:"init_detail,omitempty"`
	FirstTask  string           `json:"first_task,omitempty"`
	Commands   []string         `json:"commands"`
	Keys       *TeamProjectKeys `json:"keys,omitempty"`
	Error      string           `json:"error,omitempty"`
	// Existing are clones of it already on the box (a Shipyard location, or
	// one the box found in the usual folders), the preferred first; the
	// page offers to use one instead of cloning. ExistingNote says why the
	// first is preferred when several are locations.
	Existing     []box.ExistingClone `json:"existing,omitempty"`
	ExistingNote string              `json:"existing_note,omitempty"`
}

type TeamKitView struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit,omitempty"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Hash   string `json:"hash"`
}

// TeamProjectKeys are a project's keys: names only, never values.
type TeamProjectKeys struct {
	From string `json:"from,omitempty"`
	// Listed is how many keys the repository's .env.example names.
	Listed int      `json:"listed"`
	Shared []string `json:"shared"`
	Ask    []string `json:"ask"`
}

type TeamAsk struct {
	Project string `json:"project"`
	Key     string `json:"key"`
	// Set is true when the box already has it.
	Set bool `json:"set,omitempty"`
}

// TeamView is everything the Team setup page shows for an org.
type TeamView struct {
	Org TeamOrg `json:"org"`
	// State is found; none (no .berth this account can read, from an org
	// name typed in); unreadable (the same, opened from a team link, which
	// says there is one); or no-org.
	State string `json:"state"`
	// Source says where it was read from, for the card: the org's own
	// .berth, or a link to another repository, branch and folder.
	Source   TeamSource        `json:"source"`
	Repo     *ghRepo           `json:"repo,omitempty"`
	Commit   *ghCommit         `json:"commit,omitempty"`
	Setup    *team.Setup       `json:"setup,omitempty"`
	Files    []TeamFile        `json:"files,omitempty"`
	Steps    []TeamStepView    `json:"steps,omitempty"`
	Projects []TeamProjectView `json:"projects"`
	Access   TeamAccess        `json:"access"`
	Keys     TeamKeysView      `json:"keys"`
	Repos    []TeamRepoChoice  `json:"repos,omitempty"`
	Accepted *TeamAcceptedRef  `json:"accepted,omitempty"`
	Update   *TeamUpdate       `json:"update,omitempty"`
	Warnings []string          `json:"warnings"`
	// Scanned says where the box looked for clones it already has, and
	// whether its budget ran out first; nil without a box to ask.
	Scanned *TeamScanned `json:"scanned,omitempty"`
}

// TeamScanned is where a box looked for existing clones.
type TeamScanned struct {
	Looked    []string `json:"looked"`
	Truncated bool     `json:"truncated,omitempty"`
}

// TeamSource is where a team setup was read from.
type TeamSource struct {
	// Kind is "org" (<org>/.berth) or "link".
	Kind string `json:"kind"`
	Repo string `json:"repo"`
	Ref  string `json:"ref,omitempty"`
	// RefKind is branch, tag or commit.
	RefKind string `json:"ref_kind,omitempty"`
	Path    string `json:"path,omitempty"`
	HTMLURL string `json:"html_url"`
	// Label names it: "jo-acme/acme-setup · team-setup branch · team/".
	Label string `json:"label"`
	// Key is what the team routes take in place of an org name.
	Key string `json:"key"`
}

var commitLike = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func (r teamRead) sourceView(ctx context.Context, g ghCLI) TeamSource {
	src := r.src
	v := TeamSource{Kind: "org", Repo: src.Slug(), Ref: src.Ref, Path: src.Path, HTMLURL: src.HTMLURL(), Label: src.Slug(), Key: src.String()}
	if !src.Link {
		return v
	}
	v.Kind = "link"
	if src.Ref != "" {
		switch {
		case commitLike.MatchString(src.Ref):
			v.RefKind = "commit"
			v.Label += " · " + shortSHA(src.Ref)
		case g.api(ctx, "repos/"+src.Slug()+"/branches/"+url.PathEscape(src.Ref), nil) == nil:
			v.RefKind = "branch"
			v.Label += " · " + src.Ref + " branch"
		default:
			v.RefKind = "tag"
			v.Label += " · " + src.Ref
		}
	}
	if src.Path != "" {
		v.Label += " · " + src.Path + "/"
	}
	return v
}

type TeamAccess struct {
	Readable int      `json:"readable"`
	Total    int      `json:"total"`
	Missing  []string `json:"missing"`
}

type TeamKeysView struct {
	Shared int       `json:"shared"`
	Ask    []TeamAsk `json:"ask"`
	// OnePassword are the shared keys that are 1Password references: read
	// on the box with op, or, when the engineer skips 1Password, typed once
	// here like the asked keys (or left blank).
	OnePassword []TeamAsk `json:"onepassword"`
	// OnePasswordRequired: the team doesn't let engineers skip 1Password.
	OnePasswordRequired bool `json:"onepassword_required,omitempty"`
}

// TeamRepoChoice is one of an org's repositories, for an org with no .berth.
type TeamRepoChoice struct {
	FullName    string `json:"full_name"`
	Private     bool   `json:"private"`
	Description string `json:"description"`
	PushedAt    string `json:"pushed_at"`
	HasBerth    bool   `json:"has_berth"`
	// Existing are clones of it already on the box, as for a project.
	Existing []box.ExistingClone `json:"existing,omitempty"`
}

type TeamAcceptedRef struct {
	Commit string    `json:"commit"`
	Box    string    `json:"box"`
	At     time.Time `json:"at"`
}

// TeamUpdate is a newer commit of .berth than the one accepted.
type TeamUpdate struct {
	From    string        `json:"from"`
	To      string        `json:"to"`
	Author  string        `json:"author"`
	Date    string        `json:"date"`
	Message string        `json:"message"`
	Commits int           `json:"commits"`
	Changes []team.Change `json:"changes"`
	Sudo    []string      `json:"sudo"`
}

// teamRead is a team setup as read through gh at one commit.
type teamRead struct {
	src      team.Source
	ref      string
	org      TeamOrg
	state    string
	repo     *ghRepo
	commit   *ghCommit
	setup    *team.Setup
	files    map[string][]byte
	warnings []string
}

var errNoOrg = errors.New("no such org or user on GitHub")

// readTeam reads a team setup from src at ref ("" for the source's own
// ref, else the repository's default branch).
func readTeam(ctx context.Context, g ghCLI, src team.Source, ref string) (teamRead, error) {
	org := src.Owner
	var o struct {
		Login      string `json:"login"`
		Name       string `json:"name"`
		AvatarURL  string `json:"avatar_url"`
		IsVerified bool   `json:"is_verified"`
		Type       string `json:"type"`
		HTMLURL    string `json:"html_url"`
	}
	err := g.api(ctx, "orgs/"+org, &o)
	if errors.Is(err, errGHNotFound) {
		err = g.api(ctx, "users/"+org, &o)
	}
	if errors.Is(err, errGHNotFound) {
		return teamRead{src: src, org: TeamOrg{Login: org, Name: org}, state: "no-org"}, nil
	}
	if err != nil {
		return teamRead{}, err
	}
	if o.Name == "" {
		o.Name = o.Login
	}
	r := teamRead{src: src, org: TeamOrg{Login: o.Login, Name: o.Name, AvatarURL: o.AvatarURL, Verified: o.IsVerified, Type: o.Type, HTMLURL: o.HTMLURL}}
	slug := o.Login + "/" + src.Repo
	repo, err := g.repo(ctx, slug)
	if errors.Is(err, errGHNotFound) {
		// A link says there is a team setup there: one this account can't
		// read is unreadable. An org name without .berth gets the picker.
		r.state = "none"
		if src.Link {
			r.state = "unreadable"
		}
		return r, nil
	}
	if err != nil {
		return teamRead{}, err
	}
	r.repo = &repo
	if ref == "" {
		ref = src.Ref
	}
	if ref == "" {
		ref = repo.DefaultBranch
	}
	r.ref = ref
	c, err := g.commit(ctx, slug, ref)
	if err != nil {
		return teamRead{}, fmt.Errorf("reading %s at %s: %w", slug, ref, err)
	}
	r.commit = &c
	r.files, err = g.tree(ctx, slug, c.tree, src.Path)
	if err != nil {
		return teamRead{}, err
	}
	raw, ok := r.files[team.File]
	if !ok {
		return teamRead{}, fmt.Errorf("%s has no %s", src.String(), src.File(team.File))
	}
	s, warnings, err := team.Parse(raw)
	if err != nil {
		return teamRead{}, err
	}
	// An org's own .berth must be its own team setup. A link may hold any
	// org's (a draft, before the org publishes it), and says where it is.
	if !src.Link && !strings.EqualFold(s.Org, o.Login) {
		return teamRead{}, fmt.Errorf("%s says it is %q's team setup, not %s's", slug, s.Org, o.Login)
	}
	if s.Box.Script != "" {
		if _, ok := r.files[cleanInside(s.Box.Script)]; !ok {
			return teamRead{}, fmt.Errorf("team.json runs %s, which %s does not have", s.Box.Script, slug)
		}
	}
	for _, p := range s.Projects {
		if p.Init != "" {
			if _, ok := r.files[cleanInside(p.Init)]; !ok {
				return teamRead{}, fmt.Errorf("%s's init %s is not in %s", p.ID, p.Init, slug)
			}
		}
	}
	r.state, r.setup, r.warnings = "found", s, warnings
	return r, nil
}

func cleanInside(p string) string {
	c, _ := team.InsidePath(p)
	return c
}

// stepFunc finds a step's own commands in a box script written as Acme's
// is (a shell function step_<id>), for the plan's "exact commands".
var stepFuncStart = regexp.MustCompile(`^step_([a-z][a-z0-9_-]*)\(\)\s*\{\s*$`)

func stepBodies(script []byte) map[string][]string {
	out := map[string][]string{}
	var cur string
	for _, line := range strings.Split(string(script), "\n") {
		if cur == "" {
			if m := stepFuncStart.FindStringSubmatch(line); m != nil {
				cur = m[1]
				out[cur] = []string{}
			}
			continue
		}
		if line == "}" {
			cur = ""
			continue
		}
		if t := strings.TrimSpace(line); t != "" {
			out[cur] = append(out[cur], strings.TrimPrefix(line, "  "))
		}
	}
	return out
}

func githubStepView() TeamStepView {
	return TeamStepView{
		ID: team.GitHubStep, Title: "GitHub on the box", Berth: true,
		Detail: "The box signs in with its own gh, so it can clone as you and you can revoke it on its own",
		Commands: []string{
			"gh auth status || " + ghLoginCommand,
			"gh auth setup-git",
		},
	}
}

func agentsStepView(ids []string) TeamStepView {
	cmds := []string{"berthd agents install --integrations " + strings.Join(ids, " ") + "   # each is skipped when it is already there"}
	for _, id := range ids {
		if a, ok := agentcli.ByID(id); ok {
			cmds = append(cmds, "#   "+a.Name+": "+a.Install)
		}
	}
	return TeamStepView{
		ID: team.AgentsStep, Title: agentcli.Names(ids) + " on the box", Berth: true,
		Detail:   "The agent CLIs the team uses, installed into ~/.local/bin without sudo, with Shipyard's hooks and skills. Signing in to each stays yours",
		Commands: cmds,
	}
}

func onePasswordStepView() TeamStepView {
	return TeamStepView{
		ID: team.OnePasswordStep, Title: "1Password on the box", Berth: true,
		Detail: "The shared keys are 1Password references: op signs in on the box, in its terminal, so Shipyard reads them without asking in a service's terminal",
		Commands: []string{
			"berthd secret signin --check || berthd secret signin   # op signin; its session kept for berthd, readable by you alone",
		},
	}
}

func (r teamRead) stepViews() []TeamStepView {
	var out []TeamStepView
	bodies := stepBodies(r.files[cleanInside(r.setup.Box.Script)])
	for _, s := range r.setup.Box.Steps {
		cmds := []string{fmt.Sprintf("%s check %s || %s %s", r.setup.Box.Script, s.ID, r.setup.Box.Script, s.ID)}
		if body, ok := bodies[s.ID]; ok {
			cmds = append(cmds, "# "+r.setup.Box.Script+": step_"+s.ID)
			cmds = append(cmds, body...)
		}
		out = append(out, TeamStepView{ID: s.ID, Title: s.Title, Detail: s.Detail, Sudo: s.Sudo, Commands: cmds})
	}
	if len(r.setup.Agents) > 0 {
		out = append(out, agentsStepView(r.setup.Agents))
	}
	if len(r.setup.Projects) > 0 {
		out = append(out, githubStepView())
		if r.setup.UsesOnePassword() {
			out = append(out, onePasswordStepView())
		}
	}
	return out
}

func (r teamRead) fileViews() []TeamFile {
	var out []TeamFile
	for p, b := range r.files {
		f := TeamFile{Path: p, Size: len(b)}
		if len(b) < 256<<10 && utf8.Valid(b) {
			f.Text = string(b)
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		// team.json first, then folders after top-level files.
		a, b := out[i].Path, out[j].Path
		if (a == team.File) != (b == team.File) {
			return a == team.File
		}
		return a < b
	})
	return out
}

// teamKit is a project's kit, fetched for review or install.
type teamKit struct {
	info   KitInfo
	commit string
}

// readTeamKit fetches a project's kit: from .berth's own files, or from a
// GitHub link through gh at its pinned commit, or another link with git.
func (a *Agent) readTeamKit(ctx context.Context, g ghCLI, r teamRead, ref team.KitRef) (teamKit, error) {
	tmp, err := os.MkdirTemp(a.teamTmp(), "kit-")
	if err != nil {
		return teamKit{}, err
	}
	defer os.RemoveAll(tmp)
	var files map[string][]byte
	commit := ""
	switch {
	case ref.Path != "":
		files = map[string][]byte{}
		prefix := ref.Path + "/"
		for p, b := range r.files {
			if strings.HasPrefix(p, prefix) {
				files[strings.TrimPrefix(p, prefix)] = b
			}
		}
		if r.commit != nil {
			commit = r.commit.SHA
		}
	case ref.Owner != "":
		slug := ref.Owner + "/" + ref.Name
		c, err := g.commit(ctx, slug, ref.Ref)
		if err != nil {
			if errors.Is(err, errGHNotFound) {
				return teamKit{}, fmt.Errorf("the kit %s@%s is not there, or this account can't read it", slug, ref.Ref)
			}
			return teamKit{}, err
		}
		commit = c.SHA
		if files, err = g.tree(ctx, slug, c.tree, ref.Sub); err != nil {
			return teamKit{}, err
		}
	default:
		dir, c, err := fetchKit(ctx, ref.Link+"@"+ref.Ref, filepath.Join(tmp, "fetch"))
		if err != nil {
			return teamKit{}, err
		}
		info, err := readKit(dir, true)
		if err != nil {
			return teamKit{}, err
		}
		// Kept for the install: its files are read from Path.
		keep, err := os.MkdirTemp(a.teamTmp(), "kit-keep-")
		if err != nil {
			return teamKit{}, err
		}
		if err := copyTree(dir, keep); err != nil {
			return teamKit{}, err
		}
		info.Path = keep
		return teamKit{info: info, commit: c}, nil
	}
	if _, ok := files[kitManifest]; !ok {
		return teamKit{}, fmt.Errorf("no %s in %s", kitManifest, ref.String())
	}
	keep, err := os.MkdirTemp(a.teamTmp(), "kit-keep-")
	if err != nil {
		return teamKit{}, err
	}
	for p, b := range files {
		c, err := team.InsidePath(p)
		if err != nil {
			continue
		}
		full := filepath.Join(keep, filepath.FromSlash(c))
		os.MkdirAll(filepath.Dir(full), 0o755)
		mode := os.FileMode(0o644)
		if strings.HasPrefix(string(b), "#!") {
			mode = 0o755
		}
		if err := os.WriteFile(full, b, mode); err != nil {
			return teamKit{}, err
		}
	}
	info, err := readKit(keep, true)
	if err != nil {
		os.RemoveAll(keep)
		return teamKit{}, err
	}
	info.Origin = "team"
	info.Source = &KitSource{Src: ref.String(), Commit: commit}
	return teamKit{info: info, commit: commit}, nil
}

func (a *Agent) teamTmp() string {
	d := filepath.Join(a.cfg.Dir, "team", "tmp")
	os.MkdirAll(d, 0o700)
	return d
}

// teamProject is one project after the access check, with what its setup
// needs to reach the box.
type teamProject struct {
	view   TeamProjectView
	kit    *teamKit
	envKey []string
}

// checkProjects reads each project's repository with gh, which is the
// access check, and finds where its setup comes from.
func (a *Agent) checkProjects(ctx context.Context, g ghCLI, r teamRead, withKits bool) []teamProject {
	out := make([]teamProject, len(r.setup.Projects))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, p := range r.setup.Projects {
		wg.Add(1)
		go func(i int, p team.Project) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = a.checkProject(ctx, g, r, p, withKits)
		}(i, p)
	}
	wg.Wait()
	return out
}

func (a *Agent) checkProject(ctx context.Context, g ghCLI, r teamRead, p team.Project, withKits bool) teamProject {
	v := TeamProjectView{ID: p.ID, Repo: p.Repo, Path: p.ProjectPath(), Required: p.Required, Source: "none",
		Services: []string{}, Init: p.Init, InitDetail: p.InitDetail, FirstTask: p.FirstTask}
	tp := teamProject{}
	repo, err := g.repo(ctx, p.Repo)
	if errors.Is(err, errGHNotFound) {
		v.Commands = []string{"git clone https://github.com/" + p.Repo + ".git " + v.Path}
		v.Error = "you can't read " + p.Repo
		tp.view = v
		return tp
	}
	if err != nil {
		v.Error = err.Error()
		tp.view = v
		return tp
	}
	v.Access, v.Private, v.SizeKB, v.Description, v.DefaultBranch = true, repo.Private, repo.Size, repo.Description, repo.DefaultBranch
	v.Commands = []string{"git clone https://github.com/" + p.Repo + ".git " + v.Path}
	// The repository's own config wins; the team setup's kit is for
	// repositories without one.
	if raw, _, err := g.file(ctx, p.Repo, box.RepoConfigFile, ""); err == nil {
		sum := sha256.Sum256(raw)
		v.Source, v.ConfigHash = "repo", hex.EncodeToString(sum[:])
		var cfg box.RepoConfig
		if json.Unmarshal(raw, &cfg) == nil {
			for _, s := range cfg.Services {
				v.Services = append(v.Services, serviceTitle(s))
			}
			v.Commands = append(v.Commands, "# "+p.Repo+"'s own .berth/config.json, trusted as you see it here (sha256 "+v.ConfigHash[:12]+")")
			if cfg.Setup != "" {
				v.Commands = append(v.Commands, "# each new worktree: "+cfg.Setup)
			}
		}
	} else if !errors.Is(err, errGHNotFound) {
		v.Error = err.Error()
	} else if p.Kit != "" {
		ref, _ := team.ParseKitRef(p.Kit)
		v.Source = "kit"
		kv := &TeamKitView{Ref: p.Kit}
		if withKits {
			k, err := a.readTeamKit(ctx, g, r, ref)
			if err != nil {
				v.Error = "kit: " + err.Error()
			} else {
				tp.kit = &k
				kv.ID, kv.Name, kv.Hash, kv.Commit = k.info.ID, k.info.Name, k.info.Hash, shortSHA(k.commit)
				for _, s := range k.info.Config.Services {
					v.Services = append(v.Services, serviceTitle(s))
				}
				v.Commands = append(v.Commands, "# kit "+k.info.Name+" ("+p.Kit+")")
				if k.info.Config.Setup != "" {
					v.Commands = append(v.Commands, "# each new worktree: "+k.info.Config.Setup)
				}
			}
		}
		v.Kit = kv
	}
	if p.Init != "" {
		v.Commands = append(v.Commands, "cd "+v.Path+" && $TEAM/"+cleanInside(p.Init)+"   # once, in the fresh clone")
	}
	if k, ok := r.setup.Keys[p.ID]; ok {
		pk := &TeamProjectKeys{From: k.From, Shared: sortedNames(k.Shared), Ask: append([]string{}, k.Ask...)}
		from := k.From
		if from == "" {
			from = ".env.example"
		}
		pk.From = from
		if raw, _, err := g.file(ctx, p.Repo, from, ""); err == nil {
			tp.envKey = team.EnvKeys(raw)
			pk.Listed = len(tp.envKey)
		}
		for _, name := range pk.Shared {
			v.Commands = append(v.Commands, "# "+name+" = "+k.Shared[name]+"   (read on the box with your 1Password)")
		}
		for _, name := range pk.Ask {
			v.Commands = append(v.Commands, "# "+name+" = (you enter it once; kept on the box)")
		}
		v.Keys = pk
	}
	tp.view = v
	return tp
}

func serviceTitle(s box.WorktreeService) string {
	if s.Title != "" {
		return s.Title
	}
	return s.Name
}

func sortedNames(m map[string]string) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// teamAccepted is what this laptop keeps about a team setup it accepted:
// the commit reviewed, where it ran, and that commit's setup and text
// files, so a newer commit can be shown as a diff.
type teamAccepted struct {
	Org string `json:"org"`
	// Source is the org's name, or the link it was read from.
	Source string            `json:"source,omitempty"`
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Commit string            `json:"commit"`
	Box    string            `json:"box"`
	Boxes  []string          `json:"boxes"`
	At     time.Time         `json:"at"`
	Setup  json.RawMessage   `json:"setup"`
	Files  map[string]string `json:"files"`
	// Notified is the newest commit already announced as an update.
	Notified string `json:"notified,omitempty"`
}

// source is where the accepted team setup is read from.
func (t teamAccepted) source() team.Source {
	key := t.Source
	if key == "" {
		key = t.Org
	}
	src, err := team.ParseSource(key)
	if err != nil {
		return team.Source{Owner: t.Org, Repo: team.Repo}
	}
	return src
}

func (a *Agent) teamDir() string { return filepath.Join(a.cfg.Dir, "team") }

func (a *Agent) acceptedPath(src team.Source) string {
	return filepath.Join(a.teamDir(), "accepted", src.Key()+".json")
}

func (a *Agent) accepted(src team.Source) (*teamAccepted, error) {
	b, err := os.ReadFile(a.acceptedPath(src))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var t teamAccepted
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("%s is unreadable: %w", a.acceptedPath(src), err)
	}
	return &t, nil
}

func (a *Agent) allAccepted() []teamAccepted {
	paths, _ := filepath.Glob(filepath.Join(a.teamDir(), "accepted", "*.json"))
	out := []teamAccepted{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var t teamAccepted
		if json.Unmarshal(b, &t) == nil {
			out = append(out, t)
		}
	}
	return out
}

func (a *Agent) saveAccepted(t teamAccepted) error {
	p := a.acceptedPath(t.source())
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(p, append(b, '\n'))
}

// accept records that r was accepted on boxName.
func (a *Agent) accept(r teamRead, boxName string) error {
	prev, _ := a.accepted(r.src)
	raw, _ := json.Marshal(r.setup)
	t := teamAccepted{Org: r.org.Login, Source: r.src.String(), ID: r.setup.ID, Name: r.setup.Name, Commit: r.commit.SHA, Box: boxName, At: time.Now().UTC(), Setup: raw, Files: textFiles(r.files)}
	if prev != nil {
		t.Boxes, t.Notified = prev.Boxes, prev.Notified
	}
	if !contains(t.Boxes, boxName) {
		t.Boxes = append(t.Boxes, boxName)
	}
	return a.saveAccepted(t)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func textFiles(files map[string][]byte) map[string]string {
	out := map[string]string{}
	for p, b := range files {
		if utf8.Valid(b) {
			out[p] = string(b)
		} else {
			sum := sha256.Sum256(b)
			out[p] = "binary " + hex.EncodeToString(sum[:])
		}
	}
	return out
}

// teamUpdate compares the accepted commit with the newest one of .berth.
// Nothing runs: the update is shown for review, and applied by the person.
func teamUpdate(ctx context.Context, g ghCLI, acc *teamAccepted) (*TeamUpdate, *teamRead, error) {
	r, err := readTeam(ctx, g, acc.source(), "")
	if err != nil {
		return nil, nil, err
	}
	if r.state != "found" || r.commit.SHA == acc.Commit {
		return nil, &r, nil
	}
	u, err := diffUpdate(ctx, g, acc, r)
	return u, &r, err
}

// diffUpdate describes r, a newer commit than the accepted one.
func diffUpdate(ctx context.Context, g ghCLI, acc *teamAccepted, r teamRead) (*TeamUpdate, error) {
	var old team.Setup
	if err := json.Unmarshal(acc.Setup, &old); err != nil {
		return nil, fmt.Errorf("the accepted team setup is unreadable: %w", err)
	}
	changes := team.Diff(&old, r.setup, acc.Files, textFiles(r.files))
	u := &TeamUpdate{From: shortSHA(acc.Commit), To: r.commit.Short, Author: r.commit.Author, Date: r.commit.Date, Message: r.commit.Message,
		Changes: changes, Sudo: team.NewSudo(changes)}
	if u.Changes == nil {
		u.Changes = []team.Change{}
	}
	if u.Sudo == nil {
		u.Sudo = []string{}
	}
	var cmp struct {
		TotalCommits int `json:"total_commits"`
	}
	if g.api(ctx, "repos/"+acc.source().Slug()+"/compare/"+acc.Commit+"..."+r.commit.SHA, &cmp) == nil {
		u.Commits = cmp.TotalCommits
	}
	return u, nil
}

// teamView builds the Team setup page's data for org. fromLink says it was
// opened from a berth://team link, which says a setup exists, so one that
// can't be read is "unreadable" rather than "none".
func (a *Agent) teamView(ctx context.Context, key, boxName string, fromLink bool) (TeamView, error) {
	src, err := team.ParseSource(key)
	if err != nil {
		return TeamView{}, err
	}
	g, err := newGH()
	if err != nil {
		return TeamView{}, err
	}
	r, err := readTeam(ctx, g, src, "")
	if err != nil {
		return TeamView{}, err
	}
	v := TeamView{Org: r.org, State: r.state, Source: r.sourceView(ctx, g), Projects: []TeamProjectView{}, Warnings: append([]string{}, r.warnings...), Keys: TeamKeysView{Ask: []TeamAsk{}, OnePassword: []TeamAsk{}}, Access: TeamAccess{Missing: []string{}}}
	switch r.state {
	case "no-org", "unreadable":
		return v, nil
	case "none":
		if fromLink {
			v.State = "unreadable"
			return v, nil
		}
		v.Repos = orgRepos(ctx, g, r.org)
		refs := make([]existingRef, len(v.Repos))
		for i, rc := range v.Repos {
			name := strings.SplitN(rc.FullName, "/", 2)[1]
			refs[i] = existingRef{ID: repoProjectID(name), Repo: rc.FullName, Path: "~/code/" + name}
		}
		if found, scanned := a.existingOnBox(ctx, boxName, repoTeamID(r.org.Login), refs); found != nil {
			v.Scanned = scanned
			for i := range v.Repos {
				v.Repos[i].Existing = found[strings.ToLower(v.Repos[i].FullName)].Clones
			}
		}
		return v, nil
	}
	v.Repo, v.Commit, v.Setup, v.Files, v.Steps = r.repo, r.commit, r.setup, r.fileViews(), r.stepViews()
	v.Keys.OnePasswordRequired = r.setup.RequiresOnePassword()
	set := a.keysOnBox(ctx, boxName, r.setup.ID)
	for _, tp := range a.checkProjects(ctx, g, r, true) {
		if tp.kit != nil {
			os.RemoveAll(tp.kit.info.Path)
		}
		v.Projects = append(v.Projects, tp.view)
		v.Access.Total++
		if tp.view.Access {
			v.Access.Readable++
		} else {
			v.Access.Missing = append(v.Access.Missing, tp.view.Repo)
		}
		if tp.view.Keys != nil && tp.view.Access {
			v.Keys.Shared += len(tp.view.Keys.Shared)
			for _, k := range tp.view.Keys.Ask {
				v.Keys.Ask = append(v.Keys.Ask, TeamAsk{Project: tp.view.ID, Key: k, Set: set[tp.view.ID+"/"+k]})
			}
			for _, k := range tp.view.Keys.Shared {
				if team.IsOnePasswordRef(r.setup.Keys[tp.view.ID].Shared[k]) {
					v.Keys.OnePassword = append(v.Keys.OnePassword, TeamAsk{Project: tp.view.ID, Key: k, Set: set[tp.view.ID+"/"+k]})
				}
			}
		}
	}
	var refs []existingRef
	for _, p := range v.Projects {
		if p.Access {
			refs = append(refs, existingRef{ID: p.ID, Repo: p.Repo, Path: p.Path})
		}
	}
	if found, scanned := a.existingOnBox(ctx, boxName, r.setup.ID, refs); found != nil {
		v.Scanned = scanned
		for i := range v.Projects {
			e := found[strings.ToLower(v.Projects[i].Repo)]
			v.Projects[i].Existing, v.Projects[i].ExistingNote = e.Clones, e.Note
		}
	}
	if acc, _ := a.accepted(src); acc != nil {
		v.Accepted = &TeamAcceptedRef{Commit: acc.Commit, Box: acc.Box, At: acc.At}
		if acc.Commit != r.commit.SHA {
			v.Update, _ = diffUpdate(ctx, g, acc, r)
		}
	}
	return v, nil
}

// orgRepos lists an org's repositories this account can read, newest
// first, marking those that carry their own .berth/config.json.
func orgRepos(ctx context.Context, g ghCLI, org TeamOrg) []TeamRepoChoice {
	kind := "orgs"
	if org.Type == "User" {
		kind = "users"
	}
	var repos []ghRepo
	if err := g.api(ctx, kind+"/"+org.Login+"/repos?per_page=100&sort=pushed", &repos); err != nil {
		return []TeamRepoChoice{}
	}
	sort.SliceStable(repos, func(i, j int) bool { return repos[i].PushedAt > repos[j].PushedAt })
	if len(repos) > 50 {
		repos = repos[:50]
	}
	out := make([]TeamRepoChoice, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, r := range repos {
		out[i] = TeamRepoChoice{FullName: r.FullName, Private: r.Private, Description: r.Description, PushedAt: r.PushedAt}
		wg.Add(1)
		go func(i int, slug string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			_, _, err := g.file(ctx, slug, box.RepoConfigFile, "")
			out[i].HasBerth = err == nil
		}(i, r.FullName)
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].HasBerth && !out[j].HasBerth })
	return out
}

type existingRef struct{ ID, Repo, Path string }

// existingOnBox asks a box for the clones it already has of refs' repos,
// by repository in lower case. nil without a box, or when the box is older
// or doesn't answer in time: the page then offers fresh clones only.
func (a *Agent) existingOnBox(ctx context.Context, boxName, id string, refs []existingRef) (map[string]box.ExistingProject, *TeamScanned) {
	if boxName == "" || len(refs) == 0 || !teamIDLike.MatchString(id) {
		return nil, nil
	}
	c, ok := a.client(boxName)
	if !ok {
		return nil, nil
	}
	q := url.Values{}
	for _, r := range refs {
		q.Add("project", r.ID+":"+r.Repo+":"+r.Path)
	}
	var res box.ExistingResult
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := box.NewClient(c).Call(cctx, "GET", "/v1/team/"+url.PathEscape(id)+"/existing?"+q.Encode(), nil, &res); err != nil {
		return nil, nil
	}
	out := map[string]box.ExistingProject{}
	for _, p := range res.Projects {
		if len(p.Clones) > 0 {
			out[strings.ToLower(p.Repo)] = p
		}
	}
	return out, &TeamScanned{Looked: res.Looked, Truncated: res.Truncated}
}

var teamIDLike = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

// keysOnBox asks a box which of a team's keys it already has, so they are
// not asked for again.
func (a *Agent) keysOnBox(ctx context.Context, boxName, id string) map[string]bool {
	out := map[string]bool{}
	if boxName == "" {
		return out
	}
	c, ok := a.client(boxName)
	if !ok {
		return out
	}
	var st box.TeamStatus
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if box.NewClient(c).Call(cctx, "GET", "/v1/team/"+url.PathEscape(id), nil, &st) == nil {
		for _, k := range st.KeysSet {
			out[k] = true
		}
	}
	return out
}

// watchTeamUpdates looks for newer commits of the team setups this laptop
// accepted, now and then, and announces each one once. It never applies
// them.
func (a *Agent) watchTeamUpdates(ctx context.Context) {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		a.checkTeamUpdates(ctx)
		timer.Reset(6 * time.Hour)
	}
}

func (a *Agent) checkTeamUpdates(ctx context.Context) {
	g, err := newGH()
	if err != nil {
		return
	}
	for _, acc := range a.allAccepted() {
		var s team.Setup
		if json.Unmarshal(acc.Setup, &s) == nil && !s.NotifyUpdates() {
			continue
		}
		u, r, err := teamUpdate(ctx, g, &acc)
		if err != nil || u == nil || r.commit.SHA == acc.Notified {
			continue
		}
		acc.Notified = r.commit.SHA
		a.saveAccepted(acc)
		a.publish(Event{Type: "team.update", Data: map[string]any{"org": acc.Org, "key": acc.source().String(), "name": acc.Name, "from": u.From, "to": u.To, "changes": len(u.Changes), "sudo": u.Sudo}})
	}
}

// teamRoutes serves the Team setup page and berth team.
func (a *Agent) teamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/github", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, githubState(r.Context()))
	})
	// login opens a terminal on this computer running gh auth login; the
	// person signs in there, and the page checks GET /v1/github again.
	mux.HandleFunc("POST /v1/github/login", func(w http.ResponseWriter, r *http.Request) {
		opened, err := a.openGHLogin()
		res := map[string]any{"opened": opened, "command": ghLoginCommand}
		if err != nil {
			res["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, res)
	})
	a.teamSetupRoutes(mux)
	// The team setups this laptop accepted, each with how it stands on its
	// box when the box is online.
	mux.HandleFunc("GET /v1/team", func(w http.ResponseWriter, r *http.Request) {
		a.sync()
		out := []map[string]any{}
		for _, t := range a.allAccepted() {
			row := map[string]any{"org": t.Org, "key": t.source().String(), "source": t.source().String(), "id": t.ID, "name": t.Name, "commit": t.Commit, "box": t.Box, "boxes": t.Boxes, "at": t.At}
			var st box.TeamStatus
			if a.postToBox(r.Context(), t.Box, http.MethodGet, "/v1/team/"+url.PathEscape(t.ID), nil, &st) == nil {
				row["status"] = st
			}
			out = append(out, row)
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /v1/team/{org}", func(w http.ResponseWriter, r *http.Request) {
		if !a.githubReady(w, r) {
			return
		}
		a.sync()
		q := r.URL.Query()
		v, err := a.teamView(r.Context(), r.PathValue("org"), q.Get("box"), q.Get("from") == "link")
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	mux.HandleFunc("GET /v1/team/{org}/update", func(w http.ResponseWriter, r *http.Request) {
		if !a.githubReady(w, r) {
			return
		}
		src, err := team.ParseSource(r.PathValue("org"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		acc, err := a.accepted(src)
		if err != nil || acc == nil {
			writeError(w, http.StatusNotFound, "this laptop has not set up "+r.PathValue("org")+"'s team setup")
			return
		}
		g, _ := newGH()
		u, _, err := teamUpdate(r.Context(), g, acc)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"update": u})
	})
}

// githubReady answers for a route that needs gh signed in, when it isn't.
func (a *Agent) githubReady(w http.ResponseWriter, r *http.Request) bool {
	st := githubState(r.Context())
	switch st.State {
	case "missing":
		writeCoded(w, http.StatusPreconditionFailed, "Install the GitHub CLI (gh) first: "+st.Install.Command, "gh_missing")
		return false
	case "signed-out":
		writeCoded(w, http.StatusPreconditionFailed, "Connect GitHub first: run gh auth login", "gh_signed_out")
		return false
	}
	return true
}
