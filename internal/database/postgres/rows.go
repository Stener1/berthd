package postgres

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cosscom/shipyard/internal/database"
)

// short reports whether a column's values are always a few bytes: they are
// selected as they are. Every other column is selected as the start of its
// text, so a cell of a hundred megabytes is cut by the server and never
// sent.
//
// The few types whose cast to text is not what Postgres prints are here
// for that too: inet and cidr (the cast adds a netmask) and char(n) (it
// drops the padding).
func short(col colInfo) bool {
	switch col.base {
	case oidBool, oidInt2, oidInt4, oidInt8, oidOID, oidFloat4, oidFloat8,
		oidDate, oidTime, oidTimetz, oidTimestamp, oidTimestamptz, oidInterval, oidUUID,
		oidChar, oidName, oidCIDR, oidInet, oidMacaddr, oidMacaddr8:
		return true
	case oidBpchar:
		return col.typmod > 4 && col.typmod-4 <= database.MaxCell
	}
	return false
}

// compare is the SQL of each filter operator that compares. What a request
// says is looked up here and never written into the statement.
var compare = map[string]string{"=": "=", "!=": "<>", "<": "<", "<=": "<=", ">": ">", ">=": ">="}

func (c *conn) Rows(ctx context.Context, in database.RowsRequest) (*database.RowsResult, error) {
	out := &database.RowsResult{}
	grid := c.grid()
	err := c.within(ctx, false, true, c.timeout, func(ctx context.Context, pc *pgconn.PgConn) error {
		t, err := describe(ctx, pc, in.Schema, in.Table)
		if err != nil {
			return err
		}
		out.PrimaryKey, out.RowsEstimate = t.pk, t.estimate
		out.Columns = make([]database.Column, len(t.cols))
		list := make([]string, len(t.cols))
		for i, col := range t.cols {
			out.Columns[i] = database.Column{Name: col.name, Type: col.typ}
			list[i] = quote(col.name)
			if !short(col) {
				list[i] = fmt.Sprintf("left(%s::text, %d)", quote(col.name), database.MaxCell+1)
			}
		}
		if len(list) == 0 {
			// A table with no columns still has rows.
			list = []string{"NULL"}
		}
		var sql strings.Builder
		var params [][]byte
		arg := func(text string) string {
			params = append(params, []byte(text))
			return "$" + strconv.Itoa(len(params))
		}
		fmt.Fprintf(&sql, "SELECT %s FROM %s.%s", strings.Join(list, ", "), quote(in.Schema), quote(in.Table))
		for i, f := range in.Filters {
			if !t.has(f.Column) {
				return database.Bad("There is no column %s to filter on.", quote(f.Column))
			}
			if i == 0 {
				sql.WriteString(" WHERE ")
			} else {
				sql.WriteString(" AND ")
			}
			col := quote(f.Column)
			switch f.Op {
			case "is_null":
				sql.WriteString(col + " IS NULL")
			case "not_null":
				sql.WriteString(col + " IS NOT NULL")
			case "like":
				sql.WriteString(col + "::text LIKE " + arg(f.Value.Text))
			case "ilike":
				sql.WriteString(col + "::text ILIKE " + arg(f.Value.Text))
			default:
				op, ok := compare[f.Op]
				if !ok {
					return database.Bad("%q is not a filter operator.", f.Op)
				}
				// The parameter has no type of its own, so the server
				// reads it as the column's.
				sql.WriteString(col + " " + op + " " + arg(f.Value.Text))
			}
		}
		// The primary key last, so that rows which sort alike keep one
		// order from page to page.
		var order []string
		ordered := map[string]bool{}
		for _, o := range in.OrderBy {
			if !t.has(o.Column) {
				return database.Bad("There is no column %s to sort by.", quote(o.Column))
			}
			if ordered[o.Column] {
				continue
			}
			ordered[o.Column] = true
			term := quote(o.Column)
			if o.Desc {
				term += " DESC"
			}
			order = append(order, term)
		}
		for _, key := range t.pk {
			if !ordered[key] {
				order = append(order, quote(key))
			}
		}
		if len(order) > 0 {
			sql.WriteString(" ORDER BY " + strings.Join(order, ", "))
		}
		// One more than asked for, to know whether there are more.
		sql.WriteString(" LIMIT " + arg(strconv.Itoa(in.Limit+1)) + " OFFSET " + arg(strconv.Itoa(in.Offset)))

		res, err := run(ctx, pc, sql.String(), params, in.Limit, func(fields []field, values [][]byte) bool {
			row := make([]any, len(t.cols))
			for i := range row {
				if i < len(values) && i < len(fields) {
					row[i] = cell(fields[i].oid, values[i])
				}
			}
			return grid.Add(row)
		})
		if err != nil {
			return err
		}
		out.More = res.more
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Rows, out.Cut = grid.Rows, grid.Cut
	return out, nil
}

func (c *conn) Query(ctx context.Context, in database.QueryRequest) (*database.QueryResult, error) {
	count, lead := statements(in.SQL)
	switch {
	case count == 0:
		return nil, database.Bad("sql is required.")
	case count > 1:
		return nil, &database.Problem{Status: http.StatusBadRequest, Code: "multiple_statements",
			Message: fmt.Sprintf("That is %d statements; the editor runs one at a time.", count),
			Hint:    "Run them one by one."}
	case lead == "COPY" && !in.Write:
		// A read-only transaction does not stop COPY from writing a file
		// on the server's machine, or from running a program there.
		p := database.ReadOnly()
		p.Message = "COPY runs only while writes are on: it can write files and run programs on the database's machine."
		return nil, p
	}
	out := &database.QueryResult{Columns: []database.Column{}}
	grid := c.grid()
	err := c.within(ctx, in.Write, false, c.timeout, func(ctx context.Context, pc *pgconn.PgConn) error {
		began := time.Now()
		res, err := run(ctx, pc, in.SQL, nil, in.Limit, func(fields []field, values [][]byte) bool {
			row := make([]any, len(fields))
			for i := range row {
				if i < len(values) {
					row[i] = cell(fields[i].oid, values[i])
				}
			}
			return grid.Add(row)
		})
		if err != nil {
			return err
		}
		out.DurationMs = float64(time.Since(began).Microseconds()) / 1000
		out.Truncated, out.Notices = res.more, res.notices
		command, rows, counted := tagged(res.tag)
		if command == "" {
			// Cut short before the server said what ran.
			command = lead
		}
		// Rows are fetched a few at a time, and the server's count is then
		// of the last fetch alone. So for a statement that returns rows
		// (a SELECT, or a change with RETURNING, which returns each row it
		// changed) the rows that came are the count; the server's is for
		// one that returns none.
		if !counted || len(res.fields) > 0 {
			rows = int64(len(grid.Rows))
		}
		out.Command, out.RowCount = command, rows
		types, err := typeNames(ctx, pc, res.fields)
		if err != nil {
			return err
		}
		for i, f := range res.fields {
			out.Columns = append(out.Columns, database.Column{Name: f.name, Type: types[i]})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Rows, out.Cut = grid.Rows, grid.Cut
	return out, nil
}

// typeNames asks the server what a result's types are called, as it writes
// them in a table's definition: "character varying(20)", "integer[]".
func typeNames(ctx context.Context, pc *pgconn.PgConn, fields []field) ([]string, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	oids, mods := make([]string, len(fields)), make([]string, len(fields))
	for i, f := range fields {
		oids[i], mods[i] = strconv.FormatUint(uint64(f.oid), 10), strconv.Itoa(int(f.typmod))
	}
	rows, err := read(ctx, pc, `SELECT format_type(t.oid, t.typmod)
		FROM unnest($1::oid[], $2::int4[]) WITH ORDINALITY AS t(oid, typmod, n) ORDER BY t.n`,
		"{"+strings.Join(oids, ",")+"}", "{"+strings.Join(mods, ",")+"}")
	if err != nil {
		return nil, err
	}
	out := make([]string, len(fields))
	for i := range out {
		if i < len(rows) {
			out[i] = rows[i][0]
		}
	}
	return out, nil
}

func (c *conn) Edit(ctx context.Context, in database.EditRequest) (*database.EditResult, error) {
	out := &database.EditResult{}
	name := quote(in.Schema) + "." + quote(in.Table)
	// Every statement of a batch has the timeout to itself.
	budget := c.timeout * time.Duration(min(len(in.Changes), 6))
	err := c.within(ctx, true, true, budget, func(ctx context.Context, pc *pgconn.PgConn) error {
		t, err := describe(ctx, pc, in.Schema, in.Table)
		if err != nil {
			return err
		}
		for i, ch := range in.Changes {
			at := fmt.Sprintf("Change %d", i+1)
			var params [][]byte
			arg := func(v database.Value) string {
				params = append(params, param(v))
				return "$" + strconv.Itoa(len(params))
			}
			set := database.Names(ch.Set)
			for _, col := range set {
				if !t.has(col) {
					return database.Bad("%s: there is no column %s in %s.", at, quote(col), name)
				}
			}
			// The row an update or a delete means: by the whole primary
			// key, in the key's own order.
			where := func() (string, error) {
				if len(t.pk) == 0 {
					return "", &database.Problem{Status: http.StatusBadRequest, Code: "no_primary_key",
						Message: fmt.Sprintf("%s has no primary key, so the editor cannot tell one of its rows from another.", name),
						Hint:    "Rows can be added to it. To change or remove one, run a statement."}
				}
				if len(ch.Key) != len(t.pk) {
					return "", database.Bad("%s: the key must name the whole primary key of %s: %s.", at, name, strings.Join(t.pk, ", "))
				}
				terms := make([]string, len(t.pk))
				for j, key := range t.pk {
					v, ok := ch.Key[key]
					if !ok {
						return "", database.Bad("%s: the key must name the whole primary key of %s: %s.", at, name, strings.Join(t.pk, ", "))
					}
					terms[j] = quote(key) + " = " + arg(v)
				}
				return strings.Join(terms, " AND "), nil
			}
			var sql string
			switch ch.Op {
			case "insert":
				if len(set) == 0 {
					sql = "INSERT INTO " + name + " DEFAULT VALUES"
					break
				}
				cols, vals := make([]string, len(set)), make([]string, len(set))
				for j, col := range set {
					cols[j], vals[j] = quote(col), arg(ch.Set[col])
				}
				sql = "INSERT INTO " + name + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ")"
			case "update":
				assign := make([]string, len(set))
				for j, col := range set {
					assign[j] = quote(col) + " = " + arg(ch.Set[col])
				}
				cond, err := where()
				if err != nil {
					return err
				}
				sql = "UPDATE " + name + " SET " + strings.Join(assign, ", ") + " WHERE " + cond
			case "delete":
				cond, err := where()
				if err != nil {
					return err
				}
				sql = "DELETE FROM " + name + " WHERE " + cond
			default:
				return database.Bad(`%s: op is "update", "insert" or "delete".`, at)
			}
			n, err := change(ctx, pc, sql, params)
			if err != nil {
				// Which change it was; where in a statement the caller
				// never saw means nothing to them.
				if p, ok := c.problem(err).(*database.Problem); ok && p.Code == "query_failed" {
					p.Message, p.Position = at+": "+p.Message, 0
					return p
				}
				return err
			}
			if n != 1 {
				if ch.Op == "insert" {
					return database.Conflict("%s: the row was not added.", at)
				}
				return database.Conflict("%s: the row is not there any more.", at)
			}
			switch ch.Op {
			case "insert":
				out.Inserted++
			case "update":
				out.Updated++
			case "delete":
				out.Deleted++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
