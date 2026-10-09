package boxcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/cosscom/shipyard/internal/box"
)

// Commands for skills, previews, a repository's config, and worktree
// services.

func skills(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs, asJSON := flags(args)
	agent := fs.String("agent", "all", "claude, codex, or all")
	target := fs.String("target", "user", "user (this box's user) or project (one location's repository)")
	location := fs.String("location", "", "the location, for --target project or to list its project skills")
	commit := fs.Bool("commit", false, "leave project skills visible to git, to commit and share")
	names, err := parse(fs, args)
	usage := "skills [list|install|uninstall] [SKILL...] [--agent claude|codex|all] [--target user|project] [--location LOC] [--commit]"
	if err != nil {
		return usageErr(usage)
	}
	var rep box.SkillsReport
	switch sub {
	case "list":
		if rep, err = c.Skills(ctx, *location); err != nil {
			return err
		}
	case "install", "uninstall":
		if len(names) == 0 {
			names = []string{"all"}
		}
		req := box.SkillsRequest{Skills: names, Agent: *agent, Target: *target, Location: *location, Commit: *commit}
		if rep, err = c.ChangeSkills(ctx, req, sub == "install"); err != nil {
			return err
		}
	default:
		return usageErr(usage)
	}
	return show(out, *asJSON, rep, func() {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		head := "SKILL\tVERSION"
		for _, a := range rep.Agents {
			head += "\t" + strings.ToUpper(a)
		}
		if rep.Location != "" {
			for _, a := range rep.Agents {
				head += "\t" + strings.ToUpper(a) + " IN " + rep.Location
			}
		}
		fmt.Fprintln(w, head)
		for _, s := range rep.Skills {
			line := s.Name + "\t" + s.Version
			for _, a := range rep.Agents {
				line += "\t" + string(s.User[a])
			}
			if rep.Location != "" {
				for _, a := range rep.Agents {
					st := string(s.Project[a])
					if s.Project[a] != "missing" && !s.Excluded[a] {
						st += " (committed)"
					}
					line += "\t" + st
				}
			}
			fmt.Fprintln(w, line)
		}
		w.Flush()
		if sub == "list" {
			fmt.Fprintf(out, "\nUser skills go to %s (Claude Code) and %s (Codex).\n", rep.UserDirs["claude"], rep.UserDirs["codex"])
		}
	})
}

// findWorktree resolves "LOC/WORKTREE" or "LOC" (its main checkout), or, with
// no reference, the worktree containing dir.
func findWorktree(ctx context.Context, c *box.Client, ref, dir string) (box.Location, box.Worktree, error) {
	locs, err := c.Locations(ctx)
	if err != nil {
		return box.Location{}, box.Worktree{}, err
	}
	if ref != "" {
		name, wt, _ := strings.Cut(ref, "/")
		for _, l := range locs {
			if l.Name != name {
				continue
			}
			for _, w := range l.Worktrees {
				if w.Name == wt || (wt == "" && w.Main) {
					return l, w, nil
				}
			}
			return box.Location{}, box.Worktree{}, fmt.Errorf("%s has no worktree %q; see locations", name, wt)
		}
		return box.Location{}, box.Worktree{}, fmt.Errorf("no location named %s", name)
	}
	var best box.Worktree
	var bestLoc box.Location
	for _, l := range locs {
		for _, w := range l.Worktrees {
			if (dir == w.Path || strings.HasPrefix(dir, w.Path+string(filepath.Separator))) && len(w.Path) > len(best.Path) {
				best, bestLoc = w, l
			}
		}
	}
	if best.Path == "" {
		return box.Location{}, box.Worktree{}, errors.New("not inside a worktree berth knows; name it as LOC/WORKTREE")
	}
	return bestLoc, best, nil
}

// preview asks the user's Shipyard app to open a worktree's page in a browser
// tab, in that worktree's workspace.
func preview(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	fs, _ := flags(args)
	path := fs.String("path", "/", "the page to open, e.g. /settings")
	pos, err := parse(fs, args)
	usage := "preview [LOC/WORKTREE] [PORT] [--path /page]"
	if err != nil || len(pos) > 2 {
		return usageErr(usage)
	}
	ref, port := "", 0
	for _, p := range pos {
		if n, err := strconv.Atoi(p); err == nil && n > 0 && n < 65536 {
			port = n
		} else if ref == "" {
			ref = p
		} else {
			return usageErr(usage)
		}
	}
	dir := os.Getenv("BERTH_WORKTREE_PATH")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	loc, wt, err := findWorktree(ctx, c, ref, dir)
	if err != nil {
		return err
	}
	if port == 0 {
		port = wt.Port
	}
	if port == 0 {
		return errors.New("which port? pass one, e.g. preview 3000")
	}
	if !strings.HasPrefix(*path, "/") {
		*path = "/" + *path
	}
	err = c.Emit(ctx, "preview.open", map[string]any{
		"location": loc.Name, "name": wt.Name, "path": wt.Path, "port": port, "url_path": *path,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Asked the Shipyard app to open port %d%s for %s/%s (it opens if the app is running).\n", port, *path, loc.Name, wt.Name)
	return nil
}

func locationConfig(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	fs, asJSON := flags(args)
	trustHash := fs.String("trust", "", "run the repository's config, if it still has this hash")
	untrust := fs.Bool("untrust", false, "stop running the repository's config")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 || (*trustHash != "" && *untrust) {
		return usageErr("location config NAME [--json] [--trust HASH|--untrust]")
	}
	var cfg box.Config
	switch {
	case *trustHash != "":
		cfg, err = c.TrustRepoConfig(ctx, pos[0], *trustHash)
	case *untrust:
		cfg, err = c.UntrustRepoConfig(ctx, pos[0])
	default:
		cfg, err = c.LocationConfig(ctx, pos[0])
	}
	if err != nil {
		return err
	}
	return show(out, *asJSON, cfg, func() {
		e := cfg.Effective
		from := "no " + cfg.RepoPath
		if cfg.Repo != nil {
			from = cfg.RepoPath
		}
		if t := cfg.RepoTrust; t.Pending() {
			from = "not " + cfg.RepoPath
			why := "nobody has trusted it on this box"
			if t.State == box.RepoTrustChanged {
				why = "it changed since it was trusted"
			}
			fmt.Fprintf(out, "%s does not run: %s. It wants to run:\n", cfg.RepoPath, why)
			printRepoConfig(out, *t.Wants)
			fmt.Fprintf(out, "Review it, then run it with: location config %s --trust %s\n\n", pos[0], t.Hash)
		}
		fmt.Fprintf(out, "%s (from %s, plus this box's own config)\n", pos[0], from)
		fmt.Fprintf(out, "  setup     %s\n  archive   %s\n  ports     %d per worktree\n", orNone(e.Setup), orNone(e.Archive), max(e.Ports, 1))
		printRepoConfig(out, box.RepoConfig{Env: e.Env, Services: e.Services, Hooks: e.Hooks, Agents: e.Agents, Login: e.Login})
	})
}

// printRepoConfig lists a config's env, services, hooks, flows and agents,
// and its scripts when set.
func printRepoConfig(out io.Writer, e box.RepoConfig) {
	if e.Setup != "" {
		fmt.Fprintf(out, "  setup     %s\n", e.Setup)
	}
	if e.Archive != "" {
		fmt.Fprintf(out, "  archive   %s\n", e.Archive)
	}
	keys := make([]string, 0, len(e.Env))
	for k := range e.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(out, "  env       %s=%s\n", k, e.Env[k])
	}
	for _, s := range e.Services {
		auto := ""
		if s.Autostart {
			auto = " (autostart)"
		}
		fmt.Fprintf(out, "  service   %s: %s%s\n", s.Name, s.Run, auto)
	}
	for _, h := range e.Hooks {
		fmt.Fprintf(out, "  hook      %s: %s\n", h.On, h.Run)
	}
	for _, f := range e.Flows {
		fmt.Fprintf(out, "  flow      %s (%d steps)\n", f.Name, len(f.Steps))
	}
	for _, a := range e.Agents {
		fmt.Fprintf(out, "  agent     %s: %s\n", a.ID, a.Command)
	}
	if l := e.Login; l != nil {
		who := "only these users"
		if l.Any {
			who = "any email"
		}
		fmt.Fprintf(out, "  login     %s (%s)\n", l.Script, who)
		for _, u := range l.Users {
			if u.Label != "" {
				fmt.Fprintf(out, "  user      %s (%s)\n", u.Email, u.Label)
			} else {
				fmt.Fprintf(out, "  user      %s\n", u.Email)
			}
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func service(ctx context.Context, c *box.Client, action string, args []string, out io.Writer) error {
	fs, asJSON := flags(args)
	pos, err := parse(fs, args)
	usage := "service list|start|stop|restart|log LOC/WORKTREE [SERVICE]"
	want := 2
	if action == "list" {
		want = 1
	}
	if err != nil || len(pos) != want || !strings.Contains(pos[0], "/") {
		return usageErr(usage)
	}
	loc, wt, _ := strings.Cut(pos[0], "/")
	switch action {
	case "list":
		all, err := c.WorktreeServices(ctx, loc, wt)
		if err != nil {
			return err
		}
		return show(out, *asJSON, all, func() {
			if len(all) == 0 {
				fmt.Fprintf(out, "%s has no services; add them under \"services\" in .berth/config.json.\n", loc)
				return
			}
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SERVICE\tSTATE\tPORT\tRUN")
			for _, s := range all {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", s.Name, s.State, s.Port, s.Run)
			}
			w.Flush()
		})
	case "start", "stop", "restart":
		st, err := c.ServiceAction(ctx, loc, wt, pos[1], action)
		if err != nil {
			return err
		}
		return show(out, *asJSON, st, func() {
			fmt.Fprintf(out, "%s in %s/%s: %s (port %d)\n", st.Name, loc, wt, st.State, st.Port)
		})
	case "log":
		b, err := c.ServiceLog(ctx, loc, wt, pos[1])
		if err != nil {
			return err
		}
		_, err = out.Write(b)
		return err
	}
	return usageErr(usage)
}
