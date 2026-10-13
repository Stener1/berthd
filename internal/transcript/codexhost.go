package transcript

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// SourceCodexHost is a structured Codex session's own log, kept by berthd
// agent-host: each line an entry whose data is one JSON-RPC message of
// `codex app-server` (its notifications and requests out, berthd's
// requests and answers in). Its items read as a Codex session file's.
const SourceCodexHost = "codex-host"

// codexRPC is one JSON-RPC message of the app server.
type codexRPC struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

// codexThreadItem is the part of an app-server item read here.
type codexThreadItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Text    string `json:"text"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Command        string `json:"command"`
	CommandActions []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Path    string `json:"path"`
		Name    string `json:"name"`
	} `json:"commandActions"`
	Changes []struct {
		Path string `json:"path"`
		Diff string `json:"diff"`
	} `json:"changes"`
	Server string `json:"server"`
	Tool   string `json:"tool"`
}

// codexHostParser reads an agent-host log of `codex app-server`.
type codexHostParser struct {
	// asks are the questions asked by request id, while unanswered.
	asks map[string]string
}

// shellWrapped is a command as Codex runs it: `/bin/zsh -lc 'cmd'`.
var shellWrapped = regexp.MustCompile(`^\S*sh -l?c '(.*)'$`)

func (p *codexHostParser) line(c *conv, b []byte) {
	var e hostEntry
	if json.Unmarshal(b, &e) != nil || len(e.Data) == 0 || e.Data[0] != '{' {
		return
	}
	var m codexRPC
	if json.Unmarshal(e.Data, &m) != nil {
		return
	}
	at := e.At
	if at != 0 {
		c.lineAt = at
	}
	if e.Dir == "in" {
		// berthd's answer to a question Codex asked.
		if m.Method == "" && len(m.ID) > 0 && len(m.Result) > 0 {
			if item, ok := p.asks[string(m.ID)]; ok {
				delete(p.asks, string(m.ID))
				c.codexAnswered(item, string(m.Result))
			}
		}
		return
	}
	if e.Dir != "out" {
		return
	}
	switch m.Method {
	case "item/started", "item/completed":
		var params struct {
			Item codexThreadItem `json:"item"`
		}
		if json.Unmarshal(m.Params, &params) != nil {
			return
		}
		codexHostItem(c, params.Item, m.Method == "item/completed", at)
	case "item/tool/requestUserInput":
		var params struct {
			ItemID    string          `json:"itemId"`
			Questions json.RawMessage `json:"questions"`
		}
		if json.Unmarshal(m.Params, &params) != nil {
			return
		}
		var qs any
		json.Unmarshal(params.Questions, &qs)
		id := firstNonEmpty(params.ItemID, "ask-"+string(m.ID))
		if c.asked(id, qs) {
			if p.asks == nil {
				p.asks = map[string]string{}
			}
			p.asks[string(m.ID)] = id
		}
	}
}

// codexHostItem reads one item as it starts or completes.
func codexHostItem(c *conv, it codexThreadItem, done bool, at int64) {
	switch it.Type {
	case "userMessage":
		if done {
			return // read as it started
		}
		var parts []string
		for _, p := range it.Content {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
			userText(c, text)
		}
	case "agentMessage":
		if done && strings.TrimSpace(it.Text) != "" {
			c.add(Item{Kind: "text", ID: c.id(), Text: clip(strings.TrimSpace(it.Text), maxText)})
		}
	case "commandExecution":
		if done {
			c.result(it.ID, at)
			return
		}
		cmd := it.Command
		if m := shellWrapped.FindStringSubmatch(cmd); m != nil {
			cmd = strings.ReplaceAll(m[1], `'\''`, `'`)
		}
		if len(it.CommandActions) == 1 {
			a := it.CommandActions[0]
			switch a.Type {
			case "read":
				c.call(it.ID, ToolCall{Verb: "Read", Target: filepath.Base(firstNonEmpty(a.Path, a.Name)), File: true})
				return
			case "search", "listFiles":
				c.call(it.ID, ToolCall{Verb: "Search", Target: clip(firstLine(firstNonEmpty(a.Command, cmd)), 80)})
				return
			}
		}
		c.call(it.ID, ToolCall{Verb: "Run", Target: clip(firstLine(cmd), 80)})
	case "fileChange":
		if !done {
			return
		}
		for _, ch := range it.Changes {
			added, removed := diffCounts(ch.Diff)
			c.add(Item{Kind: "edit", ID: c.id(), File: rel(c.dir, ch.Path), Added: added, Removed: removed, Tool: it.ID})
		}
	case "mcpToolCall", "dynamicToolCall":
		if done {
			c.result(it.ID, at)
			return
		}
		c.call(it.ID, ToolCall{Verb: "Run", Target: clip(strings.Trim(it.Server+"."+it.Tool, "."), 80)})
	}
}

// diffCounts counts a unified diff's added and removed lines.
func diffCounts(diff string) (added, removed int) {
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		case strings.HasPrefix(l, "+"):
			added++
		case strings.HasPrefix(l, "-"):
			removed++
		}
	}
	return added, removed
}

// codexHostDetail reads one of the app server's items for its details: a
// command and its output, or a file change's diff.
func codexHostDetail(line []byte, id, dir string, d *ToolDetail) (call, result bool) {
	var e hostEntry
	if json.Unmarshal(line, &e) != nil || e.Dir != "out" {
		return
	}
	var m codexRPC
	if json.Unmarshal(e.Data, &m) != nil || (m.Method != "item/started" && m.Method != "item/completed") {
		return
	}
	var params struct {
		Item struct {
			codexThreadItem
			AggregatedOutput *string `json:"aggregatedOutput"`
			ExitCode         *int    `json:"exitCode"`
			Status           string  `json:"status"`
		} `json:"item"`
	}
	if json.Unmarshal(m.Params, &params) != nil || params.Item.ID != id {
		return
	}
	it := params.Item
	switch it.Type {
	case "commandExecution":
		call = true
		d.Name = "shell"
		d.Command = it.Command
		if mm := shellWrapped.FindStringSubmatch(it.Command); mm != nil {
			d.Command = strings.ReplaceAll(mm[1], `'\''`, `'`)
		}
		if m.Method == "item/completed" {
			result = true
			if it.AggregatedOutput != nil {
				d.Output = *it.AggregatedOutput
			}
			d.Error = (it.ExitCode != nil && *it.ExitCode != 0) || it.Status == "declined" || it.Status == "failed"
		}
	case "fileChange":
		call, result = true, m.Method == "item/completed"
		d.Name = "apply_patch"
		var diffs []string
		for _, ch := range it.Changes {
			if d.File == "" {
				d.File = rel(dir, ch.Path)
			}
			diffs = append(diffs, ch.Diff)
		}
		d.New = strings.Join(diffs, "\n")
	case "mcpToolCall", "dynamicToolCall":
		call, result = true, m.Method == "item/completed"
		d.Name = strings.Trim(it.Server+"."+it.Tool, ".")
	}
	return
}
