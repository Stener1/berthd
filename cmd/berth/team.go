package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/sean-brydon/berthd/internal/agent"
	"github.com/sean-brydon/berthd/internal/box"
	"github.com/sean-brydon/berthd/internal/team"
)

const teamUsage = `berth team — set a box up the way your team's are, from <org>/.berth on GitHub

  ORG is a GitHub org, whose <org>/.berth holds the team setup, or a link to one
  anywhere: github.com/OWNER/REPO[@REF] or github.com/OWNER/REPO/tree/REF/FOLDER.

  berth team show ORG [--box BOX] [--json]   What the team setup does: the org, the commit,
                                             each box step (and which need your password),
                                             each repo (and whether you can read it), the keys
  berth team setup ORG BOX [--yes] [--only ID,…] [--key PROJECT/KEY=VALUE]…
                                             Run it on BOX: the steps in a terminal on the box
                                             (sudo asks for your password there), then the repos
  berth team status [--json]                 Each team setup you accepted, and how it stands
  berth team retry ORG [--from STEP|PROJECT] [--box BOX]
                                             Start a failed setup again from where it stopped

GitHub goes through the GitHub CLI: gh on this computer reads the team setup
(gh auth login if it isn't signed in), and the box signs in with its own gh.
`

func teamCommand(l laptop, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(teamUsage)
		return nil
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	switch args[0] {
	case "show":
		fs := flag.NewFlagSet("team show", flag.ContinueOnError)
		boxName := fs.String("box", "", "say which keys that box already has")
		asJSON := fs.Bool("json", false, "print JSON")
		if err := fs.Parse(reorder(args[1:])); err != nil || fs.NArg() != 1 {
			return errors.New("usage: berth team show ORG [--box BOX] [--json]")
		}
		v, err := teamView(ctx, c, fs.Arg(0), *boxName)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(v)
		}
		describeTeam(v)
		return nil
	case "setup":
		fs := flag.NewFlagSet("team setup", flag.ContinueOnError)
		yes := fs.Bool("yes", false, "run it without asking")
		only := fs.String("only", "", "set up only these projects (comma-separated ids); required ones always")
		var keys keyFlags
		fs.Var(&keys, "key", "PROJECT/KEY=VALUE, a key the team setup asks for (repeatable)")
		if err := fs.Parse(reorder(args[1:])); err != nil || fs.NArg() != 2 {
			return errors.New("usage: berth team setup ORG BOX [--yes] [--only ID,…] [--key PROJECT/KEY=VALUE]…")
		}
		return teamSetup(ctx, c, fs.Arg(0), fs.Arg(1), *yes, *only, keys)
	case "status":
		asJSON := len(args) > 1 && args[1] == "--json"
		var rows []struct {
			Org    string          `json:"org"`
			Name   string          `json:"name"`
			Commit string          `json:"commit"`
			Box    string          `json:"box"`
			Status *box.TeamStatus `json:"status"`
		}
		if err := c.Call(ctx, "GET", "/v1/team", nil, &rows); err != nil {
			return err
		}
		if asJSON {
			return printJSON(rows)
		}
		if len(rows) == 0 {
			fmt.Println("No team setups yet. See one with: berth team show ORG")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TEAM\tBOX\tCOMMIT\tSTATE\tPROGRESS")
		for _, r := range rows {
			state, progress := "box offline", ""
			if r.Status != nil {
				state, progress = phaseWords(*r.Status), progressLine(*r.Status)
			}
			fmt.Fprintf(w, "%s (%s)\t%s\t%s\t%s\t%s\n", r.Name, r.Org, r.Box, short(r.Commit), state, progress)
		}
		return w.Flush()
	case "retry":
		fs := flag.NewFlagSet("team retry", flag.ContinueOnError)
		from := fs.String("from", "", "the step or project to start again from (default: the one that failed)")
		boxName := fs.String("box", "", "the box (default: where it was set up)")
		if err := fs.Parse(reorder(args[1:])); err != nil || fs.NArg() != 1 {
			return errors.New("usage: berth team retry ORG [--from STEP|PROJECT] [--box BOX]")
		}
		var st box.TeamStatus
		if err := c.Call(ctx, "POST", "/v1/team/"+url.PathEscape(fs.Arg(0))+"/retry", map[string]string{"box": *boxName, "from": *from}, &st); err != nil {
			return err
		}
		return followTeam(ctx, c, st.Box, st)
	}
	return fmt.Errorf("unknown command: berth team %s (see berth team help)", args[0])
}

type keyFlags []string

func (k *keyFlags) String() string     { return strings.Join(*k, ",") }
func (k *keyFlags) Set(v string) error { *k = append(*k, v); return nil }

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func teamView(ctx context.Context, c *agent.Client, org, boxName string) (agent.TeamView, error) {
	var v agent.TeamView
	q := url.Values{}
	if boxName != "" {
		q.Set("box", boxName)
	}
	path := "/v1/team/" + url.PathEscape(org)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	err := c.Call(ctx, "GET", path, nil, &v)
	return v, err
}

func describeTeam(v agent.TeamView) {
	switch v.State {
	case "no-org":
		fmt.Printf("There is no org or user called %s on GitHub.\n", v.Org.Login)
		return
	case "unreadable":
		fmt.Printf("You can't read the team setup at %s. Ask an admin of %s for access.\n", v.Source.Label, v.Org.Login)
		return
	case "none":
		fmt.Printf("%s has no team setup you can read (no %s/.berth).\n", v.Org.Name, v.Org.Login)
		if len(v.Repos) > 0 {
			fmt.Println("\nIts repos you can read (* has its own Berth setup):")
			for _, r := range v.Repos {
				mark := " "
				if r.HasBerth {
					mark = "*"
				}
				fmt.Printf("  %s %s\n", mark, r.FullName)
			}
		}
		return
	}
	s := v.Setup
	vis := "public"
	if v.Repo.Private {
		vis = "private"
	}
	verified := ""
	if v.Org.Verified {
		verified = ", verified"
	}
	if v.Source.Kind == "link" {
		// Read from a link: say where, and don't say the org published it.
		fmt.Printf("%s team setup, for %s\n  From %s on GitHub (%s; not %s/.berth), updated %s by %s, commit %s\n", s.Name, s.Org, v.Source.Label, vis, s.Org, v.Commit.Date, v.Commit.Author, v.Commit.Short)
	} else {
		fmt.Printf("%s team setup\n  Published by %s on GitHub%s: %s (%s), updated %s by %s, commit %s\n", s.Name, v.Org.Name, verified, v.Repo.FullName, vis, v.Commit.Date, v.Commit.Author, v.Commit.Short)
	}
	if s.Description != "" {
		fmt.Printf("  %s\n", s.Description)
	}
	sudo := 0
	for _, st := range v.Steps {
		if st.Sudo {
			sudo++
		}
	}
	fmt.Printf("\nOn your box, once (%d steps; %d ask for your password, which you type on the box)\n", len(v.Steps), sudo)
	for _, st := range v.Steps {
		tag := ""
		if st.Sudo {
			tag = " [sudo]"
		}
		if st.Berth {
			tag = " [Berth]"
		}
		fmt.Printf("  %-28s%s %s\n", st.Title, tag, st.Detail)
	}
	fmt.Printf("\nRepos (%d of %d you can read)\n", v.Access.Readable, v.Access.Total)
	for _, p := range v.Projects {
		src := map[string]string{"repo": "its own .berth/config.json", "kit": "the team's kit", "none": "no setup"}[p.Source]
		if !p.Access {
			src = "you can't read it: ask an admin"
		}
		req := ""
		if p.Required {
			req = " (required)"
		}
		fmt.Printf("  %-28s → %s, %s%s\n", p.Repo, p.Path, src, req)
	}
	if v.Keys.Shared > 0 || len(v.Keys.Ask) > 0 {
		fmt.Printf("\nKeys: %d from 1Password", v.Keys.Shared)
		if n := len(v.Keys.Ask); n > 0 {
			names := []string{}
			for _, k := range v.Keys.Ask {
				names = append(names, k.Project+"/"+k.Key)
			}
			fmt.Printf(", %d to enter (%s)", n, strings.Join(names, ", "))
		}
		fmt.Println(". Values stay on your box, never in git.")
	}
	if v.Update != nil {
		fmt.Printf("\nUpdate: %s → %s by %s (%d changes)\n", v.Update.From, v.Update.To, v.Update.Author, len(v.Update.Changes))
		for _, ch := range v.Update.Changes {
			sign := map[string]string{"add": "+", "remove": "-", "change": "~"}[ch.Kind]
			fmt.Printf("  %s %s %s\n", sign, ch.Text, ch.Detail)
		}
	}
	for _, w := range v.Warnings {
		fmt.Println("warning:", w)
	}
}

func teamSetup(ctx context.Context, c *agent.Client, org, boxName string, yes bool, only string, keys keyFlags) error {
	v, err := teamView(ctx, c, org, boxName)
	if err != nil {
		return err
	}
	req := agent.TeamSetupRequest{Box: boxName, Keys: map[string]map[string]string{}}
	switch v.State {
	case "found":
		req.Commit = v.Commit.SHA
	case "none":
		return fmt.Errorf("%s has no team setup you can read; pick its repos to set up in the app (Team setup)", org)
	default:
		describeTeam(v)
		return errors.New("nothing to set up")
	}
	if only != "" {
		req.Projects = strings.Split(only, ",")
	}
	for _, k := range keys {
		ref, val, ok := strings.Cut(k, "=")
		proj, name, ok2 := strings.Cut(ref, "/")
		if !ok || !ok2 {
			return fmt.Errorf("--key %q is not PROJECT/KEY=VALUE", k)
		}
		if req.Keys[proj] == nil {
			req.Keys[proj] = map[string]string{}
		}
		req.Keys[proj][name] = val
	}
	describeTeam(v)
	if !yes {
		fmt.Printf("\nRun %s's team setup (commit %s) on %s? The steps run in a terminal on %s, as you. [y/N] ", v.Setup.Name, v.Commit.Short, boxName, boxName)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
			return errors.New("not set up")
		}
	}
	for _, a := range v.Keys.Ask {
		if a.Set || req.Keys[a.Project][a.Key] != "" || !term.IsTerminal(int(os.Stdin.Fd())) {
			continue
		}
		fmt.Printf("%s for %s (kept on %s only; Enter to skip): ", a.Key, a.Project, boxName)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return err
		}
		if val := strings.TrimSpace(string(b)); val != "" {
			if req.Keys[a.Project] == nil {
				req.Keys[a.Project] = map[string]string{}
			}
			req.Keys[a.Project][a.Key] = val
		}
	}
	var st box.TeamStatus
	if err := c.Call(ctx, "POST", "/v1/team/"+url.PathEscape(org)+"/setup", req, &st); err != nil {
		return err
	}
	return followTeam(ctx, c, boxName, st)
}

// followTeam prints a team setup's progress until it ends; Ctrl-C stops
// following, not the setup.
func followTeam(ctx context.Context, c *agent.Client, boxName string, st box.TeamStatus) error {
	fmt.Printf("\nSetting up %s for %s. Its terminal: berth attach %s/%s (Ctrl-C here only stops watching)\n", boxName, st.Name, boxName, st.Session)
	said := map[string]string{}
	for {
		for _, s := range st.Steps {
			key := s.State + s.Code
			if said[s.ID] == key {
				continue
			}
			said[s.ID] = key
			switch {
			case s.State == box.TeamWaiting && s.Code != "":
				fmt.Printf("  … %s: open %s and enter %s, then press Enter in the box's terminal (berth attach %s/%s)\n", s.Title, s.URL, s.Code, boxName, st.Session)
			case s.State == box.TeamWaiting && s.ID == team.OnePasswordStep:
				fmt.Printf("  … %s: op asks you to sign in on %s; answer it there: berth attach %s/%s\n", s.Title, boxName, boxName, st.Session)
			case s.State == box.TeamWaiting:
				fmt.Printf("  … %s: sudo asks for your password on %s; type it there: berth attach %s/%s\n", s.Title, boxName, boxName, st.Session)
			case s.State == box.TeamRunning:
				fmt.Printf("  … %s\n", s.Title)
			case s.State == box.TeamDone:
				fmt.Printf("  ✓ %s (%ds)\n", s.Title, s.Secs)
			case s.State == box.TeamSkipped:
				fmt.Printf("  ✓ %s (already done)\n", s.Title)
			case s.State == box.TeamFailed:
				fmt.Printf("  ✗ %s: %s\n", s.Title, s.Error)
			}
		}
		for _, p := range st.Projects {
			if said["p:"+p.ID] == p.State {
				continue
			}
			said["p:"+p.ID] = p.State
			switch p.State {
			case box.TeamCloning:
				fmt.Printf("  … cloning %s\n", p.Repo)
			case box.TeamReady:
				fmt.Printf("  ✓ %s is ready: %s/%s\n", p.Repo, boxName, p.Location)
			case box.TeamFailed:
				fmt.Printf("  ✗ %s: %s\n", p.Repo, p.Error)
			}
			for _, w := range p.Warnings {
				fmt.Printf("    note: %s\n", w)
			}
		}
		switch st.Phase {
		case "done":
			fmt.Printf("\n%s is set up for %s.\n", boxName, st.Name)
			return nil
		case "failed":
			return fmt.Errorf("%s's team setup stopped: %s. Fix it, then: berth team retry %s", st.Name, st.Error, st.Org)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
		var rows []struct {
			Box    string          `json:"box"`
			Status *box.TeamStatus `json:"status"`
		}
		if err := c.Call(ctx, "GET", "/v1/team", nil, &rows); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, r := range rows {
			if r.Box == boxName && r.Status != nil && r.Status.ID == st.ID {
				st = *r.Status
			}
		}
	}
}

func phaseWords(st box.TeamStatus) string {
	switch st.Phase {
	case "steps":
		for _, s := range st.Steps {
			if s.State == box.TeamWaiting {
				return "waiting for you"
			}
		}
		return "running box steps"
	case "projects":
		return "setting up repos"
	case "done":
		return "set up"
	}
	return "stopped: " + st.Error
}

func progressLine(st box.TeamStatus) string {
	steps, repos := 0, 0
	for _, s := range st.Steps {
		if s.State == box.TeamDone || s.State == box.TeamSkipped {
			steps++
		}
	}
	for _, p := range st.Projects {
		if p.State == box.TeamReady {
			repos++
		}
	}
	return fmt.Sprintf("%d/%d steps, %d/%d repos", steps, len(st.Steps), repos, len(st.Projects))
}
