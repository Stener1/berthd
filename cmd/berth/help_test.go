package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/agent"
	"github.com/cosscom/shipyard/internal/usagecheck"
)

// Every flag a command defines is in its usage error and in berth help (or
// berth kit help, berth team help), and every flag the help offers exists.
func TestHelpMatchesFlags(t *testing.T) {
	cmds, err := usagecheck.Commands(".", usagecheck.Dir("internal/boxcmd"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) < 20 {
		t.Fatalf("found only %d commands with flags; is the scan still finding them?", len(cmds))
	}
	for _, p := range usagecheck.Problems("berth", helpText()+"\n"+kitUsage+"\n"+teamUsage+"\n"+reviewUsage, cmds, nil) {
		t.Error(p)
	}
}

func TestEventsForOneBox(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	evs := []agent.Event{
		{Type: "box.connected", Box: "devl", Time: at},
		{Type: "box.connected", Box: "devlx", Time: at},
		{Type: "laptop.started", Time: at},
		{Type: "forward.started", Box: "devl", Time: at, Data: map[string]any{"local": 3000, "remote": 3000}},
	}
	var all, one, oneJSON bytes.Buffer
	for _, e := range evs {
		eventPrinter(&all, "", false)(e)
		eventPrinter(&one, "devl", false)(e)
		eventPrinter(&oneJSON, "devl", true)(e)
	}
	if n := strings.Count(all.String(), "\n"); n != 4 {
		t.Errorf("without a box, printed %d events; want all 4:\n%s", n, all.String())
	}
	if got := one.String(); strings.Count(got, "\n") != 2 || strings.Contains(got, "devlx") || strings.Contains(got, "laptop.started") || !strings.Contains(got, "localhost:3000 → 3000") {
		t.Errorf("berth events devl printed:\n%s\nwant devl's two events only", got)
	}
	if got := oneJSON.String(); strings.Count(got, "\n") != 2 || strings.Contains(got, `"devlx"`) {
		t.Errorf("berth events devl --json printed:\n%s", got)
	}
}
