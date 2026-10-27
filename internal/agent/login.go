package agent

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/proxy"
)

// login answers the proxy's login route (proxy/login.go): it finds the
// worktree a host names and asks its box, over this laptop's paired
// channel, to run that worktree's login for email. The box decides who may
// log in, from config it trusts; the cookies come back for the proxy to set
// on that host alone.
func (a *Agent) login(ctx context.Context, labels []string, email string) (proxy.LoginResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	boxName, loc, wt, err := a.worktreeOf(ctx, labels)
	if err != nil {
		return proxy.LoginResult{}, err
	}
	c, ok := a.client(boxName)
	if !ok {
		return proxy.LoginResult{}, &proxy.LoginError{Status: http.StatusBadGateway, Msg: "no paired box named " + boxName}
	}
	return box.NewClient(c).Login(ctx, loc, wt, email)
}

// worktreeOf names the box, location and worktree a host's labels are for,
// as the box spells them:
//
//	[worktree, location, box]  fix-x.shop.devl.localhost
//	[location, box]            shop.devl.localhost (the main checkout)
//	[worktree, location]       fix-x.shop.localhost, when one box has it
func (a *Agent) worktreeOf(ctx context.Context, labels []string) (string, string, string, error) {
	notFound := &proxy.LoginError{Status: http.StatusNotFound, Msg: "no worktree at " + strings.Join(labels, ".") + ".localhost"}
	find := func(boxName, locName, wtName string) (string, string, bool) {
		c, ok := a.client(boxName)
		if !ok {
			return "", "", false
		}
		locs, err := box.NewClient(c).Locations(ctx)
		if err != nil {
			return "", "", false
		}
		for _, l := range locs {
			if !strings.EqualFold(l.Name, locName) {
				continue
			}
			for _, w := range l.Worktrees {
				if (wtName == "" && w.Main) || (wtName != "" && strings.EqualFold(w.Name, wtName)) {
					return l.Name, w.Name, true
				}
			}
		}
		return "", "", false
	}
	switch len(labels) {
	case 3:
		if loc, wt, ok := find(labels[2], labels[1], labels[0]); ok {
			return labels[2], loc, wt, nil
		}
	case 2:
		if _, known := a.client(labels[1]); known {
			if loc, wt, ok := find(labels[1], labels[0], ""); ok {
				return labels[1], loc, wt, nil
			}
			return "", "", "", notFound
		}
		var hit []string
		for _, b := range a.status().Boxes {
			if b.State != StateOnline {
				continue
			}
			if loc, wt, ok := find(b.Name, labels[1], labels[0]); ok {
				hit = append(hit, b.Name, loc, wt)
			}
		}
		switch len(hit) {
		case 3:
			return hit[0], hit[1], hit[2], nil
		case 0:
		default:
			return "", "", "", &proxy.LoginError{Status: http.StatusConflict, Msg: "more than one box has " + strings.Join(labels, ".") + "; name the box: " + strings.Join(labels, ".") + ".BOX.localhost"}
		}
	}
	return "", "", "", notFound
}
