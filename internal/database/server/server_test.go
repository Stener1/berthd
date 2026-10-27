package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/database"
)

const fakeURL = "fake://app:hunter2@localhost:5432/acme"

func TestEveryRouteButHealthNeedsTheToken(t *testing.T) {
	r := newRig(t, "")
	id := r.attach(map[string]string{"DATABASE_URL": fakeURL})
	_, served := r.srv.handler()
	if len(served) < 9 {
		t.Fatalf("only %d routes: %v", len(served), served)
	}
	for _, pattern := range served {
		method, path, _ := strings.Cut(pattern, " ")
		path = strings.ReplaceAll(path, "{conn}", id)
		open := pattern == "GET /api/health"
		for name, headers := range map[string]map[string]string{
			"no token":             {},
			"another token":        {"Authorization": "Bearer " + testToken + "x"},
			"the start of it":      {"Authorization": "Bearer " + testToken[:8]},
			"the token as a login": {"Authorization": "Basic " + testToken},
			"the token bare":       {"Authorization": testToken},
			"an empty one":         {"Authorization": "Bearer "},
		} {
			status, body := r.send(method, path, `{}`, headers)
			if open {
				if status != 200 {
					t.Errorf("%s with %s: %d %s", pattern, name, status, body)
				}
				continue
			}
			if status != http.StatusUnauthorized || problemCode(body) != "unauthorized" {
				t.Errorf("%s with %s: %d %s", pattern, name, status, body)
			}
		}
		// With it, the route is reached: whatever it says, it is not "who are you".
		if status, body := r.call(method, path, `{}`); status == http.StatusUnauthorized || status == http.StatusForbidden && problemCode(body) == "forbidden" {
			t.Errorf("%s with the token: %d %s", pattern, status, body)
		}
		// The token does not make a page a caller, or another host this one.
		for name, headers := range map[string]map[string]string{
			"a page on this machine": {"Origin": "http://localhost:3000"},
			"Shipyard's own window":  {"Origin": "tauri://localhost"},
			"a page somewhere":       {"Origin": "https://evil.example"},
			"a rebound name":         {"Host": "evil.example:4326"},
		} {
			headers["Authorization"] = "Bearer " + testToken
			if status, body := r.send(method, path, `{}`, headers); status != http.StatusForbidden || problemCode(body) != "forbidden" {
				t.Errorf("%s from %s: %d %s", pattern, name, status, body)
			}
		}
	}
	if status, body := r.send("OPTIONS", "/api/db/connections", nil, map[string]string{"Origin": "http://localhost:1420", "Access-Control-Request-Method": "GET"}); status != http.StatusForbidden {
		t.Errorf("a preflight: %d %s", status, body)
	}
	status, body := r.call("GET", "/api/nothing", nil)
	if status != 404 || problemCode(body) != "not_found" {
		t.Errorf("a route there is not: %d %s", status, body)
	}
	if status, body = r.send("GET", "/api/health", nil, nil); !strings.Contains(body, `"name":"berth-database"`) || !strings.Contains(body, `"engines":["fake","frozen"]`) {
		t.Errorf("health: %d %s", status, body)
	}
	if _, err := New(Options{Registry: database.NewRegistry()}); err == nil {
		t.Error("a server with no token was made")
	}
}

func TestAttachNotesLocalDatabasesAndSetsNothing(t *testing.T) {
	r := newRig(t, "")
	status, body := r.call("POST", "/api/attach", AttachRequest{Box: "devbox", Project: "acme", Worktree: "fix-orders", Branch: "fix/orders", Command: "web",
		Env: map[string]string{
			"DATABASE_URL": fakeURL,
			"POSTGRES_URL": fakeURL,
			"REMOTE_URL":   "fake://app:far-away-pw@db.acme.example:5432/prod",
			"OTHER_ENGINE": "mysql://app:mysql-pw@localhost/acme",
			"BROKEN":       "fake://",
			"NOT_A_URL":    "hello",
			"EMPTY":        "",
		}})
	var attached Attached
	if status != 200 || json.Unmarshal([]byte(body), &attached) != nil {
		t.Fatalf("attach: %d %s", status, body)
	}
	if len(attached.Env) != 0 || !strings.Contains(body, `"env":{}`) || attached.Source != "acme/fix-orders" || len(attached.Connections) != 1 {
		t.Fatalf("attach answered %s", body)
	}
	id := attached.Connections[0]
	status, body = r.call("GET", "/api/db/connections?source=acme%2Ffix-orders", nil)
	want := `{"connections":[{"id":"` + id + `","source":"acme/fix-orders","label":"DATABASE_URL","engine":"fake","database":"acme","host":"localhost","port":5432,"user":"app",` +
		`"capabilities":{"dialect":"fakeql","schemas":false,"operators":["=","is_null"],"kinds":["table"],"writes":true,"transactions":false}}]}` + "\n"
	if status != 200 || body != want {
		t.Fatalf("connections: %d %s\nwant %s", status, body, want)
	}
	for _, secret := range []string{"hunter2", "far-away-pw", "mysql-pw", "fake://"} {
		if strings.Contains(body, secret) || strings.Contains(r.log.String(), secret) {
			t.Errorf("%q is in an answer or in the log", secret)
		}
	}
	if !strings.Contains(r.log.String(), "variable=REMOTE_URL") || !strings.Contains(r.log.String(), "not on this machine") {
		t.Errorf("the log does not say why a variable was passed over:\n%s", r.log)
	}
	if _, body = r.call("GET", "/api/db/connections?source=acme%2Ffix-orders-2", nil); body != `{"connections":[]}`+"\n" {
		t.Errorf("another worktree's connections: %s", body)
	}
	// Asking again changes nothing, and the id is the same.
	if again := r.attach(map[string]string{"DATABASE_URL": fakeURL}); again != id {
		t.Fatalf("attached again: %s, was %s", again, id)
	}

	// A second command of the worktree has the same database: one
	// connection. The main checkout is a source of its own.
	attach := func(worktree, command string, env map[string]string) Attached {
		t.Helper()
		status, body := r.call("POST", "/api/attach", AttachRequest{Project: "acme", Worktree: worktree, Command: command, Env: env})
		var out Attached
		if status != 200 || json.Unmarshal([]byte(body), &out) != nil {
			t.Fatalf("attach: %d %s", status, body)
		}
		return out
	}
	ids := func(source string) string {
		t.Helper()
		_, body := r.call("GET", "/api/db/connections?source="+source, nil)
		var out Connections
		json.Unmarshal([]byte(body), &out)
		var got []string
		for _, c := range out.Connections {
			got = append(got, c.Label+"="+c.Database)
		}
		return strings.Join(got, " ")
	}
	if out := attach("fix-orders", "worker", map[string]string{"DATABASE_URL": fakeURL}); len(out.Connections) != 1 || out.Connections[0] != id {
		t.Fatalf("a second command: %+v", out)
	}
	if out := attach("", "web", map[string]string{"DATABASE_URL": fakeURL}); out.Source != "acme" || out.Connections[0] == id {
		t.Fatalf("the main checkout: %+v", out)
	}
	// Opened, so that forgetting it has something to close.
	if status, _ := r.call("GET", "/api/db/"+id+"/schema", nil); status != 200 {
		t.Fatal("the schema could not be read")
	}
	opened := r.fake.last()
	// web moves to another database: the old one is still worker's.
	attach("fix-orders", "web", map[string]string{"DATABASE_URL": "fake://app:hunter2@localhost:5432/acme_next"})
	if got := ids("acme%2Ffix-orders"); got != "DATABASE_URL=acme DATABASE_URL=acme_next" || opened.isClosed() {
		t.Fatalf("after web moved: %s (closed %v)", got, opened.isClosed())
	}
	// worker starts with no database at all: nobody has the old one now.
	attach("fix-orders", "worker", map[string]string{})
	if got := ids("acme%2Ffix-orders"); got != "DATABASE_URL=acme_next" || !opened.isClosed() {
		t.Fatalf("after worker let go: %s (closed %v)", got, opened.isClosed())
	}
	if status, body = r.call("GET", "/api/db/"+id+"/schema", nil); status != 404 || problemCode(body) != "not_found" {
		t.Fatalf("a forgotten connection: %d %s", status, body)
	}
	if got := ids("acme"); got != "DATABASE_URL=acme" {
		t.Fatalf("the main checkout's: %s", got)
	}
	// The password changed: the same connection, opened afresh.
	next := attach("fix-orders", "web", map[string]string{"DATABASE_URL": "fake://app:hunter2@localhost:5432/acme_next"}).Connections[0]
	r.call("GET", "/api/db/"+next+"/schema", nil)
	before := r.fake.last()
	if out := attach("fix-orders", "web", map[string]string{"DATABASE_URL": "fake://app:rotated@localhost:5432/acme_next"}); out.Connections[0] != next {
		t.Fatalf("a new password made a new connection: %+v", out)
	}
	r.call("GET", "/api/db/"+next+"/schema", nil)
	if !before.isClosed() || r.fake.last().url != "fake://app:rotated@localhost:5432/acme_next" {
		t.Fatal("a new password was not taken up")
	}
	if status, body = r.call("POST", "/api/attach", `{"worktree": "fix-orders"}`); status != 400 || problemCode(body) != "bad_request" {
		t.Errorf("attach with no project: %d %s", status, body)
	}
}

func TestConnectionsAreKeptAcrossARestartAndGoOnDetach(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, dir)
	id := r.attach(map[string]string{"DATABASE_URL": fakeURL})
	file := filepath.Join(dir, ConnectionsFile)
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%s: %v, mode %v", ConnectionsFile, err, info.Mode().Perm())
	}
	if raw, _ := os.ReadFile(file); !strings.Contains(string(raw), fakeURL) {
		t.Fatalf("the file does not hold the connection: %s", raw)
	}
	// A file left at the temporary name, open to all, is not what the
	// next save becomes.
	os.WriteFile(file+".tmp", []byte("x"), 0o644)
	r.call("POST", "/api/db/"+id+"/writes", `{"on": true}`)

	r.start()
	status, body := r.call("GET", "/api/db/connections", nil)
	if status != 200 || !strings.Contains(body, `"id":"`+id+`"`) || !strings.Contains(body, `"label":"DATABASE_URL"`) {
		t.Fatalf("after a restart: %d %s", status, body)
	}
	if strings.Contains(body, "writes_until") {
		t.Fatal("writes were still on after a restart")
	}
	r.attach(map[string]string{"DATABASE_URL": fakeURL})
	if info, _ = os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Fatalf("the file became %v", info.Mode().Perm())
	}
	r.call("GET", "/api/db/"+id+"/schema", nil)
	opened := r.fake.last()

	for _, how := range []string{
		`{"project": "acme", "worktree": "fix-orders"}`,
		`{"service": "acme/fix-orders:web", "keep_data": false}`,
		`{"source": "acme/fix-orders"}`,
	} {
		r.attach(map[string]string{"DATABASE_URL": fakeURL})
		status, body = r.call("POST", "/api/detach", how)
		if status != 200 || body != `{"source":"acme/fix-orders","found":true,"connections":1}`+"\n" {
			t.Fatalf("detach %s: %d %s", how, status, body)
		}
		if raw, _ := os.ReadFile(file); strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "acme") {
			t.Fatalf("after detach %s the file still holds the connection: %s", how, raw)
		}
	}
	if !opened.isClosed() {
		t.Fatal("a detached worktree's connections were left open")
	}
	if status, body = r.call("POST", "/api/detach", `{"project": "acme", "worktree": "fix-orders"}`); status != 200 || !strings.Contains(body, `"found":false`) {
		t.Fatalf("detach again: %d %s", status, body)
	}
	if status, body = r.call("POST", "/api/detach", `{}`); status != 400 {
		t.Fatalf("detach of nothing: %d %s", status, body)
	}
	if status, body = r.call("GET", "/api/db/"+id+"/schema", nil); status != 404 {
		t.Fatalf("a detached connection: %d %s", status, body)
	}

	// A saved entry that is no longer one the helper opens is dropped,
	// not opened: the rule is applied again on the way in.
	os.WriteFile(file, []byte(`{"connections":[
		{"id":"c_far","source":"acme/x","engine":"fake","url":"fake://app:pw@db.acme.example:5432/prod","claims":[{"command":"web","var":"DATABASE_URL"}]},
		{"id":"c_gone","source":"acme/x","engine":"gone","url":"gone://app:pw@localhost/acme","claims":[{"command":"web","var":"DATABASE_URL"}]},
		{"id":"c_old","source":"acme/x","engine":"fake","url":"fake://app:pw@localhost:5432/kept","claims":[{"command":"web","var":"DATABASE_URL"}]}]}`), 0o600)
	r.start()
	if _, body = r.call("GET", "/api/db/connections", nil); strings.Count(body, `"id"`) != 1 || !strings.Contains(body, `"database":"kept"`) || strings.Contains(body, "c_old") {
		t.Fatalf("read back from a file: %s", body)
	}
	os.WriteFile(file, []byte(`not json`), 0o600)
	if _, err := New(Options{Registry: database.NewRegistry(r.fake), Token: testToken, Dir: dir}); err == nil || strings.Contains(err.Error(), "not json") {
		t.Fatalf("a file that is not the helper's: %v", err)
	}
}

func TestWritesAreOffUntilTurnedOnAndGoOffAgain(t *testing.T) {
	r := newRig(t, "")
	// Two databases of one command: each has its own switch.
	status, body := r.call("POST", "/api/attach", AttachRequest{Project: "acme", Worktree: "fix-orders", Command: "web",
		Env: map[string]string{"DATABASE_URL": fakeURL, "SHADOW_DATABASE_URL": "fake://app:hunter2@localhost:5432/shadow"}})
	var attached Attached
	if status != 200 || json.Unmarshal([]byte(body), &attached) != nil || len(attached.Connections) != 2 {
		t.Fatalf("attach: %d %s", status, body)
	}
	id, other := attached.Connections[0], attached.Connections[1]
	base := "/api/db/" + id
	edit := `{"table": "things", "changes": [{"op": "insert", "set": {"v": "s3cret-cell"}}]}`

	// Off: a statement is told it may not write, and an edit is refused
	// before the database hears of it. Nothing in a request turns it on.
	status, body = r.call("POST", base+"/query", `{"sql": "write things", "write": true, "Write": true, "writes": true}`)
	mine := r.fake.last()
	if status != 403 || problemCode(body) != "read_only" || mine.lastQuery().Write {
		t.Fatalf("a write with writes off: %d %s", status, body)
	}
	if status, body = r.call("POST", base+"/edit", edit); status != 403 || problemCode(body) != "read_only" || len(mine.edits) != 0 {
		t.Fatalf("an edit with writes off: %d %s", status, body)
	}
	if status, body = r.call("POST", base+"/query", `{"sql": "read things"}`); status != 200 || !strings.Contains(body, `"rows":[["read things",false]]`) {
		t.Fatalf("a read with writes off: %d %s", status, body)
	}

	until := r.clock.Add(WritesFor).UnixMilli()
	if status, body = r.call("POST", base+"/writes", `{"on": true}`); status != 200 || body != fmt.Sprintf(`{"writes_until":%d}`+"\n", until) {
		t.Fatalf("writes on: %d %s", status, body)
	}
	if _, body = r.call("GET", "/api/db/connections", nil); !strings.Contains(body, fmt.Sprintf(`"writes_until":%d`, until)) {
		t.Fatalf("connections with writes on: %s", body)
	}
	if status, body = r.call("POST", base+"/query", `{"sql": "write things"}`); status != 200 || !strings.Contains(body, `"rows":[["write things",true]]`) {
		t.Fatalf("a write with writes on: %d %s", status, body)
	}
	if status, body = r.call("POST", base+"/edit", edit); status != 200 || body != `{"updated":0,"inserted":1,"deleted":0}`+"\n" {
		t.Fatalf("an edit with writes on: %d %s", status, body)
	}
	// Another connection's writes are its own.
	if status, body = r.call("POST", "/api/db/"+other+"/edit", edit); status != 403 {
		t.Fatalf("an edit on another connection: %d %s", status, body)
	}

	// A minute short of the quarter hour they are on; at it, off.
	r.clock = r.clock.Add(WritesFor - time.Minute)
	if status, _ = r.call("POST", base+"/edit", edit); status != 200 {
		t.Fatalf("an edit after 14 minutes: %d", status)
	}
	r.clock = r.clock.Add(time.Minute)
	if status, body = r.call("POST", base+"/edit", edit); status != 403 || problemCode(body) != "read_only" {
		t.Fatalf("an edit after 15 minutes: %d %s", status, body)
	}
	if status, body = r.call("POST", base+"/query", `{"sql": "write things"}`); status != 403 || mine.lastQuery().Write {
		t.Fatalf("a write after 15 minutes: %d %s", status, body)
	}
	if _, body = r.call("GET", "/api/db/connections", nil); strings.Contains(body, "writes_until") {
		t.Fatalf("connections after 15 minutes: %s", body)
	}
	// On again starts the quarter hour again; off is off at once.
	r.call("POST", base+"/writes", `{"on": true}`)
	r.clock = r.clock.Add(10 * time.Minute)
	if status, body = r.call("POST", base+"/writes", `{"on": true}`); body != fmt.Sprintf(`{"writes_until":%d}`+"\n", r.clock.Add(WritesFor).UnixMilli()) {
		t.Fatalf("writes on again: %d %s", status, body)
	}
	if status, body = r.call("POST", base+"/writes", `{"on": false}`); status != 200 || body != "{}\n" {
		t.Fatalf("writes off: %d %s", status, body)
	}
	if status, _ = r.call("POST", base+"/edit", edit); status != 403 {
		t.Fatalf("an edit after writes off: %d", status)
	}
	if status, body = r.call("POST", base+"/writes", `{}`); status != 400 {
		t.Fatalf("writes with nothing said: %d %s", status, body)
	}

	// A database that cannot be written cannot have writes turned on.
	frozen := r.attach(map[string]string{"ARCHIVE_URL": "frozen://app:hunter2@localhost:1/archive"})
	if status, body = r.call("POST", "/api/db/"+frozen+"/writes", `{"on": true}`); status != 400 || problemCode(body) != "bad_request" {
		t.Fatalf("writes on for a read-only engine: %d %s", status, body)
	}
	if status, body = r.call("POST", "/api/db/"+frozen+"/edit", edit); status != 400 {
		t.Fatalf("an edit on a read-only engine: %d %s", status, body)
	}
}

func TestAWriteIsLoggedByItsShapeNeverItsValues(t *testing.T) {
	r := newRig(t, "")
	id := r.attach(map[string]string{"DATABASE_URL": fakeURL})
	base := "/api/db/" + id
	r.call("POST", base+"/writes", `{"on": true}`)
	r.call("POST", base+"/edit", `{"table": "things", "changes": [
		{"op": "update", "key": {"k": 1}, "set": {"email": "jo@acme.test", "note": "s3cret-cell"}},
		{"op": "insert", "set": {"note": "an0ther-cell"}}, {"op": "delete", "key": {"k": "k3y-cell"}}]}`)
	r.call("POST", base+"/edit", `{"table": "things", "changes": [{"op": "delete", "key": {"k": 404}}]}`)
	r.call("POST", base+"/query", `{"sql": "write things with s3cret-cell and more"}`)
	r.call("POST", base+"/query", `{"sql": "explode s3cret-cell"}`)
	r.call("POST", base+"/rows", `{"table": "things", "filters": [{"column": "v", "op": "=", "value": "s3cret-cell"}]}`)
	log := r.log.String()
	for _, want := range []string{
		`msg=edit source=acme/fix-orders connection=` + id + ` table=[things] columns="[email], [note]" changes=3 updated=1 inserted=1 deleted=1`,
		`msg="edit: refused, nothing changed" source=acme/fix-orders connection=` + id + ` table=[things] columns="" changes=1 code=row_conflict`,
		`msg="statement with writes on" source=acme/fix-orders connection=` + id + ` shape="WRITE <5 words>" command=WRITE rows=1`,
		`msg="statement with writes on: refused" source=acme/fix-orders connection=` + id + ` shape="EXPLODE <1 words>" code=failed`,
		`msg="writes on" source=acme/fix-orders connection=` + id,
		`msg="request failed" kind=*errors.errorString`,
	} {
		if !strings.Contains(log, want) {
			t.Errorf("the log has no line with %s", want)
		}
	}
	for _, never := range []string{"s3cret-cell", "an0ther-cell", "k3y-cell", "jo@acme.test", "hunter2", testToken, "fake://"} {
		if strings.Contains(log, never) {
			t.Errorf("the log holds %q", never)
		}
	}
	if t.Failed() {
		t.Log(log)
	}
	// And the helper's own failure is not sent either.
	if status, body := r.call("POST", base+"/query", `{"sql": "explode s3cret-cell"}`); status != 500 || problemCode(body) != "failed" || strings.Contains(body, "s3cret") {
		t.Errorf("an engine's own error: %d %s", status, body)
	}
}

func TestRequestsAreCheckedBeforeAnEngineSeesThem(t *testing.T) {
	r := newRig(t, "")
	id := r.attach(map[string]string{"DATABASE_URL": fakeURL})
	base := "/api/db/" + id
	r.call("POST", base+"/writes", `{"on": true}`)

	// What is asked for is brought into range, by the server.
	if status, body := r.call("POST", base+"/rows", `{"table": "things"}`); status != 200 ||
		body != `{"columns":[{"name":"k","type":"number"},{"name":"v","type":"words"}],"rows":[[1,"one"],["9007199254740993","`+strings.Repeat("x", database.MaxCell)+
			`"]],"primary_key":["k"],"rows_estimate":2,"more":false,"cut":[[1,1]]}`+"\n" {
		t.Fatalf("rows: %d %s", status, body)
	}
	if got := r.fake.last().rows; got.Limit != database.DefaultRows || got.Offset != 0 {
		t.Errorf("the default page: %+v", got)
	}
	r.call("POST", base+"/rows", `{"table": "things", "limit": 100000, "offset": 40, "order_by": [{"column": "k", "desc": true}],
		"filters": [{"column": "v", "op": "=", "value": 7}, {"column": "v", "op": "is_null"}]}`)
	if got := r.fake.last().rows; got.Limit != database.MaxRows || got.Offset != 40 || !got.OrderBy[0].Desc || got.Filters[0].Value.Text != "7" || got.Filters[1].Value != nil {
		t.Errorf("a page asked for: %+v", got)
	}
	r.call("POST", base+"/query", `{"sql": "read", "limit": 999999}`)
	if got := r.fake.last().lastQuery(); got.Limit != database.MaxQueryRows {
		t.Errorf("a statement's limit: %d", got.Limit)
	}
	r.call("POST", base+"/query", `{"sql": "read"}`)
	if got := r.fake.last().lastQuery(); got.Limit != database.DefaultQueryRows {
		t.Errorf("a statement's default limit: %d", got.Limit)
	}

	seen := len(r.fake.last().queries) + len(r.fake.last().edits)
	for name, tc := range map[string]struct{ path, body string }{
		"no table":                      {"/rows", `{}`},
		"a negative limit":              {"/rows", `{"table": "things", "limit": -1}`},
		"a negative offset":             {"/rows", `{"table": "things", "offset": -1}`},
		"an operator it does not offer": {"/rows", `{"table": "things", "filters": [{"column": "v", "op": "like", "value": "x"}]}`},
		"an operator that is SQL":       {"/rows", `{"table": "things", "filters": [{"column": "v", "op": "= 1 OR 1", "value": "x"}]}`},
		"a comparison with nothing":     {"/rows", `{"table": "things", "filters": [{"column": "v", "op": "="}]}`},
		"a comparison with null":        {"/rows", `{"table": "things", "filters": [{"column": "v", "op": "=", "value": null}]}`},
		"is_null with a value":          {"/rows", `{"table": "things", "filters": [{"column": "v", "op": "is_null", "value": 1}]}`},
		"a filter on no column":         {"/rows", `{"table": "things", "filters": [{"op": "is_null"}]}`},
		"a sort on no column":           {"/rows", `{"table": "things", "order_by": [{"desc": true}]}`},
		"no statement":                  {"/query", `{"sql": "  "}`},
		"no changes":                    {"/edit", `{"table": "things", "changes": []}`},
		"an update with no key":         {"/edit", `{"table": "things", "changes": [{"op": "update", "set": {"v": 1}}]}`},
		"an update of nothing":          {"/edit", `{"table": "things", "changes": [{"op": "update", "key": {"k": 1}}]}`},
		"an insert with a key":          {"/edit", `{"table": "things", "changes": [{"op": "insert", "key": {"k": 1}, "set": {"v": 1}}]}`},
		"a delete with a set":           {"/edit", `{"table": "things", "changes": [{"op": "delete", "key": {"k": 1}, "set": {"v": 1}}]}`},
		"a null in a key":               {"/edit", `{"table": "things", "changes": [{"op": "delete", "key": {"k": null}}]}`},
		"an op there is not":            {"/edit", `{"table": "things", "changes": [{"op": "truncate"}]}`},
		"a body that is not JSON":       {"/query", `sql=read`},
		"a body of the wrong shape":     {"/rows", `{"table": 7}`},
	} {
		if status, body := r.call("POST", base+tc.path, tc.body); status != 400 || problemCode(body) != "bad_request" {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
	if got := len(r.fake.last().queries) + len(r.fake.last().edits); got != seen {
		t.Errorf("%d refused requests reached the engine", got-seen)
	}
	if status, body := r.call("POST", base+"/query", `{"sql": "`+strings.Repeat("x", maxBody)+`"}`); status != 413 {
		t.Errorf("a body past the limit: %d %s", status, body[:min(len(body), 200)])
	}
	if status, body := r.call("POST", base+"/edit", `{"table": "things", "changes": [{"op": "delete", "key": {"k": 404}}]}`); status != 409 || problemCode(body) != "row_conflict" {
		t.Errorf("a conflict: %d %s", status, body)
	}
	for _, path := range []string{"/api/db/c_nothing/schema", "/api/db/" + id + "x/schema", "/api/db/%2e%2e/schema"} {
		if status, body := r.call("GET", path, nil); status != 404 || problemCode(body) != "not_found" {
			t.Errorf("%s: %d %s", path, status, body)
		}
	}
	// One database is opened once, however many ask.
	if r.fake.opened() != 1 {
		t.Errorf("the database was opened %d times", r.fake.opened())
	}
	if r.fake.last().timeout != database.DefaultTimeout {
		t.Errorf("the engine was given a timeout of %s", r.fake.last().timeout)
	}
}

func TestTheHelperStartsStopsAndKeepsItsPort(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	fake := &fakeEngine{name: "fake", writes: true}
	start := func() *Helper {
		t.Helper()
		h, err := Start(HelperOptions{Dir: dir, Addr: "127.0.0.1:0", Registry: database.NewRegistry(fake)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { h.Close() })
		return h
	}
	h := start()
	state := h.State()
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the data directory: %v", err)
	}
	stateFile := filepath.Join(dir, StateFile)
	info, err := os.Stat(stateFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%s: %v", StateFile, err)
	}
	var onDisk State
	raw, _ := os.ReadFile(stateFile)
	if json.Unmarshal(raw, &onDisk) != nil || onDisk != state || len(state.Token) != 64 || !strings.HasPrefix(state.API, "http://127.0.0.1:") || state.PID != os.Getpid() {
		t.Fatalf("%s holds %s", StateFile, strings.ReplaceAll(string(raw), state.Token, "<token>"))
	}
	get := func(path, token string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", state.API+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b strings.Builder
		fmt.Fprint(&b, resp.Header.Get("Cache-Control"), " ")
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		b.Write(buf[:n])
		return resp.StatusCode, b.String()
	}
	if status, body := get("/api/health", ""); status != 200 || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("health: %d %s", status, body)
	}
	if status, _ := get("/api/db/connections", ""); status != 401 {
		t.Fatalf("connections with no token: %d", status)
	}
	if status, body := get("/api/db/connections", state.Token); status != 200 || !strings.HasPrefix(body, "no-store ") {
		t.Fatalf("connections with the token: %d %s", status, body)
	}
	h.Close()
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatal("a stopped helper left its ready file, with its token")
	}

	// Started again: the same port, and a token that is new.
	again := start()
	if again.State().API != state.API {
		t.Errorf("it came back at %s, was at %s", again.State().API, state.API)
	}
	if again.State().Token == state.Token {
		t.Error("the token outlived the helper")
	}
	state = again.State()
	if status, _ := get("/api/db/connections", h.State().Token); status != 401 {
		t.Errorf("the old token still opens the new helper: %d", status)
	}

	for _, addr := range []string{"0.0.0.0:0", "192.0.2.1:4326", ":4326", "nowhere"} {
		if _, err := Start(HelperOptions{Dir: dir, Addr: addr, Registry: database.NewRegistry(fake)}); err == nil {
			t.Errorf("it listened on %s", addr)
		}
	}
	if _, err := Start(HelperOptions{Addr: "127.0.0.1:0", Registry: database.NewRegistry(fake)}); err == nil {
		t.Error("it started with no data directory")
	}
}
