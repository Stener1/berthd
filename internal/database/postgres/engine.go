// Package postgres is the editor's Postgres engine: everything of
// berth-database that knows Postgres. It reads a database URL the way an
// app's driver would, keeps a few connections straight to that server, and
// answers the four things an engine is asked (database.Conn): the schema, a
// table's rows, one statement, and a batch of row edits.
//
// It speaks to the server through pgconn, pgx's connection without pgx's
// own type handling: every value comes back as the text Postgres prints,
// and goes in as text Postgres casts to the column's type. That is what the
// API promises a cell is, and it leaves nothing to keep in step with the
// server's types.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cosscom/shipyard/internal/database"
)

// Engine is the Postgres engine. Its zero value is ready to register.
type Engine struct{}

func (Engine) Name() string      { return "postgres" }
func (Engine) Schemes() []string { return []string{"postgres", "postgresql"} }

func (Engine) Capabilities() database.Capabilities {
	return database.Capabilities{
		Dialect:      "postgresql",
		Schemas:      true,
		Operators:    append([]string(nil), database.Operators...),
		Kinds:        []string{database.KindTable, database.KindView, database.KindMaterializedView},
		Writes:       true,
		Transactions: true,
	}
}

// QuoteIdent writes a name as a quoted identifier: whatever it holds
// (capitals, spaces, a reserved word, a quote) it names that one thing.
func (Engine) QuoteIdent(name string) string { return quote(name) }

func quote(name string) string {
	// A zero byte cannot be in an identifier, and would end the statement
	// for the server where it stands.
	name = strings.ReplaceAll(name, "\x00", "")
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (Engine) Shape(sql string) string { return shape(sql) }

func (Engine) Target(raw string) (database.Target, error) {
	cfg, err := config(raw)
	if err != nil {
		return database.Target{}, err
	}
	return target(cfg), nil
}

func (Engine) Open(raw string, o database.Options) (database.Conn, error) {
	cfg, err := config(raw)
	if err != nil {
		return nil, err
	}
	if o.Timeout <= 0 {
		o.Timeout = database.DefaultTimeout
	}
	if o.MaxConns <= 0 {
		o.MaxConns = 4
	}
	return &conn{pool: newPool(cfg, o.MaxConns), timeout: o.Timeout}, nil
}

func target(cfg *pgconn.Config) database.Target {
	t := database.Target{Host: cfg.Host, Port: int(cfg.Port), Socket: strings.HasPrefix(cfg.Host, "/"),
		Database: cfg.Database, User: cfg.User}
	if t.Database == "" {
		// What the server does with no database named.
		t.Database = t.User
	}
	return t
}

// errURL is all that is ever said of a URL that cannot be read: the URL
// has a password in it, and so may anything that quotes it.
var errURL = errors.New("it is not a Postgres URL the editor can read")

// config reads a database URL as written for an app: postgres:// or
// postgresql://, with a driver suffix or a jdbc: prefix, the user and
// password before the host or as parameters, and the parameters drivers
// add that the server knows nothing of (Prisma's schema and
// connection_limit, pgbouncer, …), which are dropped: sent on, the server
// would refuse the connection for them.
func config(raw string) (*pgconn.Config, error) {
	scheme, ok := database.Scheme(raw)
	if !ok || (scheme != "postgres" && scheme != "postgresql") {
		return nil, errURL
	}
	u, err := url.Parse(strings.TrimPrefix(strings.TrimSpace(raw), "jdbc:"))
	if err != nil {
		return nil, errURL
	}
	q := u.Query()
	first := func(values ...string) string {
		for _, v := range values {
			if v != "" {
				return v
			}
		}
		return ""
	}
	// libpq's rule: a parameter says more than the place before the path.
	host := first(q.Get("hostaddr"), q.Get("host"), u.Hostname())
	if strings.Contains(host, ",") || strings.Contains(u.Host, ",") {
		return nil, errors.New("it names several hosts")
	}
	port := first(q.Get("port"), u.Port(), "5432")
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, errURL
	}
	if host == "" {
		host = defaultHost(port)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		// "Any address" is where a server listens, not somewhere to dial.
		host = "127.0.0.1"
	}
	socket := strings.HasPrefix(host, "/")

	user, password := q.Get("user"), q.Get("password")
	if u.User != nil {
		user = first(u.User.Username(), user)
		if p, ok := u.User.Password(); ok {
			password = p
		}
	}
	mode := strings.ToLower(q.Get("sslmode"))
	switch mode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		mode = "prefer"
		switch strings.ToLower(q.Get("ssl")) {
		case "true", "1", "require":
			mode = "require"
		}
	}
	if socket {
		mode = "disable"
	}

	// Rebuilt as keywords, so that only what is named here reaches pgconn.
	var dsn strings.Builder
	set := func(key, value string) {
		if value == "" {
			return
		}
		value = strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `'`, `\'`)
		fmt.Fprintf(&dsn, "%s='%s' ", key, value)
	}
	set("host", host)
	set("port", port)
	set("user", user)
	set("password", password)
	set("dbname", first(strings.TrimPrefix(u.Path, "/"), q.Get("dbname")))
	set("sslmode", mode)
	for _, key := range []string{"sslrootcert", "sslcert", "sslkey", "sslpassword"} {
		set(key, q.Get(key))
	}
	cfg, err := pgconn.ParseConfig(dsn.String())
	if err != nil {
		return nil, errURL
	}
	// With TLS preferred pgconn keeps a second try without it, at the same
	// address. Anything else would be somewhere the URL did not name.
	for _, fb := range cfg.Fallbacks {
		if fb.Host != cfg.Host || fb.Port != cfg.Port {
			return nil, errors.New("it names several hosts")
		}
	}
	// The session, the same whatever the helper's own environment says:
	// dates and bytes print one way, so they read back one way.
	cfg.RuntimeParams = map[string]string{
		"application_name": "berth-database",
		"client_encoding":  "UTF8",
		"DateStyle":        "ISO",
		"bytea_output":     "hex",
	}
	if opts := q.Get("options"); opts != "" {
		cfg.RuntimeParams["options"] = opts
	} else if schema := q.Get("schema"); schema != "" {
		// Prisma's way to say where its tables are.
		cfg.RuntimeParams["search_path"] = quote(schema)
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.ValidateConnect, cfg.AfterConnect, cfg.OnNotice = nil, nil, nil
	cfg.LookupFunc = lookupLocal
	return cfg, nil
}

// defaultHost is where a URL with no host points: the server's socket,
// where one is, as libpq and pgx have it; else localhost.
func defaultHost(port string) string {
	for _, dir := range []string{"/var/run/postgresql", "/private/tmp", "/tmp"} {
		if _, err := os.Stat(dir + "/.s.PGSQL." + port); err == nil {
			return dir
		}
	}
	return "localhost"
}

// lookupLocal resolves a host and keeps only the addresses that are this
// machine. A name that reads as local and resolves somewhere else is not
// dialled: the editor's last word on where it connects is the address.
func lookupLocal(ctx context.Context, host string) ([]string, error) {
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	var local []string
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.IsLoopback() {
			local = append(local, a)
		}
	}
	if len(local) == 0 {
		return nil, errors.New("the host is not an address of this machine")
	}
	return local, nil
}
