package team

import (
	"os"
	"strings"
	"testing"
)

func TestParseCalcomExample(t *testing.T) {
	b, err := os.ReadFile("../../examples/team/calcom-dot-berth/team.json")
	if err != nil {
		t.Fatal(err)
	}
	s, warnings, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings on the example: %v", warnings)
	}
	if s.ID != "calcom" || s.Org != "calcom" || len(s.Box.Steps) != 7 || len(s.Projects) != 2 {
		t.Fatalf("parsed %+v", s)
	}
	if s.SudoSteps() != 3 {
		t.Fatalf("sudo steps = %d", s.SudoSteps())
	}
	if !s.NotifyUpdates() {
		t.Fatal("updates should notify by default and here")
	}
	p, _ := s.Project("private-api")
	if p.ProjectPath() != "~/code/private-api" {
		t.Fatal(p.ProjectPath())
	}
	k, err := ParseKitRef(p.Kit)
	if err != nil || k.Path != "projects/private-api" {
		t.Fatalf("kit ref %+v %v", k, err)
	}
	cal, _ := s.Project("cal.com")
	k, err = ParseKitRef(cal.Kit)
	if err != nil || k.Owner != "sean-brydon" || k.Name != "berth-kit-calcom" || k.Ref != "7f93797" || !k.IsCommit() {
		t.Fatalf("kit ref %+v %v", k, err)
	}
	if k.String() != cal.Kit {
		t.Fatalf("round trip %q", k.String())
	}
}

const minimal = `{"schema":"berth.team/v1","id":"acme","name":"Acme","org":"acme",
 "box":{"script":"box/setup.sh","steps":[{"id":"tools","title":"Tools","sudo":true},{"id":"db","title":"Database"}]},
 "projects":[{"id":"web","repo":"acme/web","kit":"./kits/web"},{"id":"api","repo":"acme/api","path":"~/src/api","required":true}],
 "keys":{"web":{"from":".env.example","shared":{"STRIPE_KEY":"op://Dev/Stripe/key"},"ask":["MAIL_KEY"]}}}`

func TestParseRejects(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{`"berth.team/v1"`, `"berth.team/v2"`, "this Berth reads"},
		{`"id":"acme"`, `"id":"Acme Co"`, "lowercase"},
		{`"name":"Acme"`, `"name":" "`, "name is empty"},
		{`"org":"acme"`, `"org":"ac me"`, "not a GitHub org"},
		{`"script":"box/setup.sh"`, `"script":"../setup.sh"`, "inside .berth"},
		{`"script":"box/setup.sh"`, `"script":"/usr/bin/setup.sh"`, "inside .berth"},
		{`"id":"db"`, `"id":"tools"`, "twice"},
		{`"id":"db"`, `"id":"check"`, "reserved"},
		{`"id":"db"`, `"id":"github"`, "reserved"},
		{`"title":"Database"`, `"title":""`, "no title"},
		{`"repo":"acme/api"`, `"repo":"api"`, "owner/name"},
		{`"repo":"acme/api"`, `"repo":"acme/web"`, "twice"},
		{`"path":"~/src/api"`, `"path":"src/api"`, "must start with"},
		{`"path":"~/src/api"`, `"path":"~/../etc"`, "not a folder"},
		{`"kit":"./kits/web"`, `"kit":"../kits/web"`, "inside .berth"},
		{`"kit":"./kits/web"`, `"kit":"https://github.com/acme/kit"`, "not pinned"},
		{`"op://Dev/Stripe/key"`, `"sk_live_123"`, "never a value"},
		{`"ask":["MAIL_KEY"]`, `"ask":["STRIPE_KEY"]`, "both shared and asked"},
		{`"keys":{"web"`, `"keys":{"nope"`, "not one of the projects"},
	}
	for _, c := range cases {
		doc := strings.Replace(minimal, c.from, c.to, 1)
		if doc == minimal {
			t.Fatalf("case %q did not change the document", c.from)
		}
		_, _, err := Parse([]byte(doc))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s → %s: got %v, want %q", c.from, c.to, err, c.want)
		}
	}
	if _, _, err := Parse([]byte(minimal)); err != nil {
		t.Fatal(err)
	}
}

func TestParseWarnsOnUnknownFields(t *testing.T) {
	doc := strings.Replace(minimal, `"name":"Acme"`, `"name":"Acme","github":{"require_member":true},"$note":"fine"`, 1)
	doc = strings.Replace(doc, `"title":"Tools"`, `"title":"Tools","check":"x"`, 1)
	_, warnings, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(warnings, "\n")
	if len(warnings) != 2 || !strings.Contains(joined, `"github"`) || !strings.Contains(joined, `box.steps[0]: "check"`) {
		t.Fatalf("warnings %v", warnings)
	}
}

func TestParseKitRef(t *testing.T) {
	cases := map[string]KitRef{
		"./kits/web":                                    {Path: "kits/web"},
		"kits/web":                                      {Path: "kits/web"},
		"https://github.com/o/kit@abc1234":              {Owner: "o", Name: "kit", Ref: "abc1234", Link: "https://github.com/o/kit"},
		"github.com/o/kit@v2":                           {Owner: "o", Name: "kit", Ref: "v2", Link: "https://github.com/o/kit"},
		"https://github.com/o/kits/tree/abc1234/shop":   {Owner: "o", Name: "kits", Ref: "abc1234", Sub: "shop", Link: "https://github.com/o/kits/tree/abc1234/shop"},
		"https://gitlab.com/o/kit@0123456789abcdef0123": {Link: "https://gitlab.com/o/kit", Ref: "0123456789abcdef0123"},
	}
	for in, want := range cases {
		got, err := ParseKitRef(in)
		if err != nil || got != want {
			t.Errorf("%s: got %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"https://github.com/o/kit", "https://example.com/kit.json", "../x", "/abs/kit"} {
		if _, err := ParseKitRef(bad); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

func TestEnvKeys(t *testing.T) {
	got := EnvKeys([]byte("# comment\nA=1\nexport B=\"x\"\n\nA=2\nnot a line\nlower_ok=\n9BAD=1\n"))
	if strings.Join(got, ",") != "A,B,lower_ok" {
		t.Fatal(got)
	}
}

func TestDiff(t *testing.T) {
	old, _, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	newDoc := strings.Replace(minimal, `{"id":"db","title":"Database"}`, `{"id":"db","title":"Database 16","sudo":true},{"id":"node","title":"Node 22"}`, 1)
	newDoc = strings.Replace(newDoc, `{"id":"tools","title":"Tools","sudo":true},`, ``, 1)
	newDoc = strings.Replace(newDoc, `"required":true}`, `"required":true},{"id":"video","repo":"acme/video"}`, 1)
	newDoc = strings.Replace(newDoc, `"kit":"./kits/web"`, `"kit":"./kits/web2"`, 1)
	newDoc = strings.Replace(newDoc, `"ask":["MAIL_KEY"]`, `"ask":["MAIL_KEY","SMS_KEY"]`, 1)
	newDoc = strings.Replace(newDoc, `"op://Dev/Stripe/key"`, `"op://Dev/Stripe/key","DAILY":"op://Dev/Daily/key"`, 1)
	nw, _, err := Parse([]byte(newDoc))
	if err != nil {
		t.Fatal(err)
	}
	changes := Diff(old, nw,
		map[string]string{"box/setup.sh": "a\nb\n", "team.json": "x", "old.sh": "x"},
		map[string]string{"box/setup.sh": "a\nc\nd\n", "team.json": "y", "kits/web2/kit.json": "{}"})
	var lines []string
	for _, c := range changes {
		lines = append(lines, c.Kind+" "+c.Area+" "+c.ID+" | "+c.Detail)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{
		"change step db | Database → Database 16 · now asks for your password",
		"add step node | ",
		"remove step tools | ",
		"add project video | new repo",
		"change project web | kit ./kits/web → ./kits/web2",
		"add key web/DAILY | from 1Password: op://Dev/Daily/key",
		"add key web/SMS_KEY | yours to enter, once",
		"change file box/setup.sh | +2 −1 lines",
		"add file kits/web2/kit.json | ",
		"remove file old.sh | ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "team.json") {
		t.Errorf("team.json itself is reported through its fields, not as a file:\n%s", got)
	}
	if s := NewSudo(changes); len(s) != 1 || s[0] != "Database 16" {
		t.Fatalf("new sudo = %v", s)
	}
	if len(Diff(old, old, nil, nil)) != 0 {
		t.Fatal("a setup differs from itself")
	}
}
