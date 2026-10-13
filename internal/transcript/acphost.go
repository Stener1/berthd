package transcript

import (
	"encoding/json"
	"strings"
)

// SourceACPHost is a structured agent's own log when the agent speaks the
// Agent Client Protocol (opencode, Gemini CLI, Cursor Agent…), kept by
// berthd agent-host: berthd's prompts in, the agent's session updates out.
const SourceACPHost = "acp-host"

// acpHostParser reads an agent-host log of an ACP agent. A reply streams
// as chunks: they are gathered and kept as one item once something else
// happens (a tool, the turn's end, the next prompt).
type acpHostParser struct {
	text    strings.Builder
	textOff int64
	// loading is the session/load being answered: what the agent replays
	// for it is already in the log.
	loading string
	prompts map[string]bool
}

type acpUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Content       struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	RawInput   struct {
		Command  string `json:"command"`
		FilePath string `json:"filePath"`
		Path     string `json:"path"`
	} `json:"rawInput"`
	Locations []struct {
		Path string `json:"path"`
	} `json:"locations"`
}

func (p *acpHostParser) flush(c *conv) {
	if t := strings.TrimSpace(p.text.String()); t != "" {
		off := c.lineOff
		c.lineOff = p.textOff
		c.add(Item{Kind: "text", ID: c.id(), Text: clip(t, maxText)})
		c.lineOff = off
	}
	p.text.Reset()
}

func (p *acpHostParser) line(c *conv, b []byte) {
	var e hostEntry
	if json.Unmarshal(b, &e) != nil || len(e.Data) == 0 || e.Data[0] != '{' {
		return
	}
	var m codexRPC
	if json.Unmarshal(e.Data, &m) != nil {
		return
	}
	if e.At != 0 {
		c.lineAt = e.At
	}
	id := string(m.ID)
	switch e.Dir {
	case "in":
		switch m.Method {
		case "session/prompt":
			var params struct {
				Prompt []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"prompt"`
			}
			json.Unmarshal(m.Params, &params)
			var parts []string
			for _, b := range params.Prompt {
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			p.flush(c)
			if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
				userText(c, text)
			}
			if p.prompts == nil {
				p.prompts = map[string]bool{}
			}
			p.prompts[id] = true
		case "session/load":
			p.loading = id
		}
		return
	case "out":
	default:
		return
	}
	if m.Method == "" {
		// An answer: a prompt's is its turn's end; a load's ends its replay.
		if id == p.loading {
			p.loading = ""
		}
		if p.prompts[id] {
			delete(p.prompts, id)
			p.flush(c)
		}
		return
	}
	if m.Method != "session/update" || p.loading != "" {
		return
	}
	var params struct {
		Update acpUpdate `json:"update"`
	}
	if json.Unmarshal(m.Params, &params) != nil {
		return
	}
	u := params.Update
	switch u.SessionUpdate {
	case "agent_message_chunk":
		if p.text.Len() == 0 {
			p.textOff = c.lineOff
		}
		if p.text.Len() < maxText {
			p.text.WriteString(u.Content.Text)
		}
	case "tool_call":
		p.flush(c)
		c.call(u.ToolCallID, acpToolCall(c, u))
		if u.Status == "completed" || u.Status == "failed" {
			c.result(u.ToolCallID, e.At)
		}
	case "tool_call_update":
		// A call announced bare ("bash") says what it runs as it goes.
		if u.RawInput.Command != "" || u.RawInput.FilePath != "" || u.RawInput.Path != "" || len(u.Locations) > 0 {
			c.retitle(u.ToolCallID, acpToolCall(c, u))
		}
		if u.Status == "completed" || u.Status == "failed" {
			c.result(u.ToolCallID, e.At)
		}
	}
}

// acpToolCall is a tool call as a step: its kind says what it does.
func acpToolCall(c *conv, u acpUpdate) ToolCall {
	target := firstNonEmpty(u.RawInput.Command, u.Title)
	path := firstNonEmpty(u.RawInput.FilePath, u.RawInput.Path)
	if path == "" && len(u.Locations) > 0 {
		path = u.Locations[0].Path
	}
	switch u.Kind {
	case "read":
		if path != "" {
			return ToolCall{Verb: "Read", Target: rel(c.dir, path), File: true}
		}
		return ToolCall{Verb: "Read", Target: clip(firstLine(target), 80)}
	case "edit", "delete", "move":
		if path != "" {
			return ToolCall{Verb: "Edit", Target: rel(c.dir, path), File: true}
		}
		return ToolCall{Verb: "Edit", Target: clip(firstLine(target), 80)}
	case "search":
		return ToolCall{Verb: "Search", Target: clip(firstLine(target), 80)}
	case "fetch":
		return ToolCall{Verb: "Fetch", Target: clip(firstLine(target), 80)}
	}
	return ToolCall{Verb: "Run", Target: clip(firstLine(target), 80)}
}
