package database

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/leadline"
)

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestIntegersAreNumbersWhileADoubleHoldsThem(t *testing.T) {
	for text, want := range map[string]string{
		"0": `0`, "-1": `-1`, "42": `42`,
		"9007199254740992": `9007199254740992`, "-9007199254740992": `-9007199254740992`,
		"9007199254740993": `"9007199254740993"`, "-9007199254740993": `"-9007199254740993"`,
		"9223372036854775807": `"9223372036854775807"`, "18446744073709551615": `"18446744073709551615"`,
		"12x": `"12x"`, "": `""`,
	} {
		if got := js(Int(text)); got != want {
			t.Errorf("Int(%q) = %s, want %s", text, got, want)
		}
	}
	for text, want := range map[string]string{
		"1.5": `1.5`, "-0.25": `-0.25`, "1e+300": `1e+300`, "3": `3`,
		"NaN": `"NaN"`, "Infinity": `"Infinity"`, "-Infinity": `"-Infinity"`, "1e999": `"1e999"`, "abc": `"abc"`,
	} {
		if got := js(Float(text)); got != want {
			t.Errorf("Float(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestAGridCutsLongCellsAndFillsUp(t *testing.T) {
	g := NewGrid()
	if js(g.Rows) != `[]` || js(g.Cut) != `[]` {
		t.Fatalf("an empty grid is %s, %s", js(g.Rows), js(g.Cut))
	}
	exactly := strings.Repeat("é", MaxCell)
	g.Add([]any{int64(1), exactly, nil, true})
	g.Add([]any{exactly + "é", "short", strings.Repeat("x", MaxCell+1), 1.5})
	if js(g.Cut) != `[[1,0],[1,2]]` {
		t.Fatalf("cut = %s", js(g.Cut))
	}
	if g.Rows[0][1] != exactly || g.Rows[1][0] != exactly || g.Rows[1][2] != strings.Repeat("x", MaxCell) {
		t.Error("a cell was cut in the wrong place")
	}
	// It is characters that are counted, and a character is never halved.
	if s, cut := CutCell(strings.Repeat("日", MaxCell+3)); !cut || len([]rune(s)) != MaxCell || !strings.HasSuffix(s, "日") {
		t.Errorf("a cut of wide characters has %d of them", len([]rune(s)))
	}
	if s, cut := CutCell(strings.Repeat("x", MaxCell)); cut || len(s) != MaxCell {
		t.Error("a cell of exactly the most was cut")
	}

	small := NewGrid()
	small.Max = 100
	rows := 0
	for small.Add([]any{strings.Repeat("x", 20)}) {
		rows++
	}
	if rows < 2 || rows > 5 || len(small.Rows) != rows+1 {
		t.Errorf("a grid of 100 bytes took %d rows", len(small.Rows))
	}
}

func TestAValueIsNullOrTextTheDatabaseCasts(t *testing.T) {
	for in, want := range map[string]Value{
		`null`:                        {Null: true},
		`"text"`:                      {Text: "text"},
		`""`:                          {Text: ""},
		`"null"`:                      {Text: "null"},
		`"line\nbreak \"q\" é"`:       {Text: "line\nbreak \"q\" é"},
		`42`:                          {Text: "42"},
		`-0.50`:                       {Text: "-0.50"},
		`12345678901234567890123.456`: {Text: "12345678901234567890123.456"},
		`1e3`:                         {Text: "1e3"},
		`true`:                        {Text: "true"},
		`false`:                       {Text: "false"},
		`{"a": [1, 2], "b": null}`:    {Text: `{"a":[1,2],"b":null}`},
		`[1, "two"]`:                  {Text: `[1,"two"]`},
	} {
		var got Value
		if err := json.Unmarshal([]byte(in), &got); err != nil || got != want {
			t.Errorf("%s = %+v, %v; want %+v", in, got, err, want)
		}
	}
	// In a map a null is a value that is NULL; a filter's is no value.
	var change Change
	var filter Filter
	json.Unmarshal([]byte(`{"op": "update", "set": {"a": null, "b": 1}}`), &change)
	json.Unmarshal([]byte(`{"column": "a", "op": "=", "value": null}`), &filter)
	if !change.Set["a"].Null || change.Set["b"].Text != "1" || filter.Value != nil {
		t.Errorf("nulls: %+v, %+v", change, filter)
	}
	if js(Value{Null: true}) != `null` || js(Value{Text: "x"}) != `"x"` {
		t.Error("a value does not write back as it was read")
	}
}

func TestAnEditsShapeHasNoValueInIt(t *testing.T) {
	var in EditRequest
	json.Unmarshal([]byte(`{"schema": "public", "table": "Order Items", "changes": [
		{"op": "update", "key": {"id": "k3y"}, "set": {"note": "s3cret", "Qty": 4}},
		{"op": "insert", "set": {"note": "an0ther"}}]}`), &in)
	table, columns := in.Shape(func(s string) string { return `"` + s + `"` })
	if table != `"public"."Order Items"` || js(columns) != `["\"Qty\"","\"note\""]` {
		t.Errorf("shape = %s, %s", table, js(columns))
	}
	if p := in.Check(Capabilities{Schemas: true}); p != nil {
		t.Error(p)
	}
	in.Schema = ""
	if p := in.Check(Capabilities{Schemas: true}); p == nil {
		t.Error("an edit with no schema was taken by an engine that has schemas")
	}
	if p := in.Check(Capabilities{}); p != nil {
		t.Errorf("an engine with no schemas: %v", p)
	}
	in.Changes = make([]Change, MaxChanges+1)
	if p := in.Check(Capabilities{}); p == nil {
		t.Error("an edit of too many changes was taken")
	}
}

func TestEnginesAreFoundByAURLsScheme(t *testing.T) {
	for url, want := range map[string]string{
		"postgres://app@localhost/acme":           "postgres",
		"PostgreSQL://app@localhost/acme":         "postgresql",
		"postgresql+psycopg://app@localhost/acme": "postgresql",
		"jdbc:postgresql://localhost/acme":        "postgresql",
		"  mysql://app@localhost/acme":            "mysql",
		"mongodb+srv://app@localhost/acme":        "mongodb",
		"localhost:5432":                          "",
		"://nothing":                              "",
		"not a url":                               "",
		"bad scheme://x":                          "",
		"":                                        "",
	} {
		if got, ok := Scheme(url); got != want || ok != (want != "") {
			t.Errorf("Scheme(%q) = %q, %v", url, got, ok)
		}
	}
}

// The name a worktree is listed under is the one Shipyard's other helpers
// use for it: a tab that shows both must be able to ask each for the same
// worktree by the same name.
func TestASourceIsNamedAsLeadlineNamesIt(t *testing.T) {
	for _, tc := range [][2]string{
		{"acme", "fix-orders"}, {"acme", ""}, {"acme", "acme"}, {"", "fix-orders"}, {"", ""},
		{"acme shop", "feat/fix:orders"}, {" acme ", "\tfix\\orders\n"}, {"a/b", "c:d"}, {"--acme--", "::x::"},
		{strings.Repeat("p", 80), strings.Repeat("é", 50)}, {"acme", "fix-orders-2"}, {"ACME", "Fix Orders"},
	} {
		want := leadline.Worktree{Project: tc[0], Name: tc[1]}.Service()
		if got := Source(tc[0], tc[1]); got != want {
			t.Errorf("Source(%q, %q) = %q, Leadline says %q", tc[0], tc[1], got, want)
		}
	}
}

func TestOnlyThisMachineIsLocal(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "db.localhost": true, "127.0.0.1": true, "127.8.9.1": true, "::1": true, "0.0.0.0": true, "::": true,
		"": false, "localhost.acme.example": false, "db.acme.example": false, "10.0.0.5": false, "192.168.1.9": false,
		"169.254.169.254": false, "host.docker.internal": false, "2001:db8::1": false, "localhostx": false,
	} {
		if got := LocalHost(host); got != want {
			t.Errorf("LocalHost(%q) = %v", host, got)
		}
		if got := (Target{Host: host}).Local(); got != want {
			t.Errorf("Target{%q}.Local() = %v", host, got)
		}
	}
	if !(Target{Host: "/var/run/postgresql", Socket: true}).Local() {
		t.Error("a unix socket is not local")
	}
}
