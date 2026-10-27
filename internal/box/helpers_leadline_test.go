package box

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/cosscom/shipyard/internal/service"
)

// standInPostgres is a Postgres that lives in the test: it lets anyone in
// and answers every statement with one row, which is all a recording proxy
// in front of it needs.
func standInPostgres(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	serve := func(c net.Conn) {
		defer c.Close()
		be := pgproto3.NewBackend(c, c)
		first, err := be.ReceiveStartupMessage()
		if err != nil {
			return
		}
		if _, ok := first.(*pgproto3.StartupMessage); !ok {
			t.Errorf("stand-in postgres: the connection opened with %T", first)
			return
		}
		be.Send(&pgproto3.AuthenticationOk{})
		for k, v := range map[string]string{"server_version": "17.0 (stand-in)", "client_encoding": "UTF8", "standard_conforming_strings": "on"} {
			be.Send(&pgproto3.ParameterStatus{Name: k, Value: v})
		}
		be.Send(&pgproto3.BackendKeyData{ProcessID: 4242, SecretKey: []byte{1, 2, 3, 4}})
		be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
		if be.Flush() != nil {
			return
		}
		for {
			msg, err := be.Receive()
			if err != nil {
				return
			}
			switch msg.(type) {
			case *pgproto3.Terminate:
				return
			case *pgproto3.Query:
				be.Send(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("name"), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1}}})
				be.Send(&pgproto3.DataRow{Values: [][]byte{[]byte("Acme customer")}})
				be.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
				be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				if be.Flush() != nil {
					return
				}
			default:
				t.Errorf("stand-in postgres: unexpected %T", msg)
				return
			}
		}
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c)
		}
	}()
	return ln.Addr().String()
}

// The whole of it, with the real berth-leadline: the helper is installed
// through the box's API as the laptop agent installs it, a worktree's
// service is started and comes up pointed at it, the service's database
// statement goes through the proxy the helper opened, and the query is read
// back through the route the app's panel uses.
func TestLeadlineIsInstalledAttachesAServiceAndIsReadThroughTheBox(t *testing.T) {
	if testing.Short() {
		t.Skip("builds berth-leadline")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("helpers run on Linux and macOS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	built := filepath.Join(t.TempDir(), "berth-leadline")
	// Built as these tests were: with the race detector it is the build the
	// helper's own tests already made, and nothing is compiled twice.
	args := []string{"build", "-o", built}
	if raceBuild {
		args = append(args, "-race")
	}
	build := exec.CommandContext(ctx, "go", append(args, "../../cmd/berth-leadline")...)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building berth-leadline: %v\n%s", err, out)
	}
	program, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}

	// One port for the helper's proxies that nothing else has: its default
	// range is the same on every machine, and a test takes no fixed port.
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
	database := standInPostgres(t)
	_, dbPort, _ := net.SplitHostPort(database)
	databaseURL := "postgres://app:hunter2@localhost:" + dbPort + "/acme?sslmode=disable"
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

	// ---- Installed through the box's API, with the manifest's words.
	spec := HelperSpec{
		Name: "berth-leadline", Version: "0.1.0",
		Args:  []string{"serve", "--dir", "{dir}", "--addr", "127.0.0.1:0", "--proxy-ports", fmt.Sprintf("%d-%d", proxyPort, proxyPort)},
		Ready: "helper.json", Attach: "/api/attach",
		AttachEnv: []string{"DATABASE_URL", "POSTGRES_URL", "OTEL_*"},
		SHA256:    sha256Hex(program), Size: int64(len(program)),
	}
	status, body := helperCall(t, c, "PUT", "/v1/helpers/leadline", program, specHeader(spec))
	if status != 200 || !strings.Contains(body, `"state":"running"`) {
		log, _ := h.Tail("leadline", 16<<10)
		t.Fatalf("install: %d %s\n%s", status, body, log)
	}
	api, ok := h.API("leadline")
	if !ok {
		t.Fatal("berth-leadline is not running")
	}
	status, body = helperCall(t, c, "GET", "/v1/helpers/leadline/api/api/health", nil, nil)
	if status != 200 || !strings.Contains(body, `"name":"berth-leadline"`) {
		t.Fatalf("its health through the box: %d %s", status, body)
	}

	// ---- A worktree's service starts: berthd asks the helper first.
	status, body = helperCall(t, c, "POST", "/v1/locations/acme/worktrees/fix-orders/services/web/start", nil, nil)
	if status != 200 {
		t.Fatalf("starting the service: %d %s", status, body)
	}
	env := units.env(serviceUnit("acme", "fix-orders", "web"))
	proxied := fmt.Sprintf("postgres://app:hunter2@127.0.0.1:%d/acme?sslmode=disable", proxyPort)
	if env["DATABASE_URL"] != proxied {
		t.Fatalf("the service's DATABASE_URL does not go through the helper's proxy (%d bytes, want %d)", len(env["DATABASE_URL"]), len(proxied))
	}
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://"+api.Host || env["OTEL_EXPORTER_OTLP_PROTOCOL"] != "http/protobuf" ||
		env["OTEL_SERVICE_NAME"] != "acme/fix-orders:web" || !strings.Contains(env["OTEL_RESOURCE_ATTRIBUTES"], "shipyard.worktree=fix-orders") ||
		!strings.Contains(env["OTEL_RESOURCE_ATTRIBUTES"], "shipyard.box=devbox") {
		t.Fatalf("the service's telemetry is not pointed at the helper: endpoint %q, service %q, resource %q",
			env["OTEL_EXPORTER_OTLP_ENDPOINT"], env["OTEL_SERVICE_NAME"], env["OTEL_RESOURCE_ATTRIBUTES"])
	}
	if env["BERTH_WORKTREE_NAME"] != "fix-orders" || env["PORT"] == "" {
		t.Fatalf("the service lost its own environment: %v", env["BERTH_WORKTREE_NAME"])
	}

	// ---- The dev server runs a statement, with the URL it was given.
	cfg, err := pgx.ParseConfig(env["DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting through the proxy: %v", err)
	}
	var name string
	if err := conn.QueryRow(ctx, "SELECT name FROM customers WHERE id = 7").Scan(&name); err != nil || name != "Acme customer" {
		t.Fatalf("a statement through the proxy: %q, %v", name, err)
	}
	conn.Close(ctx)

	// ---- Read back the way the app's panel reads: through the box.
	type query struct {
		Fingerprint string `json:"fingerprint"`
		Calls       int    `json:"calls"`
	}
	var queries []query
	waitUntil(t, "the query to be listed", 30*time.Second, func() bool {
		status, body = helperCall(t, c, "GET", "/v1/helpers/leadline/api/api/queries?source=acme%2Ffix-orders", nil, nil)
		queries = nil
		return status == 200 && json.Unmarshal([]byte(body), &queries) == nil && len(queries) > 0
	})
	if len(queries) != 1 || queries[0].Fingerprint != "SELECT name FROM customers WHERE id = ?" || queries[0].Calls != 1 {
		t.Fatalf("queries read through the box = %s", body)
	}
	var sources struct {
		Sources []struct {
			Name, Project, Worktree, Box string
			Via                          []string
		}
	}
	status, body = helperCall(t, c, "GET", "/v1/helpers/leadline/api/api/sources", nil, nil)
	if status != 200 || json.Unmarshal([]byte(body), &sources) != nil || len(sources.Sources) != 1 {
		t.Fatalf("sources through the box: %d %s", status, body)
	}
	if s := sources.Sources[0]; s.Name != "acme/fix-orders" || s.Project != "acme" || s.Worktree != "fix-orders" || s.Box != "devbox" {
		t.Fatalf("the worktree's source = %+v", s)
	}

	// The helper's own errors come back as it wrote them, status and body:
	// its envelope, not Shipyard's.
	status, body = helperCall(t, c, "GET", "/v1/helpers/leadline/api/api/traces/00000000000000000000000000000000", nil, nil)
	var problem struct {
		Error struct{ Code, Message, Hint string }
	}
	if status != 404 || json.Unmarshal([]byte(body), &problem) != nil || problem.Error.Code != "not_found" || problem.Error.Message == "" {
		t.Fatalf("the helper's own error through the box: %d %s", status, body)
	}

	// Its log holds no statement's values and no database URL.
	log, err := h.Tail("leadline", 1<<20)
	if err != nil || !strings.Contains(string(log), "listening") {
		t.Fatalf("the helper's log: %v\n%s", err, log)
	}
	if strings.Contains(string(log), "hunter2") {
		t.Fatalf("the helper's log holds the database's password:\n%s", log)
	}

	// ---- Turned off, with its data kept, the service starts as it would have.
	// The answer names the service that still points at the helper.
	status, body = helperCall(t, c, "DELETE", "/v1/helpers/leadline", nil, nil)
	var removed HelperStatus
	if status != 200 || json.Unmarshal([]byte(body), &removed) != nil || removed.State != HelperOff {
		t.Fatalf("removing the helper: %d %s", status, body)
	}
	if len(removed.Attached) != 1 || removed.Attached[0] != (HelperService{Location: "acme", Worktree: "fix-orders", Service: "web"}) {
		t.Fatalf("the services the removed helper was attached to = %+v", removed.Attached)
	}
	if !exists(filepath.Join(h.Dir, "leadline", "data", "leadline.db")) {
		t.Fatal("the helper's database went with it")
	}
	if status, body = helperCall(t, c, "POST", "/v1/locations/acme/worktrees/fix-orders/services/web/restart", nil, nil); status != 200 {
		t.Fatalf("restarting the service: %d %s", status, body)
	}
	if env = units.env(serviceUnit("acme", "fix-orders", "web")); env["DATABASE_URL"] != databaseURL || env["OTEL_SERVICE_NAME"] != "" {
		t.Fatal("with the helper off the service did not get the project's own environment back")
	}
}
