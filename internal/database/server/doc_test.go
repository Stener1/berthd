package server

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/database"
)

// API.md is what the Database tab is written from, by someone who has not
// read this code. So it is held to the code: every route the server serves
// has its section there, it has a section for no route the server does not
// serve, the numbers it quotes are the code's, and the interface it tells
// the author of a new engine to implement is the one there is.
func TestTheDocumentHasEveryRoute(t *testing.T) {
	raw, err := os.ReadFile("../API.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	srv, err := New(Options{Registry: database.NewRegistry(), Token: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, served := srv.handler()

	var described []string
	for _, m := range regexp.MustCompile(`(?m)^### ((?:GET|POST|DELETE) /\S+)$`).FindAllStringSubmatch(doc, -1) {
		described = append(described, m[1])
	}
	for _, pattern := range served {
		if !slices.Contains(described, pattern) {
			t.Errorf("API.md has no section for %s", pattern)
		}
		if !strings.Contains(doc, "| [`"+pattern+"`](#") {
			t.Errorf("API.md's table of routes has no row for %s", pattern)
		}
	}
	for _, pattern := range described {
		if !slices.Contains(served, pattern) {
			t.Errorf("API.md describes %s, which the server does not serve", pattern)
		}
	}
	if len(served) != 9 {
		t.Errorf("%d routes were found: %v", len(served), served)
	}

	// The numbers it quotes are the code's.
	for _, want := range []string{
		"`api` is this\nAPI's own number, `1`",
		fmt.Sprintf("| `limit` | %d (1 to %d) | How many rows.", database.DefaultRows, database.MaxRows),
		fmt.Sprintf("| `limit` | %d (1 to %d) | The most rows to send back.", database.DefaultQueryRows, database.MaxQueryRows),
		"`rows` gives at most 500 rows, `query` at most 5,000; a cell at\n  most 4,000 characters; and an answer's rows about 8 MB together",
		"**A cell longer than 4,000 characters is cut**",
		"| `changes` | 1 to 1,000 changes, applied in order. |",
		"turns them on for one connection for 15 minutes",
		"`--statement-timeout` (10 seconds)",
		"| `sql` | required | One statement, up to 1 MB.",
		"at most 4 MB (`/api/attach`, `/api/detach` and\n  `/writes`: 256 KB)",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("API.md no longer says %q", want)
		}
	}
	if Version != 1 || database.MaxRows != 500 || database.MaxQueryRows != 5000 || database.MaxCell != 4000 || database.MaxBytes != 8<<20 ||
		database.MaxChanges != 1000 || WritesFor != 15*time.Minute || database.DefaultTimeout != 10*time.Second ||
		database.MaxSQL != 1<<20 || maxBody != 4<<20 || maxAttachBody != 256<<10 {
		t.Error("a limit changed: say so in API.md and here")
	}

	// What a new engine is told to implement is what there is to implement.
	for _, iface := range []reflect.Type{reflect.TypeOf((*database.Engine)(nil)).Elem(), reflect.TypeOf((*database.Conn)(nil)).Elem()} {
		for i := 0; i < iface.NumMethod(); i++ {
			if name := iface.Method(i).Name; !strings.Contains(doc, "\t"+name+"(") {
				t.Errorf("API.md's %s has no %s", iface.Name(), name)
			}
		}
	}
	caps := reflect.TypeOf(database.Capabilities{})
	for i := 0; i < caps.NumField(); i++ {
		if field := strings.Split(caps.Field(i).Tag.Get("json"), ",")[0]; !strings.Contains(doc, "| `"+field+"` |") {
			t.Errorf("API.md does not describe the capability %s", field)
		}
	}
	// Every code the server or an engine answers with is in the table.
	for _, code := range []string{"bad_request", "multiple_statements", "no_primary_key", "query_failed", "timeout", "too_large",
		"unauthorized", "forbidden", "read_only", "not_found", "row_conflict", "db_unavailable", "shutting_down", "not_saved", "failed"} {
		if !regexp.MustCompile("(?m)^\\| .*`" + code + "`.* \\| \\d").MatchString(doc) {
			t.Errorf("API.md's table of errors has no %s", code)
		}
	}
	// And it holds nothing of this machine's, nor a real token.
	for _, never := range []string{"sekrit", "/Users/", "/private/"} {
		if strings.Contains(doc, never) {
			t.Errorf("API.md holds %q", never)
		}
	}
}
