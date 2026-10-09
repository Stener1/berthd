package prreview

import (
	"strings"
	"testing"
)

func TestALinkCanAskToOpenLoggedInAtAPage(t *testing.T) {
	for in, want := range map[string]Ref{
		"berth://review?repo=acme/web&pr=7&as=pro@acme.test":                               {Owner: "acme", Name: "web", PR: 7, As: "pro@acme.test"},
		"berth://review?repo=acme/web&pr=7&as=pro%40acme.test&path=%2Fevent-types":         {Owner: "acme", Name: "web", PR: 7, As: "pro@acme.test", Path: "/event-types"},
		"berth://review?repo=acme/web&pr=7&path=/settings/billing":                         {Owner: "acme", Name: "web", PR: 7, Path: "/settings/billing"},
		"berth://review?repo=acme/web&pr=7&path=/orders%3Ftab%3D2":                         {Owner: "acme", Name: "web", PR: 7, Path: "/orders?tab=2"},
		"berth://review?repo=acme/web&pr=7&as=team.lead%2Bqa@acme.test&path=/":             {Owner: "acme", Name: "web", PR: 7, As: "team.lead+qa@acme.test", Path: "/"},
		"berth://review?repo=acme/web&pr=7&sha=4e1c9a2&as=pro@acme.test&path=/event-types": {Owner: "acme", Name: "web", PR: 7, SHA: "4e1c9a2", As: "pro@acme.test", Path: "/event-types"},
	} {
		got, err := ParseLink(in)
		if err != nil || got != want {
			t.Errorf("ParseLink(%q) = %+v, %v; want %+v", in, got, err, want)
			continue
		}
		// The link it makes reads back the same.
		if back, err := ParseLink(got.ShareLink()); err != nil || back.As != got.As || back.Path != got.Path {
			t.Errorf("%q → %q → %+v, %v", in, got.ShareLink(), back, err)
		}
	}
	if l := LinkFor("acme/web", 7, "team.lead+qa@acme.test", "/orders?tab=2"); l != "berth://review?repo=acme/web&pr=7&as=team.lead%2Bqa@acme.test&path=/orders%3Ftab%3D2" {
		t.Errorf("LinkFor = %q", l)
	}
	if l := (Ref{Owner: "acme", Name: "web", PR: 7, As: "pro@acme.test"}).Link(); l != "berth://review?repo=acme/web&pr=7" {
		t.Errorf("Link keeps the default without them: %q", l)
	}
}

func TestAsAndPathRefuseInjection(t *testing.T) {
	base := "berth://review?repo=acme/web&pr=7&"
	for _, q := range []string{
		"as=a@b.c;rm",
		"as=a@b.c%3Brm%20-rf",
		"as=a@b.c%0Aecho",
		"as=a@b.c%0D%0Aecho",
		"as=-x@acme.test",
		"as=pro",
		"as=pro@acme",
		"as=pro@acme.test&as=admin@acme.test",
		"as=",
		"as=" + strings.Repeat("a", 250) + "@acme.test",
		"as=pro@acme.test%00",
		"as=%60id%60@acme.test",
		"path=//evil.example",
		"path=%2F%2Fevil.example",
		"path=/%2F%2Fevil.example",
		"path=/%252F%252Fevil.example",
		"path=https://evil.example",
		"path=https%3A%2F%2Fevil.example",
		"path=/x://evil",
		"path=event-types",
		"path=/%5Cevil.example",
		"path=/a%0Ab",
		"path=/a%09b",
		"path=/a%20b",
		"path=/x&path=/y",
		"path=",
		"path=/" + strings.Repeat("a", 512),
		"path=/x#frag",
		"as=pro@acme.test#x",
		"login=pro@acme.test",
	} {
		if r, err := ParseLink(base + q); err == nil {
			t.Errorf("ParseLink(…&%s) = %+v; want a refusal", q, r)
		}
	}
}

func TestLoginCheckOpensAnywayWhenNotAllowed(t *testing.T) {
	users := &Login{Users: []string{"pro@acme.test", "admin@acme.test"}}
	if ok, why := LoginCheck("acme/web", "PRO@acme.test", users); !ok || why != "" {
		t.Errorf("listed: %v %q", ok, why)
	}
	if ok, why := LoginCheck("acme/web", "free@acme.test", users); ok || why != "free@acme.test isn't one of acme/web's login users, so it opens without logging in" {
		t.Errorf("not listed: %v %q", ok, why)
	}
	if ok, _ := LoginCheck("acme/web", "anyone@acme.test", &Login{Any: true}); !ok {
		t.Error("any")
	}
	if ok, why := LoginCheck("acme/web", "pro@acme.test", nil); ok || why != "acme/web has no login set up on this box, so it opens without logging in" {
		t.Errorf("no login: %v %q", ok, why)
	}
	if ok, _ := LoginCheck("acme/web", "pro@acme.test", &Login{}); ok {
		t.Error("an empty login")
	}
}
