package database

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
)

// Problem is an error a person can act on, and the status it answers with.
// Every error of the API is one: {"error": {"code", "message", "hint"}}.
type Problem struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	// What the database said of a statement it refused, when it said.
	// Detail can quote a row's values: it goes to whoever asked, never to
	// the log.
	SQLState string `json:"sqlstate,omitempty"`
	Position int    `json:"position,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

func (p *Problem) Error() string { return p.Message }

// Bad is a 400: the request cannot be read, or asks for what is not allowed.
func Bad(format string, args ...any) *Problem {
	return &Problem{Status: http.StatusBadRequest, Code: "bad_request", Message: fmt.Sprintf(format, args...)}
}

// NotFound is a 404.
func NotFound(format string, args ...any) *Problem {
	return &Problem{Status: http.StatusNotFound, Code: "not_found", Message: fmt.Sprintf(format, args...)}
}

// ReadOnly is the 403 of a write while writes are off for the connection.
func ReadOnly() *Problem {
	return &Problem{Status: http.StatusForbidden, Code: "read_only",
		Message: "Writes are off for this connection.", Hint: "Turn writes on; they stay on for 15 minutes."}
}

// Conflict is the 409 of an edit whose row is not there as the editor saw
// it. Nothing of the batch was applied.
func Conflict(format string, args ...any) *Problem {
	return &Problem{Status: http.StatusConflict, Code: "row_conflict", Message: fmt.Sprintf(format, args...),
		Hint: "Nothing was changed. Load the rows again and repeat the edit."}
}

// Unavailable is a database that could not be reached or would not let the
// editor in. why must not hold a password or a URL.
func Unavailable(why string) *Problem {
	return &Problem{Status: http.StatusServiceUnavailable, Code: "db_unavailable",
		Message: "The database could not be opened: " + why, Hint: "Check that the worktree's database is running."}
}

// ---- what Schema answers ----

type Schema struct {
	Schemas []SchemaInfo `json:"schemas"`
}

type SchemaInfo struct {
	Name   string  `json:"name"`
	Tables []Table `json:"tables"`
}

// The kinds of table.
const (
	KindTable            = "table"
	KindView             = "view"
	KindMaterializedView = "materialized_view"
)

type Table struct {
	Name         string        `json:"name"`
	Kind         string        `json:"kind"`
	RowsEstimate int64         `json:"rows_estimate"`
	Columns      []TableColumn `json:"columns"`
	PrimaryKey   []string      `json:"primary_key"`
	Indexes      []Index       `json:"indexes"`
	ForeignKeys  []ForeignKey  `json:"foreign_keys"`
}

type TableColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	// Default is the column's default as the database writes it; left out
	// for a column with none.
	Default    *string `json:"default,omitempty"`
	PrimaryKey bool    `json:"primary_key,omitempty"`
}

type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

type ForeignKey struct {
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
}

// ---- rows ----

// The filter operators an engine may offer.
var Operators = []string{"=", "!=", "<", "<=", ">", ">=", "like", "ilike", "is_null", "not_null"}

const (
	DefaultRows = 100
	MaxRows     = 500
	// MaxFilters and MaxOrder bound a request: nobody filters a table on a
	// hundred columns by hand.
	MaxFilters = 50
	MaxOrder   = 20
)

type Order struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc"`
}

type Filter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	// Value is nil when left out, and when null: neither compares.
	Value *Value `json:"value"`
}

// Unary reports whether an operator takes no value.
func Unary(op string) bool { return op == "is_null" || op == "not_null" }

type RowsRequest struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
	OrderBy []Order  `json:"order_by"`
	Filters []Filter `json:"filters"`
}

// Check makes the request one an engine can take as it is: the limit in
// range, the operators ones the engine offers, every name there.
func (in *RowsRequest) Check(caps Capabilities) *Problem {
	if p := checkTable(in.Schema, in.Table, caps); p != nil {
		return p
	}
	switch {
	case in.Limit == 0:
		in.Limit = DefaultRows
	case in.Limit < 0:
		return Bad("limit is not a number of rows.")
	case in.Limit > MaxRows:
		in.Limit = MaxRows
	}
	if in.Offset < 0 {
		return Bad("offset is not a number of rows.")
	}
	if len(in.OrderBy) > MaxOrder || len(in.Filters) > MaxFilters {
		return Bad("There are too many filters or sort columns.")
	}
	for _, o := range in.OrderBy {
		if o.Column == "" {
			return Bad("An order_by entry has no column.")
		}
	}
	for _, f := range in.Filters {
		if f.Column == "" {
			return Bad("A filter has no column.")
		}
		if !slices.Contains(caps.Operators, f.Op) {
			return Bad("%q is not a filter operator this database takes.", clip(f.Op))
		}
		if Unary(f.Op) != (f.Value == nil) {
			if Unary(f.Op) {
				return Bad("%s takes no value.", f.Op)
			}
			return Bad("The filter on %q needs a value.", clip(f.Column)).Hinted("To find rows without one, use is_null.")
		}
	}
	return nil
}

// Hinted adds what to try.
func (p *Problem) Hinted(h string) *Problem { p.Hint = h; return p }

func checkTable(schema, table string, caps Capabilities) *Problem {
	if table == "" {
		return Bad("table is required.")
	}
	if schema == "" && caps.Schemas {
		return Bad("schema is required.").Hinted(`It is the schema's name, as in "public".`)
	}
	return nil
}

// Column is a column of a result.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type RowsResult struct {
	Columns      []Column `json:"columns"`
	Rows         [][]any  `json:"rows"`
	PrimaryKey   []string `json:"primary_key"`
	RowsEstimate int64    `json:"rows_estimate"`
	// More says rows follow these: ask again with offset + len(rows).
	More bool `json:"more"`
	// Cut lists the cells that were shortened, as [row, column].
	Cut [][2]int `json:"cut"`
}

// ---- query ----

const (
	DefaultQueryRows = 1000
	MaxQueryRows     = 5000
	// MaxSQL is the longest statement taken.
	MaxSQL = 1 << 20
)

type QueryRequest struct {
	SQL   string `json:"sql"`
	Limit int    `json:"limit"`
	// Write is the server's to set, from whether writes are on for the
	// connection. It is never read from a request.
	Write bool `json:"-"`
}

func (in *QueryRequest) Check() *Problem {
	if strings.TrimSpace(in.SQL) == "" {
		return Bad("sql is required.")
	}
	if len(in.SQL) > MaxSQL {
		return Bad("The statement is too long.")
	}
	switch {
	case in.Limit == 0:
		in.Limit = DefaultQueryRows
	case in.Limit < 0:
		return Bad("limit is not a number of rows.")
	case in.Limit > MaxQueryRows:
		in.Limit = MaxQueryRows
	}
	return nil
}

type QueryResult struct {
	Columns []Column `json:"columns"`
	Rows    [][]any  `json:"rows"`
	// RowCount is the rows the statement returned, or for one that returns
	// none the rows it changed, as the database counted them; of a result
	// that was cut short, the rows here.
	RowCount int64 `json:"row_count"`
	// Command is what ran: "SELECT", "UPDATE", "CREATE TABLE".
	Command    string  `json:"command"`
	DurationMs float64 `json:"duration_ms"`
	// Truncated says the statement had more rows than are here.
	Truncated bool     `json:"truncated"`
	Notices   []string `json:"notices"`
	Cut       [][2]int `json:"cut"`
}

// ---- edit ----

// MaxChanges is the most changes one edit carries.
const MaxChanges = 1000

type Change struct {
	// Op is "update", "insert" or "delete".
	Op  string           `json:"op"`
	Key map[string]Value `json:"key"`
	Set map[string]Value `json:"set"`
}

type EditRequest struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Changes []Change `json:"changes"`
}

func (in *EditRequest) Check(caps Capabilities) *Problem {
	if p := checkTable(in.Schema, in.Table, caps); p != nil {
		return p
	}
	if len(in.Changes) == 0 {
		return Bad("changes is empty.")
	}
	if len(in.Changes) > MaxChanges {
		return Bad("An edit carries at most %d changes.", MaxChanges)
	}
	for i, c := range in.Changes {
		at := fmt.Sprintf("Change %d", i+1)
		switch c.Op {
		case "update":
			if len(c.Key) == 0 || len(c.Set) == 0 {
				return Bad("%s: an update needs a key and a set.", at)
			}
		case "insert":
			if len(c.Key) != 0 {
				return Bad("%s: an insert takes a set and no key.", at)
			}
		case "delete":
			if len(c.Key) == 0 || len(c.Set) != 0 {
				return Bad("%s: a delete needs a key and takes no set.", at)
			}
		default:
			return Bad(`%s: op is "update", "insert" or "delete".`, at)
		}
		for col, v := range c.Key {
			if col == "" || v.Null {
				return Bad("%s: a key names every column of the primary key, and none of them is null.", at)
			}
		}
		for col := range c.Set {
			if col == "" {
				return Bad("%s: a column has no name.", at)
			}
		}
	}
	return nil
}

// Shape is an edit as the log shows it: the table, how many of each change,
// and the columns written. No value is in it.
func (in *EditRequest) Shape(quote func(string) string) (table string, columns []string) {
	table = quote(in.Table)
	if in.Schema != "" {
		table = quote(in.Schema) + "." + table
	}
	seen := map[string]bool{}
	for _, c := range in.Changes {
		for col := range c.Set {
			if !seen[col] {
				seen[col] = true
				columns = append(columns, quote(col))
			}
		}
	}
	sort.Strings(columns)
	return table, columns
}

type EditResult struct {
	Updated  int `json:"updated"`
	Inserted int `json:"inserted"`
	Deleted  int `json:"deleted"`
}

// Names is a map's keys in one order every time, so the SQL made from a
// change is the same SQL for the same change.
func Names(m map[string]Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- values in ----

// Value is one value of a request: SQL NULL, or text the database casts to
// the column's type. A JSON string is its characters; a number is its
// digits as they were written, so none are lost on the way; true and false
// are those words; an object or an array is its JSON, for a json column.
type Value struct {
	Null bool
	Text string
}

func (v *Value) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("a value is missing")
	}
	*v = Value{}
	switch raw[0] {
	case 'n':
		v.Null = true
	case '"':
		return json.Unmarshal(raw, &v.Text)
	case '{', '[':
		var b bytes.Buffer
		if err := json.Compact(&b, raw); err != nil {
			return err
		}
		v.Text = b.String()
	default:
		// A number, true or false: as written.
		v.Text = string(raw)
	}
	return nil
}

func (v Value) MarshalJSON() ([]byte, error) {
	if v.Null {
		return []byte("null"), nil
	}
	return json.Marshal(v.Text)
}

// clip shortens what a request said before it is said back.
func clip(s string) string {
	if len(s) > 80 {
		return strings.ToValidUTF8(s[:80], "") + "…"
	}
	return s
}
