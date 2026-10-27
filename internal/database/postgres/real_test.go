package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cosscom/shipyard/internal/database"
)

// The tests in this file run against a real Postgres, which the usual run
// does not have: they are skipped unless BERTH_DATABASE_TEST_POSTGRES (or
// Leadline's BERTH_LEADLINE_TEST_POSTGRES) holds its URL, as in
// postgres://user:password@127.0.0.1:5432/db. Each makes a schema of its
// own and drops it, so one server serves them all, side by side.

func realURL(t testing.TB) string {
	t.Helper()
	for _, name := range []string{"BERTH_DATABASE_TEST_POSTGRES", "BERTH_LEADLINE_TEST_POSTGRES"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	t.Skip("BERTH_DATABASE_TEST_POSTGRES is not set")
	return ""
}

// rig is the engine opened on the real server, a schema made for the test,
// and a connection of pgx's own to set things up and to check them with:
// what the editor did is never taken on the editor's word.
type rig struct {
	t      *testing.T
	c      *conn
	admin  *pgx.Conn
	schema string
}

func newRig(t *testing.T, timeout time.Duration) *rig {
	t.Helper()
	url := realURL(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connecting to the test Postgres: %v", err)
	}
	b := make([]byte, 4)
	rand.Read(b)
	r := &rig{t: t, admin: admin, schema: "ed_" + hex.EncodeToString(b)}
	r.exec(`CREATE SCHEMA ` + r.schema)
	opened, err := Engine{}.Open(url, database.Options{Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	r.c = opened.(*conn)
	t.Cleanup(func() {
		r.c.Close()
		admin.Exec(ctx, `DROP SCHEMA `+r.schema+` CASCADE`)
		admin.Close(ctx)
	})
	return r
}

func (r *rig) exec(sql string, args ...any) {
	r.t.Helper()
	if _, err := r.admin.Exec(context.Background(), sql, args...); err != nil {
		r.t.Fatalf("%s: %v", sql, err)
	}
}

func (r *rig) count(table string) int {
	r.t.Helper()
	var n int
	if err := r.admin.QueryRow(context.Background(), `SELECT count(*) FROM `+r.schema+`.`+table).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *rig) query(sql string, write bool) (*database.QueryResult, error) {
	in := database.QueryRequest{SQL: sql, Write: write}
	if p := in.Check(); p != nil {
		return nil, p
	}
	return r.c.Query(context.Background(), in)
}

func (r *rig) rows(in database.RowsRequest) *database.RowsResult {
	r.t.Helper()
	in.Schema = r.schema
	if p := in.Check(Engine{}.Capabilities()); p != nil {
		r.t.Fatal(p)
	}
	out, err := r.c.Rows(context.Background(), in)
	if err != nil {
		r.t.Fatalf("rows of %s: %v", in.Table, err)
	}
	return out
}

func (r *rig) edit(table, changes string) (*database.EditResult, error) {
	r.t.Helper()
	in := database.EditRequest{Schema: r.schema, Table: table}
	if err := json.Unmarshal([]byte(changes), &in.Changes); err != nil {
		r.t.Fatal(err)
	}
	if p := in.Check(Engine{}.Capabilities()); p != nil {
		return nil, p
	}
	return r.c.Edit(context.Background(), in)
}

func code(err error) string {
	var p *database.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	if err == nil {
		return ""
	}
	return "not a problem: " + err.Error()
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// record is a row as an object of each cell's JSON, by column.
func record(cols []database.Column, row []any) map[string]string {
	out := map[string]string{}
	for i, c := range cols {
		out[c.Name] = js(row[i])
	}
	return out
}

func TestSchemaListsTablesViewsKeysAndOddNames(t *testing.T) {
	r := newRig(t, 0)
	s := r.schema
	r.exec(`CREATE TABLE ` + s + `.customers (id serial PRIMARY KEY, email text NOT NULL UNIQUE, name varchar(40) DEFAULT 'nobody')`)
	r.exec(`CREATE TABLE ` + s + `."Order Items" (
		"order" int NOT NULL, "Line No" int NOT NULL, "select" text, customer_id int REFERENCES ` + s + `.customers (id),
		qty int GENERATED ALWAYS AS IDENTITY, total numeric(10,2) GENERATED ALWAYS AS ("Line No" * 2.5) STORED,
		PRIMARY KEY ("order", "Line No"))`)
	r.exec(`CREATE TABLE ` + s + `.shipments (id int PRIMARY KEY, o int, l int,
		FOREIGN KEY (o, l) REFERENCES ` + s + `."Order Items" ("order", "Line No"))`)
	r.exec(`CREATE INDEX "by Select" ON ` + s + `."Order Items" ("select", lower("select"))`)
	r.exec(`CREATE TABLE ` + s + `.log (at timestamptz, line text)`)
	r.exec(`CREATE VIEW ` + s + `.customer_names AS SELECT id, name FROM ` + s + `.customers`)
	r.exec(`CREATE MATERIALIZED VIEW ` + s + `.totals AS SELECT count(*) AS n FROM ` + s + `.customers`)
	r.exec(`INSERT INTO ` + s + `.customers (email) VALUES ('a@acme.test'), ('b@acme.test'), ('c@acme.test')`)
	r.exec(`ANALYZE ` + s + `.customers`)

	all, err := r.c.Schema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var mine *database.SchemaInfo
	for i, sc := range all.Schemas {
		if strings.HasPrefix(sc.Name, "pg_") || sc.Name == "information_schema" {
			t.Errorf("a system schema is listed: %s", sc.Name)
		}
		if sc.Name == s {
			mine = &all.Schemas[i]
		}
	}
	if mine == nil {
		t.Fatalf("the schema is not listed: %s", js(all))
	}
	byName := map[string]database.Table{}
	var order []string
	for _, tb := range mine.Tables {
		byName[tb.Name] = tb
		order = append(order, tb.Name+":"+tb.Kind)
	}
	if got, want := strings.Join(order, " "), "Order Items:table customer_names:view customers:table log:table shipments:table totals:materialized_view"; got != want {
		t.Fatalf("tables = %s\nwant     %s", got, want)
	}
	customers := byName["customers"]
	if want := `[{"name":"id","type":"integer","nullable":false,"default":"nextval('` + s + `.customers_id_seq'::regclass)","primary_key":true},` +
		`{"name":"email","type":"text","nullable":false},` +
		`{"name":"name","type":"character varying(40)","nullable":true,"default":"'nobody'::character varying"}]`; js(customers.Columns) != want {
		t.Errorf("customers' columns = %s\nwant %s", js(customers.Columns), want)
	}
	if customers.RowsEstimate != 3 || js(customers.PrimaryKey) != `["id"]` {
		t.Errorf("customers: estimate %d, key %s", customers.RowsEstimate, js(customers.PrimaryKey))
	}
	if want := `[{"name":"customers_pkey","columns":["id"],"unique":true},{"name":"customers_email_key","columns":["email"],"unique":true}]`; js(customers.Indexes) != want {
		t.Errorf("customers' indexes = %s", js(customers.Indexes))
	}
	items := byName["Order Items"]
	if js(items.PrimaryKey) != `["order","Line No"]` {
		t.Errorf("the composite key = %s", js(items.PrimaryKey))
	}
	if want := `[{"name":"Order Items_pkey","columns":["order","Line No"],"unique":true},{"name":"by Select","columns":["select","lower(\"select\")"],"unique":false}]`; js(items.Indexes) != want {
		t.Errorf("its indexes = %s", js(items.Indexes))
	}
	if want := `[{"columns":["customer_id"],"ref_schema":"` + s + `","ref_table":"customers","ref_columns":["id"]}]`; js(items.ForeignKeys) != want {
		t.Errorf("its foreign keys = %s", js(items.ForeignKeys))
	}
	defaults := map[string]string{}
	for _, c := range items.Columns {
		if c.Default != nil {
			defaults[c.Name] = *c.Default
		}
	}
	if defaults["qty"] != "generated always as identity" || defaults["total"] != `generated always as ((("Line No")::numeric * 2.5)) stored` {
		t.Errorf("generated columns say %v", defaults)
	}
	if want := `[{"columns":["o","l"],"ref_schema":"` + s + `","ref_table":"Order Items","ref_columns":["order","Line No"]}]`; js(byName["shipments"].ForeignKeys) != want {
		t.Errorf("the composite foreign key = %s", js(byName["shipments"].ForeignKeys))
	}
	if tb := byName["log"]; js(tb.PrimaryKey) != `[]` || js(tb.Indexes) != `[]` || js(tb.ForeignKeys) != `[]` {
		t.Errorf("a table with no key: %s", js(tb))
	}
	if v := byName["customer_names"]; len(v.Columns) != 2 || js(v.PrimaryKey) != `[]` {
		t.Errorf("the view: %s", js(v))
	}
}

const everyType = `(
	id int PRIMARY KEY, b bool, small int2, i int4, big int8, f4 float4, f8 float8, n numeric(30,10),
	t text, vc varchar(20), ch char(3), ts timestamp, tz timestamptz, d date, tm time, iv interval,
	u uuid, by bytea, j json, jb jsonb, ia int[], ta text[], e %[1]s.mood, ip inet, dom %[1]s.posint)`

func TestEveryTypeIsReadAndWrittenBack(t *testing.T) {
	r := newRig(t, 0)
	s := r.schema
	r.exec(`CREATE TYPE ` + s + `.mood AS ENUM ('happy', 'sad')`)
	r.exec(`CREATE DOMAIN ` + s + `.posint AS int CHECK (VALUE > 0)`)
	r.exec(`CREATE TABLE ` + s + `."Every Type" ` + fmt.Sprintf(everyType, s))
	r.exec(`INSERT INTO ` + s + `."Every Type" VALUES (1, true, 7, -42, 9007199254740993, 0.1, 1e300, 12345678901234567890.1234567890,
		'héllo "w"', 'abc', 'ab', '2026-01-02 03:04:05.123456', '2026-01-02 03:04:05+02', '2026-01-02', '03:04:05', '1 day 2 hours',
		'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', '\xdeadbeef', '{"a": 1}', '{"a": [1, 2]}', '{1,2,3}', '{a,"b c",NULL}', 'happy', '10.0.0.1', 5)`)
	r.exec(`INSERT INTO ` + s + `."Every Type" (id) VALUES (2)`)
	// The zone the server prints in is the session's, which is the
	// database's own: found out, not assumed.
	var zone string
	r.admin.QueryRow(context.Background(), `SELECT current_setting('TimeZone')`).Scan(&zone)
	if zone != "UTC" && zone != "Etc/UTC" && zone != "GMT" {
		t.Skipf("the test database's time zone is %s, not UTC", zone)
	}

	want := map[string]string{
		"id": `1`, "b": `true`, "small": `7`, "i": `-42`, "big": `"9007199254740993"`, "f4": `0.1`, "f8": `1e+300`,
		"n": `"12345678901234567890.1234567890"`, "t": `"héllo \"w\""`, "vc": `"abc"`, "ch": `"ab "`,
		"ts": `"2026-01-02T03:04:05.123456"`, "tz": `"2026-01-02T01:04:05Z"`, "d": `"2026-01-02"`, "tm": `"03:04:05"`,
		"iv": `"1 day 02:00:00"`, "u": `"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"`, "by": `"\\xdeadbeef"`,
		"j": `"{\"a\": 1}"`, "jb": `"{\"a\": [1, 2]}"`, "ia": `"{1,2,3}"`, "ta": `"{a,\"b c\",NULL}"`,
		"e": `"happy"`, "ip": `"10.0.0.1"`, "dom": `5`,
	}
	check := func(what string, cols []database.Column, row []any, want map[string]string) {
		t.Helper()
		got := record(cols, row)
		for name, w := range want {
			if got[name] != w {
				t.Errorf("%s: %s = %s, want %s", what, name, got[name], w)
			}
		}
	}
	page := r.rows(database.RowsRequest{Table: "Every Type"})
	if len(page.Rows) != 2 || page.More || js(page.PrimaryKey) != `["id"]` || js(page.Cut) != `[]` {
		t.Fatalf("the page: %d rows, more %v, key %s, cut %s", len(page.Rows), page.More, js(page.PrimaryKey), js(page.Cut))
	}
	check("rows", page.Columns, page.Rows[0], want)
	for i, cell := range page.Rows[1][1:] {
		if cell != nil {
			t.Errorf("a NULL %s came back as %s", page.Columns[i+1].Name, js(cell))
		}
	}
	types := map[string]string{}
	for _, c := range page.Columns {
		types[c.Name] = c.Type
	}
	for name, typ := range map[string]string{"n": "numeric(30,10)", "tz": "timestamp with time zone", "ia": "integer[]", "vc": "character varying(20)",
		"e": s + ".mood", "dom": s + ".posint", "by": "bytea", "ch": "character(3)"} {
		if types[name] != typ {
			t.Errorf("the type of %s = %q, want %q", name, types[name], typ)
		}
	}

	// A statement reads the same values.
	res, err := r.query(`SELECT * FROM `+s+`."Every Type" WHERE id = 1`, false)
	if err != nil {
		t.Fatal(err)
	}
	check("query", res.Columns, res.Rows[0], want)
	if res.Command != "SELECT" || res.RowCount != 1 || res.Truncated || res.Columns[7].Type != "numeric(30,10)" || res.Columns[24].Type != "integer" {
		t.Errorf("the result: %s %d, truncated %v, types %s", res.Command, res.RowCount, res.Truncated, js(res.Columns))
	}

	// Written: scalars and strings, cast by the server to each column.
	done, err := r.edit("Every Type", `[{"op": "insert", "set": {
		"id": 3, "b": false, "small": "1", "i": 2147483647, "big": 9223372036854775807, "f4": 2.5, "f8": "NaN", "n": 0.0000000010,
		"t": "", "vc": "x'; DROP TABLE a; --", "ch": null, "ts": "2026-10-12T08:30:00", "tz": "2026-10-12T08:30:00.5+05:30",
		"d": "2026-10-12", "tm": "23:59:59", "iv": "90 minutes", "u": "A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A12", "by": "\\x00ff",
		"j": "[1, \"two\"]", "jb": {"k": [1, {"deep": null}]}, "ia": "{4,5}", "ta": "{\"x y\"}", "e": "sad", "ip": "::1", "dom": 9}}]`)
	if err != nil || js(done) != `{"updated":0,"inserted":1,"deleted":0}` {
		t.Fatalf("the insert: %s, %v", js(done), err)
	}
	res, err = r.query(`SELECT * FROM `+s+`."Every Type" WHERE id = 3`, false)
	if err != nil {
		t.Fatal(err)
	}
	check("written back", res.Columns, res.Rows[0], map[string]string{
		"b": `false`, "small": `1`, "i": `2147483647`, "big": `"9223372036854775807"`, "f4": `2.5`, "f8": `"NaN"`, "n": `"0.0000000010"`,
		"t": `""`, "vc": `"x'; DROP TABLE a; --"`, "ch": `null`, "ts": `"2026-10-12T08:30:00"`, "tz": `"2026-10-12T03:00:00.5Z"`,
		"d": `"2026-10-12"`, "tm": `"23:59:59"`, "iv": `"01:30:00"`, "u": `"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12"`, "by": `"\\x00ff"`,
		"j": `"[1, \"two\"]"`, "jb": `"{\"k\": [1, {\"deep\": null}]}"`, "ia": `"{4,5}"`, "ta": `"{\"x y\"}"`, "e": `"sad"`, "ip": `"::1"`, "dom": `9`,
	})
}

func TestAValueThatDoesNotFitItsColumnIsRefusedByTheServer(t *testing.T) {
	r := newRig(t, 0)
	r.exec(`CREATE TABLE ` + r.schema + `.things (id int PRIMARY KEY, name varchar(5), n int)`)
	for _, changes := range []string{
		`[{"op": "insert", "set": {"id": 1, "name": "far too long"}}]`,
		`[{"op": "insert", "set": {"id": 1, "n": "seven"}}]`,
		`[{"op": "insert", "set": {"id": 1}}, {"op": "insert", "set": {"id": 1}}]`,
	} {
		_, err := r.edit("things", changes)
		var p *database.Problem
		if !errors.As(err, &p) || p.Code != "query_failed" || p.SQLState == "" || !strings.HasPrefix(p.Message, "Change ") {
			t.Errorf("%s: %s %v", changes, code(err), err)
		}
	}
	if n := r.count("things"); n != 0 {
		t.Fatalf("%d rows were left by refused edits", n)
	}
}

func TestRowsAreFilteredAndOrdered(t *testing.T) {
	r := newRig(t, 0)
	r.exec(`CREATE TABLE ` + r.schema + `."Stock" (sku text PRIMARY KEY, "Qty" int, "group" text, note text)`)
	r.exec(`INSERT INTO ` + r.schema + `."Stock" VALUES ('a-1', 5, 'bolt', NULL), ('a-2', 50, 'bolt', 'Rusty'), ('b-1', 5, 'nut', 'new'),
		('b-2', 0, 'nut', 'rusty'), ('c-1', 12, 'washer', '100%')`)
	skus := func(in database.RowsRequest) string {
		t.Helper()
		in.Table = "Stock"
		out := r.rows(in)
		var got []string
		for _, row := range out.Rows {
			got = append(got, row[0].(string))
		}
		return strings.Join(got, " ")
	}
	val := func(text string) *database.Value { return &database.Value{Text: text} }
	for _, tc := range []struct {
		name string
		in   database.RowsRequest
		want string
	}{
		{"the key's order, with nothing asked", database.RowsRequest{}, "a-1 a-2 b-1 b-2 c-1"},
		{"equal, cast to the column", database.RowsRequest{Filters: []database.Filter{{Column: "Qty", Op: "=", Value: val("5")}}}, "a-1 b-1"},
		{"not equal", database.RowsRequest{Filters: []database.Filter{{Column: "group", Op: "!=", Value: val("nut")}}}, "a-1 a-2 c-1"},
		{"a range", database.RowsRequest{Filters: []database.Filter{{Column: "Qty", Op: ">", Value: val("0")}, {Column: "Qty", Op: "<=", Value: val("12")}}}, "a-1 b-1 c-1"},
		{"at least and less", database.RowsRequest{Filters: []database.Filter{{Column: "Qty", Op: ">=", Value: val("12")}, {Column: "Qty", Op: "<", Value: val("50")}}}, "c-1"},
		{"like minds its case", database.RowsRequest{Filters: []database.Filter{{Column: "note", Op: "like", Value: val("rust%")}}}, "b-2"},
		{"ilike does not", database.RowsRequest{Filters: []database.Filter{{Column: "note", Op: "ilike", Value: val("rust%")}}}, "a-2 b-2"},
		{"like on a number", database.RowsRequest{Filters: []database.Filter{{Column: "Qty", Op: "like", Value: val("5%")}}}, "a-1 a-2 b-1"},
		{"null", database.RowsRequest{Filters: []database.Filter{{Column: "note", Op: "is_null"}}}, "a-1"},
		{"not null", database.RowsRequest{Filters: []database.Filter{{Column: "note", Op: "not_null"}}}, "a-2 b-1 b-2 c-1"},
		{"a value that is SQL stays a value", database.RowsRequest{Filters: []database.Filter{{Column: "group", Op: "=", Value: val(`nut' OR '1'='1`)}}}, ""},
		{"sorted down, ties by the key", database.RowsRequest{OrderBy: []database.Order{{Column: "Qty", Desc: true}}}, "a-2 c-1 a-1 b-1 b-2"},
		{"sorted by two", database.RowsRequest{OrderBy: []database.Order{{Column: "group", Desc: true}, {Column: "Qty"}}}, "c-1 b-2 b-1 a-1 a-2"},
		{"a page", database.RowsRequest{Limit: 2, Offset: 2}, "b-1 b-2"},
	} {
		if got := skus(tc.in); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	if out := r.rows(database.RowsRequest{Table: "Stock", Limit: 2}); !out.More || len(out.Rows) != 2 {
		t.Errorf("the first page of two: %d rows, more %v", len(out.Rows), out.More)
	}
	if out := r.rows(database.RowsRequest{Table: "Stock", Limit: 5}); out.More || len(out.Rows) != 5 {
		t.Errorf("a page that is all of it: %d rows, more %v", len(out.Rows), out.More)
	}
	// What is not there is said, not sent to the server to find out.
	ctx := context.Background()
	for name, in := range map[string]database.RowsRequest{
		"not_found":   {Schema: r.schema, Table: "stock", Limit: 1},
		"bad_request": {Schema: r.schema, Table: "Stock", Limit: 1, OrderBy: []database.Order{{Column: `Qty"; DROP TABLE "Stock`}}},
	} {
		if _, err := r.c.Rows(ctx, in); code(err) != name {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := r.c.Rows(ctx, database.RowsRequest{Schema: r.schema, Table: "Stock", Limit: 1,
		Filters: []database.Filter{{Column: "Qty", Op: "=", Value: val("many")}}}); code(err) != "query_failed" {
		t.Errorf("a value the column cannot be: %v", err)
	}
	if r.count(`"Stock"`) != 5 {
		t.Fatal("the table was changed by reading it")
	}
}

func TestNothingIsWrittenWhileWritesAreOff(t *testing.T) {
	r := newRig(t, 0)
	s := r.schema
	r.exec(`CREATE TABLE ` + s + `.notes (id int PRIMARY KEY, body text)`)
	r.exec(`INSERT INTO ` + s + `.notes VALUES (1, 'kept')`)
	r.exec(`CREATE SEQUENCE ` + s + `.counter`)
	for name, sql := range map[string]string{
		"an insert":        `INSERT INTO ` + s + `.notes VALUES (2, 'x')`,
		"an update":        `UPDATE ` + s + `.notes SET body = 'changed'`,
		"a delete":         `DELETE FROM ` + s + `.notes`,
		"a writable CTE":   `WITH gone AS (DELETE FROM ` + s + `.notes RETURNING *) SELECT * FROM gone`,
		"a CTE that adds":  `WITH added AS (INSERT INTO ` + s + `.notes VALUES (3, 'x') RETURNING id) SELECT count(*) FROM added`,
		"DDL":              `CREATE TABLE ` + s + `.sneaky (id int)`,
		"a drop":           `DROP TABLE ` + s + `.notes`,
		"truncate":         `TRUNCATE ` + s + `.notes`,
		"a sequence":       `SELECT nextval('` + s + `.counter')`,
		"select into":      `SELECT * INTO ` + s + `.copied FROM ` + s + `.notes`,
		"a function's DML": `DO $$ BEGIN DELETE FROM ` + s + `.notes; END $$`,
		// Read-write cannot be had back once the transaction has run a
		// query, which the editor's own first statement has seen to.
		"asking for read-write":       `SET TRANSACTION READ WRITE`,
		"asking again, by a setting":  `SET transaction_read_only = off`,
		"asking and writing, in a DO": `DO $$ BEGIN SET TRANSACTION READ WRITE; DELETE FROM ` + s + `.notes; END $$`,
		"begin again, read-write":     `START TRANSACTION READ WRITE`,
	} {
		_, err := r.query(sql, false)
		var p *database.Problem
		if !errors.As(err, &p) {
			t.Errorf("%s was not refused (%v)", name, err)
			continue
		}
		if strings.HasPrefix(name, "asking") || strings.HasPrefix(name, "begin again") {
			// Refused by its own rule, with its own SQLSTATE.
			if p.Code != "query_failed" && p.Code != "read_only" {
				t.Errorf("%s: %s %s", name, p.Code, p.Message)
			}
			continue
		}
		if p.Code != "read_only" || p.Status != 403 || p.SQLState != "25006" {
			t.Errorf("%s: %s %d %s: %s", name, p.Code, p.Status, p.SQLState, p.Message)
		}
	}
	// A COMMIT of the editor's transaction ends nothing but itself.
	if _, err := r.query(`COMMIT`, false); err != nil {
		t.Errorf("a COMMIT alone: %v", err)
	}
	if _, err := r.query(`COPY `+s+`.notes TO '/tmp/berth-database-test-never-written'`, false); code(err) != "read_only" {
		t.Errorf("COPY to a file with writes off: %v", err)
	}
	var body string
	var tables, next int
	ctx := context.Background()
	r.admin.QueryRow(ctx, `SELECT body FROM `+s+`.notes WHERE id = 1`).Scan(&body)
	r.admin.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = $1`, s).Scan(&tables)
	r.admin.QueryRow(ctx, `SELECT nextval('`+s+`.counter')`).Scan(&next)
	if r.count("notes") != 1 || body != "kept" || tables != 1 || next != 1 {
		t.Fatalf("something was written with writes off: %d rows, body %q, %d tables, sequence at %d", r.count("notes"), body, tables, next)
	}

	// With writes on the same statements run, and are kept.
	res, err := r.query(`WITH added AS (INSERT INTO `+s+`.notes VALUES (2, 'two'), (3, 'three') RETURNING id) SELECT id FROM added ORDER BY id`, true)
	if err != nil || js(res.Rows) != `[[2],[3]]` || res.Command != "SELECT" || res.RowCount != 2 {
		t.Fatalf("a writable CTE with writes on: %s, %v", js(res), err)
	}
	res, err = r.query(`UPDATE `+s+`.notes SET body = upper(body) WHERE id > 1`, true)
	if err != nil || res.Command != "UPDATE" || res.RowCount != 2 || len(res.Rows) != 0 || js(res.Columns) != `[]` {
		t.Fatalf("an update with writes on: %s, %v", js(res), err)
	}
	res, err = r.query(`CREATE TABLE `+s+`.made (id int)`, true)
	if err != nil || res.Command != "CREATE TABLE" || res.RowCount != 0 {
		t.Fatalf("DDL with writes on: %s, %v", js(res), err)
	}
	// A statement that fails with writes on leaves nothing behind either.
	if _, err := r.query(`INSERT INTO `+s+`.notes VALUES (9, 'nine'), (1, 'again')`, true); code(err) != "query_failed" {
		t.Fatalf("a duplicate key: %v", err)
	}
	if r.count("notes") != 3 || r.count("made") != 0 {
		t.Fatalf("writes on: %d notes", r.count("notes"))
	}
}

func TestOneStatementAtATime(t *testing.T) {
	r := newRig(t, 0)
	s := r.schema
	r.exec(`CREATE TABLE ` + s + `.notes (id int PRIMARY KEY, body text)`)
	for _, sql := range []string{
		`SELECT 1; SELECT 2`,
		`INSERT INTO ` + s + `.notes VALUES (1, 'a'); INSERT INTO ` + s + `.notes VALUES (2, 'b')`,
		`SELECT 1; -- and then
		 DROP TABLE ` + s + `.notes`,
		`COMMIT; INSERT INTO ` + s + `.notes VALUES (1, 'a')`,
	} {
		for _, write := range []bool{false, true} {
			if _, err := r.query(sql, write); code(err) != "multiple_statements" {
				t.Errorf("%q (writes %v): %v", sql, write, err)
			}
		}
	}
	// One statement, however it looks.
	for _, sql := range []string{
		`SELECT 'a;b' AS "x;y" /* ; */ -- ;`,
		`SELECT $q$one; two$q$;`,
		`  ;; SELECT 1 ;; `,
	} {
		if _, err := r.query(sql, false); err != nil {
			t.Errorf("%q: %v", sql, err)
		}
	}
	// And were the reading of it ever wrong, the server is asked to parse
	// one statement and refuses two.
	err := r.c.within(context.Background(), true, false, time.Second, func(ctx context.Context, pc *pgconn.PgConn) error {
		_, err := run(ctx, pc, `INSERT INTO `+s+`.notes VALUES (1, 'a'); INSERT INTO `+s+`.notes VALUES (2, 'b')`, nil, 10, func([]field, [][]byte) bool { return true })
		return err
	})
	var p *database.Problem
	if !errors.As(err, &p) || p.SQLState != "42601" {
		t.Errorf("two statements sent to the server anyway: %v", err)
	}
	if r.count("notes") != 0 {
		t.Fatal("a statement of several ran")
	}
	if _, err := r.query(`  -- nothing
	`, false); code(err) != "bad_request" {
		t.Errorf("no statement at all: %v", err)
	}
}

func TestAStatementIsStoppedAtTheTimeout(t *testing.T) {
	r := newRig(t, 300*time.Millisecond)
	began := time.Now()
	_, err := r.query(`SELECT pg_sleep(20)`, false)
	var p *database.Problem
	if !errors.As(err, &p) || p.Code != "timeout" || p.SQLState != "57014" {
		t.Fatalf("a slow statement: %v", err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("it was stopped after %s", took)
	}
	// The timeout is the server's: a statement cannot lift it for itself.
	began = time.Now()
	if _, err := r.query(`SELECT set_config('statement_timeout', '0', true), pg_sleep(20)`, false); code(err) != "timeout" || time.Since(began) > 5*time.Second {
		t.Fatalf("a statement that lifts its own timeout: %v after %s", err, time.Since(began))
	}
	// And the next one runs: the stopped statement took nothing with it.
	if res, err := r.query(`SELECT 1 AS one`, false); err != nil || js(res.Rows) != `[[1]]` {
		t.Fatalf("after a timeout: %s, %v", js(res), err)
	}
}

func TestAnswersAreCapped(t *testing.T) {
	r := newRig(t, 0)
	s := r.schema
	// Rows: a statement with a hundred million rows sends the first few.
	began := time.Now()
	res, err := r.c.Query(context.Background(), database.QueryRequest{SQL: `SELECT generate_series(1, 100000000) AS n`, Limit: 7})
	if err != nil || len(res.Rows) != 7 || !res.Truncated || res.RowCount != 7 || res.Command != "SELECT" {
		t.Fatalf("a huge result: %d rows, truncated %v, %v", len(res.Rows), res.Truncated, err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("it took %s: the rest was read", took)
	}
	for _, n := range []int{6, 7, 8} {
		res, err = r.c.Query(context.Background(), database.QueryRequest{SQL: fmt.Sprintf(`SELECT n FROM generate_series(1, %d) AS n`, n), Limit: 7})
		if err != nil || len(res.Rows) != min(n, 7) || res.Truncated != (n > 7) {
			t.Errorf("%d rows with a limit of 7: %d rows, truncated %v, %v", n, len(res.Rows), res.Truncated, err)
		}
	}
	// A write that returns more than is sent is still all done.
	r.exec(`CREATE TABLE ` + s + `.many (id int PRIMARY KEY, body text)`)
	res, err = r.c.Query(context.Background(), database.QueryRequest{SQL: `INSERT INTO ` + s + `.many SELECT n, 'x' FROM generate_series(1, 600) AS n RETURNING id`, Limit: 5, Write: true})
	if err != nil || len(res.Rows) != 5 || !res.Truncated || res.Command != "INSERT" || r.count("many") != 600 {
		t.Fatalf("a write with more rows than the limit: %s, %v, %d rows in the table", js(res.Rows), err, r.count("many"))
	}

	// Cells: cut at 4,000 characters, in the database for a table's rows.
	r.exec(`CREATE TABLE ` + s + `.wide (id int PRIMARY KEY, body text, blob bytea, doc jsonb)`)
	r.exec(`INSERT INTO ` + s + `.wide VALUES (1, repeat('é', 4000), '\x00', '{}'), (2, repeat('é', 4001), decode(repeat('ab', 5000), 'hex'), ('"' || repeat('x', 9000) || '"')::jsonb)`)
	page := r.rows(database.RowsRequest{Table: "wide"})
	if js(page.Cut) != `[[1,1],[1,2],[1,3]]` {
		t.Fatalf("cut = %s", js(page.Cut))
	}
	for col := 1; col <= 3; col++ {
		if n := len([]rune(page.Rows[1][col].(string))); n != database.MaxCell {
			t.Errorf("a cut cell has %d characters", n)
		}
	}
	if len([]rune(page.Rows[0][1].(string))) != 4000 || !strings.HasPrefix(page.Rows[1][2].(string), `\xabab`) {
		t.Errorf("an uncut cell changed: %d", len(page.Rows[0][1].(string)))
	}
	res, err = r.query(`SELECT body, id FROM `+s+`.wide ORDER BY id`, false)
	if err != nil || js(res.Cut) != `[[1,0]]` || len([]rune(res.Rows[1][0].(string))) != database.MaxCell {
		t.Fatalf("a statement's cut = %s, %v", js(res.Cut), err)
	}

	// More rows than are fetched at a time are all there, in order, and the
	// server's own count of them with them.
	res, err = r.query(`SELECT id FROM `+s+`.many ORDER BY id`, false)
	if err != nil || len(res.Rows) != 600 || res.Truncated || res.RowCount != 600 || res.Rows[0][0] != int64(1) || res.Rows[599][0] != int64(600) {
		t.Fatalf("600 rows, fetched %d at a time: %d rows, truncated %v, %v", chunk, len(res.Rows), res.Truncated, err)
	}
	res, err = r.query(`UPDATE `+s+`.many SET body = 'y' RETURNING id`, true)
	if err != nil || len(res.Rows) != 600 || res.Truncated || res.RowCount != 600 || res.Command != "UPDATE" {
		t.Fatalf("a change of 600 rows that returns them: %d rows, count %d, %s, %v", len(res.Rows), res.RowCount, res.Command, err)
	}
	if res, err = r.query(`UPDATE `+s+`.many SET body = 'x'`, true); err != nil || len(res.Rows) != 0 || res.RowCount != 600 || res.Command != "UPDATE" {
		t.Fatalf("a change of 600 rows that returns none: count %d, %v", res.RowCount, err)
	}
	if page = r.rows(database.RowsRequest{Table: "many", Limit: 500}); len(page.Rows) != 500 || !page.More || page.Rows[499][0] != int64(500) {
		t.Fatalf("a page of 500 of 600: %d rows, more %v", len(page.Rows), page.More)
	}
	if page = r.rows(database.RowsRequest{Table: "many", Limit: 500, Offset: 500}); len(page.Rows) != 100 || page.More || page.Rows[99][0] != int64(600) {
		t.Fatalf("the page after it: %d rows, more %v", len(page.Rows), page.More)
	}

	// Bytes: past the cap the rows stop, and the answer says so.
	r.c.maxBytes = 2000
	page = r.rows(database.RowsRequest{Table: "many", Limit: 500})
	if len(page.Rows) < 100 || len(page.Rows) >= 500 || !page.More {
		t.Fatalf("under a byte cap: %d rows, more %v", len(page.Rows), page.More)
	}
	next := r.rows(database.RowsRequest{Table: "many", Limit: 500, Offset: len(page.Rows)})
	if next.Rows[0][0] != int64(len(page.Rows)+1) {
		t.Fatalf("the page after it begins at %v", next.Rows[0][0])
	}
	res, err = r.query(`SELECT * FROM `+s+`.many`, false)
	if err != nil || !res.Truncated || len(res.Rows) >= 600 {
		t.Fatalf("a statement under a byte cap: %d rows, truncated %v, %v", len(res.Rows), res.Truncated, err)
	}
}

func TestEditsAreAllOrNothing(t *testing.T) {
	r := newRig(t, 0)
	s := r.schema
	r.exec(`CREATE TABLE ` + s + `."Line Items" ("order" int, line int, "Qty" int, note text, PRIMARY KEY ("order", line))`)
	r.exec(`INSERT INTO ` + s + `."Line Items" VALUES (1, 1, 5, 'a'), (1, 2, 6, 'b'), (2, 1, 7, 'c')`)
	state := func() string {
		t.Helper()
		var out string
		r.admin.QueryRow(context.Background(), `SELECT string_agg("order" || '.' || line || '=' || "Qty" || coalesce(note, '-'), ' ' ORDER BY "order", line) FROM `+s+`."Line Items"`).Scan(&out)
		return out
	}
	done, err := r.edit("Line Items", `[
		{"op": "update", "key": {"order": 1, "line": 2}, "set": {"Qty": 60, "note": null}},
		{"op": "insert", "set": {"order": 3, "line": 1, "Qty": 1}},
		{"op": "delete", "key": {"line": "1", "order": "2"}}]`)
	if err != nil || js(done) != `{"updated":1,"inserted":1,"deleted":1}` {
		t.Fatalf("the edit: %s, %v", js(done), err)
	}
	if got := state(); got != "1.1=5a 1.2=60- 3.1=1-" {
		t.Fatalf("after the edit: %s", got)
	}

	// A row that is gone: the changes before it in the batch go back too.
	for name, changes := range map[string]string{
		"an update of a row that is gone": `[{"op": "update", "key": {"order": 1, "line": 1}, "set": {"Qty": 0}},
			{"op": "insert", "set": {"order": 9, "line": 9}},
			{"op": "update", "key": {"order": 2, "line": 1}, "set": {"Qty": 0}}]`,
		"a delete of a row that is gone": `[{"op": "delete", "key": {"order": 1, "line": 1}},
			{"op": "delete", "key": {"order": 2, "line": 1}}]`,
	} {
		_, err = r.edit("Line Items", changes)
		var p *database.Problem
		if !errors.As(err, &p) || p.Code != "row_conflict" || p.Status != 409 {
			t.Errorf("%s: %v", name, err)
		}
		if got := state(); got != "1.1=5a 1.2=60- 3.1=1-" {
			t.Fatalf("%s left: %s", name, got)
		}
	}
	// Part of a key is no key: it could mean several rows.
	for name, changes := range map[string]string{
		"half the key":         `[{"op": "update", "key": {"order": 1}, "set": {"Qty": 0}}]`,
		"a key of another":     `[{"op": "delete", "key": {"order": 1, "Qty": 5}}]`,
		"more than the key":    `[{"op": "delete", "key": {"order": 1, "line": 1, "Qty": 5}}]`,
		"a column not there":   `[{"op": "update", "key": {"order": 1, "line": 1}, "set": {"qty": 0}}]`,
		"a null in the key":    `[{"op": "delete", "key": {"order": 1, "line": null}}]`,
		"an op that is no op":  `[{"op": "upsert", "set": {"order": 1}}]`,
		"an update of nothing": `[{"op": "update", "key": {"order": 1, "line": 1}}]`,
	} {
		if _, err = r.edit("Line Items", changes); code(err) != "bad_request" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := state(); got != "1.1=5a 1.2=60- 3.1=1-" {
		t.Fatalf("refused edits left: %s", got)
	}
	if _, err = r.edit("No Such", `[{"op": "insert", "set": {"a": 1}}]`); code(err) != "not_found" {
		t.Errorf("a table that is not there: %v", err)
	}
}

func TestATableWithNoPrimaryKeyTakesInsertsOnly(t *testing.T) {
	r := newRig(t, 0)
	s := r.schema
	r.exec(`CREATE TABLE ` + s + `.log (at timestamptz DEFAULT now(), line text)`)
	done, err := r.edit("log", `[{"op": "insert", "set": {"line": "one"}}, {"op": "insert", "set": {}}]`)
	if err != nil || done.Inserted != 2 {
		t.Fatalf("inserts: %s, %v", js(done), err)
	}
	for _, changes := range []string{
		`[{"op": "update", "key": {"line": "one"}, "set": {"line": "two"}}]`,
		`[{"op": "insert", "set": {"line": "three"}}, {"op": "delete", "key": {"line": "one"}}]`,
	} {
		_, err = r.edit("log", changes)
		var p *database.Problem
		if !errors.As(err, &p) || p.Code != "no_primary_key" || p.Status != 400 {
			t.Errorf("%s: %v", changes, err)
		}
	}
	if r.count("log") != 2 {
		t.Fatalf("%d rows in the table", r.count("log"))
	}
	page := r.rows(database.RowsRequest{Table: "log"})
	if js(page.PrimaryKey) != `[]` || len(page.Rows) != 2 {
		t.Fatalf("its rows: key %s, %d rows", js(page.PrimaryKey), len(page.Rows))
	}
}

func TestNoticesCopyAndWhatAStatementLeavesBehind(t *testing.T) {
	r := newRig(t, 0)
	s := r.schema
	res, err := r.query(`DO $$ BEGIN RAISE NOTICE 'hello from %', 'acme'; RAISE WARNING 'careful'; END $$`, false)
	if err != nil || js(res.Notices) != `["NOTICE: hello from acme","WARNING: careful"]` || res.Command != "DO" {
		t.Fatalf("notices: %s, %v", js(res), err)
	}
	if res, err = r.query(`SELECT 1`, false); err != nil || js(res.Notices) != `[]` || js(res.Cut) != `[]` {
		t.Fatalf("no notices: %s, %v", js(res), err)
	}
	// COPY to or from the caller has nowhere to go.
	r.exec(`CREATE TABLE ` + s + `.notes (id int)`)
	for _, sql := range []string{`COPY ` + s + `.notes TO STDOUT`, `COPY ` + s + `.notes FROM STDIN`} {
		if _, err := r.query(sql, true); code(err) != "bad_request" {
			t.Errorf("%s: %v", sql, err)
		}
	}
	// What one statement sets is gone for the next: each has a session
	// to itself.
	for _, sql := range []string{`SET search_path = ` + s, `SET statement_timeout = 0`, `SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE`,
		`SELECT pg_advisory_lock(42)`, `PREPARE p AS SELECT 1`, `CREATE TEMP TABLE scratch (id int)`} {
		if _, err := r.query(sql, true); err != nil {
			t.Errorf("%s: %v", sql, err)
		}
	}
	if res, err = r.query(`SELECT current_setting('search_path') <> '`+s+`', current_setting('statement_timeout') <> '0',
		(SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid()),
		(SELECT count(*) FROM pg_prepared_statements), to_regclass('scratch') IS NULL`, false); err != nil || js(res.Rows) != `[[true,true,0,0,true]]` {
		t.Fatalf("what a statement left behind: %s, %v", js(res), err)
	}
	if _, err := r.query(`INSERT INTO `+s+`.notes VALUES (1)`, false); code(err) != "read_only" {
		t.Fatalf("after SET SESSION CHARACTERISTICS: %v", err)
	}
	// A statement that is wrong says where.
	_, err = r.query(`SELECT * FORM `+s+`.notes`, false)
	var p *database.Problem
	if !errors.As(err, &p) || p.Code != "query_failed" || p.SQLState != "42601" || p.Position != 10 {
		t.Fatalf("a syntax error: %+v", p)
	}
}

func TestThePoolIsSmallAndCloses(t *testing.T) {
	r := newRig(t, 0)
	ctx := context.Background()
	// The server's own word on which of the editor's sessions are open.
	var pids []uint32
	alive := func() int {
		var n int
		r.admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid = ANY($1)`, pids).Scan(&n)
		return n
	}
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		go func() {
			_, err := r.c.Schema(ctx)
			errs <- err
		}()
	}
	for i := 0; i < 12; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	idle, busy := r.c.pool.open()
	if idle < 1 || idle > 4 || busy != 0 {
		t.Fatalf("after twelve requests at once: %d idle, %d busy", idle, busy)
	}
	// A statement's connection is not kept: it may have left anything.
	if _, err := r.query(`SELECT 1`, false); err != nil {
		t.Fatal(err)
	}
	if again, _ := r.c.pool.open(); again != idle-1 {
		t.Fatalf("a statement's connection was kept: %d idle, were %d", again, idle)
	}
	// Closed while a statement runs: the statement ends, and so do the
	// connections.
	slow := make(chan error, 1)
	go func() {
		_, err := r.query(`SELECT pg_sleep(8)`, false)
		slow <- err
	}()
	time.Sleep(300 * time.Millisecond)
	r.c.pool.mu.Lock()
	for _, kept := range r.c.pool.idle {
		pids = append(pids, kept.pc.PID())
	}
	for pc := range r.c.pool.busy {
		pids = append(pids, pc.PID())
	}
	r.c.pool.mu.Unlock()
	if len(pids) < 2 || alive() != len(pids) {
		t.Fatalf("%d sessions of the editor's, %d on the server", len(pids), alive())
	}
	r.c.Close()
	select {
	case err := <-slow:
		if err == nil {
			t.Fatal("a statement outlived its connection")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing did not end a running statement")
	}
	if _, err := r.c.Schema(ctx); code(err) != "not_found" {
		t.Fatalf("after closing: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for alive() > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if n := alive(); n > 0 {
		t.Fatalf("%d sessions of the editor's are still open on the server", n)
	}
}

func TestADatabaseThatWillNotOpenSaysSoWithoutItsPassword(t *testing.T) {
	url := realURL(t)
	cfg, err := config(url)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, bad := range map[string]string{
		"a wrong password":   fmt.Sprintf("postgres://%s:not-the-password-9f3@%s:%d/%s", cfg.User, cfg.Host, cfg.Port, cfg.Database),
		"no such database":   fmt.Sprintf("postgres://%s:%s@%s:%d/no_such_db_9f3", cfg.User, cfg.Password, cfg.Host, cfg.Port),
		"nothing listening":  "postgres://acme:not-the-password-9f3@127.0.0.1:1/acme",
		"Prisma's own words": url,
	} {
		if name == "Prisma's own words" {
			sep := "?"
			if strings.Contains(bad, "?") {
				sep = "&"
			}
			bad += sep + "schema=public&connection_limit=5&pgbouncer=true"
		}
		c, err := Engine{}.Open(bad, database.Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = c.Schema(ctx)
		c.Close()
		if name == "Prisma's own words" {
			if err != nil {
				t.Errorf("a URL with a driver's own parameters: %v", err)
			}
			continue
		}
		var p *database.Problem
		if !errors.As(err, &p) || p.Code != "db_unavailable" || p.Status != 503 {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if said := js(p); strings.Contains(said, "not-the-password") || (cfg.Password != "" && strings.Contains(said, cfg.Password)) {
			t.Errorf("%s: the error holds the password: %s", name, said)
		}
	}
}
