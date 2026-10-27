package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/cosscom/shipyard/internal/database"
)

// conn is one database, opened: database.Conn.
type conn struct {
	pool    *pool
	timeout time.Duration
	// maxBytes is how much one answer's rows may hold; zero is
	// database.MaxBytes. Tests make it small.
	maxBytes int
}

func (c *conn) grid() *database.Grid {
	g := database.NewGrid()
	g.Max = c.maxBytes
	return g
}

func (c *conn) Close() { c.pool.close() }

// within runs fn in a transaction of its own, on one connection.
//
// The transaction is where the guard rails are, and the server keeps them:
// it is READ ONLY unless write is set, and every statement in it is stopped
// at the timeout. A SELECT is run before fn, because Postgres lets a
// transaction be made read-write again only before its first query: after
// it, no statement can undo READ ONLY.
//
// reuse says the connection may be kept when fn is done. It is false for a
// stranger's statement, which can leave anything behind in a session.
func (c *conn) within(ctx context.Context, write, reuse bool, budget time.Duration, fn func(context.Context, *pgconn.PgConn) error) error {
	// The server's timeout is the one that counts. This is for a server
	// that has stopped answering at all.
	ctx, cancel := context.WithTimeout(ctx, budget+5*time.Second)
	defer cancel()
	pc, err := c.pool.acquire(ctx)
	if err != nil {
		return c.problem(err)
	}
	sound := false
	defer func() { c.pool.release(pc, reuse && sound) }()

	begin := "BEGIN READ ONLY"
	if write {
		begin = "BEGIN READ WRITE"
	}
	begin += fmt.Sprintf("; SET LOCAL statement_timeout = %d; SELECT 1", c.timeout.Milliseconds())
	if err := simple(ctx, pc, begin); err != nil {
		return c.problem(err)
	}
	err = fn(ctx, pc)
	var pgErr *pgconn.PgError
	var said *database.Problem
	if err == nil || errors.As(err, &pgErr) || errors.As(err, &said) {
		// The connection is in step with the server: end the transaction
		// on it, even for a caller who has gone.
		end, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer done()
		ending := "ROLLBACK"
		if err == nil && write {
			ending = "COMMIT"
		}
		if endErr := simple(end, pc, ending); endErr == nil {
			sound = true
		} else if err == nil {
			err = endErr
		}
	}
	return c.problem(err)
}

// simple sends statements of the editor's own, with no values in them.
func simple(ctx context.Context, pc *pgconn.PgConn, sql string) error {
	results, err := pc.Exec(ctx, sql).ReadAll()
	if err != nil {
		return err
	}
	// A COMMIT of a transaction that had failed is answered ROLLBACK.
	if sql == "COMMIT" && len(results) > 0 && results[len(results)-1].CommandTag.String() != "COMMIT" {
		return &database.Problem{Status: http.StatusBadRequest, Code: "query_failed", Message: "The transaction was rolled back."}
	}
	return nil
}

// read runs a statement of the editor's own and returns its rows as text.
// Each parameter goes as text, for the server to cast.
func read(ctx context.Context, pc *pgconn.PgConn, sql string, params ...string) ([][]string, error) {
	values := make([][]byte, len(params))
	for i, p := range params {
		values[i] = append(make([]byte, 0, len(p)), p...)
	}
	res := pc.ExecParams(ctx, sql, values, nil, nil, nil).Read()
	if res.Err != nil {
		return nil, res.Err
	}
	out := make([][]string, len(res.Rows))
	for i, row := range res.Rows {
		out[i] = make([]string, len(row))
		for j, v := range row {
			out[i][j] = string(v)
		}
	}
	return out, nil
}

// change runs one statement that writes, and reports how many rows it
// touched. A nil parameter is NULL.
func change(ctx context.Context, pc *pgconn.PgConn, sql string, params [][]byte) (int64, error) {
	res := pc.ExecParams(ctx, sql, params, nil, nil, nil).Read()
	if res.Err != nil {
		return 0, res.Err
	}
	return res.CommandTag.RowsAffected(), nil
}

// param is a request's value as a parameter: text for the server to cast
// to the type of the column it meets, or NULL.
func param(v database.Value) []byte {
	if v.Null {
		return nil
	}
	return append(make([]byte, 0, len(v.Text)), v.Text...)
}

// field is one column of a result.
type field struct {
	name   string
	oid    uint32
	typmod int32
}

// outcome is what a statement run with run came to.
type outcome struct {
	fields []field
	// tag is the server's word on what ran ("SELECT 3"); empty when the
	// rows were cut short before it could say.
	tag string
	// more says the statement had rows past the ones taken.
	more    bool
	notices []string
}

var errCopy = errors.New("copy")

// chunk is how many rows are asked for at a time.
const chunk = 250

// run sends one statement and hands its rows to row, at most limit of them
// (0: all), in text. row reports whether it has room for another.
//
// It drives the extended protocol itself, for two things pgconn's own
// calls do not give. The statement is parsed alone, so the server refuses
// a second statement in the same string. And the rows are fetched a few at
// a time from a portal, so a statement that has a million rows sends the
// first of them and no more: nothing is read off the wire to be thrown
// away, and the rest never leaves the server.
//
// An error that is a *pgconn.PgError is the server refusing the statement:
// the connection is in step and can end its transaction. Any other error
// means the connection is not to be used again.
func run(ctx context.Context, pc *pgconn.PgConn, sql string, params [][]byte, limit int, row func([]field, [][]byte) bool) (*outcome, error) {
	out := &outcome{notices: []string{}}
	fe := pc.Frontend()
	fe.SendParse(&pgproto3.Parse{Query: sql})
	fe.SendBind(&pgproto3.Bind{Parameters: params})
	fe.SendDescribe(&pgproto3.Describe{ObjectType: 'P'})

	var refused *pgconn.PgError
	taken, full := 0, false
	for {
		n := chunk
		switch {
		case full:
			// Only to learn whether there was another.
			n = 1
		case limit > 0:
			n = min(chunk, limit-taken)
		}
		fe.SendExecute(&pgproto3.Execute{MaxRows: uint32(n)})
		fe.Send(&pgproto3.Flush{})
		if err := fe.Flush(); err != nil {
			return nil, err
		}
		suspended := false
	reading:
		for {
			msg, err := pc.ReceiveMessage(ctx)
			if err != nil {
				return nil, err
			}
			switch m := msg.(type) {
			case *pgproto3.RowDescription:
				out.fields = make([]field, len(m.Fields))
				for i, f := range m.Fields {
					out.fields[i] = field{name: string(f.Name), oid: f.DataTypeOID, typmod: f.TypeModifier}
				}
			case *pgproto3.DataRow:
				if full {
					out.more = true
					continue
				}
				taken++
				if !row(out.fields, m.Values) || (limit > 0 && taken >= limit) {
					full = true
				}
			case *pgproto3.PortalSuspended:
				suspended = true
				break reading
			case *pgproto3.CommandComplete:
				out.tag = string(m.CommandTag)
				break reading
			case *pgproto3.EmptyQueryResponse:
				break reading
			case *pgproto3.ErrorResponse:
				refused = pgconn.ErrorResponseToPgError(m)
				break reading
			case *pgproto3.NoticeResponse:
				out.notices = append(out.notices, m.Severity+": "+m.Message)
			case *pgproto3.CopyInResponse, *pgproto3.CopyOutResponse, *pgproto3.CopyBothResponse:
				// The server now waits for, or sends, a stream this API
				// has no place for. The connection is given up.
				return nil, errCopy
			}
		}
		if !suspended || out.more || refused != nil {
			break
		}
	}
	fe.SendSync(&pgproto3.Sync{})
	if err := fe.Flush(); err != nil {
		return nil, err
	}
	for {
		msg, err := pc.ReceiveMessage(ctx)
		if err != nil {
			return nil, err
		}
		switch m := msg.(type) {
		case *pgproto3.ReadyForQuery:
			if refused != nil {
				return nil, refused
			}
			return out, nil
		case *pgproto3.ErrorResponse:
			if refused == nil {
				refused = pgconn.ErrorResponseToPgError(m)
			}
		case *pgproto3.NoticeResponse:
			out.notices = append(out.notices, m.Severity+": "+m.Message)
		}
	}
}

// The types whose text is turned into something other than a string.
const (
	oidBool        = 16
	oidChar        = 18
	oidName        = 19
	oidCIDR        = 650
	oidMacaddr8    = 774
	oidMacaddr     = 829
	oidInet        = 869
	oidBpchar      = 1042
	oidInt8        = 20
	oidInt2        = 21
	oidInt4        = 23
	oidOID         = 26
	oidFloat4      = 700
	oidFloat8      = 701
	oidDate        = 1082
	oidTime        = 1083
	oidTimestamp   = 1114
	oidTimestamptz = 1184
	oidInterval    = 1186
	oidTimetz      = 1266
	oidUUID        = 2950
)

// cell is one value as the API sends it, from the text Postgres printed.
// Everything not named here stays that text: a numeric keeps its digits, a
// uuid, json, an array and bytea (as \x and hex) are as Postgres writes
// them.
func cell(oid uint32, raw []byte) any {
	if raw == nil {
		return nil
	}
	text := string(raw)
	switch oid {
	case oidBool:
		return text == "t" || text == "true"
	case oidInt2, oidInt4, oidInt8, oidOID:
		return database.Int(text)
	case oidFloat4, oidFloat8:
		return database.Float(text)
	case oidTimestamp, oidTimestamptz:
		return isoTime(text)
	}
	return text
}

// stamp is a timestamp as Postgres prints it with DateStyle ISO.
var stamp = regexp.MustCompile(`^(\d{4,}-\d\d-\d\d) (\d\d:\d\d:\d\d(?:\.\d+)?)(?:([+-])(\d\d)(?::(\d\d))?(:\d\d)?)?$`)

// isoTime writes a timestamp as ISO 8601: a T between the date and the
// time, and the zone as Z or ±hh:mm. What is not a plain timestamp
// (infinity, a date before Christ) stays as Postgres printed it.
func isoTime(text string) string {
	m := stamp.FindStringSubmatch(text)
	if m == nil {
		return text
	}
	out := m[1] + "T" + m[2]
	if m[3] == "" {
		return out
	}
	minutes := m[5]
	if minutes == "" {
		minutes = "00"
	}
	if m[4] == "00" && minutes == "00" && m[6] == "" {
		return out + "Z"
	}
	return out + m[3] + m[4] + ":" + minutes + m[6]
}

// tagged reads the server's word on a statement: "INSERT 0 3" is the
// command INSERT and three rows; "CREATE TABLE" has no count.
func tagged(tag string) (command string, rows int64, counted bool) {
	words := strings.Fields(tag)
	end := len(words)
	for end > 0 {
		n, err := strconv.ParseInt(words[end-1], 10, 64)
		if err != nil {
			break
		}
		if !counted {
			rows, counted = n, true
		}
		end--
	}
	return strings.Join(words[:end], " "), rows, counted
}

// problem turns what went wrong into what the API answers. A statement the
// server refused is the caller's to read: Postgres's message, its SQLSTATE,
// where in the statement, and its detail. Nothing here is logged.
func (c *conn) problem(err error) error {
	if err == nil {
		return nil
	}
	var said *database.Problem
	if errors.As(err, &said) {
		return said
	}
	slow := &database.Problem{Status: http.StatusBadRequest, Code: "timeout",
		Message: fmt.Sprintf("The statement ran longer than %s and was stopped.", c.timeout),
		Hint:    "Narrow it, or add a LIMIT."}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "25006": // read_only_sql_transaction
			p := database.ReadOnly()
			p.SQLState, p.Detail = pgErr.Code, pgErr.Message
			return p
		case "57014": // query_canceled: the statement timeout
			slow.SQLState = pgErr.Code
			return slow
		}
		return &database.Problem{Status: http.StatusBadRequest, Code: "query_failed", Message: sentence(pgErr.Message),
			Hint: pgErr.Hint, SQLState: pgErr.Code, Position: int(pgErr.Position), Detail: pgErr.Detail}
	}
	var big *pgproto3.ExceededMaxBodyLenErr
	switch {
	case errors.Is(err, errCopy):
		return database.Bad("COPY to or from the editor is not something this API can carry.")
	case errors.As(err, &big):
		return &database.Problem{Status: http.StatusBadRequest, Code: "too_large",
			Message: "A row of the answer is too large to send.", Hint: "Select fewer columns, or part of the large one."}
	case errors.Is(err, errClosed):
		return database.NotFound("This connection was closed.")
	case errors.Is(err, context.Canceled):
		return err
	case errors.Is(err, context.DeadlineExceeded):
		return slow
	}
	return database.Unavailable("the connection was lost.")
}

func sentence(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}
