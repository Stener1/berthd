package box

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"

	"github.com/cosscom/shipyard/internal/statefile"
)

// AgentDefaults is how an agent starts when whatever starts it names no
// driver: another agent through the berth tools, the CLI, a run or an
// automation. The app names one with every start of its own, and keeps
// these in step with the person's Labs setting so the rest follow it too.
type AgentDefaults struct {
	// Structured starts Claude Code, Codex and an agent whose preset speaks
	// ACP structured, where this box can (driver.claude and the rest).
	Structured bool `json:"structured"`
	// Mode is the permission mode they start in: "bypassPermissions" has
	// berthd allow what they ask, "default" (or none) brings each to the
	// person.
	Mode string `json:"mode,omitempty"`
}

// AgentDefaultsStore keeps the box's AgentDefaults in a file of its state.
type AgentDefaultsStore struct {
	Path string
	mu   sync.Mutex
}

// Get is what was saved; nothing saved, or no store, is today's way:
// agents run in their terminals.
func (s *AgentDefaultsStore) Get() AgentDefaults {
	var d AgentDefaults
	if s == nil || s.Path == "" {
		return d
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if raw, err := os.ReadFile(s.Path); err == nil {
		json.Unmarshal(raw, &d)
	}
	return d
}

// Set saves d; the next agent to start follows it.
func (s *AgentDefaultsStore) Set(d AgentDefaults) error {
	if s == nil || s.Path == "" {
		return httpError{http.StatusNotImplemented, "this box keeps no agent defaults"}
	}
	switch d.Mode {
	case "", "default", "bypassPermissions":
	default:
		return badRequest("mode must be default or bypassPermissions")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.MarshalIndent(d, "", "  ")
	return statefile.Write(s.Path, append(raw, '\n'))
}

func (b *Box) getAgentDefaults(w http.ResponseWriter, r *http.Request) error {
	if b.AgentDefaults == nil {
		return httpError{http.StatusNotFound, "this box keeps no agent defaults"}
	}
	writeJSON(w, b.AgentDefaults.Get())
	return nil
}

func (b *Box) putAgentDefaults(w http.ResponseWriter, r *http.Request) error {
	if b.AgentDefaults == nil {
		return httpError{http.StatusNotFound, "this box keeps no agent defaults"}
	}
	var d AgentDefaults
	if err := decode(r, &d); err != nil {
		return err
	}
	if err := b.AgentDefaults.Set(d); err != nil {
		return err
	}
	return b.getAgentDefaults(w, r)
}

// structuredKind is the structured driver preset p runs under, the same
// choice the app makes (structured.ts): Claude Code and Codex their own,
// an agent whose preset says how it speaks ACP that; "" for the rest.
func structuredKind(p AgentPreset) string {
	switch {
	case p.ID == "claude":
		return DriverClaude
	case p.ID == "codex":
		return DriverCodex
	case p.ACP != "":
		return DriverACP
	}
	return ""
}

// startDriver is the structured driver an agent from preset p starts
// under ("" for its terminal) and the mode it starts in. A request that
// names a driver gets it, "terminal" included; one that names none gets
// the box's default, its own mode winning over the default's.
func (b *Box) startDriver(p AgentPreset, driver, mode string) (string, string, error) {
	if driver != "" {
		kind, err := wantStructured(driver)
		return kind, mode, err
	}
	d := b.AgentDefaults.Get()
	if !d.Structured || b.ConversationsDir == "" {
		return "", mode, nil
	}
	kind := structuredKind(p)
	if kind == "" {
		return "", mode, nil
	}
	if mode == "" {
		// As the app starts them: ACP agents have modes of their own, so
		// they get bypass or theirs as they start.
		switch {
		case d.Mode == "bypassPermissions":
			mode = d.Mode
		case kind != DriverACP:
			mode = "default"
		}
	}
	return kind, mode, nil
}
