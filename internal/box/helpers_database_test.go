package box

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cosscom/shipyard/internal/service"
)

// buildHelper builds one of this repository's helpers as these tests were
// built, and returns its bytes.
func buildHelper(t *testing.T, ctx context.Context, name string) []byte {
	t.Helper()
	built := filepath.Join(t.TempDir(), name)
	args := []string{"build", "-o", built}
	if raceBuild {
		args = append(args, "-race")
	}
	if out, err := exec.CommandContext(ctx, "go", append(args, "../../cmd/"+name)...).CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", name, err, out)
	}
	program, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// The whole of it, with the real berth-database beside the real
// berth-leadline, and a real Postgres: both helpers are installed through
// the box's API, a worktree's service starts with a DATABASE_URL, and then
//
//   - the editor reads and edits a table through the box, which adds the
//     helper's token;
//   - the helper itself, asked directly without the token, refuses;
//   - the editor connects to the database, not to Leadline's proxy in front
//     of it, so nothing it runs is recorded as the app's queries.
//
// It needs a real Postgres: skipped unless BERTH_DATABASE_TEST_POSTGRES (or
// BERTH_LEADLINE_TEST_POSTGRES) holds its URL, on this machine.
func TestTheDatabaseEditorWorksThroughTheBoxBesideLeadline(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// A table of the test's own, made and checked with pgx: what the
	// editor did is never taken on the editor's word.
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connecting to the test Postgres: %v", err)
	}
	defer admin.Close(context.Background())
	b := make([]byte, 4)
	rand.Read(b)
	schema := "box_" + hex.EncodeToString(b)
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

	h, _ := testHelpers(t)
	units := &unitSpecs{specs: map[string]service.Spec{}}
	var bx *Box
	c, _ := servedBox(t, func(b *Box) {
		b.Helpers = h
		b.Units = &Units{Dir: t.TempDir(), svc: units.ops()}
		bx = b
	})
	if _, err := bx.Locations.Add(ctx, "acme", gitRepo(t)); err != nil {
		t.Fatal(err)
	}
	if err := bx.Locations.SetLocalConfig("acme", RepoConfig{
		Env:      map[string]string{"DATABASE_URL": databaseURL},
		Services: []WorktreeService{{Name: "web", Run: "pnpm dev"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bx.Locations.CreateWorktree(ctx, "acme", "fix-orders", "", ""); err != nil {
		t.Fatal(err)
	}

	// ---- Both installed through the box's API, with their manifests' words.
	install := func(plugin string, spec HelperSpec, program []byte) {
		t.Helper()
		spec.SHA256, spec.Size = sha256Hex(program), int64(len(program))
		status, body := helperCall(t, c, "PUT", "/v1/helpers/"+plugin, program, specHeader(spec))
		if status != 200 || !strings.Contains(body, `"state":"running"`) {
			log, _ := h.Tail(plugin, 16<<10)
			t.Fatalf("installing %s: %d %s\n%s", plugin, status, body, log)
		}
	}
	install("leadline", HelperSpec{Name: "berth-leadline", Version: "0.1.0",
		Args:  []string{"serve", "--dir", "{dir}", "--addr", "127.0.0.1:0", "--proxy-ports", fmt.Sprintf("%d-%d", proxyPort, proxyPort)},
		Ready: "helper.json", Attach: "/api/attach", AttachEnv: []string{"DATABASE_URL", "POSTGRES_URL", "OTEL_*"}}, leadline)
	install("database", HelperSpec{Name: "berth-database", Version: "0.1.0",
		Args:  []string{"serve", "--dir", "{dir}", "--addr", "127.0.0.1:0"},
		Ready: "helper.json", Attach: "/api/attach", AttachEnv: []string{"DATABASE_URL", "POSTGRES_URL"}}, database)

	// ---- The service starts: berthd asks both helpers first.
	status, body := helperCall(t, c, "POST", "/v1/locations/acme/worktrees/fix-orders/services/web/start", nil, nil)
	if status != 200 {
		t.Fatalf("starting the service: %d %s", status, body)
	}
	env := units.env(serviceUnit("acme", "fix-orders", "web"))
	if !strings.Contains(env["DATABASE_URL"], fmt.Sprintf("@127.0.0.1:%d/", proxyPort)) {
		t.Fatalf("the service's DATABASE_URL does not go through Leadline's proxy on %d", proxyPort)
	}

	// ---- The editor's connection is the database's own address: it was
	// shown the project's URL, not the one Leadline answered with.
	asJSON := http.Header{"Content-Type": {"application/json"}}
	api := func(method, path, body string) (int, string) {
		t.Helper()
		var raw []byte
		if body != "" {
			raw = []byte(body)
		}
		return helperCall(t, c, method, "/v1/helpers/database/api/api"+path, raw, asJSON)
	}
	var conns struct {
		Connections []struct {
			ID, Source, Label, Engine, Database, Host, User string
			Port                                            int
			Capabilities                                    struct{ Dialect string }
		}
	}
	status, body = api("GET", "/db/connections?source=acme%2Ffix-orders", "")
	if status != 200 || json.Unmarshal([]byte(body), &conns) != nil || len(conns.Connections) != 1 {
		t.Fatalf("connections through the box: %d %s", status, body)
	}
	conn := conns.Connections[0]
	if conn.Port != int(real.Port) || conn.Port == proxyPort || conn.Database != real.Database || conn.User != real.User ||
		conn.Label != "DATABASE_URL" || conn.Engine != "postgres" || conn.Capabilities.Dialect != "postgresql" {
		t.Fatalf("the editor's connection is %+v; the database is at port %d, the proxy at %d", conn, real.Port, proxyPort)
	}
	if password != "" && strings.Contains(body, password) {
		t.Fatal("the connections hold the database's password")
	}
	base := "/db/" + conn.ID

	// ---- The dev server runs one statement, through the proxy.
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
	if err := app.QueryRow(ctx, `SELECT name FROM `+schema+`.widgets WHERE id = 1`).Scan(&name); err != nil || name != "sprocket" {
		t.Fatalf("the app's statement through the proxy: %q, %v", name, err)
	}
	app.Close(ctx)

	// ---- The editor reads the table through the box.
	status, body = api("POST", base+"/rows", `{"schema": "`+schema+`", "table": "widgets"}`)
	if status != 200 || !strings.Contains(body, `"rows":[[1,"sprocket"],[2,"flange"]]`) || !strings.Contains(body, `"primary_key":["id"]`) {
		t.Fatalf("rows through the box: %d %s", status, body)
	}
	if status, body = api("GET", base+"/schema", ""); status != 200 || !strings.Contains(body, `"name":"`+schema+`"`) || !strings.Contains(body, `"name":"widgets"`) {
		t.Fatalf("the schema through the box: %d", status)
	}
	status, body = api("POST", base+"/query", `{"sql": "SELECT count(*) AS editor_only_shape FROM `+schema+`.widgets"}`)
	if status != 200 || !strings.Contains(body, `"rows":[[2]]`) {
		t.Fatalf("a statement through the box: %d %s", status, body)
	}
	// The app's own Authorization is not the helper's token, and is not
	// what reaches the helper.
	status, _ = helperCall(t, c, "GET", "/v1/helpers/database/api/api/db/connections", nil, http.Header{"Authorization": {"Bearer from-the-app"}})
	if status != 200 {
		t.Fatalf("with an Authorization of the app's own: %d", status)
	}

	// ---- Asked directly, without the token, the helper refuses.
	direct, ok := h.API("database")
	if !ok {
		t.Fatal("berth-database is not running")
	}
	ask := func(method, path, token, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, method, direct.String()+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/db/connections", ""},
		{"GET", "/api" + base + "/schema", ""},
		{"POST", "/api" + base + "/rows", `{"schema": "` + schema + `", "table": "widgets"}`},
		{"POST", "/api" + base + "/query", `{"sql": "SELECT 1"}`},
		{"POST", "/api" + base + "/edit", `{"schema": "` + schema + `", "table": "widgets", "changes": [{"op": "delete", "key": {"id": 1}}]}`},
		{"POST", "/api" + base + "/writes", `{"on": true}`},
		{"POST", "/api/attach", `{"project": "acme", "env": {}}`},
		{"POST", "/api/detach", `{"project": "acme", "worktree": "fix-orders"}`},
	} {
		for _, token := range []string{"", "not-the-token"} {
			if status, body := ask(tc.method, tc.path, token, tc.body); status != 401 || !strings.Contains(body, `"code":"unauthorized"`) {
				t.Errorf("%s %s asked of the helper directly with token %q: %d %s", tc.method, tc.path, token, status, body)
			}
		}
	}
	if status, body := ask("GET", "/api/health", "", ""); status != 200 || !strings.Contains(body, `"name":"berth-database"`) {
		t.Errorf("its health, which needs no token: %d %s", status, body)
	}
	// Its ready file, with the token, is this user's alone.
	ready := filepath.Join(h.Dir, "database", "data", "helper.json")
	if info, err := os.Stat(ready); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the ready file: %v", err)
	}

	// ---- Writes are off until they are turned on, through the box.
	edit := `{"schema": "` + schema + `", "table": "widgets", "changes": [
		{"op": "update", "key": {"id": 2}, "set": {"name": "gasket-s3cret"}}, {"op": "insert", "set": {"id": 3, "name": "washer"}}]}`
	if status, body = api("POST", base+"/edit", edit); status != 403 || !strings.Contains(body, `"code":"read_only"`) {
		t.Fatalf("an edit with writes off: %d %s", status, body)
	}
	if status, body = api("POST", base+"/query", `{"sql": "DELETE FROM `+schema+`.widgets"}`); status != 403 || !strings.Contains(body, `"code":"read_only"`) {
		t.Fatalf("a delete with writes off: %d %s", status, body)
	}
	if status, body = api("POST", base+"/writes", `{"on": true}`); status != 200 || !strings.Contains(body, `"writes_until":`) {
		t.Fatalf("writes on: %d %s", status, body)
	}
	if status, body = api("POST", base+"/edit", edit); status != 200 || body != `{"updated":1,"inserted":1,"deleted":0}`+"\n" {
		t.Fatalf("an edit with writes on: %d %s", status, body)
	}
	var names string
	if err := admin.QueryRow(ctx, `SELECT string_agg(name, ',' ORDER BY id) FROM `+schema+`.widgets`).Scan(&names); err != nil || names != "sprocket,gasket-s3cret,washer" {
		t.Fatalf("the table after the edit: %q, %v", names, err)
	}

	// ---- Nothing the editor ran is among the app's queries: Leadline saw
	// the one connection and the one statement that were the app's.
	type query struct {
		Fingerprint string `json:"fingerprint"`
		Calls       int    `json:"calls"`
	}
	var queries []query
	listed := func() bool {
		status, body = helperCall(t, c, "GET", "/v1/helpers/leadline/api/api/queries?source=acme%2Ffix-orders", nil, nil)
		queries = nil
		return status == 200 && json.Unmarshal([]byte(body), &queries) == nil && len(queries) > 0
	}
	waitUntil(t, "the app's query to be listed", 30*time.Second, listed)
	// Long enough for anything else the proxy had seen to be stored too.
	time.Sleep(1500 * time.Millisecond)
	listed()
	if len(queries) != 1 || queries[0].Fingerprint != "SELECT name FROM "+schema+".widgets WHERE id = ?" || queries[0].Calls != 1 {
		t.Fatalf("Leadline's queries for the worktree = %s", body)
	}
	for _, control := range []string{"", "&control=1"} {
		_, body = helperCall(t, c, "GET", "/v1/helpers/leadline/api/api/queries?source=acme%2Ffix-orders"+control, nil, nil)
		for _, editors := range []string{"editor_only_shape", "pg_class", "pg_namespace", "gasket", "statement_timeout", "READ ONLY", "UPDATE", "INSERT"} {
			if strings.Contains(body, editors) {
				t.Errorf("Leadline recorded a statement of the editor's (%s): %s", editors, body)
			}
		}
	}
	var proxies struct {
		Proxies []struct {
			ConnectionsTotal int64 `json:"connections_total"`
			Statements       int64 `json:"statements"`
		}
	}
	status, body = helperCall(t, c, "GET", "/v1/helpers/leadline/api/api/proxies?source=acme%2Ffix-orders", nil, nil)
	if status != 200 || json.Unmarshal([]byte(body), &proxies) != nil || len(proxies.Proxies) != 1 ||
		proxies.Proxies[0].ConnectionsTotal != 1 || proxies.Proxies[0].Statements != 1 {
		t.Fatalf("Leadline's proxy saw more than the app's one connection and statement: %s", body)
	}

	// ---- The helper's log has the edit's shape, and nothing it held.
	log, err := h.Tail("database", 1<<20)
	if err != nil || !strings.Contains(string(log), "msg=edit ") && !strings.Contains(string(log), `INFO edit `) {
		t.Fatalf("the helper's log has no line for the edit: %v\n%s", err, log)
	}
	token := ""
	if raw, err := os.ReadFile(ready); err == nil {
		var doc struct{ Token string }
		json.Unmarshal(raw, &doc)
		token = doc.Token
	}
	if token == "" {
		t.Fatal("the ready file has no token")
	}
	for what, secret := range map[string]string{"the database's password": password, "a cell's value": "gasket-s3cret", "its token": token, "a database URL": "postgres://"} {
		if secret != "" && strings.Contains(string(log), secret) {
			t.Errorf("the helper's log holds %s:\n%s", what, log)
		}
	}

	// ---- Kept across a restart of the helper, by its file; and the file,
	// with the password in it, goes when the helper is removed with its data.
	saved := filepath.Join(h.Dir, "database", "data", "connections.json")
	if info, err := os.Stat(saved); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("connections.json: %v", err)
	}
	was := h.Status("database").PID
	signalProcess(was, syscall.SIGTERM)
	waitUntil(t, "berth-database to be started again", 30*time.Second, func() bool {
		st := h.Status("database")
		return st.State == HelperRunning && st.PID != was
	})
	again, _ := h.API("database")
	if again.Host != direct.Host {
		t.Errorf("it came back at %s, was at %s", again.Host, direct.Host)
	}
	// The token it had is not the one it has: the old one opens nothing.
	if status, _ := ask("GET", "/api/db/connections", token, ""); status != 401 {
		t.Errorf("the token of the helper before the restart still works: %d", status)
	}
	if status, body = api("POST", base+"/rows", `{"schema": "`+schema+`", "table": "widgets", "limit": 1}`); status != 200 || !strings.Contains(body, `"rows":[[1,"sprocket"]]`) {
		t.Fatalf("rows after the helper restarted: %d %s", status, body)
	}
	// Writes did not come back on with it.
	if status, body = api("POST", base+"/edit", edit); status != 403 {
		t.Fatalf("an edit after the helper restarted: %d %s", status, body)
	}
	if status, body = helperCall(t, c, "DELETE", "/v1/helpers/database?purge=1", nil, nil); status != 200 {
		t.Fatalf("removing the helper with its data: %d %s", status, body)
	}
	if exists(saved) {
		t.Fatal("the removed helper's connections, with their passwords, are still on disk")
	}
}
