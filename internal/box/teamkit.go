package box

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cosscom/shipyard/internal/team"
)

// Just the kit: a team setup's kit for one project, taken on its own for a
// project that is already on the box. Only the kit is installed (its
// per-worktree setup runs in new worktrees, and in the ones already there
// the first time each is opened); no box step, sudo, clone or 1Password.
// The project's shared keys and its init are each the engineer's choice,
// off unless asked for.

// TeamKitRequest is a team kit on its way to a project.
type TeamKitRequest struct {
	// Kit carries Team: the org, commit and project it comes from.
	Kit KitInstall `json:"kit"`
	// Env is the project's shared keys (secret references), only when the
	// engineer chose to add them.
	Env map[string]string `json:"env,omitempty"`
	// Init runs the project's init once in the checkout, only when chosen.
	Init *TeamKitInit `json:"init,omitempty"`
}

// TeamKitInit is a project's init, with what it needs of .berth.
type TeamKitInit struct {
	// Team is team.json's id; Script a path inside Files.
	Team     string            `json:"team"`
	Project  string            `json:"project"`
	Script   string            `json:"script"`
	Files    map[string]string `json:"files"`
	Settings map[string]string `json:"settings,omitempty"`
}

// TeamKitResult says what was done.
type TeamKitResult struct {
	Kit      InstalledKit `json:"kit"`
	Warnings []string     `json:"warnings"`
	// FirstOpen counts the worktrees already there, set up the first time
	// each is opened.
	FirstOpen int `json:"first_open"`
	// Session is the init's terminal, when it runs.
	Session string `json:"session,omitempty"`
}

var toolName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// ApplyTeamKit installs a team setup's kit on a project that is already
// here, and only that, unless keys or an init were asked for.
func (b *Box) ApplyTeamKit(ctx context.Context, location string, req TeamKitRequest) (TeamKitResult, error) {
	if req.Kit.Team == nil || !team.ValidOrg(req.Kit.Team.Org) || req.Kit.Team.Commit == "" {
		return TeamKitResult{}, badRequest("a team kit says which team setup it comes from")
	}
	saved, err := b.Locations.saved(location)
	if err != nil {
		return TeamKitResult{}, err
	}
	res, err := b.InstallKit(ctx, location, req.Kit)
	if err != nil {
		return TeamKitResult{}, err
	}
	out := TeamKitResult{Kit: res.Kit, Warnings: res.Warnings}
	// The worktrees already there get the kit's setup on their first open,
	// as an adopted clone's do; their files are not touched now.
	out.FirstOpen = b.markFirstOpen(ctx, location, saved.Path)
	if len(req.Env) > 0 {
		if err := b.mergeLocalEnv(location, req.Env); err != nil {
			return out, err
		}
	}
	if req.Init != nil {
		s, err := b.startTeamKitInit(ctx, location, saved.Path, *req.Init)
		if err != nil {
			out.Warnings = append(out.Warnings, "its first-time setup didn't start: "+err.Error())
		}
		out.Session = s
	}
	return out, nil
}

// startTeamKitInit runs a project's init in a terminal of its own, in the
// checkout, and leaves it there to watch; it is not waited for.
func (b *Box) startTeamKitInit(ctx context.Context, location, dest string, in TeamKitInit) (string, error) {
	if b.Team == nil {
		return "", fmt.Errorf("this box does not run team setups")
	}
	if !kitID.MatchString(in.Team) || !kitID.MatchString(in.Project) {
		return "", badRequest("bad team or project id")
	}
	script, err := team.InsidePath(in.Script)
	if err != nil {
		return "", badRequest("%v", err)
	}
	files := filepath.Join(b.Team.dir(in.Team), "kit-files")
	tmp := files + ".new"
	os.RemoveAll(tmp)
	for p, enc := range in.Files {
		c, err := team.InsidePath(p)
		if err != nil {
			return "", badRequest("%v", err)
		}
		data, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return "", badRequest("file %s is not base64", p)
		}
		full := filepath.Join(tmp, filepath.FromSlash(c))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(string(data), "#!") {
			mode = 0o755
		}
		if err := os.WriteFile(full, data, mode); err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, filepath.FromSlash(script))); err != nil {
		os.RemoveAll(tmp)
		return "", badRequest("%s is not among the files sent", script)
	}
	os.RemoveAll(files)
	if err := os.Rename(tmp, files); err != nil {
		return "", err
	}
	name := sessionSafe("team-" + in.Team + "-" + in.Project)
	if s, err := b.Sessions.Get(ctx, name); err == nil && !s.Exited {
		return name, nil
	}
	b.Sessions.Kill(ctx, name)
	command := fmt.Sprintf("cd %s && %s; c=$?; [ $c = 0 ] && echo && echo 'Shipyard: %s is set up.'", shellQuote(dest), shellQuote(filepath.Join(files, filepath.FromSlash(script))), in.Project)
	env := initEnv(ctx, b, TeamBundle{ID: in.Team, Settings: in.Settings}, location, dest, files)
	if _, err := b.Sessions.Create(ctx, name, location, dest, command, env); err != nil {
		return "", err
	}
	b.Sessions.SetTitle(ctx, name, in.Project+" first-time setup")
	return name, nil
}

func (b *Box) postTeamKit(w http.ResponseWriter, r *http.Request) error {
	var req TeamKitRequest
	if err := decodeLimit(r, &req, 16<<20); err != nil {
		return err
	}
	location := r.PathValue("name")
	data := map[string]any{"location": location, "kit": req.Kit.Kit.ID, "source": req.Kit.Source, "keys": len(req.Env) > 0, "init": req.Init != nil}
	if req.Kit.Team != nil {
		data["org"], data["commit"] = req.Kit.Team.Org, req.Kit.Team.Commit
	}
	if err := b.before(r, "kit.install", data); err != nil {
		return err
	}
	res, err := b.ApplyTeamKit(r.Context(), location, req)
	if err != nil {
		return err
	}
	data["version"], data["warnings"], data["first_open"] = res.Kit.Version, res.Warnings, res.FirstOpen
	b.publish(r, "kit.installed", data)
	writeJSON(w, res)
	return nil
}
