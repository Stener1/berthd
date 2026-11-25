package boxcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/cosscom/shipyard/internal/box"
)

// appendTo collects a flag given more than once. Keys can hold commas.
func appendTo(list *[]string) func(string) error {
	return func(v string) error {
		*list = append(*list, v)
		return nil
	}
}

// threadCmd is `work find`, the verbs that put a session on a thread key, and
// delivery records (internal/box/workthreads.go).
func threadCmd(ctx context.Context, c *box.Client, sub string, args []string, out io.Writer) error {
	switch sub {
	case "find":
		fs, asJSON := flags(args)
		key := fs.String("key", "", "the thread key")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 0 || *key == "" {
			return usageErr("work find --key K [--json]")
		}
		var t box.Thread
		if err := c.Call(ctx, http.MethodGet, "/v1/work/threads?"+url.Values{"key": {*key}}.Encode(), nil, &t); err != nil {
			return err
		}
		return show(out, *asJSON, t, func() { threadText(out, t) })
	case "claim", "unclaim", "subscribe", "unsubscribe":
		fs, asJSON := flags(args)
		var keys []string
		fs.Func("key", "a thread key; give it again for more", appendTo(&keys))
		session := fs.String("session", "", "the session (default: this one)")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 0 || len(keys) == 0 {
			return usageErr("work claim|unclaim|subscribe|unsubscribe --key K [--key K] [--session NAME] [--json]")
		}
		name := firstNonEmpty(*session, os.Getenv("BERTH_SESSION"))
		if name == "" {
			return errors.New("which session? say --session NAME")
		}
		return workCall(ctx, c, out, *asJSON, http.MethodPost, "/v1/work/threads/"+sub, map[string]any{"session": name, "keys": keys})
	case "deliveries":
		fs, asJSON := flags(args)
		key := fs.String("key", "", "only this thread key's")
		session := fs.String("session", "", "only those sent to this session")
		state := fs.String("state", "", "only those in this state")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 0 {
			return usageErr("work deliveries [--key K] [--session NAME] [--state held|typed|cancelled|undone|handed_back|lost] [--json]")
		}
		var ds []box.WorkDelivery
		q := url.Values{"key": {*key}, "session": {*session}, "state": {*state}}
		if err := c.Call(ctx, http.MethodGet, "/v1/work/deliveries?"+q.Encode(), nil, &ds); err != nil {
			return err
		}
		return show(out, *asJSON, ds, func() { deliveryTable(out, ds) })
	case "delivery":
		fs, asJSON := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("work delivery ID [--json]")
		}
		return deliveryCall(ctx, c, out, *asJSON, http.MethodGet, "/v1/work/deliveries/"+url.PathEscape(pos[0]), nil)
	case "undo":
		fs, asJSON := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("work undo ID [--json]")
		}
		return deliveryCall(ctx, c, out, *asJSON, http.MethodPost, "/v1/work/deliveries/"+url.PathEscape(pos[0])+"/undo", nil)
	case "hand-back":
		fs, asJSON := flags(args)
		why := fs.String("why", "", "why it isn't this session's work")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("work hand-back ID [--why TEXT] [--json]")
		}
		return deliveryCall(ctx, c, out, *asJSON, http.MethodPost, "/v1/work/deliveries/"+url.PathEscape(pos[0])+"/hand-back", map[string]string{"why": *why})
	}
	return fmt.Errorf("unknown command %q", "work "+sub)
}

func deliveryCall(ctx context.Context, c *box.Client, out io.Writer, asJSON bool, method, path string, body any) error {
	var d box.WorkDelivery
	if err := c.Call(ctx, method, path, body, &d); err != nil {
		return err
	}
	return show(out, asJSON, d, func() {
		deliveryTable(out, []box.WorkDelivery{d})
		switch {
		case d.State == box.DeliveryUndone && d.Retracted:
			fmt.Fprintln(out, "It was still held: it's been taken out and was never typed.")
		case d.State == box.DeliveryUndone:
			fmt.Fprintln(out, "It had already been typed, so its session has seen it.")
		case d.Why != "":
			fmt.Fprintf(out, "Why: %s\n", d.Why)
		}
	})
}

func deliveryTable(out io.Writer, ds []box.WorkDelivery) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tKEY\tTO\tTURN\tFROM\tSOURCE")
	for _, d := range ds {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, d.State, d.Key, d.To.Name, d.Turn, d.From, d.Source)
	}
	tw.Flush()
}

func threadText(out io.Writer, t box.Thread) {
	if t.Owner != nil {
		fmt.Fprintf(out, "%s is owned by %s\n", t.Key, memberLine(*t.Owner))
	} else {
		fmt.Fprintf(out, "%s has no owner\n", t.Key)
	}
	if t.Record != nil {
		fmt.Fprintf(out, "record: %s (%s) %s\n", t.Record.ID, t.Provenance, t.Record.Title)
	}
	if len(t.Also) > 0 {
		fmt.Fprintf(out, "also on: %s\n", strings.Join(t.Also, ", "))
	}
	for _, m := range t.Claimants {
		if t.Owner == nil || m.Name != t.Owner.Name {
			fmt.Fprintf(out, "claimed by %s\n", memberLine(m))
		}
	}
	for _, m := range t.Subscribers {
		fmt.Fprintf(out, "subscriber %s\n", memberLine(m))
	}
}

func memberLine(m box.ThreadMember) string {
	switch {
	case m.Exited:
		return m.Name + " (its agent has ended)"
	case !m.Live:
		return m.Name + " (ended)"
	case m.AgentState != "":
		return m.Name + " (" + m.AgentState + ")"
	}
	return m.Name + " (live)"
}

// resumeCmd is what can be done about a session's held prompts and an ended
// session: the box's routes for them, by name.
func resumeCmd(ctx context.Context, c *box.Client, cmd string, args []string, out io.Writer) error {
	switch cmd {
	case "session queue":
		fs, asJSON := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("session queue NAME [--json]")
		}
		held := []box.QueuedPrompt{}
		if err := c.Call(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(pos[0])+"/queue", nil, &held); err != nil {
			return err
		}
		return show(out, *asJSON, held, func() {
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TURN\tFROM\tHELD\tPROMPT")
			for _, q := range held {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", q.Turn, q.Origin, since(q.At), strings.ReplaceAll(q.Preview, "\n", " "))
			}
			tw.Flush()
		})
	case "session unqueue":
		fs, asJSON := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 2 {
			return usageErr("session unqueue NAME TURN [--json]")
		}
		var res map[string]any
		if err := c.Call(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(pos[0])+"/queue/"+url.PathEscape(pos[1]), nil, &res); err != nil {
			return err
		}
		return show(out, *asJSON, res, func() { fmt.Fprintf(out, "Cancelled %s: it won't be typed\n", pos[1]) })
	case "session resume":
		fs, asJSON := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("session resume NAME [--json]")
		}
		return resumed(ctx, c, out, *asJSON, "/v1/sessions/"+url.PathEscape(pos[0])+"/resume", nil)
	case "conversation resume":
		fs, asJSON := flags(args)
		open := fs.String("open", "", "show it in the Shipyard app: split or tab")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("conversation resume ID [--open split|tab] [--json]")
		}
		var body any
		if *open != "" {
			body = map[string]string{"open": *open}
		}
		return resumed(ctx, c, out, *asJSON, "/v1/conversations/"+url.PathEscape(pos[0])+"/resume", body)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func resumed(ctx context.Context, c *box.Client, out io.Writer, asJSON bool, path string, body any) error {
	var sess box.Session
	if err := c.Call(ctx, http.MethodPost, path, body, &sess); err != nil {
		return err
	}
	return show(out, asJSON, sess, func() { fmt.Fprintf(out, "Resumed in session %s\n", sess.Name) })
}
