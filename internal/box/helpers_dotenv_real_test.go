package box

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// manifestHelper is "box.helper" of one of this repository's plugins: the
// words the person allows, which these tests install a helper with.
func manifestHelper(t *testing.T, plugin string) HelperManifest {
	t.Helper()
	raw, err := os.ReadFile("../../plugins/" + plugin + "/berth-plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Box struct {
			Helper HelperManifest `json:"helper"`
		} `json:"box"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Box.Helper.Attach == "" {
		t.Fatalf("%s's manifest names the attach path %q (%v)", plugin, doc.Box.Helper.Attach, err)
	}
	return doc.Box.Helper
}

// The whole of it for a project that keeps its database's address in the
// worktree's .env and nowhere Shipyard assembles an environment from, with
// the real berth-database and berth-leadline and a real Postgres:
//
//   - the Database tab's look finds the worktree's database in .env, and
//     the editor reads a table of it through the box;
//   - a look after the file changed replaces what the last one found;
//   - a database said by hand is taken when it is on this machine, beside
//     the file's, and taken back on its own;
//   - the worktree's service, started with no DATABASE_URL of Shipyard's
//     own, is given Leadline's proxy in front of the file's database, and a
//     statement it runs is among Leadline's queries, while the editor's
//     connection is the database itself;
//   - removing the worktree forgets it in both.
//
// It needs a real Postgres: skipped unless BERTH_DATABASE_TEST_POSTGRES (or
// BERTH_LEADLINE_TEST_POSTGRES) holds its URL, on this machine.
func TestADatabaseKeptOnlyInDotenvShowsInTheEditorAndIsRecordedByLeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("builds berth-database and berth-leadline")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("helpers run on Linux and macOS")
	}
	databaseURL := os.Getenv("BERTH_DATABASE_TEST_POSTGRES")
	if databaseURL == "" {
		databaseURL = os.Getenv("BERTH_LEADLINE_TEST_POSTGRES")
	}
	if databaseURL == "" {
		t.Skip("BERTH_DATABASE_TEST_POSTGRES is not set")
	}
	if !strings.Contains(databaseURL, "sslmode=") {
		// Leadline's proxy stands only in front of a connection it can read.
		sep := "?"
		if strings.Contains(databaseURL, "?") {
			sep = "&"
		}
		databaseURL += sep + "sslmode=disable"
	}
	if strings.ContainsAny(databaseURL, "'\n") {
		t.Skip("the test Postgres's URL cannot be written in single quotes in a .env")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// A table of the test's own, made with pgx.
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connecting to the test Postgres: %v", err)
	}
	defer admin.Close(context.Background())
	b := make([]byte, 4)
	rand.Read(b)
	schema := "dotenv_" + hex.EncodeToString(b)
	for _, sql := range []string{
		`CREATE SCHEMA ` + schema,
		`CREATE TABLE ` + schema + `.widgets (id int PRIMARY KEY, name text)`,
		`INSERT INTO ` + schema + `.widgets VALUES (1, 'sprocket'), (2, 'flange')`,
	} {
		if _, err := admin.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	defer admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
	real := admin.Config()
	password := real.Password

	leadline := buildHelper(t, ctx, "berth-leadline")
	database := buildHelper(t, ctx, "berth-database")
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyPort := free.Addr().(*net.TCPAddr).Port
	free.Close()

	// A project with a service and no environment of Shipyard's own.
	r := newEnvRig(t, nil)
	const source = "acme%2Ffix-orders"
	install := func(plugin string, program []byte, args ...string) {
		t.Helper()
		m := manifestHelper(t, plugin)
		spec := HelperSpec{Name: m.Name, Version: m.Version, Args: append(m.Args, args...), Ready: m.Ready,
			Attach: m.Attach, AttachEnv: m.AttachEnv, Detach: m.Detach, SHA256: sha256Hex(program), Size: int64(len(program))}
		status, body := helperCall(t, r.c, "PUT", "/v1/helpers/"+plugin, program, specHeader(spec))
		if status != 200 || !strings.Contains(body, `"state":"running"`) {
			log, _ := r.h.Tail(plugin, 16<<10)
			t.Fatalf("installing %s: %d %s\n%s", plugin, status, body, log)
		}
	}
	install("leadline", leadline, "--proxy-ports", fmt.Sprintf("%d-%d", proxyPort, proxyPort))
	install("database", database)

	type connection struct {
		ID, Source, Label, Engine, Database, Host, User string
		Port                                            int
	}
	connections := func() []connection {
		t.Helper()
		var out struct{ Connections []connection }
		status, body := helperCall(t, r.c, "GET", "/v1/helpers/database/api/api/db/connections?source="+source, nil, nil)
		if status != 200 || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatalf("connections through the box: %d %s", status, body)
		}
		if password != "" && strings.Contains(body, password) {
			t.Fatal("the connections hold the database's password")
		}
		return out.Connections
	}
	// look is the Database tab's look, and the connections it answered with.
	look := func() (HelperEnvLook, []string) {
		t.Helper()
		status, raw, look := r.look("database", `{"location": "acme", "worktree": "fix-orders"}`)
		if status != 200 {
			t.Fatalf("the look: %d %s", status, raw)
		}
		if password != "" && strings.Contains(raw, password) || strings.Contains(raw, "postgres://") || strings.Contains(raw, "sslmode") {
			t.Fatalf("the look's answer holds the database's address: %s", raw)
		}
		var ids []string
		if err := json.Unmarshal(look.Answer["connections"], &ids); err != nil || string(look.Answer["source"]) != `"acme/fix-orders"` {
			t.Fatalf("the helper's answer in the look: %s", raw)
		}
		return look, ids
	}

	// ---- Nothing yet: the helper knows no database for the worktree, and a
	// look at a worktree with no .env finds none.
	if got := connections(); len(got) != 0 {
		t.Fatalf("before anything, the worktree's connections are %+v", got)
	}
	if found, ids := look(); len(found.Files) != 0 || len(found.Variables) != 0 || len(ids) != 0 {
		t.Fatalf("a look with no .env: %+v", found)
	}

	// ---- The worktree gets a .env, and a look finds its database.
	r.dotenv(r.wt.Path, ".env", "# acme\nNODE_ENV=development\nDATABASE_URL='"+databaseURL+"'\n")
	found, ids := look()
	if len(found.Files) != 1 || found.Files[0] != ".env" || len(found.Variables) != 1 ||
		found.Variables[0] != (HelperEnvVariable{Name: "DATABASE_URL", File: ".env", Sent: true}) || len(ids) != 1 {
		t.Fatalf("a look with a .env: %+v, connections %v", found, ids)
	}
	conns := connections()
	if len(conns) != 1 {
		t.Fatalf("after the look the worktree's connections are %+v", conns)
	}
	first := conns[0]
	if first.ID != ids[0] || first.Label != "DATABASE_URL" || first.Engine != "postgres" || first.Port != int(real.Port) || first.Database != real.Database || first.User != real.User {
		t.Fatalf("the connection a look found is %+v; the database is %s at port %d", first, real.Database, real.Port)
	}
	asJSON := http.Header{"Content-Type": {"application/json"}}
	rows := func() (int, string) {
		t.Helper()
		return helperCall(t, r.c, "POST", "/v1/helpers/database/api/api/db/"+first.ID+"/rows", []byte(`{"schema": "`+schema+`", "table": "widgets"}`), asJSON)
	}
	if status, body := rows(); status != 200 || !strings.Contains(body, `"rows":[[1,"sprocket"],[2,"flange"]]`) {
		t.Fatalf("rows of the database a look found: %d %s", status, body)
	}
	// The helper filed it under ".env", where a service's name would be.
	saved := filepath.Join(r.h.Dir, "database", "data", "connections.json")
	if raw := string(mustRead(saved)); !strings.Contains(raw, `"command": ".env"`) || strings.Contains(raw, `"command": "web"`) {
		t.Fatalf("connections.json does not file the look under .env:\n%s", strings.ReplaceAll(raw, password, "…"))
	}
	// Nothing was started, and nothing is noted as pointing at a helper.
	for _, plugin := range []string{"database", "leadline"} {
		if at := r.h.attachedTo(plugin); len(at) != 0 {
			t.Fatalf("attached.json of %s after a look: %+v", plugin, at)
		}
	}
	// Leadline was not told: only the helper that was asked for.
	proxies := func() int {
		t.Helper()
		var out struct {
			Proxies []json.RawMessage `json:"proxies"`
		}
		status, body := helperCall(t, r.c, "GET", "/v1/helpers/leadline/api/api/proxies", nil, nil)
		if status != 200 || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatalf("Leadline's proxies: %d %s", status, body)
		}
		return len(out.Proxies)
	}
	if n := proxies(); n != 0 {
		t.Fatalf("Leadline has %d proxies after Database's look", n)
	}

	// ---- The file changes: a look again replaces what the last one found.
	other := strings.Replace(databaseURL, "/"+real.Database+"?", "/"+real.Database+"_elsewhere?", 1)
	if other == databaseURL {
		t.Fatalf("could not write another database's URL from the test Postgres's")
	}
	r.dotenv(r.wt.Path, ".env.local", "DATABASE_URL='"+other+"'\n")
	found, ids = look()
	if len(ids) != 1 || ids[0] == first.ID || found.Variables[0].File != ".env.local" {
		t.Fatalf("a look after .env.local was added: %+v, connections %v", found, ids)
	}
	if conns = connections(); len(conns) != 1 || conns[0].ID != ids[0] || conns[0].Database != real.Database+"_elsewhere" {
		t.Fatalf("after the file changed the worktree's connections are %+v", conns)
	}
	if status, body := rows(); status != 404 {
		t.Fatalf("the connection the file no longer names still answers: %d %s", status, body)
	}
	// The variable goes: so does the connection.
	r.dotenv(r.wt.Path, ".env.local", "")
	r.dotenv(r.wt.Path, ".env", "NODE_ENV=development\n")
	if found, ids = look(); len(found.Variables) != 0 || len(ids) != 0 || len(connections()) != 0 {
		t.Fatalf("a look after the variable went: %+v, connections %v", found, ids)
	}
	// And comes back.
	r.dotenv(r.wt.Path, ".env", "NODE_ENV=development\nDATABASE_URL='"+databaseURL+"'\n")
	if _, ids = look(); len(ids) != 1 || ids[0] != first.ID {
		t.Fatalf("a look once the variable is back: %v, want %s", ids, first.ID)
	}

	// ---- A database said by hand: the same server by its other name, which
	// is another connection to the helper. One on another machine is not
	// taken; one on this machine is, beside the file's, and is forgotten on
	// its own.
	give := func(env string) []string {
		t.Helper()
		status, raw, look := r.look("database", `{"location": "acme", "worktree": "fix-orders", "env": `+env+`}`)
		if status != 200 || look.Command != ".manual" || len(look.Files) != 0 {
			t.Fatalf("a database given by hand: %d %s", status, raw)
		}
		if password != "" && strings.Contains(raw, password) || strings.Contains(raw, "postgres://") || strings.Contains(raw, "sslmode") {
			t.Fatalf("the answer to a database given by hand holds its address: %s", raw)
		}
		var ids []string
		if err := json.Unmarshal(look.Answer["connections"], &ids); err != nil {
			t.Fatalf("the helper's answer: %s", raw)
		}
		return ids
	}
	quoted := func(url string) string {
		b, _ := json.Marshal(url)
		return string(b)
	}
	byHand := strings.Replace(databaseURL, "@127.0.0.1:", "@localhost:", 1)
	if byHand == databaseURL {
		byHand = strings.Replace(databaseURL, "@localhost:", "@127.0.0.1:", 1)
	}
	if byHand == databaseURL {
		t.Fatal("could not write the test Postgres's URL with its host's other name")
	}
	elsewhere := strings.Replace(byHand, "@"+strings.SplitN(strings.SplitN(byHand, "@", 2)[1], ":", 2)[0]+":", "@db.acme.example:", 1)
	if ids = give(`{"DATABASE_URL": ` + quoted(elsewhere) + `}`); len(ids) != 0 {
		t.Fatalf("a database on another machine was taken: %v", ids)
	}
	if conns = connections(); len(conns) != 1 || conns[0].ID != first.ID {
		t.Fatalf("after a database on another machine was refused the connections are %+v", conns)
	}
	ids = give(`{"DATABASE_URL": ` + quoted(byHand) + `}`)
	if len(ids) != 1 || ids[0] == first.ID {
		t.Fatalf("a database given by hand: connections %v", ids)
	}
	pasted := ids[0]
	if conns = connections(); len(conns) != 2 {
		t.Fatalf("with one from the file and one by hand the connections are %+v", conns)
	}
	if raw := string(mustRead(saved)); !strings.Contains(raw, `"command": ".manual"`) || !strings.Contains(raw, `"command": ".env"`) {
		t.Fatalf("connections.json does not file one under manual and one under .env:\n%s", strings.ReplaceAll(raw, password, "…"))
	}
	if status, body := helperCall(t, r.c, "POST", "/v1/helpers/database/api/api/db/"+pasted+"/rows", []byte(`{"schema": "`+schema+`", "table": "widgets"}`), asJSON); status != 200 || !strings.Contains(body, `"rows":[[1,"sprocket"],[2,"flange"]]`) {
		t.Fatalf("rows of the database given by hand: %d %s", status, body)
	}
	// It is read-only until writes are turned on, like any other.
	if status, body := helperCall(t, r.c, "POST", "/v1/helpers/database/api/api/db/"+pasted+"/query", []byte(`{"sql": "DELETE FROM `+schema+`.widgets"}`), asJSON); status != 403 || !strings.Contains(body, `"code":"read_only"`) {
		t.Fatalf("a delete through the database given by hand, with writes off: %d %s", status, body)
	}
	// A look in the files again leaves it be, and taking it back leaves the
	// file's.
	if _, ids = look(); len(ids) != 1 || ids[0] != first.ID || len(connections()) != 2 {
		t.Fatalf("a look after a database was given by hand: %v, connections %+v", ids, connections())
	}
	if ids = give(`{}`); len(ids) != 0 {
		t.Fatalf("taking back the database given by hand: %v", ids)
	}
	if conns = connections(); len(conns) != 1 || conns[0].ID != first.ID {
		t.Fatalf("after it was taken back the connections are %+v", conns)
	}
	if raw := string(mustRead(saved)); strings.Contains(raw, `"command": ".manual"`) {
		t.Fatal("connections.json still has the database that was taken back")
	}
	if status, _ := helperCall(t, r.c, "POST", "/v1/helpers/database/api/api/db/"+pasted+"/rows", []byte(`{"schema": "`+schema+`", "table": "widgets"}`), asJSON); status != 404 {
		t.Fatalf("the connection that was taken back still answers: %d", status)
	}

	// ---- The worktree's service starts. Shipyard's environment for it has
	// no DATABASE_URL; the file's is shown to both helpers, and the service
	// is given Leadline's proxy in front of it.
	if status, body := helperCall(t, r.c, "POST", "/v1/locations/acme/worktrees/fix-orders/services/web/start", nil, nil); status != 200 {
		t.Fatalf("starting the service: %d %s", status, body)
	}
	env := r.units.env(serviceUnit("acme", "fix-orders", "web"))
	if !strings.Contains(env["DATABASE_URL"], fmt.Sprintf("@127.0.0.1:%d/", proxyPort)) {
		t.Fatalf("the service's DATABASE_URL does not go through Leadline's proxy on %d", proxyPort)
	}
	web := HelperService{Location: "acme", Worktree: "fix-orders", Service: "web"}
	if at := r.h.attachedTo("leadline"); len(at) != 1 || at[0] != web {
		t.Fatalf("attached.json of leadline: %+v", at)
	}
	if at := r.h.attachedTo("database"); len(at) != 0 {
		t.Fatalf("attached.json of database, which changes nothing: %+v", at)
	}
	// The editor's connection is still the database itself, not the proxy:
	// one connection, which the service and the look both have.
	if conns = connections(); len(conns) != 1 || conns[0].ID != first.ID || conns[0].Port != int(real.Port) || conns[0].Port == proxyPort {
		t.Fatalf("with the service started the editor's connections are %+v; the proxy is at %d", conns, proxyPort)
	}
	if raw := string(mustRead(saved)); !strings.Contains(raw, `"command": ".env"`) || !strings.Contains(raw, `"command": "web"`) {
		t.Fatalf("connections.json does not have both the look and the service:\n%s", strings.ReplaceAll(raw, password, "…"))
	}

	// The dev server runs one statement, through the proxy it was given.
	cfg, err := pgx.ParseConfig(env["DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	app, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting through the proxy: %v", err)
	}
	var name string
	if err := app.QueryRow(ctx, `SELECT name FROM `+schema+`.widgets WHERE id = 2`).Scan(&name); err != nil || name != "flange" {
		t.Fatalf("the app's statement through the proxy: %q, %v", name, err)
	}
	app.Close(ctx)
	// And the editor reads the table, straight from the database.
	if status, body := rows(); status != 200 || !strings.Contains(body, `"rows":[[1,"sprocket"],[2,"flange"]]`) {
		t.Fatalf("rows with the service started: %d %s", status, body)
	}
	type query struct {
		Fingerprint string `json:"fingerprint"`
		Calls       int    `json:"calls"`
	}
	var queries []query
	var listing string
	waitUntil(t, "the app's query to be among Leadline's", 30*time.Second, func() bool {
		var status int
		status, listing = helperCall(t, r.c, "GET", "/v1/helpers/leadline/api/api/queries?source="+source, nil, nil)
		queries = nil
		return status == 200 && json.Unmarshal([]byte(listing), &queries) == nil && len(queries) > 0
	})
	if len(queries) != 1 || queries[0].Fingerprint != "SELECT name FROM "+schema+".widgets WHERE id = ?" || queries[0].Calls != 1 {
		t.Fatalf("Leadline's queries for the worktree = %s", listing)
	}

	// ---- The file loses the variable while the service still has the
	// database: a look takes the file's claim away, and the service's keeps
	// the connection.
	r.dotenv(r.wt.Path, ".env", "NODE_ENV=development\n")
	if found, ids = look(); len(found.Variables) != 0 || len(ids) != 0 {
		t.Fatalf("a look after the variable went, with the service running: %+v, connections %v", found, ids)
	}
	if conns = connections(); len(conns) != 1 || conns[0].ID != first.ID {
		t.Fatalf("the connection the service still has went with the file's: %+v", conns)
	}

	// ---- Nothing berthd logged holds the address, and neither helper's log.
	logs := r.logs.String()
	for _, plugin := range []string{"database", "leadline"} {
		tail, _ := r.h.Tail(plugin, 1<<20)
		logs += "\n" + string(tail)
	}
	for what, secret := range map[string]string{"the database's password": password, "a database URL": "postgres://"} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Errorf("a log holds %s", what)
		}
	}

	// ---- The worktree is removed: both helpers forget it, a database that
	// was given by hand too.
	if ids = give(`{"DATABASE_URL": ` + quoted(byHand) + `}`); len(ids) != 1 || ids[0] != pasted || len(connections()) != 2 {
		t.Fatalf("the same database given by hand again: %v, want %s", ids, pasted)
	}
	if status := call(t, r.c, "DELETE", "/v1/locations/acme/worktrees/fix-orders?force=1", "", nil, nil); status != 200 {
		t.Fatalf("removing the worktree: %d", status)
	}
	waitUntil(t, "the removed worktree's database to be forgotten", 20*time.Second, func() bool { return len(connections()) == 0 })
	waitUntil(t, "the removed worktree's proxy to close", 20*time.Second, func() bool { return proxies() == 0 })
	if raw := string(mustRead(saved)); strings.Contains(raw, "fix-orders") || password != "" && strings.Contains(raw, password) {
		t.Fatal("connections.json still has the removed worktree's database")
	}
	for _, plugin := range []string{"database", "leadline"} {
		for _, s := range r.h.attachedTo(plugin) {
			if s.Worktree == "fix-orders" {
				t.Fatalf("attached.json of %s still names the removed worktree: %+v", plugin, s)
			}
		}
	}
}
