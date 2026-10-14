package box

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// berth.chart/v1 is a chart an agent writes as JSON (never as code); the
// app draws it with bklit UI's charts in the person's theme. The box
// checks it on every version so the agent hears what's wrong in the same
// turn, in words it can act on. The app's mapping is lib/art/chart-spec.ts.

const (
	maxChartRows   = 5000
	maxChartSeries = 12
	maxSankeyNodes = 100
)

var (
	chartTypes  = []string{"bar", "line", "area", "funnel", "sankey", "gauge", "pie", "ring", "heatmap"}
	chartColors = []string{"1", "2", "3", "4", "5", "good", "bad", "warn", "muted"}
	chartFields = map[string]bool{
		"$schema": true, "type": true, "title": true, "subtitle": true, "data": true, "x": true, "y": true,
		"units": true, "better": true, "stacked": true, "orientation": true, "value": true, "max": true,
		"label": true, "nodes": true, "links": true, "row": true, "z": true, "notes": true, "color": true,
	}
)

type chartProblem struct{ msgs []string }

func (p *chartProblem) add(format string, args ...any) {
	if len(p.msgs) < 8 {
		p.msgs = append(p.msgs, fmt.Sprintf(format, args...))
	}
}

// ValidateChart checks a berth.chart/v1 spec, with every problem it finds
// (up to eight) in its error.
func ValidateChart(content []byte) error {
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(content))
	if err := dec.Decode(&raw); err != nil {
		return badRequest("berth.chart: the JSON doesn't parse: %s", jsonProblem(err))
	}
	p := &chartProblem{}
	var unknown []string
	for k := range raw {
		if !chartFields[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		p.add("%q isn't a berth.chart/v1 field", k)
	}
	str := func(k string, max int) string {
		v, ok := raw[k]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			p.add("%s must be a string", k)
			return ""
		}
		if len([]rune(s)) > max {
			p.add("%s is longer than %d characters", k, max)
		}
		return s
	}
	if str("$schema", 40) != "berth.chart/v1" {
		p.add(`"$schema" must be "berth.chart/v1"`)
	}
	typ := str("type", 20)
	if !slices.Contains(chartTypes, typ) {
		p.add("type must be one of %s (got %q)", strings.Join(chartTypes, ", "), typ)
	}
	str("title", 200)
	str("subtitle", 300)
	str("units", 16)
	str("label", 80)
	if b := str("better", 10); b != "" && b != "lower" && b != "higher" {
		p.add(`better is "lower" or "higher"`)
	}
	if o := str("orientation", 12); o != "" && o != "vertical" && o != "horizontal" {
		p.add(`orientation is "vertical" or "horizontal"`)
	}
	if c := str("color", 8); c != "" && !slices.Contains(chartColors, c) {
		p.add("color %q isn't one of %s (colours are names, never hex)", c, strings.Join(chartColors, ", "))
	}
	if v, ok := raw["stacked"]; ok {
		var b bool
		if json.Unmarshal(v, &b) != nil {
			p.add("stacked is true or false")
		}
	}
	if v, ok := raw["notes"]; ok {
		var notes []string
		if json.Unmarshal(v, &notes) != nil {
			p.add("notes is a list of strings")
		} else if len(notes) > 10 {
			p.add("notes may have 10 lines")
		}
	}

	var rows []map[string]any
	if v, ok := raw["data"]; ok {
		if err := json.Unmarshal(v, &rows); err != nil {
			p.add("data is a list of objects, one per row")
		} else if len(rows) > maxChartRows {
			p.add("data may have %d rows; this has %d. Aggregate first", maxChartRows, len(rows))
			rows = nil
		}
	}
	needRows := func() bool {
		if len(rows) == 0 {
			if _, ok := raw["data"]; !ok {
				p.add("a %s chart needs data: a list of rows", typ)
			} else {
				p.add("data is empty")
			}
			return false
		}
		return true
	}
	key := func(k string, def string) string {
		s := str(k, 64)
		if s == "" {
			return def
		}
		return s
	}
	// rowsHave checks every row has k, as a number when num.
	rowsHave := func(k, role string, num bool) {
		for i, r := range rows {
			v, ok := r[k]
			if !ok {
				p.add("data[%d] has no %q (the %s)", i, k, role)
				return
			}
			if num {
				if _, isNum := v.(float64); !isNum && v != nil {
					p.add("data[%d].%s is %s, not a number", i, k, chartValue(v))
					return
				}
			} else {
				switch v.(type) {
				case string, float64:
				default:
					p.add("data[%d].%s is %s; a %s is a string or a number", i, k, chartValue(v), role)
					return
				}
			}
		}
	}

	switch typ {
	case "bar", "line", "area":
		x := str("x", 64)
		if x == "" {
			p.add(`a %s chart needs "x": the key of each row's category or time`, typ)
		}
		ys := chartSeries(raw["y"], p)
		if len(ys) == 0 {
			p.add(`a %s chart needs "y": the value keys to draw, as names or {"key", "label", "color"}`, typ)
		}
		if len(ys) > maxChartSeries {
			p.add("y may have %d series", maxChartSeries)
		}
		if needRows() {
			if x != "" {
				rowsHave(x, "x value", false)
			}
			for _, y := range ys {
				rowsHave(y, "value", true)
			}
		}
	case "funnel", "pie", "ring":
		if needRows() {
			rowsHave(key("x", "label"), "label", false)
			rowsHave(key("z", "value"), "value", true)
		}
	case "heatmap":
		x, row, z := str("x", 64), str("row", 64), str("z", 64)
		if x == "" || row == "" || z == "" {
			p.add(`a heatmap needs "x" (the column key), "row" (the row key) and "z" (the value key)`)
		}
		if needRows() && x != "" && row != "" && z != "" {
			rowsHave(x, "column", false)
			rowsHave(row, "row", false)
			rowsHave(z, "value", true)
		}
	case "gauge":
		var val, max *float64
		if v, ok := raw["value"]; !ok || json.Unmarshal(v, &val) != nil || val == nil {
			p.add("a gauge needs value, a number")
		}
		if v, ok := raw["max"]; ok && (json.Unmarshal(v, &max) != nil || max == nil || *max <= 0) {
			p.add("max is a number above 0")
		}
		m := 100.0
		if max != nil && *max > 0 {
			m = *max
		}
		if val != nil && (*val < 0 || *val > m) {
			p.add("value %g is outside 0–%g", *val, m)
		}
	case "sankey":
		checkSankey(raw, p)
	}
	if len(p.msgs) > 0 {
		return badRequest("berth.chart: %s", strings.Join(p.msgs, "; "))
	}
	return nil
}

// chartSeries is y's keys.
func chartSeries(v json.RawMessage, p *chartProblem) []string {
	if len(v) == 0 {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(v, &items) != nil {
		p.add(`y is a list of value keys`)
		return nil
	}
	var keys []string
	for i, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil {
			keys = append(keys, s)
			continue
		}
		var o struct {
			Key   string `json:"key"`
			Label string `json:"label"`
			Color string `json:"color"`
		}
		if json.Unmarshal(it, &o) != nil || o.Key == "" {
			p.add(`y[%d] is a key, or {"key", "label", "color"}`, i)
			continue
		}
		if o.Color != "" && !slices.Contains(chartColors, o.Color) {
			p.add("y[%d].color %q isn't one of %s (colours are names, never hex)", i, o.Color, strings.Join(chartColors, ", "))
		}
		keys = append(keys, o.Key)
	}
	return keys
}

func checkSankey(raw map[string]json.RawMessage, p *chartProblem) {
	var nodes []struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	var links []struct {
		Source string   `json:"source"`
		Target string   `json:"target"`
		Value  *float64 `json:"value"`
	}
	if v, ok := raw["nodes"]; !ok || json.Unmarshal(v, &nodes) != nil || len(nodes) == 0 {
		p.add(`a sankey needs nodes: [{"name": "…"}]`)
		return
	}
	if v, ok := raw["links"]; !ok || json.Unmarshal(v, &links) != nil || len(links) == 0 {
		p.add(`a sankey needs links: [{"source": "…", "target": "…", "value": 1}]`)
		return
	}
	if len(nodes) > maxSankeyNodes {
		p.add("a sankey may have %d nodes", maxSankeyNodes)
	}
	names := map[string]bool{}
	for i, n := range nodes {
		switch {
		case n.Name == "":
			p.add("nodes[%d] has no name", i)
		case names[n.Name]:
			p.add("two nodes are named %q", n.Name)
		}
		if n.Color != "" && !slices.Contains(chartColors, n.Color) {
			p.add("nodes[%d].color %q isn't one of %s", i, n.Color, strings.Join(chartColors, ", "))
		}
		names[n.Name] = true
	}
	next := map[string][]string{}
	for i, l := range links {
		if !names[l.Source] {
			p.add("links[%d].source %q isn't a node's name", i, l.Source)
		}
		if !names[l.Target] {
			p.add("links[%d].target %q isn't a node's name", i, l.Target)
		}
		if l.Source == l.Target {
			p.add("links[%d] goes from %q to itself", i, l.Source)
		}
		if l.Value == nil || *l.Value <= 0 {
			p.add("links[%d].value must be a number above 0", i)
		}
		next[l.Source] = append(next[l.Source], l.Target)
	}
	// A sankey flows one way: a cycle can't be laid out.
	state := map[string]int{}
	var visit func(n string) bool
	visit = func(n string) bool {
		switch state[n] {
		case 1:
			return true
		case 2:
			return false
		}
		state[n] = 1
		for _, m := range next[n] {
			if visit(m) {
				return true
			}
		}
		state[n] = 2
		return false
	}
	for _, n := range nodes {
		if visit(n.Name) {
			p.add("the links loop back (through %q); a sankey flows one way", n.Name)
			return
		}
	}
}

func chartValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		if len(t) > 24 {
			t = t[:24] + "…"
		}
		return fmt.Sprintf("%q", t)
	case bool:
		return fmt.Sprint(t)
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	}
	return fmt.Sprint(v)
}

// Secrets. Shipyard has no secret scanner of its own elsewhere (the Team
// setup refuses a shared key that isn't a reference, op:// or env://, and
// doctor reports redact by pattern), so artifacts get a simple pattern
// check of their own: well-known token formats, private keys, and
// .env-style assignments of a secret-sounding name to a long value. It
// errs towards refusing; the agent hears what matched and can leave it
// out.
var secretPatterns = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a private key", regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`)},
	{"an AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"a GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})\b`)},
	{"a GitLab token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}\b`)},
	{"an Anthropic or OpenAI key", regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{32,}\b`)},
	{"a Stripe key", regexp.MustCompile(`\b[rs]k_live_[A-Za-z0-9]{20,}\b`)},
	{"a Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"an npm token", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`)},
	{"a JSON web token", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"a password in a URL", regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^/\s:@"'<>]+:[^/\s@"'<>]{6,}@`)},
	{"a secret in an .env-style line", regexp.MustCompile(`(?im)^\s*(?:export\s+)?[A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSWD|API_?KEY|PRIVATE_?KEY|ACCESS_?KEY)[A-Z0-9_]*\s*=\s*['"]?[^\s'"$]{12,}`)},
}

// privateKeyBlock is a whole private key, header to footer (or to the end
// of a text that was cut off), for RedactSecrets: the header alone is
// enough to refuse an artifact, not to take the key out of a text.
var privateKeyBlock = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----(?s:.*?)(?:-----END (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----|\z)`)

// RedactSecrets replaces what LooksLikeSecret finds in s with what kind of
// secret it was, for text that leaves the box (turn check).
func RedactSecrets(s string) string {
	s = privateKeyBlock.ReplaceAllLiteralString(s, "[redacted: a private key]")
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllLiteralString(s, "[redacted: "+p.what+"]")
	}
	return s
}

// LooksLikeSecret names the kind of secret content seems to hold, or "".
func LooksLikeSecret(content []byte) string {
	for _, p := range secretPatterns {
		if p.re.Match(content) {
			return p.what
		}
	}
	return ""
}
