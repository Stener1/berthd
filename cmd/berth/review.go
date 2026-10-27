package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/cosscom/shipyard/internal/agent"
	"github.com/cosscom/shipyard/internal/prreview"
)

const reviewUsage = `berth review — open a teammate's pull request on your own box, set up like any worktree

  berth review OWNER/NAME#N [--box BOX] [--as EMAIL] [--path /x] [--yes] [--json]
                                   Show the review sheet: the PR, its author and the exact
                                   commit, the box, what will run there and which keys it
                                   gets, and what in it changes setup. Then ask, and make
                                   the worktree at that commit (--yes: don't ask; --json:
                                   print the sheet and make nothing). --as and --path open
                                   it logged in as one of the project's dev users, at a page
  berth review link OWNER/NAME#N [--as EMAIL] [--path /x]
                                   Print the PR's review link, berth://review?repo=…&pr=…,
                                   to paste in its description or a chat

A review link carries no authority: only the repository and the PR number.
Who may be reviewed is read from GitHub with this computer's gh, as you: an
open PR from a branch of a repository your team's setup lists (or one already
on your box), by a member, owner or collaborator. Its setup comes from your
team's kit and the default branch's config, never from the PR.
`

func reviewCommand(l laptop, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(reviewUsage)
		return nil
	}
	if args[0] == "link" {
		return reviewLinkCommand(args[1:])
	}
	return reviewOpenCommand(l, args)
}

func reviewLinkCommand(args []string) error {
	fs := flag.NewFlagSet("review link", flag.ContinueOnError)
	as := fs.String("as", "", "open it logged in as this dev user")
	page := fs.String("path", "", "open it at this page")
	if err := fs.Parse(reorder(args)); err != nil || fs.NArg() != 1 {
		return errors.New("usage: berth review link OWNER/NAME#N [--as EMAIL] [--path /x]")
	}
	ref, err := reviewRef(fs.Arg(0), *as, *page)
	if err != nil {
		return err
	}
	fmt.Println(ref.ShareLink())
	return nil
}

func reviewOpenCommand(l laptop, args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	boxName := fs.String("box", "", "the box to review on (default: the first with the project)")
	yes := fs.Bool("yes", false, "make the worktree without asking")
	asJSON := fs.Bool("json", false, "print the sheet as JSON and make nothing")
	as := fs.String("as", "", "open it logged in as this dev user")
	page := fs.String("path", "", "open it at this page")
	if err := fs.Parse(reorder(args)); err != nil || fs.NArg() != 1 {
		return errors.New("usage: berth review OWNER/NAME#N [--box BOX] [--as EMAIL] [--path /x] [--yes] [--json]")
	}
	ref, err := reviewRef(fs.Arg(0), *as, *page)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	var sh agent.ReviewSheet
	if err := c.Call(ctx, "POST", "/v1/pr-review/plan", map[string]string{"link": ref.ShareLink(), "box": *boxName}, &sh); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(sh)
	}
	describeReview(sh)
	if !sh.Verdict.Allowed {
		return errors.New(sh.Verdict.Reason)
	}
	if !*yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("nothing was made: run it again with --yes to review it without asking")
		}
		if !confirm(fmt.Sprintf("Review it on %s?", sh.Box), false) {
			fmt.Println("Nothing was made.")
			return nil
		}
	}
	return openReview(ctx, c, sh)
}

func openReview(ctx context.Context, c *agent.Client, sh agent.ReviewSheet) error {
	var out agent.ReviewOpened
	req := agent.ReviewOpenRequest{Repo: sh.Repo, PR: sh.PR, SHA: sh.Head.SHA, Box: sh.Box}
	if sh.Login != nil {
		req.As, req.Path = sh.Login.As, sh.Login.Path
	}
	err := c.Call(ctx, "POST", "/v1/pr-review/open", req, &out)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s is ready on %s at %s (%s/%s). Its setup and dev server start there now;\nopen it in Shipyard, or: berth attach %s\n", reviewName(sh), out.Box, short(sh.Head.SHA), out.Location, out.Worktree.Name, out.Box)
	if out.Open != "" {
		fmt.Printf("In a browser on this computer: %s\n", out.Open)
	}
	return nil
}

// reviewRef reads OWNER/NAME#N or a link, with --as and --path read as a
// link's as and path are.
func reviewRef(arg, as, page string) (prreview.Ref, error) {
	ref, err := prreview.ParseArg(arg)
	if err != nil || (as == "" && page == "") {
		return ref, err
	}
	if as == "" {
		as = ref.As
	}
	if page == "" {
		page = ref.Path
	}
	return prreview.ParseLink(prreview.LinkFor(ref.Repo(), ref.PR, as, page))
}

// loginLine says how the review opens, as the sheet does.
func loginLine(sh agent.ReviewSheet, b *agent.ReviewBox) string {
	if sh.Login == nil {
		return ""
	}
	page := sh.Login.Path
	if page == "" {
		page = "/"
	}
	switch {
	case sh.Login.As == "":
		return "Opens " + page
	case b != nil && b.Login != nil && b.Login.Allowed:
		return fmt.Sprintf("Opens %s, logged in as %s", page, sh.Login.As)
	case b != nil && b.Login != nil:
		return fmt.Sprintf("Opens %s. %s", page, b.Login.Reason)
	}
	return ""
}

func reviewName(sh agent.ReviewSheet) string {
	return fmt.Sprintf("%s#%d", sh.Repo, sh.PR)
}

var associationWords = map[string]string{
	"OWNER":                  "an owner of %s",
	"MEMBER":                 "a member of %s",
	"COLLABORATOR":           "a collaborator on %s",
	"CONTRIBUTOR":            "a past contributor, outside %s",
	"FIRST_TIME_CONTRIBUTOR": "a first-time contributor, outside %s",
	"FIRST_TIMER":            "new to GitHub, outside %s",
	"NONE":                   "outside %s",
}

// describeReview prints the review sheet.
func describeReview(sh agent.ReviewSheet) {
	fmt.Printf("%s", reviewName(sh))
	if sh.Title != "" {
		fmt.Printf("  %s", sh.Title)
	}
	fmt.Println()
	if sh.Author != nil {
		words, ok := associationWords[sh.Author.Association]
		if !ok {
			words = strings.ToLower(sh.Author.Association) + " (%s)"
		}
		fmt.Printf("  by %s, %s\n", sh.Author.Login, fmt.Sprintf(words, sh.Org))
	}
	if sh.Head != nil {
		from := ""
		if sh.Head.Cross {
			from = " in " + sh.Head.Repo
		}
		fmt.Printf("  branch %s%s at %s\n", sh.Head.Branch, from, sh.Head.SHA)
	}
	if sh.URL != "" {
		fmt.Printf("  %s\n", sh.URL)
	}
	if sh.Hint != nil && !sh.Hint.Matches {
		fmt.Printf("  The link was made at %s; the PR has moved on since. This is its head now.\n", sh.Hint.SHA)
	}
	if !sh.Verdict.Allowed {
		fmt.Printf("\n%s.\n", strings.TrimSuffix(sh.Verdict.Reason, "."))
		return
	}
	var b *agent.ReviewBox
	for i := range sh.Boxes {
		if sh.Boxes[i].Box == sh.Box {
			b = &sh.Boxes[i]
		}
	}
	if b != nil {
		others := []string{}
		for _, o := range sh.Boxes {
			if o.Box != b.Box {
				others = append(others, o.Box)
			}
		}
		fmt.Printf("\nOn %s (project %s)", b.Box, b.Location)
		if len(others) > 0 {
			fmt.Printf("; also on %s (--box)", strings.Join(others, ", "))
		}
		fmt.Println()
		s := b.Setup
		from := map[string]string{"kit": "your team's kit", "repo": "the repo's .berth/config.json, as trusted on this box", "box": "this box's own config", "none": "nothing"}[s.From]
		if s.Kit != nil && s.From == "kit" {
			from = "your team's kit, " + s.Kit.Name
		}
		if s.Script != "" {
			fmt.Printf("  Setup      %s: %s\n", from, s.Script)
		} else {
			fmt.Printf("  Setup      none\n")
		}
		for i, sv := range s.Services {
			label := "           "
			if i == 0 {
				label = "  Services "
			}
			name := sv.Name
			if sv.Title != "" {
				name += " (" + sv.Title + ")"
			}
			fmt.Printf("%s %s: %s\n", label, name, sv.Run)
		}
		if s.MatchesDefault != nil && !*s.MatchesDefault {
			fmt.Printf("  Note       the main checkout's .berth/config.json differs from %s's; the one trusted on this box is used\n", s.DefaultBranch)
		}
		fmt.Printf("  Never      anything from this PR's .berth/; git hooks are off while it is checked out\n")
		keys := fmt.Sprintf("the team's shared keys (%d)", len(b.Secrets.Shared))
		if len(b.Secrets.Withheld) > 0 {
			keys += "; never your own (" + strings.Join(b.Secrets.Withheld, ", ") + ")"
		}
		fmt.Printf("  Keys       %s\n", keys)
		if b.IdleDays > 0 {
			fmt.Printf("  Clean-up   when the PR is merged or closed, or after %d days unused; never with uncommitted changes\n", b.IdleDays)
		} else {
			fmt.Printf("  Clean-up   when the PR is merged or closed; never with uncommitted changes\n")
		}
		if l := loginLine(sh, b); l != "" {
			fmt.Printf("  Opens      %s\n", strings.TrimPrefix(l, "Opens "))
		}
		if b.Existing != nil {
			fmt.Printf("  Already    open as %s at %s; Review moves it to this commit\n", b.Existing.Worktree, short(b.Existing.SHA))
		}
	}
	if len(sh.Changes) > 0 {
		n := 0
		for _, c := range sh.Changes {
			n += len(c.Files)
		}
		fmt.Printf("\nIt changes setup (%d of its %d files):\n", n, sh.Files)
		for _, c := range sh.Changes {
			fmt.Printf("  %s: %s\n", c.Title, strings.Join(c.Files, ", "))
			if c.Detail != "" {
				fmt.Printf("    %s\n", c.Detail)
			}
		}
	} else {
		fmt.Printf("\nIt changes no setup files (%d files in all).\n", sh.Files)
	}
	fmt.Println("\nNothing has been fetched to the box or run yet.")
}
