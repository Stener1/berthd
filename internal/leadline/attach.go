// Package leadline is what a dev server needs to be told for berth-leadline,
// the box-side helper that records what code did in development, to see it:
// where to send OpenTelemetry, under which name, and how to reach its
// Postgres through the helper's recording proxy.
//
// This package is the part the box daemon will use, and it imports nothing
// but the standard library. The helper itself is cmd/berth-leadline and the
// packages beside this file (store, ingest, pgproxy, capture, api, helper),
// which the daemon must not import: they bring SQLite and protobuf with
// them. API.md, in this folder, describes the helper's HTTP API and this
// file's functions.
package leadline

import (
	"net"
	"net/url"
	"strings"
)

// Worktree is one checkout of a project, made for one task: what Shipyard
// runs each agent in. The helper shows each as a source of its own, so two
// agents working on one project are two rows and not one muddle.
type Worktree struct {
	// Project is the repository as Shipyard names it (its location); Name
	// the worktree's own name, empty for the project's main checkout.
	Project, Name string
	// Branch and Box are for showing: the worktree's branch, and the
	// machine it is on.
	Branch, Box string
}

// SubSep joins a worktree's service name to the name of one of several
// commands run in it: the command "worker" in acme/fix-orders files under
// "acme/fix-orders:worker". The helper knows the same rule: what is filed
// under such a name is the worktree's.
const SubSep = ":"

// Service is the name a worktree is filed under, and its source in the
// helper's API: "<project>/<worktree>", the way Shipyard itself addresses
// one, or the project's name alone for its main checkout. It is made of
// names that do not change for the life of the worktree.
func (w Worktree) Service() string {
	project, name := namePart(w.Project), namePart(w.Name)
	switch {
	case project == "":
		return name
	case name == "" || name == project:
		return project
	}
	return project + "/" + name
}

// ServiceFor is the name for one command among several in the worktree
// (a second dev server, a worker): the worktree's own when no name is given.
func (w Worktree) ServiceFor(command string) string {
	base := w.Service()
	// The worktree's own name, given back, is no second name.
	if strings.TrimSpace(command) == base {
		return base
	}
	if command = namePart(command); command == "" {
		return base
	}
	return base + SubSep + command
}

// tags are what a command in the worktree marks its telemetry with, in the
// resource every span and log of it carries. The helper reads these four.
func (w Worktree) tags() [][2]string {
	var out [][2]string
	for _, kv := range [][2]string{
		{"shipyard.project", w.Project}, {"shipyard.worktree", w.Name},
		{"shipyard.branch", w.Branch}, {"shipyard.box", w.Box},
	} {
		if kv[1] != "" {
			out = append(out, kv)
		}
	}
	return out
}

// namePart makes a name safe to be one part of a service name: no slash and
// no colon, which are the joins, and no white space or control characters.
func namePart(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == ':' || r == '\\' || r <= 0x20 || r == 0x7f:
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.ToValidUTF8(out[:60], "")
	}
	return out
}

// Helper is where a running berth-leadline listens.
type Helper struct {
	// OTLP is the base URL it takes OTLP/HTTP on, as in
	// "http://127.0.0.1:4325": the helper's own address, or the second one
	// it was given for OTLP alone (--otlp).
	OTLP string
}

// Var is one environment variable to set for a command.
type Var struct{ Name, Value string }

// Env returns the environment variables a dev server needs for the helper
// to see its traces and logs under the worktree's name: any OpenTelemetry
// SDK reads them, in any language.
//
// command names this command among several run in the worktree; "" is the
// worktree's one command. have is the value OTEL_RESOURCE_ATTRIBUTES
// already has in the command's environment ("" for none): what it sets is
// kept, and the worktree's four attributes are added to it.
//
// The variables, in order:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT   the helper's address
//	OTEL_EXPORTER_OTLP_PROTOCOL   http/protobuf
//	OTEL_SERVICE_NAME             <project>/<worktree>, or that and :<command>
//	OTEL_RESOURCE_ATTRIBUTES      shipyard.project, .worktree, .branch, .box
//	OTEL_BSP_SCHEDULE_DELAY       500: spans are sent within half a second
//	OTEL_BLRP_SCHEDULE_DELAY      500: and so are logs
//
// An SDK's own default is to send every five seconds, which in a tool
// watched while working reads as "nothing arrived".
func Env(w Worktree, command string, h Helper, have string) []Var {
	out := []Var{
		{"OTEL_EXPORTER_OTLP_ENDPOINT", strings.TrimRight(h.OTLP, "/")},
		{"OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf"},
	}
	// A worktree with no name at all has none to give its telemetry; the
	// SDK's own default stands.
	if service := w.ServiceFor(command); service != "" {
		out = append(out, Var{"OTEL_SERVICE_NAME", service})
	}
	if attrs := resourceAttributes(have, w.tags()); attrs != "" {
		out = append(out, Var{"OTEL_RESOURCE_ATTRIBUTES", attrs})
	}
	return append(out, Var{"OTEL_BSP_SCHEDULE_DELAY", "500"}, Var{"OTEL_BLRP_SCHEDULE_DELAY", "500"})
}

// resourceAttributes adds a worktree's tags to an OTEL_RESOURCE_ATTRIBUTES
// value, leaving what is there and never setting a key twice. The format
// is OpenTelemetry's: key=value pairs with commas between, values
// percent-encoded.
func resourceAttributes(have string, tags [][2]string) string {
	set := map[string]bool{}
	for _, pair := range strings.Split(have, ",") {
		key, _, _ := strings.Cut(pair, "=")
		set[strings.TrimSpace(key)] = true
	}
	out := strings.TrimRight(strings.TrimSpace(have), ",")
	for _, kv := range tags {
		if set[kv[0]] {
			continue
		}
		if out != "" {
			out += ","
		}
		out += kv[0] + "=" + url.PathEscape(kv[1])
	}
	return out
}

// LocalPostgres reports whether a value, as of DATABASE_URL, is the URL of
// a Postgres on this machine that the helper's proxy can stand in front
// of, and if so where that Postgres listens (host:port): the upstream to
// ask the helper for a proxy to (POST /api/proxies).
//
// A remote database, a Unix socket, a URL naming several hosts and a
// connection that insists on TLS (which the proxy cannot read) are all
// refused, so they are left alone. The address returned has no user and no
// password in it.
func LocalPostgres(value string) (upstream string, ok bool) {
	if !strings.Contains(value, "://") {
		return "", false
	}
	u, err := url.Parse(strings.TrimPrefix(value, "jdbc:"))
	if err != nil {
		return "", false
	}
	// postgres, postgresql, and SQLAlchemy's postgresql+psycopg and the like.
	scheme, _, _ := strings.Cut(strings.ToLower(u.Scheme), "+")
	if scheme != "postgres" && scheme != "postgresql" {
		return "", false
	}
	host := u.Hostname()
	if host == "" || strings.Contains(u.Host, ",") || !isLocalHost(host) {
		return "", false
	}
	q := u.Query()
	// libpq lets the query string name the real host, or a socket's folder.
	if q.Get("host") != "" || q.Get("hostaddr") != "" {
		return "", false
	}
	switch strings.ToLower(q.Get("sslmode")) {
	case "require", "verify-ca", "verify-full":
		return "", false
	}
	switch strings.ToLower(q.Get("ssl")) {
	case "true", "1", "require":
		return "", false
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	if _, _, ok := hostSpan(value); !ok {
		return "", false
	}
	// "Any address" is where a server listens, not somewhere to dial.
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), true
}

// ThroughProxy returns a database URL with the database's address replaced
// by proxy, the address a proxy of the helper's listens on (the "listen" of
// POST /api/proxies), and everything else (user, password, database,
// options) as it was written, byte for byte. ok is false, and the value
// comes back as it was, when it is not a URL LocalPostgres accepts.
func ThroughProxy(value, proxy string) (rewritten string, ok bool) {
	if _, local := LocalPostgres(value); !local {
		return value, false
	}
	start, end, ok := hostSpan(value)
	if !ok {
		return value, false
	}
	return value[:start] + proxy + value[end:], true
}

func isLocalHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// hostSpan finds the host and port in a URL as it was written: after the
// last @ of the authority, up to the path. Cutting the string there, and
// not printing a parsed URL back, keeps a password's every byte as it was.
func hostSpan(raw string) (start, end int, ok bool) {
	i := strings.Index(raw, "://")
	if i < 0 {
		return 0, 0, false
	}
	start = i + 3
	end = len(raw)
	if j := strings.IndexAny(raw[start:], "/?#"); j >= 0 {
		end = start + j
	}
	if at := strings.LastIndex(raw[start:end], "@"); at >= 0 {
		start += at + 1
	}
	return start, end, start < end
}
