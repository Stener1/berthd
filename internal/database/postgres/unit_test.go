package postgres

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/database"
)

// These need no database: they are what CI holds the engine to.

func TestIdentifiersAreQuotedWhateverTheyHold(t *testing.T) {
	for name, want := range map[string]string{
		"orders":               `"orders"`,
		"Order Items":          `"Order Items"`,
		"select":               `"select"`,
		`we"ird`:               `"we""ird"`,
		`"; DROP TABLE x; --`:  `"""; DROP TABLE x; --"`,
		"":                     `""`,
		"nul\x00byte":          `"nulbyte"`,
		"käse":                 `"käse"`,
		`back\slash 'n' quote`: `"back\slash 'n' quote"`,
	} {
		if got := (Engine{}).QuoteIdent(name); got != want {
			t.Errorf("quote(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestStatementsAreCounted(t *testing.T) {
	for sql, want := range map[string]struct {
		n    int
		lead string
	}{
		``:                               {0, ""},
		"  \n\t ":                        {0, ""},
		`-- only a comment`:              {0, ""},
		`/* a /* nested */ comment */ ;`: {0, ""},
		`SELECT 1`:                       {1, "SELECT"},
		`select 1;`:                      {1, "SELECT"},
		`;;select 1;;`:                   {1, "SELECT"},
		`SELECT 1; SELECT 2`:             {2, "SELECT"},
		`SELECT 1;SELECT 2;SELECT 3;`:    {3, "SELECT"},
		`SELECT ';'`:                     {1, "SELECT"},
		`SELECT 'it''s; fine'`:           {1, "SELECT"},
		`SELECT E'it\'s; fine'`:          {1, "SELECT"},
		`SELECT 'a\'; DELETE FROM t`:     {2, "SELECT"},
		`SELECT ";" FROM "a;b"`:          {1, "SELECT"},
		`SELECT $$;$$`:                   {1, "SELECT"},
		`SELECT $tag$ ; $$ ; $tag$`:      {1, "SELECT"},
		`SELECT $1; SELECT $2`:           {2, "SELECT"},
		`SELECT 1 -- ; DROP TABLE t`:     {1, "SELECT"},
		"SELECT 1 -- x\n; DROP TABLE t":  {2, "SELECT"},
		`SELECT 1 /* ; */ + 2`:           {1, "SELECT"},
		`/* lead */ WITH x AS (SELECT 1) SELECT * FROM x`:                      {1, "WITH"},
		`  copy t TO PROGRAM 'rm -rf /'`:                                       {1, "COPY"},
		`"copy" t`:                                                             {1, `"COPY"`},
		`DO $$ BEGIN PERFORM 1; PERFORM 2; END $$`:                             {1, "DO"},
		`CREATE FUNCTION f() RETURNS int AS 'select 1; select 2' LANGUAGE sql`: {1, "CREATE"},
		`SELECT 'never closed; SELECT 2`:                                       {1, "SELECT"},
		`SELECT 1; /* trailing */`:                                             {1, "SELECT"},
		`SELECT a$b; SELECT 1`:                                                 {2, "SELECT"},
	} {
		if n, lead := statements(sql); n != want.n || lead != want.lead {
			t.Errorf("%q: %d statements beginning %q, want %d beginning %q", sql, n, lead, want.n, want.lead)
		}
	}
}

func TestAShapeHoldsNoValue(t *testing.T) {
	for sql, want := range map[string]string{
		`UPDATE users SET email = 'jo@acme.test', age = 41 WHERE id = 7`:  `UPDATE users SET email = ?, age = ? WHERE id = ?`,
		"INSERT INTO t (a, b)\n  VALUES ('x''y', 1.5e-3), (E'\\'z', .5)":  `INSERT INTO t (a, b) VALUES (?, ?), (?, ?)`,
		`DELETE FROM "Order Items" WHERE "Line No" = 2 -- the second`:     `DELETE FROM "Order Items" WHERE "Line No" = ?`,
		`ALTER ROLE app PASSWORD 'hunter2'`:                               `ALTER ROLE app PASSWORD ?`,
		`SELECT $$ secret $$, $t$ more $t$, x'ff', B'101', N'n' /* s3 */`: `SELECT ?, ?, ?, ?, ?`,
		`UPDATE t SET col1 = col2 + 1 WHERE id = $1`:                      `UPDATE t SET col1 = col2 + ? WHERE id = $1`,
		`INSERT INTO t VALUES ('never closed`:                             `INSERT INTO t VALUES (?`,
	} {
		if got := shape(sql); got != want {
			t.Errorf("shape(%q)\n  = %s\nwant %s", sql, got, want)
		}
	}
	long := shape(`INSERT INTO t VALUES (` + strings.Repeat(`'v', `, 400) + `'v')`)
	if len(long) > 410 || !strings.HasSuffix(long, "…") || strings.Contains(long, "v") {
		t.Errorf("a long statement's shape is %d bytes: %s", len(long), long)
	}
}

func TestCellsAreTheJSONTheAPIPromises(t *testing.T) {
	js := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	for _, tc := range []struct {
		oid  uint32
		text string
		want string
	}{
		{oidBool, "t", `true`}, {oidBool, "f", `false`},
		{oidInt4, "-42", `-42`}, {oidInt2, "0", `0`},
		{oidInt8, "9007199254740992", `9007199254740992`}, {oidInt8, "-9007199254740992", `-9007199254740992`},
		{oidInt8, "9007199254740993", `"9007199254740993"`}, {oidInt8, "-9223372036854775808", `"-9223372036854775808"`},
		{oidOID, "4294967295", `4294967295`},
		{oidFloat8, "1.5", `1.5`}, {oidFloat8, "1e+300", `1e+300`}, {oidFloat4, "0.1", `0.1`}, {oidFloat8, "-0", `-0`},
		{oidFloat8, "NaN", `"NaN"`}, {oidFloat8, "Infinity", `"Infinity"`}, {oidFloat4, "-Infinity", `"-Infinity"`},
		{1700, "12345678901234567890.10", `"12345678901234567890.10"`}, {1700, "1", `"1"`},
		{oidTimestamp, "2026-10-12 08:30:00", `"2026-10-12T08:30:00"`},
		{oidTimestamp, "2026-10-12 08:30:00.123456", `"2026-10-12T08:30:00.123456"`},
		{oidTimestamptz, "2026-10-12 08:30:00+00", `"2026-10-12T08:30:00Z"`},
		{oidTimestamptz, "2026-10-12 08:30:00.5-07", `"2026-10-12T08:30:00.5-07:00"`},
		{oidTimestamptz, "2026-10-12 08:30:00+05:30", `"2026-10-12T08:30:00+05:30"`},
		{oidTimestamptz, "1883-11-18 12:03:58-04:56:02", `"1883-11-18T12:03:58-04:56:02"`},
		{oidTimestamptz, "12026-01-01 00:00:00+00", `"12026-01-01T00:00:00Z"`},
		{oidTimestamptz, "infinity", `"infinity"`}, {oidTimestamp, "0044-03-15 00:00:00 BC", `"0044-03-15 00:00:00 BC"`},
		{oidDate, "2026-10-12", `"2026-10-12"`}, {oidUUID, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", `"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"`},
		{17, `\xdeadbeef`, `"\\xdeadbeef"`}, {3802, `{"a": 1}`, `"{\"a\": 1}"`}, {1007, `{1,2,NULL}`, `"{1,2,NULL}"`},
		{25, "", `""`}, {25, "null", `"null"`},
	} {
		if got := js(cell(tc.oid, []byte(tc.text))); got != tc.want {
			t.Errorf("cell(%d, %q) = %s, want %s", tc.oid, tc.text, got, tc.want)
		}
	}
	if cell(oidInt4, nil) != nil || cell(25, nil) != nil {
		t.Error("NULL is not null")
	}
}

func TestTheServersWordOnAStatementIsRead(t *testing.T) {
	for tag, want := range map[string]struct {
		command string
		rows    int64
		counted bool
	}{
		"SELECT 3":     {"SELECT", 3, true},
		"INSERT 0 5":   {"INSERT", 5, true},
		"UPDATE 0":     {"UPDATE", 0, true},
		"CREATE TABLE": {"CREATE TABLE", 0, false},
		"DO":           {"DO", 0, false},
		"":             {"", 0, false},
	} {
		if command, rows, counted := tagged(tag); command != want.command || rows != want.rows || counted != want.counted {
			t.Errorf("tagged(%q) = %q, %d, %v", tag, command, rows, counted)
		}
	}
}

func TestADatabaseURLIsReadAsADriverWould(t *testing.T) {
	t.Setenv("PGHOST", "db.acme.example")
	t.Setenv("PGSSLMODE", "require")
	t.Setenv("PGAPPNAME", "from-the-environment")
	for raw, want := range map[string]database.Target{
		"postgres://app:hunter2@localhost:5433/acme":                       {Host: "localhost", Port: 5433, Database: "acme", User: "app"},
		"postgresql://app@127.0.0.1/acme?sslmode=disable":                  {Host: "127.0.0.1", Port: 5432, Database: "acme", User: "app"},
		"postgres://app:p%40ss%2Fword@[::1]:5432/acme":                     {Host: "::1", Port: 5432, Database: "acme", User: "app"},
		"postgresql+psycopg://app:x@localhost/acme":                        {Host: "localhost", Port: 5432, Database: "acme", User: "app"},
		"jdbc:postgresql://localhost:5433/acme?user=app&password=hunter2":  {Host: "localhost", Port: 5433, Database: "acme", User: "app"},
		"postgres://app:x@0.0.0.0:5432/acme":                               {Host: "127.0.0.1", Port: 5432, Database: "acme", User: "app"},
		"postgres://app:x@localhost/acme?schema=public&connection_limit=5": {Host: "localhost", Port: 5432, Database: "acme", User: "app"},
		"postgres://app:x@db.acme.example:5432/acme":                       {Host: "db.acme.example", Port: 5432, Database: "acme", User: "app"},
		"postgres://app@localhost":                                         {Host: "localhost", Port: 5432, Database: "app", User: "app"},
		"postgres://app:x@/acme?host=/var/run/postgresql&port=5433":        {Host: "/var/run/postgresql", Port: 5433, Socket: true, Database: "acme", User: "app"},
		"postgres://app:x@localhost/acme?host=/tmp":                        {Host: "/tmp", Port: 5432, Socket: true, Database: "acme", User: "app"},
		"postgres://app:x@localhost/acme?hostaddr=10.1.2.3":                {Host: "10.1.2.3", Port: 5432, Database: "acme", User: "app"},
		"postgres://app:it's%20a%20%5C%20quote@localhost/we'ird%20db":      {Host: "localhost", Port: 5432, Database: "we'ird db", User: "app"},
	} {
		got, err := Engine{}.Target(raw)
		if err != nil || got != want {
			t.Errorf("%s\n  = %+v, %v\nwant %+v", raw, got, err, want)
		}
	}
	// What is local is the address, whatever the URL was dressed as.
	for raw, local := range map[string]bool{
		"postgres://app:x@localhost/acme":                      true,
		"postgres://app:x@db.localhost/acme":                   true,
		"postgres://app:x@[::1]/acme":                          true,
		"postgres://app:x@/acme?host=/var/run/postgresql":      true,
		"postgres://app:x@db.acme.example/acme":                false,
		"postgres://app:x@10.0.0.5/acme":                       false,
		"postgres://app:x@localhost/acme?hostaddr=10.1.2.3":    false,
		"postgres://app:x@localhost/acme?host=db.acme.example": false,
		"postgres://app:x@localhost.acme.example/acme":         false,
	} {
		got, err := Engine{}.Target(raw)
		if err != nil || got.Local() != local {
			t.Errorf("%s: local %v, %v; want %v", raw, got.Local(), err, local)
		}
	}
	// The session is the editor's own, not the environment's.
	cfg, err := config("postgres://app:hunter2@localhost/acme?options=-c%20search_path%3Dapp&application_name=mine&statement_timeout=0")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "hunter2" || cfg.RuntimeParams["application_name"] != "berth-database" || cfg.RuntimeParams["DateStyle"] != "ISO" ||
		cfg.RuntimeParams["options"] != "-c search_path=app" || cfg.RuntimeParams["statement_timeout"] != "" || len(cfg.RuntimeParams) != 5 {
		t.Errorf("the session's settings: %v", cfg.RuntimeParams)
	}
	if cfg, err = config("postgres://app:x@localhost/acme?schema=My%20Schema"); err != nil || cfg.RuntimeParams["search_path"] != `"My Schema"` {
		t.Errorf("Prisma's schema: %v, %v", cfg.RuntimeParams, err)
	}
	if cfg, err = config("postgres://app:x@localhost/acme?sslmode=require"); err != nil || cfg.TLSConfig == nil {
		t.Errorf("sslmode=require: %v", err)
	}
	if cfg, err = config("postgres://app:x@localhost/acme?sslmode=disable"); err != nil || cfg.TLSConfig != nil || len(cfg.Fallbacks) != 0 {
		t.Errorf("sslmode=disable: %v", err)
	}
}

func TestAURLThatCannotBeReadIsNeverRepeated(t *testing.T) {
	for _, raw := range []string{
		"", "localhost:5432", "mysql://app:hunter2@localhost/acme", "postgres://app:hunter2@localhost:port/acme",
		"postgres://app:hunter2@one,two/acme", "postgres://app:hunter2@localhost/acme?host=one,two",
		"postgres://app:hunter2@localhost:99999/acme", "postgres://app:hunter2@[::1/acme", "host=localhost password=hunter2",
		"postgres://app:hunter2@localhost/acme?sslmode=verify-full&sslrootcert=/no/such/file-hunter2",
	} {
		if _, err := (Engine{}).Target(raw); err == nil {
			t.Errorf("%q was read", raw)
		} else if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%q: the error repeats the URL: %v", raw, err)
		}
		if _, err := (Engine{}).Open(raw, database.Options{}); err == nil {
			t.Errorf("%q was opened", raw)
		}
	}
}

func TestOnlyThisMachinesAddressesAreDialled(t *testing.T) {
	ctx := context.Background()
	if addrs, err := lookupLocal(ctx, "localhost"); err != nil || len(addrs) == 0 {
		t.Errorf("localhost: %v, %v", addrs, err)
	}
	if addrs, err := lookupLocal(ctx, "127.0.0.1"); err != nil || len(addrs) != 1 {
		t.Errorf("127.0.0.1: %v, %v", addrs, err)
	}
	for _, host := range []string{"192.0.2.7", "2001:db8::1"} {
		if addrs, err := lookupLocal(ctx, host); err == nil {
			t.Errorf("%s would be dialled: %v", host, addrs)
		}
	}
}

func TestWhatTheEngineOffers(t *testing.T) {
	caps := Engine{}.Capabilities()
	// As the UI reads it: the JSON, decoded.
	raw, _ := json.Marshal(caps)
	var got, want any
	json.Unmarshal(raw, &got)
	json.Unmarshal([]byte(`{"dialect": "postgresql", "schemas": true,
		"operators": ["=", "!=", "<", "<=", ">", ">=", "like", "ilike", "is_null", "not_null"],
		"kinds": ["table", "view", "materialized_view"], "writes": true, "transactions": true}`), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("capabilities = %s", raw)
	}
	// Every operator it offers is one Rows knows how to write.
	for _, op := range caps.Operators {
		if _, ok := compare[op]; !ok && op != "like" && op != "ilike" && !database.Unary(op) {
			t.Errorf("%s is offered and not written", op)
		}
	}
	reg := database.NewRegistry(Engine{})
	for _, url := range []string{"postgres://x@localhost/a", "POSTGRESQL://x@localhost/a", "postgresql+asyncpg://x@localhost/a", "jdbc:postgresql://localhost/a"} {
		if e, ok := reg.For(url); !ok || e.Name() != "postgres" {
			t.Errorf("no engine for %s", url)
		}
	}
	if _, ok := reg.For("mysql://x@localhost/a"); ok {
		t.Error("an engine for MySQL")
	}
}
