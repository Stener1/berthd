package box

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/sean-brydon/berthd/internal/agentcli"
	"github.com/sean-brydon/berthd/internal/guided"
)

// Adding agent CLIs to a box that is already paired: the app's "Add agents"
// in a box's settings. POST /v1/agents/install runs `berthd agents install
// --integrations --markers <ids>` here, as the box's user, and streams its
// output as NDJSON: {"line": …} for what it prints, {"step": {…}} for each
// agent's marker, then {"done": true} or {"done": true, "error": …}. The
// agents install without sudo, into ~/.local/bin, so nothing needs a
// person at a terminal. GET /v1/agents says which are here.

func (b *Box) listAgentCLIs(w http.ResponseWriter, r *http.Request) error {
	self, err := b.self()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, self, "agents", "list", "--json").Output()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
	return nil
}

func (b *Box) installAgentCLIs(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Agents []string `json:"agents"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ids, err := agentcli.ParseList(strings.Join(req.Agents, ","))
	if err != nil {
		return badRequest("%v", err)
	}
	if len(ids) == 0 {
		return badRequest("no agents to install")
	}
	self, err := b.self()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	send := func(v any) { enc.Encode(v); rc.Flush() }

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, append([]string{"agents", "install", "--integrations", "--markers"}, ids...)...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		send(map[string]any{"done": true, "error": err.Error()})
		return nil
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		send(map[string]any{"done": true, "error": err.Error()})
		return nil
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	// Installers redraw progress with \r; each redraw is a line here.
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, c := range data {
			if c == '\n' || c == '\r' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	var last string
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " ")
		if e, ok := guided.ParseMarker(line); ok {
			send(map[string]any{"step": e})
			continue
		}
		if strings.TrimSpace(line) != "" {
			last = strings.TrimSpace(line)
			send(map[string]any{"line": line})
		}
	}
	if err := cmd.Wait(); err != nil {
		msg := last
		if msg == "" {
			msg = err.Error()
		}
		send(map[string]any{"done": true, "error": msg})
		return nil
	}
	b.publish(r, "agents.installed", map[string]any{"agents": ids})
	send(map[string]any{"done": true})
	return nil
}
