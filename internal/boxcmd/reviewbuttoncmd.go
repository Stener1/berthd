package boxcmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/cosscom/shipyard/internal/box"
)

// hereDir is the worktree a command run inside one is about: the agent's
// $BERTH_WORKTREE_PATH, else the current folder.
func hereDir() string {
	dir := os.Getenv("BERTH_WORKTREE_PATH")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return dir
}

// loginUsersCmd lists the users a project's worktrees can be logged in as:
// what a review link's or button's as may name.
func loginUsersCmd(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	fs, asJSON := flags(args)
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 1 {
		return usageErr("login users [LOC[/WT]] [--json]")
	}
	ref := ""
	if len(pos) == 1 {
		ref = pos[0]
	}
	loc, _, err := findWorktree(ctx, c, ref, hereDir())
	if err != nil {
		return err
	}
	cfg, err := c.LocationConfig(ctx, loc.Name)
	if err != nil {
		return err
	}
	l := cfg.Effective.Login
	users := []box.LoginUser{}
	anyEmail := false
	if l != nil {
		users, anyEmail = append(users, l.Users...), l.Any
	}
	return show(out, *asJSON, map[string]any{"location": loc.Name, "users": users, "any": anyEmail}, func() {
		if l == nil {
			fmt.Fprintf(out, "%s has no login users: its kit or .berth/config.json sets none.\n", loc.Name)
			return
		}
		for _, u := range users {
			if u.Label != "" {
				fmt.Fprintf(out, "%s\t%s\n", u.Email, u.Label)
			} else {
				fmt.Fprintln(out, u.Email)
			}
		}
		if anyEmail {
			fmt.Fprintln(out, "(any email may be asked for, too)")
		}
	})
}

// reviewButtonCmd shows a worktree's "Review in Shipyard" button, and with
// --as or --path sets what it opens with; berthd puts that in the PR's
// description on its next look.
func reviewButtonCmd(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	fs, asJSON := flags(args)
	as := fs.String("as", "", "open the review logged in as this login user")
	path := fs.String("path", "", "open the review at this page, such as /settings")
	clear := fs.Bool("clear", false, "open the review without logging in, at its home page")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 1 || (*clear && (*as != "" || *path != "")) {
		return usageErr("review-button [LOC/WT] [--as EMAIL] [--path /x] [--clear] [--json]")
	}
	ref := ""
	if len(pos) == 1 {
		ref = pos[0]
	}
	loc, wt, err := findWorktree(ctx, c, ref, hereDir())
	if err != nil {
		return err
	}
	p := "/v1/worktrees/" + url.PathEscape(loc.Name) + "/" + url.PathEscape(wt.Name) + "/review-button"
	var info box.ReviewButtonInfo
	if *as != "" || *path != "" || *clear {
		err = c.Call(ctx, http.MethodPut, p, map[string]string{"as": *as, "path": *path}, &info)
	} else {
		err = c.Call(ctx, http.MethodGet, p, nil, &info)
	}
	if err != nil {
		return err
	}
	return show(out, *asJSON, info, func() {
		switch info.State {
		case "off":
			fmt.Fprintf(out, "%s has the review button off: PRs get none. Turn it on with \"review_button\": true in .berth/config.json or Project settings.\n", loc.Name)
		case "waiting":
			fmt.Fprintf(out, "Waiting for %s/%s's branch to have a PR; berthd adds the button then.\n", loc.Name, wt.Name)
		case "skipped":
			fmt.Fprintf(out, "%s#%d gets no button (%s).\n", info.Repo, info.PR, info.Skipped)
		case "placed":
			fmt.Fprintf(out, "%s#%d has the button.\n", info.Repo, info.PR)
		}
		if info.As != "" {
			fmt.Fprintf(out, "It opens logged in as %s.\n", info.As)
		}
		if info.Path != "" {
			fmt.Fprintf(out, "It opens at %s.\n", info.Path)
		}
		if info.Link != "" {
			fmt.Fprintln(out, info.Link)
		}
	})
}
