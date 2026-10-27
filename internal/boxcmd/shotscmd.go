package boxcmd

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/cosscom/shipyard/internal/box"
)

// berthd shots: visual before/after of a worktree's pages (box/shots.go).
//
//	shots compare [LOC/WT] [--pages / /login] [--sizes 375 768 1280]
//	              [--base main|turn-start|accepted|NAME] [--mask SEL]...
//	              [--color-scheme light|dark|both] [--as EMAIL] [--title T] [--note N] [--new]
//	shots baseline [LOC/WT] [--name turn-start] [--pages …] [--sizes …] [--color-scheme …]
//	shots accept [LOC/WT] ARTIFACT
//
// A flag takes every word after it up to the next flag, so the lists read
// as you would say them; "/,/login" works too.

const shotsUsage = "shots compare [LOC/WT] [--pages / /login] [--sizes 375 768 1280] [--base main|turn-start|accepted|NAME] [--mask SEL]... [--color-scheme light|dark|both] [--as EMAIL] [--title T] [--note N] [--new]  ·  shots baseline [LOC/WT] [--name turn-start] [--as EMAIL]  ·  shots accept [LOC/WT] ID"

func shotsArgs(args []string) (pos []string, lists map[string][]string, err error) {
	lists = map[string][]string{}
	cur := ""
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			name, val, has := strings.Cut(strings.TrimPrefix(a, "--"), "=")
			switch name {
			case "pages", "sizes", "mask", "base", "title", "note", "name", "color-scheme", "as":
				cur = name
				if _, ok := lists[name]; !ok {
					lists[name] = nil
				}
				if has {
					lists[name] = append(lists[name], val)
					cur = ""
				}
			case "new":
				lists["new"] = []string{"1"}
				cur = ""
			default:
				return nil, nil, fmt.Errorf("unknown flag --%s", name)
			}
			continue
		}
		if cur == "" {
			pos = append(pos, a)
			continue
		}
		lists[cur] = append(lists[cur], a)
		if cur != "pages" && cur != "sizes" && cur != "mask" {
			cur = ""
		}
	}
	// "/,/login" and "375,768" split too.
	for _, k := range []string{"pages", "sizes"} {
		var out []string
		for _, v := range lists[k] {
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
		}
		lists[k] = out
	}
	return pos, lists, nil
}

func shotsCmd(ctx context.Context, c *box.Client, sub string, args []string, out io.Writer) error {
	pos, l, err := shotsArgs(args)
	if err != nil {
		return usageErr(shotsUsage)
	}
	loc, wt, rest, err := browserWorktree(pos)
	if err != nil {
		return err
	}
	req := box.ShotsRequest{Pages: l["pages"], Mask: l["mask"], Session: os.Getenv("BERTH_SESSION"), Agent: os.Getenv("BERTH_AGENT")}
	if cs := l["color-scheme"]; len(cs) > 0 {
		req.ColorScheme = cs[0]
	}
	for _, s := range l["sizes"] {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "px"))
		if err != nil {
			return usageErr(shotsUsage)
		}
		req.Sizes = append(req.Sizes, n)
	}
	one := func(k string) string {
		if len(l[k]) > 0 {
			return l[k][0]
		}
		return ""
	}
	req.As = one("as")
	if req.As != "" && !box.ValidLoginEmail(req.As) {
		return fmt.Errorf("--as takes an email, such as pro@acme.test; %q isn't one", req.As)
	}
	base := "/v1/worktrees/" + url.PathEscape(loc) + "/" + url.PathEscape(wt) + "/shots/"
	var res box.ShotsResult
	switch sub {
	case "compare":
		if len(rest) > 0 {
			return usageErr(shotsUsage)
		}
		req.Base, req.Title, req.Note, req.New = one("base"), one("title"), one("note"), len(l["new"]) > 0
		err = c.Call(ctx, "POST", base+"compare", req, &res)
	case "baseline":
		if len(rest) > 0 {
			return usageErr(shotsUsage)
		}
		req.Save = one("name")
		if req.Save == "" {
			req.Save = "turn-start"
		}
		err = c.Call(ctx, "POST", base+"compare", req, &res)
	case "accept":
		if len(rest) != 1 {
			return usageErr(shotsUsage)
		}
		err = c.Call(ctx, "POST", base+"accept", map[string]string{"artifact": rest[0]}, &res)
	default:
		return usageErr(shotsUsage)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, res.Text)
	return nil
}
