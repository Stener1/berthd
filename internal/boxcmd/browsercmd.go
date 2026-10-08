package boxcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/cosscom/shipyard/internal/box"
)

// The agent browser from the command line. Every command prints the short
// text the box returns, which is what an agent reads: a compact snapshot
// with refs, a delta after an action, a shot's file path.

// browserWorktree takes the worktree a command is for: a first argument
// LOC/WT, else the worktree this shell is in ($BERTH_LOCATION and
// $BERTH_WORKTREE_NAME, set in every berth session).
func browserWorktree(pos []string) (loc, wt string, rest []string, err error) {
	if len(pos) > 0 && strings.Contains(pos[0], "/") && !strings.HasPrefix(pos[0], "/") && !strings.Contains(pos[0], "://") {
		l, w, _ := strings.Cut(pos[0], "/")
		if l != "" && w != "" {
			return l, w, pos[1:], nil
		}
	}
	loc, wt = os.Getenv("BERTH_LOCATION"), os.Getenv("BERTH_WORKTREE_NAME")
	if loc == "" || wt == "" {
		return "", "", nil, errors.New("which worktree? give LOC/WT, or run this in a berth session")
	}
	return loc, wt, pos, nil
}

func browserCall(ctx context.Context, c *box.Client, loc, wt, method, action string, in any, out io.Writer) error {
	var res struct {
		Text string `json:"text"`
	}
	path := "/v1/worktrees/" + url.PathEscape(loc) + "/" + url.PathEscape(wt) + "/browser/" + action
	if err := c.Call(ctx, method, path, in, &res); err != nil {
		return err
	}
	fmt.Fprintln(out, res.Text)
	return nil
}

// checkSize says what is wrong with a size or a scale before anything is
// sent: the box checks again.
func checkSize(size, scale string) error {
	if size == "" && scale == "" {
		return nil
	}
	_, err := box.DefaultViewport.Resolve(size, scale)
	return err
}

func browserCmd(ctx context.Context, c *box.Client, sub string, args []string, out io.Writer) error {
	switch sub {
	case "open":
		fs, _ := flags(args)
		size := fs.String("size", "", "the page's size first: WIDTHxHEIGHT or a preset (phone, phone-max, tablet, laptop, desktop)")
		scale := fs.String("scale", "", "the device scale factor first, 1 to 3")
		as := fs.String("as", "", "log in as this user first (an email the project's login lists)")
		path := fs.String("path", "", "the page to open, such as /settings (the same as PATH)")
		usage := "browser open [LOC/WT] [PATH|URL] [--as EMAIL] [--path /x] [--size WxH|PRESET] [--scale N]"
		pos, err := parse(fs, args)
		if err != nil || len(pos) > 2 {
			return usageErr(usage)
		}
		loc, wt, rest, err := browserWorktree(pos)
		if err != nil {
			return err
		}
		target := *path
		if len(rest) > 0 {
			if target != "" {
				return usageErr(usage)
			}
			target = rest[0]
		}
		if *path != "" && !strings.HasPrefix(*path, "/") {
			return errors.New("--path is a path on the worktree, such as /settings")
		}
		if *as != "" && !box.ValidLoginEmail(*as) {
			return fmt.Errorf("--as takes an email, such as pro@acme.test; %q isn't one", *as)
		}
		if err := checkSize(*size, *scale); err != nil {
			return err
		}
		return browserCall(ctx, c, loc, wt, "POST", "open", map[string]string{"url": target, "size": *size, "scale": *scale, "as": *as}, out)
	case "resize":
		fs, _ := flags(args)
		scale := fs.String("scale", "", "the device scale factor, 1 to 3 (2 for a Retina screen)")
		pos, err := parse(fs, args)
		if err != nil || len(pos) > 2 {
			return usageErr("browser resize [LOC/WT] WxH|PRESET [--scale N]")
		}
		loc, wt, rest, err := browserWorktree(pos)
		if err != nil {
			return err
		}
		size := ""
		if len(rest) > 0 {
			size = rest[0]
		}
		// Nothing to set says what can be, presets and all.
		if _, err := box.DefaultViewport.Resolve(size, *scale); err != nil {
			return err
		}
		return browserCall(ctx, c, loc, wt, "POST", "resize", map[string]string{"size": size, "scale": *scale}, out)
	case "snapshot":
		fs, _ := flags(args)
		full := fs.Bool("full", false, "every element, not just what you can act on")
		delta := fs.Bool("delta", false, "only what changed since the last snapshot")
		sel := fs.String("selector", "", "only under this CSS selector")
		depth := fs.Int("depth", 0, "levels deep")
		pos, err := parse(fs, args)
		if err != nil || len(pos) > 1 {
			return usageErr("browser snapshot [LOC/WT] [--full] [--delta] [--selector SEL] [--depth N]")
		}
		loc, wt, _, err := browserWorktree(pos)
		if err != nil {
			return err
		}
		return browserCall(ctx, c, loc, wt, "POST", "snapshot", map[string]any{"full": *full, "delta": *delta, "selector": *sel, "depth": *depth}, out)
	case "click", "fill", "press", "select", "hover", "check":
		fs, _ := flags(args)
		pos, err := parse(fs, args)
		usage := "browser click|hover|check [LOC/WT] @REF  ·  browser fill|select [LOC/WT] @REF VALUE  ·  browser press [LOC/WT] KEY"
		if err != nil {
			return usageErr(usage)
		}
		loc, wt, rest, err := browserWorktree(pos)
		if err != nil {
			return err
		}
		req := map[string]string{"action": sub}
		switch {
		case sub == "press" && len(rest) == 1:
			req["value"] = rest[0]
		case sub == "press" && len(rest) == 2:
			req["target"], req["value"] = rest[0], rest[1]
		case (sub == "fill" || sub == "select") && len(rest) == 2:
			req["target"], req["value"] = rest[0], rest[1]
		case len(rest) == 1 && sub != "fill" && sub != "select" && sub != "press":
			req["target"] = rest[0]
		default:
			return usageErr(usage)
		}
		return browserCall(ctx, c, loc, wt, "POST", "act", req, out)
	case "wait":
		fs, _ := flags(args)
		text := fs.String("text", "", "until the page shows this text")
		u := fs.String("url", "", "until the URL contains this")
		idle := fs.Bool("idle", false, "until the network is quiet")
		timeout := fs.String("timeout", "10s", "at most this long")
		pos, err := parse(fs, args)
		if err != nil || len(pos) > 1 || (*text == "" && *u == "" && !*idle) {
			return usageErr("browser wait [LOC/WT] --text T | --url U | --idle [--timeout 10s]")
		}
		loc, wt, _, err := browserWorktree(pos)
		if err != nil {
			return err
		}
		return browserCall(ctx, c, loc, wt, "POST", "wait", map[string]any{"text": *text, "url": *u, "idle": *idle, "timeout": *timeout}, out)
	case "shot":
		fs, _ := flags(args)
		el := fs.String("el", "", "only this element (@REF or a CSS selector)")
		full := fs.Bool("full", false, "the whole page, not just what shows")
		width := fs.Int("width", 800, "the image's width in pixels, at most")
		native := fs.Bool("native", false, "at the page's own size and scale, not shrunk to --width")
		pos, err := parse(fs, args)
		if err != nil || len(pos) > 1 {
			return usageErr("browser shot [LOC/WT] [--el @REF] [--full] [--width 800 | --native]")
		}
		loc, wt, _, err := browserWorktree(pos)
		if err != nil {
			return err
		}
		return browserCall(ctx, c, loc, wt, "POST", "shot", map[string]any{"el": *el, "full": *full, "width": *width, "native": *native}, out)
	case "console":
		fs, _ := flags(args)
		all := fs.Bool("all", false, "every level, not just errors and warnings")
		pos, err := parse(fs, args)
		if err != nil || len(pos) > 1 {
			return usageErr("browser console [LOC/WT] [--all]")
		}
		loc, wt, _, err := browserWorktree(pos)
		if err != nil {
			return err
		}
		q := ""
		if *all {
			q = "?all=1"
		}
		return browserCall(ctx, c, loc, wt, "GET", "console"+q, nil, out)
	case "network", "status", "close":
		fs, _ := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) > 1 {
			return usageErr("browser network|status|close [LOC/WT]")
		}
		loc, wt, _, err := browserWorktree(pos)
		if err != nil {
			return err
		}
		method := "GET"
		if sub == "close" {
			method = "POST"
		}
		return browserCall(ctx, c, loc, wt, method, sub, nil, out)
	case "eval":
		fs, _ := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) < 1 || len(pos) > 2 {
			return usageErr("browser eval [LOC/WT] JS")
		}
		loc, wt, rest, err := browserWorktree(pos)
		if err != nil || len(rest) != 1 {
			return usageErr("browser eval [LOC/WT] JS")
		}
		return browserCall(ctx, c, loc, wt, "POST", "eval", map[string]string{"js": rest[0]}, out)
	case "allow":
		fs, _ := flags(args)
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usageErr("browser allow ORIGIN")
		}
		var res struct {
			Text string `json:"text"`
		}
		if err := c.Call(ctx, "POST", "/v1/browser/allow", map[string]string{"origin": pos[0]}, &res); err != nil {
			return err
		}
		fmt.Fprintln(out, res.Text)
		return nil
	case "reap":
		fs, asJSON := flags(args)
		dry := fs.Bool("dry-run", false, "only list them")
		if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
			return usageErr("browser reap [--dry-run] [--json]")
		}
		var res struct {
			Sessions []box.AgentBrowserSession `json:"sessions"`
			Text     string                    `json:"text"`
		}
		if err := c.Call(ctx, "POST", "/v1/browser/reap", map[string]bool{"dry_run": *dry}, &res); err != nil {
			return err
		}
		return show(out, *asJSON, res.Sessions, func() { fmt.Fprintln(out, res.Text) })
	case "list":
		fs, asJSON := flags(args)
		if pos, err := parse(fs, args); err != nil || len(pos) > 0 {
			return usageErr("browser list [--json]")
		}
		var all []box.BrowserStatus
		if err := c.Call(ctx, "GET", "/v1/browsers", nil, &all); err != nil {
			return err
		}
		return show(out, *asJSON, all, func() {
			if len(all) == 0 {
				fmt.Fprintln(out, "No browsers open.")
			}
			for _, b := range all {
				fmt.Fprintf(out, "%s/%s  %s  %s  %d MB  last used %s\n", b.Location, b.Worktree, b.URL, b.Size, b.RSS>>20, b.LastUsed.Local().Format("15:04"))
			}
		})
	}
	return fmt.Errorf("unknown command browser %s", sub)
}
