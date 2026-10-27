# The berth-database API

`berth-database` is a helper that runs on a box beside its dev servers and
lets Shipyard's Database tab browse and edit **a worktree's own development
database**: its schema, a table's rows, one statement at a time, and edits to
rows by their primary key. It is one small static program with no cgo. It
opens Postgres today, behind an interface that the next engine fits into
([Engines](#engines)).

It can run SQL, so it is closed in ways Shipyard's other helpers are not:
every route but `/api/health` needs a token only the box daemon holds, a
database is opened only if it is on this machine, nothing is written unless
writes were turned on for that database in the last 15 minutes, and what was
written is logged by its shape and never by its values.

This file is everything a UI, or the box daemon, needs to use it. The code is
`cmd/berth-database` and the packages in this folder; the routes are in
`server/`. Change them together with this file: the UI is written from it.

Every example is a real response, from a helper attached to a made-up "acme"
database on a Postgres 17 that listened on port 55432.

- [Running it](#running-it)
- [Who may ask: the token](#who-may-ask-the-token)
- [Which databases](#which-databases)
- [Values](#values)
- [The guard rails](#the-guard-rails)
- [Errors](#errors)
- [The routes](#the-routes)
- [Engines](#engines)

## Running it

```sh
berth-database serve --dir DIR [--addr 127.0.0.1:4326] [--statement-timeout 10s]
berth-database version
```

| flag | variable | default | what |
| --- | --- | --- | --- |
| `--dir` | `BERTH_DATABASE_DIR` | none: required | The data directory. Made if missing, mode `0700`. |
| `--addr` | `BERTH_DATABASE_ADDR` | `127.0.0.1:4326` | Where the API is served. Must be loopback (`127.0.0.1`, `[::1]`, `localhost`); anything else is refused at the start. Port `0` takes any free port, and at the next start the same one again if it is still free. |
| `--statement-timeout` | `BERTH_DATABASE_STATEMENT_TIMEOUT` | `10s` | How long a statement may run before the database stops it: a Go duration from `1s` to `1h`. It is the helper's to set, never a request's. |

A flag wins over its variable. `SIGINT` or `SIGTERM` stops it: statements in
flight end, its connections close and it exits 0. A start that fails prints
one line to stderr and exits 1. Its log goes to stderr.

What it keeps, all in `--dir`, all readable by its owner alone (`0600`):

| file | what |
| --- | --- |
| `connections.json` | The databases of the worktrees it was attached to: each one's URL, **with its password**, the worktree it belongs to and the variable it came in. See [Which databases](#which-databases) for why it is kept and when an entry goes. |
| `helper.json` | Where the running helper listens, and its token. Written once it is listening, removed when it stops. |
| `listen.json` | The address a helper started on port 0 was given, kept when it stops, so the next start asks for the same port first. |

`helper.json`:

```json
{
  "api": "http://127.0.0.1:63498",
  "token": "3f1c…64 hex digits…9ab2",
  "pid": 64425,
  "version": "dev"
}
```

`api` is the base URL for every route below. `token` is new at every start:
32 random bytes, in hex.

The helper holds no data of its own. Removing it with its data removes
`connections.json`, and with it the only copy it had of any password.

## Who may ask: the token

- **The token.** Every route but [`GET /api/health`](#get-apihealth) needs
  `Authorization: Bearer <token>`, the `token` of `helper.json`. Without it,
  or with another, the answer is `401`:

  ```json
  {
    "error": {
      "code": "unauthorized",
      "message": "This route needs the helper's token.",
      "hint": "The box daemon sends it with what it relays: ask through the box, not the helper."
    }
  }
  ```

  The box daemon reads the token from the ready file and adds the header to
  everything it relays to the helper, and to its own attach and detach calls;
  whatever `Authorization` the app sent is taken off first. So the UI never
  sees the token and never sends one: it asks the box
  (`helpers/database/api/api/db/…`), and that is the only way in.
- **Localhost only.** The helper listens on loopback, and a request must name
  a loopback host in its `Host` header (`127.0.0.1`, `[::1]`, `localhost`),
  which stops DNS rebinding; anything else is a `403`.
- **No pages.** A request with an `Origin` header is a `403`, whatever the
  origin and whatever token it carries. No page reaches this helper directly,
  Shipyard's own window included: it goes through the box daemon, which sends
  no `Origin`. There are no CORS headers.
- **Bodies are JSON**, at most 4 MB (`/api/attach`, `/api/detach` and
  `/writes`: 256 KB). A larger one is a `413`.
- **Lists are never `null`.** An empty list is `[]`.
- **Answers are not to be cached**: each has `Cache-Control: no-store`.

What the token does and does not do: it keeps out the other users of the
machine (who can reach a loopback port, and cannot read a `0600` file) and
every web page (which cannot read a file at all). It does not keep out a
program running as the same user, which can read the ready file, and could
read the project's own configuration and open the database itself.

## Which databases

The helper opens what the box daemon tells it a worktree has, and nothing
else: no route of the helper's own takes a URL from the UI. The box daemon
tells it in three cases, always by
[`POST /api/attach`](#post-apiattach), and tells them apart by `command`:

| `command` | what the variables are |
| --- | --- |
| a service's name (`web`) | The service's environment, before the box daemon starts it. |
| `.env` | What the worktree's own `.env` files say, read by the box daemon because the worktree's tab was opened. |
| `manual` | An address a person gave for the worktree in the app (**Connect a database…**), which the box daemon passes on in its own route and keeps nowhere. With no variables, that address is taken back. |

The rules below are the same for all three: a pasted address that is not a
database on this machine is passed over like any other.

Before the box daemon starts a worktree's service it calls
[`POST /api/attach`](#post-apiattach) with the variables of the service's
environment that the plugin's manifest names (`DATABASE_URL`,
`POSTGRES_URL`). For each one that is the URL of a database:

- **Its engine** is found by the URL's scheme: `postgres://`, `postgresql://`,
  with a driver suffix (`postgresql+psycopg://`) or a `jdbc:` prefix. A URL
  no engine opens is passed over.
- **It must be on this machine**: `localhost`, a name under `.localhost`, a
  loopback address, `0.0.0.0` (dialled as `127.0.0.1`), or a unix socket
  (`?host=/var/run/postgresql`). Anything else is passed over, and is not in
  `connections`: the editor is for a development database of the worktree's
  own, never one shared with anybody. The address is checked again when a
  connection is dialled: a name that resolves to somewhere else is not
  connected to.
- **It becomes a connection** of the worktree's **source**:
  `<project>/<worktree>`, or the project alone for its main checkout, with
  `/`, `:`, `\` and white space in either name written as `-`. It is the name
  Leadline lists the same worktree under. A worktree's several commands
  (`web`, `worker`) share its connections: the same database in two of them
  is one connection.

Attach **changes nothing of the service's environment**: the answer's `env`
is always empty. The helper connects straight to the database with the URL
the service has, so nothing the editor runs passes through anything else that
watches the service's connections (Leadline's recording proxy, when that
helper is on too, is given the same original URL and stands only in front of
the service).

A connection is kept until its worktree is detached
([`POST /api/detach`](#post-apidetach), which the box daemon calls when the
worktree is removed), or until every command that had it starts again
without it. It is **kept across a restart** of the helper, in
`connections.json`: the box daemon says which database a worktree has once,
when the service starts, and a helper that forgot at every restart would show
an empty tab until each dev server was started again. Writes being on is not
kept: after a restart they are off.

## Values

A cell of `rows`, in every answer, is JSON:

| the value | the cell |
| --- | --- |
| NULL | `null` |
| a boolean | `true`, `false` |
| an integer that a double holds exactly (up to 2^53 either way) | a number |
| a larger integer (`bigint` beyond 2^53) | a string of its digits: `"9007199254740993"` |
| a float | a number; `"NaN"`, `"Infinity"` and `"-Infinity"` as strings |
| `numeric` | a string, with every digit: `"12345678.90"` |
| `timestamp`, `timestamptz` | ISO 8601: `"2026-10-01T09:00:00"`, `"2026-10-01T09:00:00Z"`, `"2026-10-03T13:45:00.5+02:00"` in the database's own time zone. `infinity` and dates before Christ stay as Postgres prints them. |
| `date`, `time`, `interval` | as Postgres prints them: `"2026-10-01"`, `"09:00:00"`, `"1 day 02:00:00"` |
| `uuid` | a string |
| `bytea` | `"\\xdeadbeef"`: `\x` and hex |
| `json`, `jsonb` | **their text**, as a string: `"{\"gift\": true}"`. Parse it to show it as a tree. |
| an array, an enum, `inet`, anything else | a string, as Postgres prints it: `"{1,2,3}"` |

**A cell longer than 4,000 characters is cut** to its first 4,000, and listed
in the answer's `cut` as `[row, column]`, both counted from 0. `cut` is
always there; `[]` when nothing was cut. In [`rows`](#post-apidbconnrows) the
database does the cutting, so a huge cell is never read; to see a whole one,
select it with [`query`](#post-apidbconnquery) in parts
(`substring(body from 4001 for 4000)`).

A value going in (`value` of a filter, and the values of `key` and `set`) is
a JSON scalar that Postgres casts to the column's type:

| JSON | what the database is given |
| --- | --- |
| `null` | NULL |
| a string | its characters: `"2026-10-12T08:30:00Z"`, `"{4,5}"`, `"\\x00ff"`, `"sad"` |
| a number | its digits exactly as written, so `12345678901234567890` and `0.10` lose nothing |
| `true`, `false` | those words |
| an object or an array | its JSON, for a `json` or `jsonb` column |

A value the column cannot be (`"seven"` for an integer, a string too long) is
refused by the database, with its own message. So a cell read from one
answer can be sent back as it came.

## The guard rails

- **Read only, by the database.** Every statement runs in a transaction of
  its own, and unless writes are on for the connection that transaction is
  `READ ONLY`: Postgres itself refuses an `INSERT`, a writable `WITH`, DDL,
  `nextval`. The statement is not read to decide; nothing a statement says
  can make its transaction read-write again.
- **Writes are a switch with a timer.** [`POST …/writes`](#post-apidbconnwrites)
  turns them on for one connection for 15 minutes, in the helper's memory:
  they are off after that, and after a restart. Whether a statement may write
  is decided by the helper's clock, never by anything in the request.
- **What READ ONLY does not stop.** It stops writes to the database. A
  superuser's statement can still do things outside it that are not writes
  to a table: `COPY … TO` a file or a program is refused while writes are
  off, by its first word; the same thing wrapped in a function or a `DO`
  block is not caught. The editor is for a development database whose
  password the developer already has; it is not a sandbox.
- **One statement.** `query` takes one statement. Two are refused before
  anything is sent (`multiple_statements`), and the server is also asked to
  parse a single statement, which it refuses to do with two.
- **A timeout.** A statement is stopped by the database after
  `--statement-timeout` (10 seconds): `timeout`.
- **Caps.** `rows` gives at most 500 rows, `query` at most 5,000; a cell at
  most 4,000 characters; and an answer's rows about 8 MB together, past
  which the rows stop and the answer says there were more (`more`,
  `truncated`). The rest of a large result is never fetched from the
  database. A single row of more than 64 MB ends the statement
  (`too_large`).
- **Identifiers are quoted by the helper** and values are sent as
  parameters, in every statement it writes itself: a table called
  `Order Items` or `select` works, and a name or a value that looks like SQL
  is only a name or a value.
- **A statement leaves nothing behind.** Each `query` has a session to
  itself, closed after it: a `SET`, a temporary table or an advisory lock
  does not reach the next statement.
- **A few connections.** At most 4 to a database, opened when first needed,
  closed after a minute unused, and closed at once when the worktree is
  detached or the helper stops.
- **The log.** Every edit, and every statement run while writes are on, is a
  line in the helper's log: the worktree, the connection, the table and
  columns or the statement's shape (every string and number replaced by
  `?`), and the row counts. Never a value, a password, a URL or the token.

  ```
  INFO edit source=acme/fix-orders connection=c_1c28ba20e1b6 table="\"public\".\"orders\"" columns="\"customer_id\", \"id\", \"notes\", \"status\", \"total\"" changes=3 updated=1 inserted=1 deleted=1
  INFO statement with writes on source=acme/fix-orders connection=c_1c28ba20e1b6 shape="UPDATE orders SET total = total * ? WHERE customer_id = ? RETURNING id, total" command=UPDATE rows=1
  ```

## Errors

A failed request answers with a status that fits and this body:

```json
{
  "error": {
    "code": "query_failed",
    "message": "Syntax error at or near \"FORM\".",
    "sqlstate": "42601",
    "position": 10
  }
}
```

`message` is a sentence for the person at the screen. `hint`, when there is
one, is what to try. `code` is for the caller to branch on. For a statement
the database refused there may also be `sqlstate` (Postgres's five
characters), `position` (the character of the statement it stopped at,
counted from 1) and `detail` (the database's own detail, which can quote a
row).

| code | status | when |
| --- | --- | --- |
| `bad_request` | 400, 413 | the body or a field cannot be read, is missing or is out of range; a column that is not in the table; `COPY` to or from the caller |
| `multiple_statements` | 400 | `query` was given more than one statement |
| `no_primary_key` | 400 | an update or a delete on a table with no primary key |
| `query_failed` | 400 | the database refused the statement, or a change of an edit |
| `timeout` | 400 | the statement ran past the timeout and was stopped |
| `too_large` | 400 | one row of the answer is larger than the helper reads |
| `unauthorized` | 401 | no token, or not the helper's |
| `forbidden` | 403 | the `Host` is not loopback, or the request came from a page |
| `read_only` | 403 | a write while writes are off for the connection |
| `not_found` | 404 | no such connection, table or route |
| `row_conflict` | 409 | an edit's row is not there as the editor saw it; nothing was changed |
| `db_unavailable` | 503 | the database could not be reached, or would not let the editor in |
| `shutting_down` | 503 | the helper is stopping |
| `not_saved`, `failed` | 500 | the helper's own fault; its log says what kind |

## The routes

| route | what |
| --- | --- |
| [`GET /api/health`](#get-apihealth) | Is it there, which version, which engines |
| [`POST /api/attach`](#post-apiattach) | The box daemon: a worktree's command and its database |
| [`POST /api/detach`](#post-apidetach) | The box daemon: forget a worktree |
| [`GET /api/db/connections`](#get-apidbconnections) | A worktree's databases, and what each can do |
| [`GET /api/db/{conn}/schema`](#get-apidbconnschema) | Schemas, tables, columns, keys and indexes |
| [`POST /api/db/{conn}/rows`](#post-apidbconnrows) | A page of a table, filtered and sorted |
| [`POST /api/db/{conn}/query`](#post-apidbconnquery) | Run one statement |
| [`POST /api/db/{conn}/edit`](#post-apidbconnedit) | Change rows by their primary key, all or nothing |
| [`POST /api/db/{conn}/writes`](#post-apidbconnwrites) | Turn writes on for 15 minutes, or off |

`{conn}` is a connection's `id`.

### GET /api/health

No parameters, and no token.

```json
{
  "ok": true,
  "name": "berth-database",
  "version": "dev",
  "api": 1,
  "engines": ["postgres"]
}
```

`name` is always `berth-database`. `version` is the build's. `api` is this
API's own number, `1`: it goes up when a route or a field is taken away or
changes meaning, not when one is added. `engines` are the kinds of database
this build opens.

### POST /api/attach

```
POST /api/attach
{"box": "devbox", "project": "acme", "worktree": "fix-orders", "branch": "fix/orders-n-plus-one",
 "path": "/home/dev/acme-fix-orders", "command": "web",
 "env": {"DATABASE_URL": "postgres://app:hunter2@localhost:55432/acme?schema=public",
         "POSTGRES_URL": "postgres://app:hunter2@db.acme.example:5432/acme"}}
```

| field | default | what |
| --- | --- | --- |
| `project` | required | The repository, as Shipyard names it. |
| `worktree` | `""` | The worktree's name; empty for the project's main checkout. |
| `command` | `""` | The service's name in the project's config. `.env` when the variables are not a service's but what the box daemon read in the worktree's own `.env` files, because the worktree's tab was opened: no service has that name, so what the files say is kept apart from what a service was started with. `manual` when they are an address a person gave in the app. |
| `env` | `{}` | The variables of the service's environment that the plugin's manifest names. |
| `box`, `branch`, `path` | none | Taken and not used. |

What the box daemon asks before it starts a worktree's service, and when
the worktree's Database tab is opened and it has looked in the worktree's
`.env` files (`command` is then `.env`). The helper
reads each value ([Which databases](#which-databases)) and notes the ones
that are a database on this machine as connections of the worktree.

```json
{
  "env": {},
  "source": "acme/fix-orders",
  "connections": ["c_1c28ba20e1b6"]
}
```

- `env` is always `{}`: nothing of the service's environment is changed.
- `source` is the name the connections are listed under.
- `connections` are the ids of the databases this command has. Here the
  second URL, on another machine, was passed over; the log says so, by the
  variable's name and not its value.

**Asking again is safe**, and is how a change arrives: the command has what
it says now. A connection it no longer names goes, unless another command of
the worktree still has it. The same database keeps its `id` every time, and
across restarts; a new password for it replaces the old one. A missing
`project` is a `400`.

### POST /api/detach

```
POST /api/detach
{"project": "acme", "worktree": "fix-orders"}
```

| field | what |
| --- | --- |
| `project`, `worktree` | The worktree, as in attach. |
| `source` | Or its source outright: `"acme/fix-orders"`. |
| `service` | Or a service name as Shipyard's other helpers take it: `"acme/fix-orders"`, `"acme/fix-orders:web"`. |

Has the helper forget a worktree, for when the worktree is removed: its
connections close, statements in flight on them end, and its entries, with
their passwords, are taken out of `connections.json` before the answer is
sent. One of the three ways to name it is required.

```json
{
  "source": "acme/fix-orders",
  "found": true,
  "connections": 1
}
```

`found`: the helper had a database of that worktree. Detaching one it never
heard of is no error: `"found": false`. `connections`: how many were
forgotten.

### GET /api/db/connections

```
GET /api/db/connections?source=acme/fix-orders
```

| parameter | default | what |
| --- | --- | --- |
| `source` | all | Only this worktree's. |

The databases of a worktree, by `label`. Here with writes on:

```json
{
  "connections": [
    {
      "id": "c_1c28ba20e1b6",
      "source": "acme/fix-orders",
      "label": "DATABASE_URL",
      "engine": "postgres",
      "database": "acme",
      "host": "localhost",
      "port": 55432,
      "user": "app",
      "writes_until": 1791790863954,
      "capabilities": {
        "dialect": "postgresql",
        "schemas": true,
        "operators": ["=", "!=", "<", "<=", ">", ">=", "like", "ilike", "is_null", "not_null"],
        "kinds": ["table", "view", "materialized_view"],
        "writes": true,
        "transactions": true
      }
    }
  ]
}
```

| field | what |
| --- | --- |
| `id` | For `{conn}`. It is made from what the connection is (worktree, engine, host, port, database, user), so it stays the same across attaches and restarts. |
| `source` | The worktree. |
| `label` | The variable the service has the database in: `DATABASE_URL`. |
| `engine` | `"postgres"`. |
| `database`, `user` | As the URL names them. |
| `host`, `port` | Where it is. For a unix socket `host` is the socket's folder. |
| `writes_until` | When writes go off again, in milliseconds since 1970. Left out while they are off. |
| `capabilities` | What the UI may offer for this connection; see below. |

There is never a password in it, and never a URL.

**Capabilities** are what to read in place of assuming Postgres:

| field | what |
| --- | --- |
| `dialect` | The SQL, for an editor's highlighting: `"postgresql"`. |
| `schemas` | Tables live in named schemas. `false`: there is one schema, named `""`, and `schema` is left out of requests. |
| `operators` | The `op`s a filter of `rows` may use. |
| `kinds` | The `kind`s `schema` may list. |
| `writes` | `edit` and turning writes on can work at all. `false`: both are a `400`. |
| `transactions` | An edit's changes are applied all or not at all. |

Listing connections opens none of them: a database that is down is still
listed, and says so (`db_unavailable`) when it is first asked for something.

### GET /api/db/{conn}/schema

```
GET /api/db/c_1c28ba20e1b6/schema
```

Everything there is to browse: every schema but the system's
(`pg_catalog`, `information_schema`, `pg_toast`, temporary schemas), by name,
and in each its tables, views and materialized views, by name. Shortened
here to one of the three tables and the view:

```json
{
  "schemas": [
    {
      "name": "public",
      "tables": [
        {
          "name": "open_orders",
          "kind": "view",
          "rows_estimate": 0,
          "columns": [
            {"name": "id", "type": "bigint", "nullable": true},
            {"name": "customer_id", "type": "integer", "nullable": true},
            {"name": "total", "type": "numeric(10,2)", "nullable": true}
          ],
          "primary_key": [],
          "indexes": [],
          "foreign_keys": []
        },
        {
          "name": "orders",
          "kind": "table",
          "rows_estimate": 3,
          "columns": [
            {"name": "id", "type": "bigint", "nullable": false, "primary_key": true},
            {"name": "customer_id", "type": "integer", "nullable": false},
            {"name": "status", "type": "text", "nullable": false, "default": "'new'::text"},
            {"name": "total", "type": "numeric(10,2)", "nullable": true},
            {"name": "notes", "type": "jsonb", "nullable": true}
          ],
          "primary_key": ["id"],
          "indexes": [
            {"name": "orders_pkey", "columns": ["id"], "unique": true},
            {"name": "orders_by_customer", "columns": ["customer_id", "status"], "unique": false}
          ],
          "foreign_keys": [
            {"columns": ["customer_id"], "ref_schema": "public", "ref_table": "customers", "ref_columns": ["id"]}
          ]
        }
      ]
    }
  ]
}
```

- A schema with no tables is listed, with `"tables": []`.
- `kind`: `"table"` (a partitioned or foreign table too), `"view"` or
  `"materialized_view"`.
- `rows_estimate`: about how many rows, without counting them: the
  planner's number from the last `ANALYZE`, or for a table that never had
  one what the statistics have seen go in and out. `0` for a view.
- `columns[]`, in the table's order:
  - `type` is the type as Postgres writes it in a definition:
    `"character varying(40)"`, `"numeric(10,2)"`, `"integer[]"`,
    `"timestamp with time zone"`; an enum or a domain by its name, with its
    schema when that is not on the search path.
  - `default` is the default as Postgres writes it
    (`"nextval('customers_id_seq'::regclass)"`, `"now()"`); for an identity
    column `"generated always as identity"` or
    `"generated by default as identity"`; for a computed one
    `"generated always as (…) stored"`. Left out for a column with none. A
    column with a default can be left out of an insert; a
    `generated always` one must be.
  - `primary_key` is `true` on the columns of the key, and left out on the
    others.
- `primary_key`: the key's columns, in the key's order; `[]` for none.
- `indexes`: the primary key's first, then by name. A column of an index
  that is an expression is that expression (`"lower(email)"`).
- `foreign_keys`: `columns` of this table that point at `ref_columns` of
  `ref_schema`.`ref_table`, pair by pair.

### POST /api/db/{conn}/rows

```
POST /api/db/c_1c28ba20e1b6/rows
{"schema": "public", "table": "orders", "limit": 2,
 "order_by": [{"column": "total", "desc": true}],
 "filters": [{"column": "status", "op": "=", "value": "new"}]}
```

| field | default | what |
| --- | --- | --- |
| `schema`, `table` | required | The table, a view or a materialized view. |
| `limit` | 100 (1 to 500) | How many rows. More than 500 is 500. |
| `offset` | 0 | Rows to skip. |
| `order_by` | the primary key | `[{"column", "desc"?}]`, the first sorting first. |
| `filters` | none | `[{"column", "op", "value"?}]`; a row must pass all of them. |

A page of a table.

```json
{
  "columns": [
    {"name": "id", "type": "bigint"},
    {"name": "customer_id", "type": "integer"},
    {"name": "status", "type": "text"},
    {"name": "total", "type": "numeric(10,2)"},
    {"name": "notes", "type": "jsonb"}
  ],
  "rows": [
    ["9007199254740993", 2, "new", "12345678.90", "{\"tags\": [\"bulk\", \"net30\"]}"],
    [1001, 1, "new", "19.90", "{\"gift\": true}"]
  ],
  "primary_key": ["id"],
  "rows_estimate": 3,
  "more": false,
  "cut": []
}
```

- `columns`: every column of the table, in its order; `rows[i][j]` is the
  cell of `columns[j]` ([Values](#values)).
- `primary_key`: what to send as an edit's `key`. `[]`: the table's rows can
  be read and added to, not changed or removed.
- `rows_estimate`: of the whole table, as in `schema`, whatever the filters.
- `more`: rows follow these. **Ask for the next page with
  `offset + rows.length`**, not `offset + limit`: a page can stop short of
  `limit` when it reaches the size cap, with `more` true.
- `cut`: the cells that were shortened.

**Order.** With no `order_by` the rows come by primary key; with one, the
primary key breaks its ties, so a page never repeats or skips a row that
sorts the same as its neighbour. A table with no primary key and no
`order_by` comes in whatever order Postgres gives.

**Filters.**

| `op` | keeps the rows whose column |
| --- | --- |
| `=`, `!=`, `<`, `<=`, `>`, `>=` | compares so with `value`, which Postgres casts to the column's type |
| `like`, `ilike` | as text, matches the pattern in `value` (`%` any run, `_` any one character); `ilike` without regard to case |
| `is_null`, `not_null` | is, or is not, NULL. No `value`. |

A comparison needs a `value` that is not `null`: nothing equals NULL, so
that is a `400` that says to use `is_null`. A column that is not in the
table, in a filter or in `order_by`, is a `400`; a table that is not there a
`404`; a value the column cannot be (`"many"` for an integer) is
`query_failed`.

### POST /api/db/{conn}/query

```
POST /api/db/c_1c28ba20e1b6/query
{"sql": "SELECT c.name, count(*) AS orders, sum(o.total) AS spent FROM customers c JOIN orders o ON o.customer_id = c.id GROUP BY c.name ORDER BY spent DESC"}
```

| field | default | what |
| --- | --- | --- |
| `sql` | required | One statement, up to 1 MB. A closing `;` and comments are fine. |
| `limit` | 1000 (1 to 5000) | The most rows to send back. |

Runs one statement ([The guard rails](#the-guard-rails)).

```json
{
  "columns": [
    {"name": "name", "type": "text"},
    {"name": "orders", "type": "bigint"},
    {"name": "spent", "type": "numeric"}
  ],
  "rows": [
    ["Grace", 1, "12345678.90"],
    ["Ada", 2, "259.90"]
  ],
  "row_count": 2,
  "command": "SELECT",
  "duration_ms": 1.277,
  "truncated": false,
  "notices": [],
  "cut": []
}
```

| field | what |
| --- | --- |
| `columns` | The result's columns; `[]` for a statement that returns none. Two may share a name. |
| `rows` | The rows, at most `limit`. |
| `row_count` | For a statement that returns rows, how many it returned; for one that returns none, the rows it changed, as the database counted them (`UPDATE` of 40 rows: 40, with no `rows`). For a result that was cut short, only the rows that are here. |
| `command` | What ran, as the database names it: `"SELECT"`, `"UPDATE"`, `"CREATE TABLE"`. For a result that was cut short, the statement's first word. |
| `duration_ms` | How long the statement took, on the helper's clock. |
| `truncated` | The statement had more rows than are here. The rest were not fetched. |
| `notices` | What the database said along the way, each as `"NOTICE: …"` or `"WARNING: …"`. |
| `cut` | The cells that were shortened. |

With writes off, a statement that writes is refused by the database, and
changes nothing:

```json
{
  "error": {
    "code": "read_only",
    "message": "Writes are off for this connection.",
    "hint": "Turn writes on; they stay on for 15 minutes.",
    "sqlstate": "25006",
    "detail": "cannot execute UPDATE in a read-only transaction"
  }
}
```

With writes on it runs, and is kept: its transaction is committed when the
statement succeeds, and rolled back when it fails.

```
POST /api/db/c_1c28ba20e1b6/query
{"sql": "UPDATE orders SET total = total * 2 WHERE customer_id = 3 RETURNING id, total"}
```

```json
{
  "columns": [
    {"name": "id", "type": "bigint"},
    {"name": "total", "type": "numeric(10,2)"}
  ],
  "rows": [
    [1003, "10.00"]
  ],
  "row_count": 1,
  "command": "UPDATE",
  "duration_ms": 0.368,
  "truncated": false,
  "notices": [],
  "cut": []
}
```

More than one statement:

```json
{
  "error": {
    "code": "multiple_statements",
    "message": "That is 2 statements; the editor runs one at a time.",
    "hint": "Run them one by one."
  }
}
```

What cannot be run here, because every statement is inside a transaction:
`VACUUM`, `CREATE DATABASE`, `CREATE INDEX CONCURRENTLY` and the like
(`query_failed`, with Postgres's own words). A `BEGIN`, `COMMIT` or
`ROLLBACK` of the caller's own does nothing useful: there is no session
kept from one statement to the next. `COPY … FROM STDIN` and
`COPY … TO STDOUT` are a `400`.

### POST /api/db/{conn}/edit

```
POST /api/db/c_1c28ba20e1b6/edit
{"schema": "public", "table": "orders", "changes": [
  {"op": "update", "key": {"id": 1001}, "set": {"status": "shipped", "notes": null}},
  {"op": "insert", "set": {"id": 1003, "customer_id": 3, "total": "5.00", "notes": {"gift": false}}},
  {"op": "delete", "key": {"id": 1002}}]}
```

| field | what |
| --- | --- |
| `schema`, `table` | The table. |
| `changes` | 1 to 1,000 changes, applied in order. |

| a change | what |
| --- | --- |
| `{"op": "update", "key": {…}, "set": {…}}` | Set the columns of `set` on the one row `key` names. |
| `{"op": "insert", "set": {…}}` | Add a row. Columns left out get their defaults; `"set": {}` is a row of defaults. |
| `{"op": "delete", "key": {…}}` | Remove the one row `key` names. |

Needs writes on: otherwise `403` `read_only`, and the database is not asked.

`key` is **the whole primary key**: every column of the table's
`primary_key`, each with its value (not `null`), and no other column. Part
of a key, or other columns, is a `400`: it could mean several rows. Values
are as in [Values](#values).

```json
{
  "updated": 1,
  "inserted": 1,
  "deleted": 1
}
```

**All or nothing.** The changes run in one transaction. If any of them does
not do exactly what it says, none is kept:

- An update or a delete must match **exactly one row**. A row that is gone
  (deleted, or its key changed, since the editor read it) is a `409`:

  ```json
  {
    "error": {
      "code": "row_conflict",
      "message": "Change 2: the row is not there any more.",
      "hint": "Nothing was changed. Load the rows again and repeat the edit."
    }
  }
  ```

- A change the database refuses (a value the column cannot be, a duplicate
  key, a foreign key with nothing to point at) is `query_failed`, with the
  change's number in `message` and the database's `sqlstate` and `detail`.
- **A table with no primary key takes inserts only.** An update or a delete
  on one is a `400` `no_primary_key`; so is one on a view.

A message names the change that failed by its place in `changes`, counted
from 1.

### POST /api/db/{conn}/writes

```
POST /api/db/c_1c28ba20e1b6/writes
{"on": true}
```

| field | what |
| --- | --- |
| `on` | required: `true` or `false`. |

Turns writes on for this connection, for 15 minutes from now, or off.

```json
{
  "writes_until": 1791790863954
}
```

`writes_until` is when they go off again, in milliseconds since 1970; the
same number is on the connection in
[`/api/db/connections`](#get-apidbconnections) until then. Turning them on
again starts the 15 minutes again. Off answers `{}`, and is off at once.

It is held in the helper's memory, for this connection alone: another
connection's writes are its own, and after the helper restarts they are all
off. A connection whose `capabilities.writes` is `false` cannot have them
turned on (`400`).

## Engines

Postgres is the only engine, and nothing outside `internal/database/postgres`
knows it. Package `database` (this folder) is what an engine is; package
`server` checks requests, keeps the connections and the writes switch, and
logs. Adding an engine is **one package and one line**:

1. A package beside `postgres` with a type that implements
   `database.Engine`.
2. That type in `engines()` of `cmd/berth-database/main.go`.

```go
// Engine is one kind of database the editor can open.
type Engine interface {
	Name() string               // a connection's "engine": "postgres"
	Schemes() []string          // the URL schemes it opens: "postgres", "postgresql"
	Capabilities() Capabilities // what the UI may offer
	// Target reads a URL, without dialling: where it points and as whom.
	// The error it returns must not repeat the URL.
	Target(url string) (Target, error)
	// Open makes a Conn for a URL Target accepted. It need not dial yet.
	Open(url string, o Options) (Conn, error)
	QuoteIdent(name string) string // a name as an identifier of the dialect
	Shape(sql string) string       // a statement with every value taken out, for the log
}

// Conn is one database, opened, with a small pool of its own.
type Conn interface {
	Schema(ctx context.Context) (*Schema, error)
	Rows(ctx context.Context, in RowsRequest) (*RowsResult, error)
	Query(ctx context.Context, in QueryRequest) (*QueryResult, error)
	Edit(ctx context.Context, in EditRequest) (*EditResult, error)
	Close()
}
```

What the server does for every engine, so an engine does not: the token and
who may ask; finding the engine for a URL by its scheme (a `jdbc:` prefix and
a `+driver` suffix are taken off first); refusing a `Target` that is not
`Local()`; keeping connections and forgetting them; the writes switch and
its clock (`QueryRequest.Write` is set by the server, and `Edit` is called
only while writes are on); bringing `limit` into range and checking a
request's shape against the engine's `Capabilities` (an operator the engine
does not list never reaches it); the log.

What an engine must hold to, and Postgres's tests show how:

- **Local means local.** `Target` says where the URL points, after
  everything the dialect lets a URL say (a host in a parameter, a socket).
  Refuse a URL that names several hosts.
- **No value in SQL text, no name unquoted.** Names from a request go
  through the engine's own quoting, values go as parameters, and an operator
  is looked up in a table of the engine's, never pasted in.
- **Read only is the database's job.** `Rows`, `Schema` and a `Query` with
  `Write` false must be unable to write because the database refuses, not
  because the statement was read. An engine whose database cannot promise
  that says `Writes: false` and opens it read-only.
- **The timeout is the database's job** too: `Options.Timeout` stops a
  statement there, with the context as a second line.
- **Stop fetching.** At `limit` rows, or when the `Grid` says it is full,
  stop reading from the database and say there were more.
- **The same JSON.** Make cells with `database.Int`, `database.Float` and
  `database.Grid` (which cuts long cells and counts bytes), and follow
  [Values](#values) for the rest: every digit of a big number, ISO 8601 for
  a timestamp, text for everything else.
- **`Edit` is exact.** By the whole primary key, one row each, all changes
  or none (`Transactions: true`), `database.Conflict` when a row is not
  there.
- **Errors are `*database.Problem`** when they are the caller's to read,
  and never hold a password or a URL. Any other error is logged by its type
  alone and answered `failed`.
- **`Close` ends everything**, statements in flight too.

`server`'s tests run on a fake engine with no schemas, one operator and
square brackets for quotes, and on nothing else: if they pass, the HTTP
layer assumed nothing about Postgres.
