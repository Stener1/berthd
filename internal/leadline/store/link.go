package store

import (
	"hash/maphash"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What a span counts as is settled here, once, as it is written, and kept
// in two small columns. Every aggregate then asks with one comparison
// instead of working it out again from strings.
//
// entry says the span is a request: a call arriving at a service. That is a
// server or consumer span, or a span with no parent, except
//   - a span the browser made (the request it led to is the server's span),
//   - a server span inside a server span of the same service (two layers of
//     one app each reporting the one request),
//   - a database call a capture recorded (a statement is not a request), and
//   - a database call an app made outside any request: a seed, a migration,
//     a query at startup. It has no parent, and it is still a statement.
//
// db_role says what a database call counts as:
//
//	0  not a database call
//	1  a query: listed, counted per request, looked at for N+1
//	2  transaction and session control (BEGIN, COMMIT, SET, Redis's PING):
//	   in the trace and in database time, not among the queries
//	3  a second sighting of a call that another span already counts
//
// The second sightings are the reason for the linker below. An app may be
// seen two ways at once: its own OpenTelemetry spans, and the Postgres
// proxy recording the same statement off the wire. The two arrive apart,
// in either order, up to seconds apart. The linker remembers the unmatched
// ones for a short while and pairs them: same database system, same
// fingerprint, and the wire's timing inside the app's span, preferring the
// pair a sqlcommenter traceparent names. Then each statement is one query
// with the best of both: the wire's span moves into the app's trace, under
// the app's span, and is the one that counts (exact timing, row count,
// parameters), while the app's span stays as the step that led to it.
//
// A statement only one of them saw is simply a query.
//
// The linker is Leadline's (internal/store/link.go), without the half that
// pairs Redis's MONITOR, which this helper does not capture. What it decides
// is written to SQLite by correct, in store.go.
const (
	roleNone      int8 = 0
	roleQuery     int8 = 1
	roleControl   int8 = 2
	roleDuplicate int8 = 3

	// The predicates the aggregates use.
	isQuery  = `db_role = 1`
	isDBTime = `db_role BETWEEN 1 AND 2`
	isEntry  = `entry = 1`
	// A failed call seen twice failed once.
	isFailed = `(status = 'error' AND db_role <> 3)`
)

// roleName is db_role as the API spells it.
func roleName(role int8) string {
	switch role {
	case roleQuery:
		return "query"
	case roleControl:
		return "control"
	case roleDuplicate:
		return "duplicate"
	}
	return ""
}

const (
	// linkWait is how long a database call waits for its other sighting.
	// An OpenTelemetry SDK exports every five seconds unless told
	// otherwise, and a capture within a fifth of one.
	linkWait = 30 * time.Second
	// captureMemory is how long after a capture last recorded a call the
	// app's own calls to that system are still held for pairing.
	captureMemory = 2 * time.Minute
	// maxLinked bounds what the linker remembers. Past it the oldest go
	// unpaired, which is only ever a query counted twice.
	maxLinked = 50_000
	// maxPerKey bounds how many unmatched calls of one shape are compared.
	maxPerKey = 2_000
	// linkSlack is how far outside the app's span the wire's timing may
	// fall and still be the same call: the two clocks are one machine's,
	// but an SDK may round its own to the millisecond, and a database in a
	// container keeps time of its own.
	linkSlack = int64(50 * time.Millisecond)
	// tightSlack is what counts as inside without question.
	tightSlack = int64(2 * time.Millisecond)
)

// mark is what the store works out for a span before writing it.
type mark struct {
	role  int8
	entry bool
}

// sighting is one database call waiting to be paired.
type sighting struct {
	key        uint64
	start, end int64
	// As stored: a capture's span may have moved with its transaction.
	spanID, traceID, parentID string
	service, dbName           string
	capture                   string // which capture recorded it
	wire                      bool   // from a capture, not the app
	linked                    bool   // a capture's span that names the app's trace itself
	// txTrace and frame are a capture's own transaction around the call:
	// the trace id it made up for it, and the transaction's span.
	txTrace, frame string
	batch          int // its place in the batch being written, or -1 once stored
	done           bool
}

// home is where a capture's transaction went when its statements were
// paired with an app's.
type home struct{ trace, parent, service string }

// kid is a server span whose parent has not been seen yet.
type kid struct {
	spanID, service string
	start           int64
}

// expiry is one thing the linker will forget, in the order it learned them.
type expiry struct {
	at    int64
	s     *sighting
	tx    string // a key of txs and moved
	dbKid string // a key of dbKids
	kid   string // a key of kids
}

// linker is only ever used by the flusher, holding Store.wmu.
type linker struct {
	seed maphash.Seed
	// Unpaired database calls, the app's and the captures', by shape.
	app, wire map[uint64][]*sighting
	// capturing counts the captures running for each database system;
	// lastWire is when one last recorded a call.
	capturing   map[string]int
	nCapturing  int
	lastWire    map[string]int64
	lastWireAny int64
	// txs is where each capture's open transaction began, by the trace id
	// the capture gave it; moved is where a paired one went.
	txs   map[string]int64
	moved map[string]home
	// dbKids is, for each span that is the parent of an app's database
	// span, that span's system.
	dbKids map[string]string
	// kids are server spans waiting for their parent, by the parent's id.
	kids  map[string]kid
	queue []expiry
	head  int

	marks []mark
	order []int

	// seen counts the capture spans paired with each service's own, by the
	// capture. It has a lock of its own: it is read from outside.
	seenMu sync.Mutex
	seen   map[Pairing]int64
}

func newLinker() *linker {
	return &linker{
		seed: maphash.MakeSeed(),
		app:  map[uint64][]*sighting{}, wire: map[uint64][]*sighting{},
		capturing: map[string]int{}, lastWire: map[string]int64{},
		txs: map[string]int64{}, moved: map[string]home{},
		dbKids: map[string]string{}, kids: map[string]kid{},
		seen: map[Pairing]int64{},
	}
}

// Pairing names a service and a capture that recorded some of its database
// calls: Source is SourcePostgresWire, and Capture the id the spans came
// with (Span.Capture).
type Pairing struct{ Service, Source, Capture string }

// Paired reports how many database calls of each service a capture also
// recorded since the store opened. Those calls are stored under the
// service's own name, whatever the capture would have called it.
func (s *Store) Paired() map[Pairing]int64 {
	l := s.link
	l.seenMu.Lock()
	defer l.seenMu.Unlock()
	out := make(map[Pairing]int64, len(l.seen))
	for k, n := range l.seen {
		out[k] = n
	}
	return out
}

func (l *linker) paired(service, capture string) {
	l.seenMu.Lock()
	l.seen[Pairing{service, SourcePostgresWire, capture}]++
	l.seenMu.Unlock()
}

// Capturing tells the store that a capture has started (or stopped)
// recording calls to a database system off the wire. While one runs, the
// app's own spans for that system are held a moment to be paired with it.
func (s *Store) Capturing(system string, on bool) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if on {
		s.link.capturing[system]++
		s.link.nCapturing++
	} else if s.link.capturing[system] > 0 {
		s.link.capturing[system]--
		s.link.nCapturing--
	}
}

// A correction to rows already stored.
const (
	fixRehome = iota + 1 // move a capture's span under the app's
	fixRole              // a stored span turned out to be a second sighting
	fixEntry             // a stored server span turned out to be nested
	fixMove              // move the rest of a capture's transaction
)

type fix struct {
	kind                   int
	id                     string // the span, or for fixMove the trace
	at                     int64  // its start, or for fixMove where to look from
	trace, parent, service string
	frame                  string // fixMove: the transaction's own span
}

func (l *linker) key(system, fingerprint string) uint64 {
	var h maphash.Hash
	h.SetSeed(l.seed)
	h.WriteString(system)
	h.WriteByte(0)
	h.WriteString(fingerprint)
	return h.Sum64()
}

func (l *linker) later(e expiry) { l.queue = append(l.queue, e) }

// forget drops what has waited long enough, and the oldest of too much.
func (l *linker) forget(now int64) {
	cutoff := now - int64(linkWait)
	for l.head < len(l.queue) {
		e := &l.queue[l.head]
		if e.at >= cutoff && len(l.queue)-l.head <= maxLinked {
			break
		}
		switch {
		case e.s != nil:
			if !e.s.done {
				l.drop(e.s)
			}
		case e.tx != "":
			delete(l.txs, e.tx)
			delete(l.moved, e.tx)
		case e.dbKid != "":
			delete(l.dbKids, e.dbKid)
		case e.kid != "":
			delete(l.kids, e.kid)
		}
		*e = expiry{}
		l.head++
	}
	if l.head == len(l.queue) {
		l.queue, l.head = l.queue[:0], 0
	} else if l.head > 1024 && l.head*2 >= len(l.queue) {
		n := copy(l.queue, l.queue[l.head:])
		clear(l.queue[n:])
		l.queue, l.head = l.queue[:n], 0
	}
}

// drop takes an unpaired sighting out of its list.
func (l *linker) drop(s *sighting) {
	lists := l.app
	if s.wire {
		lists = l.wire
	}
	list := lists[s.key]
	for i, x := range list {
		if x == s {
			list[i] = list[len(list)-1]
			list[len(list)-1] = nil
			list = list[:len(list)-1]
			break
		}
	}
	if len(list) == 0 {
		delete(lists, s.key)
	} else {
		lists[s.key] = list
	}
	s.done = true
}

func (l *linker) hold(s *sighting, now int64) {
	lists := l.app
	if s.wire {
		lists = l.wire
	}
	list := lists[s.key]
	if len(list) >= maxPerKey {
		list[0].done = true
		list = append(list[:0], list[1:]...)
	}
	lists[s.key] = append(list, s)
	l.later(expiry{at: now, s: s})
}

// twin finds the other sighting of s among the unpaired, or nil.
func (l *linker) twin(s *sighting) *sighting {
	lists := l.wire
	if s.wire {
		lists = l.app
	}
	var best *sighting
	bestTier, bestGap := 9, int64(0)
	for _, c := range lists[s.key] {
		a, w := s, c
		if s.wire {
			a, w = c, s
		}
		if a.dbName != "" && w.dbName != "" && a.dbName != w.dbName {
			continue
		}
		if w.linked && w.traceID != a.traceID {
			continue // the statement itself says which trace it ran in
		}
		if w.start < a.start-linkSlack || w.end > a.end+linkSlack {
			continue
		}
		tier := 3
		switch {
		case w.traceID == a.traceID && w.parentID == a.spanID:
			// The statement carried this very span's id to the database.
			return c
		case w.traceID == a.traceID:
			tier = 1
		case w.start >= a.start-tightSlack && w.end <= a.end+tightSlack:
			tier = 2
		}
		gap := w.start - a.start
		if gap < 0 {
			gap = -gap
		}
		if best == nil || tier < bestTier || (tier == bestTier && gap < bestGap) {
			best, bestTier, bestGap = c, tier, gap
		}
	}
	return best
}

// place settles what each span of a batch counts as. It may change the
// spans themselves (a capture's span moving into the app's trace), and it
// returns the corrections that are due to rows already stored. marks is
// only good until the next call.
func (l *linker) place(spans []Span, now int64) ([]mark, []fix) {
	if cap(l.marks) < len(spans) {
		l.marks = make([]mark, len(spans))
	}
	marks := l.marks[:len(spans)]
	l.forget(now)

	var fixes []fix
	nested, dbs := 0, l.order[:0]
	for i := range spans {
		sp := &spans[i]
		m := mark{role: dbRole(sp.DBSystem, sp.DBStatement)}
		wire := sp.Source == SourcePostgresWire
		m.entry = (sp.ParentID == "" || sp.Kind == "server" || sp.Kind == "consumer") &&
			sp.Source != SourceBrowser && !wire
		if m.entry && sp.DBSystem != "" && sp.Kind != "server" && sp.Kind != "consumer" {
			// A root database span: a query run outside any request. It is
			// in its trace and among the queries, and no endpoint.
			m.entry = false
		}
		marks[i] = m
		if m.entry && sp.Kind == "server" {
			if k, ok := l.kids[sp.SpanID]; ok && k.service == sp.Service {
				// Its child came in an earlier batch, counted as a request.
				fixes = append(fixes, fix{kind: fixEntry, id: k.spanID, at: k.start})
				delete(l.kids, sp.SpanID)
			}
			if sp.ParentID != "" {
				nested++
			}
		}
		switch {
		case wire:
			dbs = append(dbs, i)
			if sp.DBSystem != "" {
				l.lastWire[sp.DBSystem] = now
				l.lastWireAny = now
			}
		case sp.DBSystem != "" && sp.Source == "":
			dbs = append(dbs, i)
			if sp.ParentID != "" {
				if l.dbKids[sp.ParentID] != sp.DBSystem {
					l.dbKids[sp.ParentID] = sp.DBSystem
					l.later(expiry{at: now, dbKid: sp.ParentID})
				}
			}
		}
	}
	l.order = dbs

	if nested > 0 {
		servers := make(map[string]int, nested*2)
		for i := range spans {
			if spans[i].Kind == "server" {
				servers[spans[i].SpanID] = i
			}
		}
		for i := range spans {
			sp := &spans[i]
			if !marks[i].entry || sp.Kind != "server" || sp.ParentID == "" {
				continue
			}
			if j, ok := servers[sp.ParentID]; ok {
				if spans[j].Service == sp.Service {
					marks[i].entry = false
				}
				continue
			}
			l.kids[sp.ParentID] = kid{spanID: sp.SpanID, service: sp.Service, start: sp.StartNs}
			l.later(expiry{at: now, kid: sp.ParentID})
		}
	}
	if len(dbs) == 0 {
		return marks, fixes
	}

	pairing := l.nCapturing > 0 || len(l.wire) > 0 || now-l.lastWireAny <= int64(captureMemory)
	if !pairing {
		for _, i := range dbs {
			if l.dbKids[spans[i].SpanID] == spans[i].DBSystem {
				marks[i].role = roleDuplicate
			}
		}
		return marks, fixes
	}

	// In time order, so that a run of the same statement pairs first with
	// first: a batch holds them as they were exported, not as they ran.
	sort.SliceStable(dbs, func(a, b int) bool { return spans[dbs[a]].StartNs < spans[dbs[b]].StartNs })
	var held []*sighting
	for _, i := range dbs {
		sp := &spans[i]
		wire := sp.Source == SourcePostgresWire
		if !wire {
			if l.dbKids[sp.SpanID] == sp.DBSystem {
				// A query builder's span around the driver's: the driver's
				// is the query.
				marks[i].role = roleDuplicate
				continue
			}
			if l.capturing[sp.DBSystem] == 0 && now-l.lastWire[sp.DBSystem] > int64(captureMemory) {
				continue // nothing else can have seen it
			}
		}
		s := &sighting{
			key: l.key(sp.DBSystem, sp.DBFingerprint), start: sp.StartNs, end: sp.StartNs + sp.DurationNs,
			spanID: sp.SpanID, traceID: sp.TraceID, parentID: sp.ParentID, service: sp.Service,
			dbName: sp.DBName, capture: sp.Capture, wire: wire, linked: wire && sp.Linked, batch: i,
		}
		if wire {
			if !sp.Linked && sp.ParentID != "" {
				// Inside a transaction the capture framed with a span and a
				// trace id of its own.
				s.txTrace, s.frame = sp.TraceID, sp.ParentID
			}
			if h, ok := l.moved[sp.TraceID]; ok && !sp.Linked {
				// The rest of a transaction that has moved already.
				sp.TraceID, sp.Service = h.trace, h.service
				if sp.DBSystem == "" {
					sp.ParentID = h.parent
				}
				s.traceID, s.service = h.trace, h.service
			}
			if sp.DBSystem == "" {
				continue // a transaction's own span: nothing to pair
			}
			if s.txTrace != "" {
				if from, ok := l.txs[s.txTrace]; !ok {
					l.txs[s.txTrace] = sp.StartNs
					l.later(expiry{at: now, tx: s.txTrace})
				} else if sp.StartNs < from {
					l.txs[s.txTrace] = sp.StartNs
				}
			}
		}
		other := l.twin(s)
		if other == nil {
			l.hold(s, now)
			held = append(held, s)
			continue
		}
		l.drop(other)
		a, w := s, other
		if wire {
			a, w = other, s
		}
		l.paired(a.service, w.capture)
		// The proxy's span goes under the app's. The proxy timed the call
		// on the wire, so its span is the query, and the app's the step
		// that led to it.
		if w.batch >= 0 {
			x := &spans[w.batch]
			x.TraceID, x.ParentID, x.Service = a.traceID, a.spanID, a.service
		} else if w.traceID != a.traceID || w.parentID != a.spanID || w.service != a.service {
			fixes = append(fixes, fix{kind: fixRehome, id: w.spanID, at: w.start,
				trace: a.traceID, parent: a.spanID, service: a.service})
		}
		if a.batch >= 0 {
			marks[a.batch].role = roleDuplicate
		} else {
			fixes = append(fixes, fix{kind: fixRole, id: a.spanID, at: a.start})
		}
		// A statement of a transaction takes the transaction with it: its
		// span, and the statements the app did not report.
		if w.txTrace != "" && w.txTrace != a.traceID {
			if _, ok := l.moved[w.txTrace]; !ok {
				h := home{trace: a.traceID, parent: a.parentID, service: a.service}
				from, known := l.txs[w.txTrace]
				if !known {
					from = w.start
					l.later(expiry{at: now, tx: w.txTrace})
				}
				l.moved[w.txTrace] = h
				fixes = append(fixes, fix{kind: fixMove, id: w.txTrace, at: from,
					trace: h.trace, parent: h.parent, service: h.service, frame: w.frame})
			}
		}
	}
	// The transaction spans of this batch, and the statements of it that
	// were passed over before their transaction moved.
	for _, i := range dbs {
		sp := &spans[i]
		if sp.Source != SourcePostgresWire || sp.Linked {
			continue
		}
		if h, ok := l.moved[sp.TraceID]; ok {
			sp.TraceID, sp.Service = h.trace, h.service
			if sp.DBSystem == "" {
				sp.ParentID = h.parent
			}
		}
	}
	for _, s := range held {
		if !s.done {
			// What is stored is what the span says now.
			sp := &spans[s.batch]
			s.traceID, s.parentID, s.service = sp.TraceID, sp.ParentID, sp.Service
		}
		s.batch = -1
	}
	return marks, fixes
}

// dbRole is what a database call counts as, from its statement.
func dbRole(system, statement string) int8 {
	if system == "" {
		return roleNone
	}
	var control bool
	switch {
	case redisLike(system):
		control = redisControl(firstWord(statement, false))
	case system == "mongodb":
		control = mongoControl(firstWord(statement, false))
	case sqlSystem(system):
		control = sqlControl(firstWord(statement, true))
	}
	if control {
		return roleControl
	}
	return roleQuery
}

func redisLike(system string) bool {
	switch system {
	case "redis", "valkey", "keydb", "dragonfly":
		return true
	}
	return false
}

// sqlSystem reports whether a database system speaks SQL, where BEGIN and
// SET are bookkeeping. Elsewhere the same words may be real work (SET is
// Redis's and memcached's write).
func sqlSystem(system string) bool {
	if strings.Contains(system, "sql") {
		return true // postgresql, mysql, mssql, microsoft.sql_server, sqlite, other_sql
	}
	switch system {
	case "mariadb", "oracle", "oracle.db", "db2", "ibm.db2", "cockroachdb", "sap.hana", "hanadb",
		"redshift", "aws.redshift", "h2", "derby", "firebird", "trino", "presto", "clickhouse",
		"duckdb", "snowflake", "vertica", "teradata", "sybase", "informix", "tidb", "spanner", "gcp.spanner":
		return true
	}
	return false
}

// firstWord is a statement's first word. For SQL it steps over leading
// white space, comments and brackets first.
func firstWord(s string, sql bool) string {
	i := 0
	for i < len(s) {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || (sql && c == '('):
			i++
			continue
		case sql && c == '-' && i+1 < len(s) && s[i+1] == '-':
			nl := strings.IndexByte(s[i:], '\n')
			if nl < 0 {
				return ""
			}
			i += nl + 1
			continue
		case sql && c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return ""
			}
			i += end + 4
			continue
		}
		break
	}
	j := i
	for j < len(s) && (s[j]|0x20 >= 'a' && s[j]|0x20 <= 'z' || s[j] == '_') {
		j++
	}
	return s[i:j]
}

func oneOf(word string, words ...string) bool {
	for _, w := range words {
		if len(w) == len(word) && strings.EqualFold(w, word) {
			return true
		}
	}
	return false
}

// sqlControl: statements that open, close or set up, and fetch or change
// no data of their own.
func sqlControl(word string) bool {
	return oneOf(word, "BEGIN", "START", "COMMIT", "END", "ROLLBACK", "ABORT", "SAVEPOINT", "RELEASE",
		"SET", "RESET", "SHOW", "DEALLOCATE", "DISCARD", "PREPARE", "USE")
}

// redisControl: what a client says to set a connection up or keep it open,
// and the frame around a transaction.
func redisControl(word string) bool {
	return oneOf(word, "PING", "HELLO", "AUTH", "CLIENT", "ECHO", "QUIT", "SELECT", "RESET", "READONLY",
		"READWRITE", "COMMAND", "INFO", "CONNECT", "MULTI", "EXEC", "DISCARD", "WATCH", "UNWATCH")
}

func mongoControl(word string) bool {
	return oneOf(word, "hello", "isMaster", "ping", "endSessions", "saslStart", "saslContinue", "buildInfo")
}

// DBServer spells a database server's address one way, so that the same
// server named by an app's span, a proxy and a saved connection compares
// equal: lower case, "localhost" as 127.0.0.1, and host:port when there is
// a port. An empty host is no address.
func DBServer(host string, port int64) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if h, p, err := net.SplitHostPort(host); err == nil && port == 0 {
		host = h
		port, _ = strconv.ParseInt(p, 10, 64)
	}
	if host == "localhost" || host == "::1" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		return host
	}
	return net.JoinHostPort(host, strconv.FormatInt(port, 10))
}
