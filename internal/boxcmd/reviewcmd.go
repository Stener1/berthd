package boxcmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"text/tabwriter"
	"time"

	"github.com/cosscom/shipyard/internal/box"
)

// reviewsCmd lists the pull requests opened for review on the box
// (box/prreview.go), and with --idle-days sets how long one may sit unused
// before it is cleaned up.
func reviewsCmd(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	fs, asJSON := flags(args)
	idle := fs.Int("idle-days", -1, "days a review may sit unused before it is cleaned up (0: never)")
	if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
		return usageErr("reviews [--idle-days N] [--json]")
	}
	var settings struct {
		IdleDays int `json:"idle_days"`
	}
	if *idle >= 0 {
		if err := c.Call(ctx, http.MethodPut, "/v1/reviews/settings", map[string]int{"idle_days": *idle}, &settings); err != nil {
			return err
		}
	} else if err := c.Call(ctx, http.MethodGet, "/v1/reviews/settings", nil, &settings); err != nil {
		return fmt.Errorf("this box runs an older berthd without PR reviews; update it (berth upgrade): %w", err)
	}
	var list []box.ReviewEntry
	if err := c.Call(ctx, http.MethodGet, "/v1/reviews", nil, &list); err != nil {
		return err
	}
	return show(out, *asJSON, map[string]any{"idle_days": settings.IdleDays, "reviews": list}, func() {
		if settings.IdleDays == 0 {
			fmt.Fprintln(out, "Reviews are cleaned up when their PR is merged or closed; never for sitting unused.")
		} else {
			fmt.Fprintf(out, "Reviews are cleaned up when their PR is merged or closed, or after %d days unused.\n", settings.IdleDays)
		}
		if len(list) == 0 {
			fmt.Fprintln(out, "No pull requests are open for review here.")
			return
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "\nWORKTREE\tPR\tCOMMIT\tAUTHOR\tOPENED\tWAITING")
		for _, r := range list {
			m := r.Review
			sha := m.SHA
			if len(sha) > 7 {
				sha = sha[:7]
			}
			fmt.Fprintf(tw, "%s/%s\t%s#%d\t%s\t%s\t%s\t%s\n", r.Location, r.Worktree, m.Repo, m.PR, sha, m.Author, m.Opened.Local().Format(time.DateOnly), m.Cleanup)
		}
		tw.Flush()
	})
}
