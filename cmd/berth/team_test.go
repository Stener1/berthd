package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/box"
)

func TestTeamSetupUseFlags(t *testing.T) {
	got, err := parseUse([]string{"web=~/work/acme-web", " api = /srv/acme-api "}, []string{"docs"})
	want := map[string]string{"web": "~/work/acme-web", "api": "/srv/acme-api", "docs": ""}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseUse = %v %v; want %v", got, err, want)
	}
	if got, err := parseUse(nil, nil); err != nil || got != nil {
		t.Fatalf("no flags: %v %v", got, err)
	}
	for _, bad := range [][2][]string{
		{{"web"}, nil},
		{{"web="}, nil},
		{{"web=work/acme-web"}, nil},
		{{"web=~/a", "web=~/b"}, nil},
		{{"web=~/a"}, {"web"}},
	} {
		if _, err := parseUse(bad[0], bad[1]); err == nil {
			t.Errorf("parseUse(%v, %v) was accepted", bad[0], bad[1])
		}
	}
	line := cloneLine(box.ExistingClone{Display: "~/work/acme-web", Branch: "feat/x", Dirty: 3, Behind: 2})
	if line != "~/work/acme-web (on feat/x, 3 uncommitted changes, 2 behind)" {
		t.Fatalf("cloneLine = %q", line)
	}
	if line := cloneLine(box.ExistingClone{Display: "~/code/web", Branch: "main", Location: "web"}); !strings.Contains(line, "clean") || !strings.Contains(line, "Shipyard project web") {
		t.Fatalf("cloneLine = %q", line)
	}
}
