package helper_test

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
)

// fakePG is a Postgres that lives in the test: it speaks the wire protocol
// well enough for pgx, and answers a handful of made-up statements. It is
// the one Leadline's proxy is tested against (internal/leadline/pgproxy),
// cut down to what this test sends. It asks for a cleartext password so
// the test can check none is recorded.
type fakePG struct {
	t  testing.TB
	ln net.Listener
}

const fakePassword = "sekret-acme-pw"

func newFakePG(t testing.TB) *fakePG {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePG{t: t, ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakePG) addr() string { return f.ln.Addr().String() }

type fakePortal struct {
	sql    string
	params []string
}

func (f *fakePG) serve(c net.Conn) {
	defer c.Close()
	be := pgproto3.NewBackend(c, c)
	first, err := be.ReceiveStartupMessage()
	if err != nil {
		return
	}
	start, ok := first.(*pgproto3.StartupMessage)
	if !ok {
		// The proxy answers requests for encryption itself; one arriving
		// here is a bug.
		f.t.Errorf("fake postgres: the connection opened with %T", first)
		return
	}
	be.Send(&pgproto3.AuthenticationCleartextPassword{})
	if be.Flush() != nil {
		return
	}
	be.SetAuthType(pgproto3.AuthTypeCleartextPassword)
	msg, err := be.Receive()
	if err != nil {
		return
	}
	if pw, ok := msg.(*pgproto3.PasswordMessage); !ok || pw.Password != fakePassword {
		be.Send(&pgproto3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28P01", Message: "password authentication failed"})
		be.Flush()
		return
	}
	be.Send(&pgproto3.AuthenticationOk{})
	for k, v := range map[string]string{
		"server_version":              "17.0 (fake)",
		"client_encoding":             "UTF8",
		"standard_conforming_strings": "on",
		"integer_datetimes":           "on",
		"application_name":            start.Parameters["application_name"],
	} {
		be.Send(&pgproto3.ParameterStatus{Name: k, Value: v})
	}
	be.Send(&pgproto3.BackendKeyData{ProcessID: 4242, SecretKey: []byte{1, 2, 3, 4}})
	be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if be.Flush() != nil {
		return
	}

	status := byte('I')
	stmts := map[string]string{}
	portals := map[string]fakePortal{}
	// run executes one statement and sends its result.
	run := func(sql string, params []string, describe bool) {
		switch word := strings.ToLower(firstWord(sql)); word {
		case "begin":
			status = 'T'
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("BEGIN")})
		case "commit":
			status = 'I'
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("COMMIT")})
		case "select":
			if describe {
				be.Send(rowDescription())
			}
			value := "Acme customer"
			if len(params) > 0 {
				value += " " + strings.Join(params, ",")
			}
			be.Send(&pgproto3.DataRow{Values: [][]byte{[]byte(value)}})
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")})
		case "insert":
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("INSERT 0 1")})
		default:
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte(strings.ToUpper(word))})
		}
	}
	for {
		msg, err := be.Receive()
		if err != nil {
			return
		}
		switch m := msg.(type) {
		case *pgproto3.Terminate:
			return
		case *pgproto3.Flush:
			be.Flush()
		case *pgproto3.Sync:
			be.Send(&pgproto3.ReadyForQuery{TxStatus: status})
			if be.Flush() != nil {
				return
			}
		case *pgproto3.Query:
			run(strings.TrimRight(strings.TrimSpace(m.String), ";"), nil, true)
			be.Send(&pgproto3.ReadyForQuery{TxStatus: status})
			if be.Flush() != nil {
				return
			}
		case *pgproto3.Parse:
			stmts[m.Name] = m.Query
			be.Send(&pgproto3.ParseComplete{})
		case *pgproto3.Bind:
			p := fakePortal{sql: stmts[m.PreparedStatement]}
			for _, v := range m.Parameters {
				p.params = append(p.params, string(v))
			}
			portals[m.DestinationPortal] = p
			be.Send(&pgproto3.BindComplete{})
		case *pgproto3.Describe:
			sql := portals[m.Name].sql
			if m.ObjectType == 'S' {
				sql = stmts[m.Name]
				oids := make([]uint32, paramCount(sql))
				for i := range oids {
					oids[i] = 25 // text
				}
				be.Send(&pgproto3.ParameterDescription{ParameterOIDs: oids})
			}
			if strings.ToLower(firstWord(sql)) == "select" {
				be.Send(rowDescription())
			} else {
				be.Send(&pgproto3.NoData{})
			}
		case *pgproto3.Execute:
			p := portals[m.Portal]
			run(p.sql, p.params, false)
		case *pgproto3.Close:
			if m.ObjectType == 'S' {
				delete(stmts, m.Name)
			} else {
				delete(portals, m.Name)
			}
			be.Send(&pgproto3.CloseComplete{})
		default:
			f.t.Errorf("fake postgres: unexpected %T", msg)
			return
		}
	}
}

var reParam = regexp.MustCompile(`\$(\d+)`)

// paramCount is the highest $n in the statement.
func paramCount(sql string) int {
	n := 0
	for _, m := range reParam.FindAllStringSubmatch(sql, -1) {
		if i, _ := strconv.Atoi(m[1]); i > n {
			n = i
		}
	}
	return n
}

func firstWord(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func rowDescription() *pgproto3.RowDescription {
	// One text column.
	return &pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{
		Name: []byte("v"), DataTypeOID: 25, DataTypeSize: -1, TypeModifier: -1,
	}}}
}

func dbURL(addr string) string {
	return fmt.Sprintf("postgres://app:%s@%s/acme?sslmode=disable", fakePassword, addr)
}
