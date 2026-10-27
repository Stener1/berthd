package api

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/leadline/store"
)

// API.md is what a UI is written from, by someone who has not read this
// code. So it is held to the code: every route the server serves has its
// section there, and it has a section for no route the server does not
// serve.
func TestTheDocumentHasEveryRoute(t *testing.T) {
	raw, err := os.ReadFile("../API.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	_, served := New(nil, nil).handler()

	var described []string
	for _, m := range regexp.MustCompile(`(?m)^### ((?:GET|POST|DELETE) /\S+)$`).FindAllStringSubmatch(doc, -1) {
		described = append(described, m[1])
	}
	for _, pattern := range served {
		if strings.Contains(pattern, " /v1/") {
			// The OTLP routes share one section.
			if !strings.Contains(doc, "\n"+pattern+"\n") {
				t.Errorf("API.md does not show %s", pattern)
			}
			continue
		}
		if !slices.Contains(described, pattern) {
			t.Errorf("API.md has no section for %s", pattern)
		}
	}
	for _, pattern := range described {
		if !slices.Contains(served, pattern) {
			t.Errorf("API.md describes %s, which the server does not serve", pattern)
		}
	}
	if len(served) < 20 {
		t.Errorf("only %d routes were found: %v", len(served), served)
	}
	// And the numbers it quotes are the code's.
	for _, want := range []string{
		"`api` is this API's own number, `1`",
		"**The N+1 rule** is one number, 5",
		"`by` | required | `db_time`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("API.md no longer says %q", want)
		}
	}
	if Version != 1 || store.NPlusOne != 5 || store.Groupings() != "db_calls, db_time, service, service_time, status" {
		t.Errorf("the API's version, the N+1 rule or the groupings changed (%d, %d, %s): say so in API.md and here",
			Version, store.NPlusOne, store.Groupings())
	}
}
