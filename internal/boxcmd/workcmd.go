package boxcmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cosscom/shipyard/internal/box"
)

// workCmd is `work …`: a box's work records and pins (internal/box/work.go).
func workCmd(ctx context.Context, c *box.Client, sub string, args []string, out io.Writer) error {
	switch sub {
	case "list":
		return workList(ctx, c, args, out)
	case "get":
		fs, asJSON := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("work get ID [--json]")
		}
		return workCall(ctx, c, out, *asJSON, http.MethodGet, "/v1/work/"+url.PathEscape(pos[0]), nil)
	case "create":
		return workCreate(ctx, c, args, out)
	case "update":
		return workUpdate(ctx, c, args, out)
	case "group":
		fs, asJSON := flags(args)
		with := fs.String("with", "", "other records to group into it, with commas")
		sessions := fs.String("session", "", "sessions to group into it, with commas")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("work group ID [--with ID,ID] [--session NAME,NAME] [--json]")
		}
		body := map[string]any{"with": commaList(*with), "sessions": sessionRefs(*sessions)}
		return workCall(ctx, c, out, *asJSON, http.MethodPost, "/v1/work/"+url.PathEscape(pos[0])+"/group", body)
	case "ungroup":
		fs, asJSON := flags(args)
		sessions := fs.String("session", "", "sessions to take out, with commas")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 || *sessions == "" {
			return usageErr("work ungroup ID --session NAME,NAME [--json]")
		}
		var recs []box.WorkRecord
		if err := c.Call(ctx, http.MethodPost, "/v1/work/"+url.PathEscape(pos[0])+"/ungroup", map[string]any{"sessions": commaList(*sessions)}, &recs); err != nil {
			return err
		}
		return show(out, *asJSON, recs, func() { workTable(out, recs) })
	case "accept", "reject", "seen":
		fs, asJSON := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("work accept|reject|seen ID [--json]")
		}
		return workCall(ctx, c, out, *asJSON, http.MethodPost, "/v1/work/"+url.PathEscape(pos[0])+"/"+sub, nil)
	case "pins":
		fs, asJSON := flags(args)
		if _, err := parse(fs, args); err != nil {
			return usageErr("work pins [--json]")
		}
		var pins []box.WorkPin
		if err := c.Call(ctx, http.MethodGet, "/v1/work/pins", nil, &pins); err != nil {
			return err
		}
		return show(out, *asJSON, pins, func() {
			for _, p := range pins {
				fmt.Fprintf(out, "%s\t%s\n", p.Kind, p.Target)
			}
		})
	case "pin", "unpin":
		fs, asJSON := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 2 {
			return usageErr("work pin|unpin repo|project|work TARGET [--json]")
		}
		var res any
		if sub == "pin" {
			err = c.Call(ctx, http.MethodPost, "/v1/work/pins", map[string]string{"kind": pos[0], "target": pos[1]}, &res)
		} else {
			err = c.Call(ctx, http.MethodDelete, "/v1/work/pins?"+url.Values{"kind": {pos[0]}, "target": {pos[1]}}.Encode(), nil, &res)
		}
		if err != nil {
			return err
		}
		return show(out, *asJSON, res, func() {
			word := "Pinned"
			if sub == "unpin" {
				word = "Unpinned"
			}
			fmt.Fprintf(out, "%s %s %s\n", word, pos[0], pos[1])
		})
	case "settings":
		fs, asJSON := flags(args)
		pattern := fs.String("id-pattern", "", "the regular expression that finds a tracker id")
		reset := fs.Bool("default", false, "go back to the built-in pattern")
		if _, err := parse(fs, args); err != nil {
			return usageErr("work settings [--id-pattern RE|--default] [--json]")
		}
		var st box.WorkSettings
		var err error
		if *pattern != "" || *reset {
			err = c.Call(ctx, http.MethodPut, "/v1/work/settings", box.WorkSettings{IDPattern: *pattern}, &st)
		} else {
			err = c.Call(ctx, http.MethodGet, "/v1/work/settings", nil, &st)
		}
		if err != nil {
			return err
		}
		return show(out, *asJSON, st, func() {
			if st.IDPattern == "" {
				fmt.Fprintf(out, "id pattern: %s (the default)\n", box.DefaultIssuePattern)
				return
			}
			fmt.Fprintf(out, "id pattern: %s\n", st.IDPattern)
		})
	}
	return fmt.Errorf("unknown command %q", "work "+sub)
}

func commaList(s string) []string {
	out := []string{}
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func sessionRefs(s string) []box.WorkSessionRef {
	out := []box.WorkSessionRef{}
	for _, name := range commaList(s) {
		out = append(out, box.WorkSessionRef{Name: name})
	}
	return out
}

func workList(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	fs, asJSON := flags(args)
	all := fs.Bool("all", false, "closed records too")
	status := fs.String("status", "", "only records with this status")
	if _, err := parse(fs, args); err != nil {
		return usageErr("work list [--all] [--status open|parked|waiting|closed] [--json]")
	}
	q := url.Values{}
	if *all {
		q.Set("all", "1")
	}
	if *status != "" {
		q.Set("status", *status)
	}
	path := "/v1/work"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var recs []box.WorkRecord
	if err := c.Call(ctx, http.MethodGet, path, nil, &recs); err != nil {
		return err
	}
	return show(out, *asJSON, recs, func() { workTable(out, recs) })
}

func workCreate(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	fs, asJSON := flags(args)
	title := fs.String("title", "", "what the work is")
	sessions := fs.String("session", "", "sessions to move into it, with commas")
	issue := fs.String("issue", "", "its tracker id")
	project := fs.String("project", "", "its tracker project")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 0 {
		return usageErr("work create [--title T] [--session NAME,NAME] [--issue ID] [--project P] [--json]")
	}
	in := box.WorkInput{Title: *title, Sessions: sessionRefs(*sessions), Issue: *issue, Project: *project}
	return workCall(ctx, c, out, *asJSON, http.MethodPost, "/v1/work", in)
}

func workUpdate(ctx context.Context, c *box.Client, args []string, out io.Writer) error {
	fs, asJSON := flags(args)
	// Only what's given changes; given empty, it's cleared.
	var p box.WorkPatch
	set := func(field **string) func(string) error {
		return func(v string) error { *field = &v; return nil }
	}
	fs.Func("title", "what the work is", set(&p.Title))
	fs.Func("status", "open, parked, waiting or closed", set(&p.Status))
	fs.Func("issue", "its tracker id", set(&p.Issue))
	fs.Func("project", "its tracker project", set(&p.Project))
	fs.Func("not-until", "park it until this date", set(&p.NotUntil))
	fs.Func("waiting-on", "who or what it waits for", set(&p.WaitingOn))
	fs.Func("check-back", "when to look at it again", set(&p.CheckBack))
	fs.Func("suggest", "records proposed to join it, with commas", func(v string) error {
		ids := commaList(v)
		p.SuggestedWith = &ids
		return nil
	})
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return usageErr("work update ID [--title T] [--status open|parked|waiting|closed] [--issue ID] [--project P] [--not-until DATE] [--waiting-on WHO] [--check-back DATE] [--suggest ID,ID] [--json]")
	}
	return workCall(ctx, c, out, *asJSON, http.MethodPatch, "/v1/work/"+url.PathEscape(pos[0]), p)
}

// workCall makes a call that answers with one record, and shows it.
func workCall(ctx context.Context, c *box.Client, out io.Writer, asJSON bool, method, path string, body any) error {
	var rec box.WorkRecord
	if err := c.Call(ctx, method, path, body, &rec); err != nil {
		return err
	}
	return showWork(out, asJSON, rec)
}

func showWork(out io.Writer, asJSON bool, rec box.WorkRecord) error {
	return show(out, asJSON, rec, func() {
		workTable(out, []box.WorkRecord{rec})
		for _, m := range rec.Sessions {
			line := "  " + m.Name
			if m.StartedBy != "" {
				line += " (started by " + m.StartedBy + ")"
			}
			if !m.Ended.IsZero() {
				line += " (ended)"
			}
			fmt.Fprintln(out, line)
		}
		if rec.WaitingOn != "" {
			fmt.Fprintf(out, "  waiting on %s\n", rec.WaitingOn)
		}
		if !rec.NotUntil.IsZero() {
			fmt.Fprintf(out, "  not until %s\n", rec.NotUntil.Local().Format("2006-01-02"))
		}
		if len(rec.SuggestedWith) > 0 {
			fmt.Fprintf(out, "  suggested with %s\n", strings.Join(rec.SuggestedWith, ", "))
		}
	})
}

func workTable(out io.Writer, recs []box.WorkRecord) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tSEEN\tISSUE\tSESSIONS\tTITLE")
	for _, r := range recs {
		status := r.Status
		if r.Pinned {
			status += " (pin)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", r.ID, status, seenAgo(r.LastSeen), r.Issue, len(r.Sessions), r.Title)
	}
	tw.Flush()
}

func seenAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return since(t)
}
