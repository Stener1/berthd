package prreview

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseLinkTakesARepositoryAndANumberOnly(t *testing.T) {
	good := map[string]Ref{
		"berth://review?repo=acme/shop&pr=42":                    {Owner: "acme", Name: "shop", PR: 42},
		"berth://review?pr=7&repo=acme/shop.app":                 {Owner: "acme", Name: "shop.app", PR: 7},
		"BERTH://Review?repo=Acme-Co/web_v2&pr=1":                {Owner: "Acme-Co", Name: "web_v2", PR: 1},
		"berth://review/?repo=acme/shop&pr=2147483647":           {Owner: "acme", Name: "shop", PR: 2147483647},
		"berth://review?repo=acme%2Fshop&pr=42":                  {Owner: "acme", Name: "shop", PR: 42},
		"berth://review?repo=acme/shop&pr=42&sha=4E1C9A2b7d0f3e": {Owner: "acme", Name: "shop", PR: 42, SHA: "4e1c9a2b7d0f3e"},
	}
	for in, want := range good {
		got, err := ParseLink(in)
		if err != nil || got != want {
			t.Errorf("ParseLink(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	if l := (Ref{Owner: "acme", Name: "shop", PR: 42, SHA: "abc1234"}).Link(); l != "berth://review?repo=acme/shop&pr=42" {
		t.Errorf("Link = %q; the hint is not part of it", l)
	}
}

func TestParseLinkRefusesJunkAndInjection(t *testing.T) {
	bad := []string{
		"",
		"https://review?repo=acme/shop&pr=42",
		"berth://team?repo=acme/shop&pr=42",
		"berth://review",
		"berth://review?repo=acme/shop",
		"berth://review?pr=42",
		"berth://review?repo=acme&pr=42",
		"berth://review?repo=acme/shop/extra&pr=42",
		"berth://review?repo=../shop&pr=42",
		"berth://review?repo=acme/..&pr=42",
		"berth://review?repo=acme/.&pr=42",
		"berth://review?repo=acme/shop;rm%20-rf%20~&pr=42",
		"berth://review?repo=acme/shop$(id)&pr=42",
		"berth://review?repo=acme/shop%60id%60&pr=42",
		"berth://review?repo=acme/sh op&pr=42",
		"berth://review?repo=acme/shop%0Aecho&pr=42",
		"berth://review?repo=acme/shop&pr=0",
		"berth://review?repo=acme/shop&pr=-3",
		"berth://review?repo=acme/shop&pr=+3",
		"berth://review?repo=acme/shop&pr=042",
		"berth://review?repo=acme/shop&pr=1e3",
		"berth://review?repo=acme/shop&pr=42.0",
		"berth://review?repo=acme/shop&pr=99999999999",
		"berth://review?repo=acme/shop&pr=42&pr=43",
		"berth://review?repo=acme/shop&repo=evil/x&pr=42",
		"berth://review?repo=acme/shop&pr=42&cmd=make",
		"berth://review?repo=acme/shop&pr=42&setup=curl%20evil",
		"berth://review?repo=acme/shop&pr=42&env=FOO%3Dbar",
		"berth://review?repo=acme/shop&pr=42&dir=/etc",
		"berth://review?repo=acme/shop&pr=42&path=../etc",
		"berth://review?repo=acme/shop&pr=42&box=devl",
		"berth://review?repo=acme/shop&pr=42&sha=nothex",
		"berth://review?repo=acme/shop&pr=42&sha=abc",
		"berth://review?repo=acme/shop&pr=42#frag",
		"berth://user@review?repo=acme/shop&pr=42",
		"berth://review/run?repo=acme/shop&pr=42",
		"berth:review?repo=acme/shop&pr=42",
		"berth://review?repo=acme/shop&pr",
		"berth://review?repo=acme/shop&pr=42;x=1",
		"berth://review?repo=acme\\shop&pr=42",
		"berth://review?repo=" + strings.Repeat("a", 40) + "/shop&pr=1",
		"berth://review?repo=acme/shop&pr=42&" + strings.Repeat("x", 600),
	}
	for _, in := range bad {
		if r, err := ParseLink(in); err == nil {
			t.Errorf("ParseLink(%q) = %+v; want a refusal", in, r)
		} else if !errors.Is(err, ErrBadLink) {
			t.Errorf("ParseLink(%q): %v is not ErrBadLink", in, err)
		}
	}
}

func TestParseArgTakesOwnerNameNumberOrALink(t *testing.T) {
	for in, want := range map[string]Ref{
		"acme/shop#42":                        {Owner: "acme", Name: "shop", PR: 42},
		"  acme/shop.app#7 ":                  {Owner: "acme", Name: "shop.app", PR: 7},
		"berth://review?repo=acme/shop&pr=42": {Owner: "acme", Name: "shop", PR: 42},
	} {
		if got, err := ParseArg(in); err != nil || got != want {
			t.Errorf("ParseArg(%q) = %+v, %v", in, got, err)
		}
	}
	for _, in := range []string{"acme/shop", "acme/shop#", "acme/shop#0", "acme/shop#x", "acme#42", "acme/shop/x#42", "acme/shop#42;id", "acme/$(id)#42", "../x#1", "acme/shop#42#43", "https://github.com/acme/shop/pull/42"} {
		if r, err := ParseArg(in); err == nil {
			t.Errorf("ParseArg(%q) = %+v; want a refusal", in, r)
		}
	}
}

const head = "4e1c9a2b7d0f3e58a1c64d2b9f7e0a35c8d1b246"

func member() PR {
	return PR{Number: 42, Title: "Fix checkout rounding", State: "OPEN", Author: "dana-acme", Association: "MEMBER", HeadBranch: "fix-rounding", HeadSHA: head, HeadRepo: "acme/shop", BaseBranch: "main"}
}

func TestAuthorizeLetsMembersCollaboratorsAndOwnersIn(t *testing.T) {
	p := Policy{TeamRepos: []string{"acme/shop"}, Org: "acme"}
	for _, a := range []string{"MEMBER", "OWNER", "COLLABORATOR", "member"} {
		pr := member()
		pr.Association = a
		if v := Authorize("acme/shop", pr, p); !v.Allowed {
			t.Errorf("%s: %+v", a, v)
		}
	}
	// A project already on a box counts as well as one in team.json.
	tools := member()
	tools.HeadRepo = "acme/tools"
	if v := Authorize("acme/tools", tools, Policy{BoxRepos: []string{"Acme/Tools"}}); !v.Allowed {
		t.Errorf("on a box: %+v", v)
	}
}

func TestAuthorizeRefusesOutsidersForksAndOtherRepos(t *testing.T) {
	p := Policy{TeamRepos: []string{"acme/shop"}, Org: "acme"}
	cases := []struct {
		name   string
		repo   string
		change func(*PR)
		code   string
		reason string
	}{
		{"contributor", "acme/shop", func(pr *PR) { pr.Association = "CONTRIBUTOR" }, CodeOutsider, "Review this one by hand: its author is outside acme"},
		{"first-timer", "acme/shop", func(pr *PR) { pr.Association = "FIRST_TIME_CONTRIBUTOR" }, CodeOutsider, "outside acme"},
		{"none", "acme/shop", func(pr *PR) { pr.Association = "NONE" }, CodeOutsider, "outside acme"},
		{"empty", "acme/shop", func(pr *PR) { pr.Association = "" }, CodeOutsider, "outside acme"},
		{"fork by a member", "acme/shop", func(pr *PR) { pr.Cross, pr.HeadRepo = true, "jo/shop" }, CodeFork, "Review this one by hand: it comes from a fork (jo/shop)"},
		{"fork without the flag", "acme/shop", func(pr *PR) { pr.HeadRepo = "jo/shop" }, CodeFork, "fork"},
		{"not in team.json", "acme/secret-tool", func(*PR) {}, CodeNotAProject, "isn't one of your team's projects"},
		{"merged", "acme/shop", func(pr *PR) { pr.State = "MERGED" }, CodeClosed, "merged"},
		{"closed", "acme/shop", func(pr *PR) { pr.State = "CLOSED" }, CodeClosed, "closed"},
		{"no head", "acme/shop", func(pr *PR) { pr.HeadSHA = "main" }, CodeBadPR, "no head commit"},
	}
	for _, c := range cases {
		pr := member()
		c.change(&pr)
		v := Authorize(c.repo, pr, p)
		if v.Allowed || v.Code != c.code || !strings.Contains(v.Reason, c.reason) {
			t.Errorf("%s: %+v; want %s %q", c.name, v, c.code, c.reason)
		}
	}
	// Without a team, the author must belong to the repository's owner.
	pr := member()
	pr.Association, pr.HeadRepo = "NONE", "acme/tools"
	if v := Authorize("acme/tools", pr, Policy{BoxRepos: []string{"acme/tools"}}); v.Reason != "Review this one by hand: its author is outside acme" {
		t.Errorf("no team: %+v", v)
	}
}

func TestCheckRepoComesBeforeAnythingIsRead(t *testing.T) {
	if v := CheckRepo("evil/x", Policy{TeamRepos: []string{"acme/shop"}, BoxRepos: []string{"acme/web"}}); v.Allowed || v.Code != CodeNotAProject {
		t.Fatalf("%+v", v)
	}
	if v := CheckRepo("acme/web", Policy{BoxRepos: []string{"acme/web"}}); !v.Allowed {
		t.Fatalf("%+v", v)
	}
}

func TestCleanupOnMergedClosedAndIdleKeepsDirtyWorktrees(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	week := DefaultIdle
	cases := []struct {
		name  string
		state string
		last  time.Time
		idle  time.Duration
		dirty bool
		want  Cleanup
	}{
		{"open and used", "OPEN", now.Add(-time.Hour), week, false, Cleanup{}},
		{"merged", "MERGED", now, week, false, Cleanup{Reason: ReasonMerged, Remove: true}},
		{"closed", "CLOSED", now, week, false, Cleanup{Reason: ReasonClosed, Remove: true}},
		{"idle a week", "OPEN", now.Add(-week), week, false, Cleanup{Reason: ReasonIdle, Remove: true}},
		{"idle, state unknown", "", now.Add(-8 * 24 * time.Hour), week, false, Cleanup{Reason: ReasonIdle, Remove: true}},
		{"idle not long enough", "OPEN", now.Add(-6 * 24 * time.Hour), week, false, Cleanup{}},
		{"idle cleanup off", "OPEN", now.Add(-60 * 24 * time.Hour), 0, false, Cleanup{}},
		{"merged with uncommitted changes", "MERGED", now, week, true, Cleanup{Reason: ReasonMerged, Ask: true}},
		{"closed with uncommitted changes", "CLOSED", now, week, true, Cleanup{Reason: ReasonClosed, Ask: true}},
		{"idle with uncommitted changes", "OPEN", now.Add(-week), week, true, Cleanup{Reason: ReasonIdle, Ask: true}},
	}
	for _, c := range cases {
		if got := CleanupDue(c.state, c.last, now, c.idle, c.dirty); got != c.want {
			t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
		}
	}
}

func TestWithheldIsTheReviewersOwnKeysNeverTheShared(t *testing.T) {
	env := map[string]string{"STRIPE_KEY": "op://dev/stripe/key", "MAIL_API_KEY": "SG.mine", "PORT": "3000"}
	got := Withheld(env, []string{"MAIL_API_KEY", "NOT_SET"})
	if len(got) != 1 || got[0] != "MAIL_API_KEY" {
		t.Fatalf("withheld %v", got)
	}
}
