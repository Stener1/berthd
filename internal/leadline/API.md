# The berth-leadline API

`berth-leadline` is a helper that runs on a box beside its dev servers and
records what their code did: every database query, grouped by shape and
checked for N+1s; the traces of their requests; their logs. It is
[Leadline](https://github.com/sean-brydon/leadline)'s collector, rebuilt to
live inside Shipyard: one static program with no cgo, one SQLite file.

This file is everything a UI, or the box daemon, needs to use it: how to run
it, how an app is pointed at it, and every HTTP route with its parameters and
a real response. The code is `cmd/berth-leadline` and the packages in this
folder; the routes are in `api/`. Change them together with this file: the
UI is written from it.

Every example is a real response, from a helper that was sent made-up "acme"
telemetry and a few statements through its proxy to a stand-in Postgres. A
few long lists are shortened where the text says so, and nothing else was
done to them.

- [Running it](#running-it)
- [Sources: one for each worktree](#sources-one-for-each-worktree)
- [Getting data in](#getting-data-in)
- [Ground rules](#ground-rules)
- [The routes](#the-routes)
- [The attach helper (Go)](#the-attach-helper-go)
- [What differs from Leadline's API](#what-differs-from-leadlines-api)

## Running it

```sh
berth-leadline serve --dir DIR [--addr 127.0.0.1:4325] [--otlp ADDR] [--retain 72h] [--max-size 2GB]
                     [--pg LISTEN=UPSTREAM]... [--proxy-ports 25432-25531]
berth-leadline version
```

| flag | variable | default | what |
| --- | --- | --- | --- |
| `--dir` | `BERTH_LEADLINE_DIR` | none: required | The data directory. Made if missing, mode `0700`. |
| `--addr` | `BERTH_LEADLINE_ADDR` | `127.0.0.1:4325` | Where the API and OTLP/HTTP are served. Must be loopback (`127.0.0.1`, `[::1]`, `localhost`); anything else is refused at the start. Port `0` takes any free port, and at the next start the same one again if it is still free; see `helper.json`. |
| `--otlp` | `BERTH_LEADLINE_OTLP_ADDR` | off | A second loopback address that takes OTLP and answers nothing else: an address to give to apps. `off` or empty for none. |
| `--retain` | `BERTH_LEADLINE_RETAIN` | `72h` | How long a row is kept: a Go duration (`72h`, `90m`) or days (`7d`). `0` or `off` keeps rows whatever their age. |
| `--max-size` | `BERTH_LEADLINE_MAX_SIZE` | `2GB` | Past this much data the oldest rows go early. `2GB`, `500MB`, `64K`, or plain bytes. `0` or `off` is no cap. |
| `--pg` | `BERTH_LEADLINE_PG` | none | A proxy to open at the start: `listen=upstream`, as in `127.0.0.1:15433=127.0.0.1:5432`; either side may be a bare port. Repeat the flag, or separate with commas in the variable. The listen side must be loopback and the upstream on this machine. |
| `--proxy-ports` | `BERTH_LEADLINE_PROXY_PORTS` | `25432-25531` | The range the proxies opened over the API get their ports from. |

A flag wins over its variable. `SIGINT` or `SIGTERM` stops it: it writes what
was waiting, closes the database and exits 0. A start that fails (no
directory, an address that is not loopback or is taken, a database from a
newer helper) prints one line to stderr and exits 1. Its log goes to stderr;
it never holds a statement's values, a URL or a password.

What it keeps, all in `--dir`, all readable by its owner alone (`0600`):

| file | what |
| --- | --- |
| `leadline.db` | Everything recorded: one SQLite file, with `leadline.db-wal` and `leadline.db-shm` beside it while the helper runs. |
| `proxies.json` | The proxies opened over the API: id, port, upstream address, label. No URL, no user, no password: the helper is never given one. They are opened again, on the same ports, at the next start. |
| `helper.json` | Where the running helper listens. Written once it is listening, removed when it stops. Read it to find a helper started on port 0. |
| `listen.json` | The addresses a helper started on port 0 was given (`api`, and `otlp` for `--otlp`), kept when it stops. The next start asks for the same ports first, so a dev server that was told the helper's address when it started still reaches it after the helper restarts; a port something else has taken since is replaced by any free one. |

`helper.json`:

```json
{
  "api": "http://127.0.0.1:4325",
  "otlp": ["http://127.0.0.1:4325"],
  "pid": 64425,
  "version": "dev"
}
```

`api` is the base URL for every route below; it takes OTLP too. `otlp` lists
every base URL that takes OTLP: `api` first, then the `--otlp` address if
there is one. A helper that was killed leaves the file behind: `pid`, or
`GET /api/health` on `api`, says whether the one it names is still there.

Nothing leaves the machine. The helper listens on loopback only, dials
nothing but the Postgres servers on this machine it was asked to proxy, and
has no password because there is no way to it from the network. Do not
publish it through a reverse proxy.

Two helpers must not share one `--dir`: nothing stops a second one from
starting, and each would pair statements without the other's half.

## Sources: one for each worktree

A **service** is a name telemetry is filed under: the `service.name` of an
app's OpenTelemetry resource, or the label a proxy was opened with.

A **source** is a Shipyard worktree. It is the service of its name and the
services under it:

| service | source |
| --- | --- |
| `acme/fix-orders` | `acme/fix-orders`: the worktree `fix-orders` of the project `acme` |
| `acme/fix-orders:worker` | `acme/fix-orders`: a second command, named `worker`, run in that worktree |
| `acme` | `acme`: the project's main checkout |
| `acme/fix-orders-2` | `acme/fix-orders-2`: another worktree, whose name only begins the same |

The rule is all there is to it: a service's source is its name up to the
first `:`. The names are made by the [attach helper](#the-attach-helper-go),
which takes `/`, `:`, `\` and white space out of a project's or a worktree's
name (each becomes `-`) so the two joins stay readable. A service that was
not named by Shipyard (an app with its own `OTEL_SERVICE_NAME`) is a source
of its own, by the same rule.

Every list route takes `source=NAME` and keeps what belongs to that worktree:
its own service and its commands'. `service=NAME` keeps one service, by its
exact name. Both may be given.

What the worktree **is** (its project, worktree, branch and box) comes from
whoever says so: the four `shipyard.*` attributes in the resource of an app's
telemetry, or the body of the request that opened a proxy for it. The newest
word wins. A source nobody described has only its name.

## Getting data in

### OTLP/HTTP

```
POST /v1/traces
POST /v1/logs
POST /v1/metrics
```

On `--addr`, and on `--otlp` if given. The body is an OTLP export request:
protobuf (`Content-Type: application/x-protobuf`, what
`OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf` sends) or JSON
(`Content-Type: application/json`, OTLP's JSON with ids in hex), either one
gzipped or not (`Content-Encoding: gzip`), up to 64 MB. The answer is `200`
with the empty export response in the same encoding: no bytes for protobuf,
`{}` for JSON. A body that is not OTLP is a `400` with a line of text.

Traces and logs are stored. **Metrics are taken and dropped**: the route is
there so that an SDK that was only given the endpoint does not log an error
every minute.

What is read from a span, by attribute, with the older names still accepted:

| stored as | from |
| --- | --- |
| the service | resource `service.name` (`unknown` without one) |
| the worktree | resource `shipyard.project`, `shipyard.worktree`, `shipyard.branch`, `shipyard.box` |
| `db_system` | `db.system.name`, `db.system` |
| `db_statement` | `db.query.text`, `db.statement`; without either, the span's name |
| `http_method`, `http_route`, `http_status` | `http.request.method` / `http.method`; `http.route` / `url.path` / `http.target`; `http.response.status_code` / `http.status_code` |
| `user_id` | `user.id`, `enduser.id` |
| `db_server` | `server.address` / `net.peer.name` / `network.peer.address`, with `server.port` / `net.peer.port` / `network.peer.port` |
| `attrs` | every attribute, as a JSON object |

A span with `db_system` is a database call. Its fingerprint is its statement
with every value taken out: literals, `$1`, `:name` and `@name` become `?`,
an `IN` list of any length becomes `IN (?)`, white space collapses, a closing
semicolon is dropped. For Redis and its relatives the fingerprint is the
command and the shape of its key (`GET session:?`).

Of a log record: its time (`timeUnixNano`, else the observed time), severity
(the number, else the text; neither is `info`), body (a string as it is,
anything else as JSON), trace and span ids, attributes, `user.id`, and
`leadline.kind` as its `kind`. A record whose `leadline.kind` ends in
`.routes` is a framework's route list, which this helper does not keep: it
is dropped, not stored as a log.

### The Postgres proxy

A proxy is one loopback port in front of one Postgres on this machine. An
app whose database URL names the proxy's port instead of the database's has
every statement recorded, with no instrumentation, in any language: the
statement's text, its exact time on the wire, how many rows it gave, its
parameters and its error if it failed. Statements between `BEGIN` and
`COMMIT` are one trace under a `transaction` span.

- Open one with [`POST /api/proxies`](#post-apiproxies), giving the
  database's address and the source's name as `label`. Everything through it
  is filed under that name.
- The proxy answers a request for TLS with "no", so the client carries on in
  plaintext, which is the only way the statements can be read. A URL that
  insists on TLS (`sslmode=require` and stronger) cannot go through it; the
  attach helper leaves such a URL alone.
- Passwords and SASL exchanges pass through unread. The password in
  `ALTER ROLE ... PASSWORD '...'` is replaced by `***` before the statement
  is stored.
- What is kept of a statement is what Leadline keeps: up to 16 KB of its
  text, and up to 32 parameters of up to 200 bytes each, as
  `db.query.parameters` in the span's `attrs`.
- A connection the proxy does not understand (a replication stream) is
  relayed as it is and not recorded; `unread_connections` counts them. The
  proxy never holds traffic back, and a failure of its own ends the
  recording, not the connection.

### A statement seen twice is counted once

An app can be seen two ways at once: its own OpenTelemetry spans, and the
proxy recording the same statements off the wire. The helper pairs the two
(same database system, same fingerprint, the wire's timing inside the app's
span; exactly, when the app tags its SQL with a
[sqlcommenter](https://google.github.io/sqlcommenter/) `traceparent`) and
each statement is **one query**. The proxy's span moves into the app's
trace, under the app's span, and is the one that counts: it has the exact
timing, the row count and the parameters. The app's span stays in the trace
as the step that led to it.

That is what a span's `db_role` says:

| `db_role` | what |
| --- | --- |
| `query` | A query: listed in `/api/queries`, counted per request, looked at for N+1. |
| `control` | Transaction and session control (`BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT`, `SET`, `SHOW`, `DEALLOCATE`, ...; Redis `PING`, `MULTI`, ...). In the trace and in database time; not among the queries (`/api/queries?control=1` lists them). |
| `duplicate` | A second sighting of a call another span already counts: the app's span around a statement the proxy timed, or a query builder's span around the driver's. In the trace; counted nowhere. |
| absent | Not a database call. |

And `entry: true` says a span is a **request**: a server or consumer span,
or a span with no parent; not a span a browser made, not a server span
inside a server span of the same service, and not a database call. The
endpoints, the overview's series and the heatmap all count exactly the
`entry` spans.

A pair is waited for for 30 seconds. The half that arrives later than that,
and the pairs in flight when the helper restarts, are counted twice.

## Ground rules

- **Base URL** is `api` of `helper.json`: `http://127.0.0.1:4325` unless
  the helper was started with `--addr`.
- **Localhost only.** A request must name a loopback host in its `Host`
  header (`127.0.0.1`, `[::1]`, `localhost`, `*.localhost`), which stops DNS
  rebinding; anything else is a `403`. A caller that reaches the helper
  through a forwarded port must still send a loopback `Host`.
- **Origins.** A request with an `Origin` header (from a page) is answered,
  with CORS headers, only for loopback origins (`http://localhost:*`,
  `http://127.0.0.1:*`) and `tauri://` ones. Others are a `403`.
- **Only Shipyard's window changes things.** A request under `/api/` that is
  not a `GET` is taken from a caller with no `Origin` at all (the box daemon,
  `curl`) or from Shipyard's own window: `tauri://*`,
  `http://tauri.localhost`, `http://localhost:1420`, `http://127.0.0.1:1420`,
  and any origin listed, comma separated, in `BERTH_LEADLINE_APP_ORIGINS`.
  From any other page it is a `403`: the app being debugged is also a page on
  this machine. Any loopback page may read, and may send telemetry (`/v1/*`).
- **Methods** are `GET`, `POST` and `DELETE`; `OPTIONS` answers a browser's
  preflight with `204`.
- **Bodies are JSON**, at most 64 KB (OTLP: 64 MB).
- **Times** are unix milliseconds, as floats, in fields ending `_ms`.
  Durations are milliseconds too. Sizes are bytes. Stored times are
  nanoseconds, which a JS number cannot hold: where the exact time matters
  (a cursor) it is a string.
- **Lists are never `null`.** An empty list is `[]`.
- **A search** (`q`) is a substring, without regard to the case of ASCII
  letters. `%` and `_` in it are wildcards.

### The filters every list takes

| parameter | on | what |
| --- | --- | --- |
| `source` | every list route | Keep one worktree: the service of that name and those under it. |
| `service` | every list route | Keep one service, by its exact name. |
| `since_ms` | every list route | Only rows at or after this time. |
| `until_ms` | the routes with a window | Only rows before this time; now, when left out. |
| `limit` | every list route | The page's size; each route says its default and its most. A `limit` that is not a number in that range is the default. |

A **window** is `since_ms` to `until_ms`. Where a route takes a window,
`until_ms` defaults to now and `since_ms` to 15 minutes before `until_ms`; a
window that ends at or before its start is a `400`. The routes without a
window (`/api/queries`, `/api/traces`, `/api/logs`) take `since_ms` alone and
default to everything held.

A wide window over a full store is slow, because SQLite reads every row of
it. Measured on an Apple-silicon Mac, on 400,000 spans spread evenly over a
day: every route answers a 15-minute window in under 25 ms and an hour in
under 120 ms, while the whole day takes about 1 s for `/api/queries` and
`/api/endpoints` and about 3 s for either detail route. The newest page of
`/api/traces` (35 ms) and of `/api/logs` (under 1 ms) does not depend on how
much is held; a filter that matches almost nothing reads everything (0.9 s
for such a search of the traces). Ask for the window that is on screen.

### Paging

Two ways, by what orders the list:

- **By time, with a cursor**: `/api/traces` and `/api/logs`. Each row has a
  `cursor`. Send the last row's as `before` to get the rows that follow it.
  A page shorter than `limit` is the last. Rows that arrive while paging
  never shift a page: a cursor is a row's exact place.
- **By rank, with an offset**: `/api/queries`, `/api/endpoints`,
  `/api/sources`, `/api/proxies`. Send `offset` to skip that many rows.
  `/api/endpoints`, `/api/sources` and `/api/proxies` say the `total`;
  `/api/queries` is a bare list, and a page shorter than `limit` is its last.

### Errors

A failed request under `/api/` answers with a status that fits and this body:

```json
{
  "error": {
    "code": "capture_remote",
    "message": "The proxy only goes in front of a Postgres on this machine: db.acme.example is not.",
    "hint": "Run the app against that database directly; its queries then show only if the app reports them itself."
  }
}
```

`message` is a sentence for the person at the screen. `hint`, when there is
one, is what to try. `code` is for the caller to branch on:

| code | status | when |
| --- | --- | --- |
| `bad_request` | 400 | a parameter or the body cannot be read, or is missing, or is out of range |
| `capture_remote` | 400 | a proxy was asked for in front of a server that is not on this machine |
| `too_many` | 400 | there are already 200 proxies |
| `forbidden` | 403 | the `Host` is not loopback, the origin is not allowed, or a page other than Shipyard's tried to change something |
| `not_found` | 404 | no such trace, proxy or route |
| `port_in_use`, `no_port` | 409 | the proxy's port cannot be had |
| `shutting_down` | 503 | the helper is stopping |
| `not_saved`, `failed` | 500 | the helper's own fault; its log says why |

The OTLP routes (`/v1/*`) answer an error with a line of plain text, as OTLP
clients expect.

## The routes

| route | what |
| --- | --- |
| [`GET /api/health`](#get-apihealth) | Is it there, and which version |
| [`GET /api/summary`](#get-apisummary) | How much is held, and the services' names |
| [`GET /api/sources`](#get-apisources) | The worktrees, their services and their proxies |
| [`GET /api/queries`](#get-apiqueries) | Queries grouped by shape, with the N+1 flag |
| [`GET /api/queries/detail`](#get-apiqueriesdetail) | One query shape, and the requests that show its N+1 |
| [`GET /api/traces`](#get-apitraces) | Traces, newest first |
| [`GET /api/traces/{id}`](#get-apitracesid) | One trace: its spans, the tree, its SQL and its logs |
| [`GET /api/logs`](#get-apilogs) | Logs, newest first, searched and filtered |
| [`GET /api/endpoints`](#get-apiendpoints) | Per-route numbers: requests, latency, queries per request, database time |
| [`GET /api/endpoints/detail`](#get-apiendpointsdetail) | One route: over time, its queries, its slowest and failing requests |
| [`GET /api/series`](#get-apiseries) | The overview: requests, errors, queries, latency and logs over time |
| [`GET /api/heatmap`](#get-apiheatmap) | Latency of every request over time |
| [`GET /api/breakdown`](#get-apibreakdown) | One measure over time, split by a dimension |
| [`GET /api/storage`](#get-apistorage) | How much is kept and how much may be |
| [`GET /api/events`](#get-apievents) | A stream that says when new data arrived |
| [`GET /api/proxies`](#get-apiproxies) | The Postgres proxies |
| [`POST /api/proxies`](#post-apiproxies) | Open a proxy for a source |
| [`DELETE /api/proxies/{id}`](#delete-apiproxiesid) | Close one |
| [`POST /api/attach`](#post-apiattach) | Point a worktree's command at the helper: its telemetry and its database |
| [`POST /api/detach`](#post-apidetach) | Forget a worktree: its proxies and its data |
| [`POST /v1/traces`, `/v1/logs`, `/v1/metrics`](#otlphttp) | OTLP/HTTP in |

### GET /api/health

No parameters.

```json
{
  "ok": true,
  "name": "berth-leadline",
  "version": "dev",
  "api": 1
}
```

`name` is always `berth-leadline`: how to tell this helper from whatever else
may answer on a port. `version` is the build's (`dev` from a checkout, `v1.2.3`
from a release). `api` is this API's own number, `1`: it goes up when a route
or a field is taken away or changes meaning, not when one is added.

### GET /api/summary

No parameters. What the helper holds now.

```json
{
  "spans": 52,
  "logs": 5,
  "traces": 5,
  "services": ["acme/add-refunds", "acme/fix-orders", "acme/fix-orders:worker"]
}
```

`traces` counts distinct traces; `services` is every service with a span or a
log, by name. The counts fall as old data is dropped.

### GET /api/sources

```
GET /api/sources
GET /api/sources?source=acme/fix-orders
GET /api/sources?since_ms=1791754000000&limit=50&offset=50
```

| parameter | default | what |
| --- | --- | --- |
| `source` | all | Only this source, with its services and its proxies. |
| `since_ms` | 15 minutes ago | Where each proxy's `window_statements` starts counting. |
| `limit` | 200 (1 to 1000) | How many sources. |
| `offset` | 0 | Sources to skip. |

One call for "what is feeding the helper": the worktrees, by name; the
services filed under the ones listed; and the proxies opened for them. It is
made to be polled.

```json
{
  "now_ms": 1791754259272.826,
  "since_ms": 1791753359272.826,
  "otlp": {
    "endpoints": ["http://127.0.0.1:50761"]
  },
  "total": 2,
  "sources": [
    {
      "name": "acme/add-refunds",
      "services": ["acme/add-refunds"],
      "spans": 0,
      "logs": 1,
      "last_seen_ms": 1791754259269.408,
      "via": ["otlp"],
      "proxy_ids": []
    },
    {
      "name": "acme/fix-orders",
      "project": "acme",
      "worktree": "fix-orders",
      "branch": "fix/orders-n-plus-one",
      "box": "devbox",
      "services": ["acme/fix-orders", "acme/fix-orders:worker"],
      "spans": 52,
      "logs": 4,
      "last_seen_ms": 1791754259265.692,
      "via": ["otlp", "postgres-proxy"],
      "proxy_ids": ["p_f46655d8b2"]
    }
  ],
  "services": [
    {
      "name": "acme/add-refunds",
      "source": "acme/add-refunds",
      "spans": 0,
      "logs": 1,
      "last_seen_ms": 1791754259269.408,
      "via": ["otlp"],
      "through": []
    },
    {
      "name": "acme/fix-orders",
      "source": "acme/fix-orders",
      "spans": 51,
      "logs": 4,
      "last_seen_ms": 1791754259265.692,
      "owner": {
        "kind": "worktree",
        "via": "shipyard",
        "service": "acme/fix-orders",
        "project": "acme",
        "worktree": "fix-orders",
        "branch": "fix/orders-n-plus-one",
        "box": "devbox",
        "label": "from Shipyard, worktree fix-orders"
      },
      "via": ["otlp", "postgres-proxy"],
      "through": [
        {
          "via": "postgres-proxy",
          "proxy_id": "p_f46655d8b2"
        }
      ]
    },
    {
      "name": "acme/fix-orders:worker",
      "source": "acme/fix-orders",
      "spans": 1,
      "logs": 0,
      "last_seen_ms": 1791754259229.408,
      "via": ["otlp"],
      "through": []
    }
  ],
  "proxies": [
    {
      "id": "p_f46655d8b2",
      "kind": "postgres",
      "listen": "127.0.0.1:50764",
      "port": 50764,
      "upstream": "localhost:50762",
      "label": "acme/fix-orders",
      "origins": ["api"],
      "state": "listening",
      "owner": {
        "kind": "worktree",
        "via": "shipyard",
        "service": "acme/fix-orders",
        "project": "acme",
        "worktree": "fix-orders",
        "branch": "fix/orders-n-plus-one",
        "box": "devbox",
        "label": "from Shipyard, worktree fix-orders"
      },
      "connections_open": 0,
      "connections_total": 1,
      "statements": 24,
      "failed_statements": 0,
      "unread_connections": 0,
      "last_activity_ms": 1791754259266.1592,
      "window_statements": 24
    }
  ]
}
```

- `now_ms`, `since_ms`: the helper's clock, and where the windowed counts
  start.
- `otlp.endpoints`: the base URLs that take OTLP (`POST` to `/v1/traces` and
  `/v1/logs` under each).
- `total`: how many sources match, before `limit` and `offset`.
- `sources[]`, by name:
  - `name`: what every list route's `source` takes.
  - `project`, `worktree`, `branch`, `box`: what the worktree is, when anyone
    said. Each is left out when unknown; `worktree` is also left out for a
    project's main checkout.
  - `services`: the names filed under it.
  - `spans`, `logs`: what is stored for it now. `last_seen_ms`: the time of
    its newest span or log, `0` for none.
  - `via`: how its data arrives: `"otlp"` (the app exports it),
    `"postgres-proxy"` (the helper recorded it off the wire), both, or
    neither yet (`[]`).
  - `proxy_ids`: the proxies opened for it; each is in `proxies`.
- `services[]`, by name, for the sources listed: `name`, `source`, `spans`,
  `logs`, `last_seen_ms`, `via` as above;
  - `owner`: the worktree the service belongs to, when it said which (see
    below). Left out otherwise.
  - `through`: the proxies some of its database calls came through:
    `{"via": "postgres-proxy", "proxy_id": "..."}` each.
- `proxies[]`: [the proxy object](#the-proxy-object), for the sources listed.
  Proxies opened with no label, and the `--pg` flag's, are listed too unless
  `source` was given.

A source appears as soon as a proxy is opened for it, with zeros, before its
first statement. A statement the app also reported is stored under the app's
service name and counted there.

**An owner**, on a service and on a proxy:

| field | what |
| --- | --- |
| `kind` | `"worktree"`, or `"flag"` for a proxy from `--pg` |
| `via` | `"shipyard"` |
| `service` | the service or the label it describes |
| `project`, `worktree`, `branch`, `box` | as on a source; each left out when unknown |
| `label` | a few words to show as they are: `"from Shipyard, worktree fix-orders"`, `"from Shipyard, project acme"`, `"the helper's --pg flag"` |

### GET /api/queries

```
GET /api/queries?source=acme/fix-orders&since_ms=1791754000000
GET /api/queries?q=order_items&limit=50&offset=50
GET /api/queries?control=1
```

| parameter | default | what |
| --- | --- | --- |
| `source`, `service` | all | The worktree, or the one service, whose queries to list. They filter on the query's own span. |
| `since_ms` | everything held | Only calls at or after this time. |
| `q` | none | Keep the shapes with a statement containing this. |
| `db_system` | all | One database system, as `postgresql`. |
| `db_server` | all | One server, as `127.0.0.1:5432` (`localhost` and `127.0.0.1` are the same). |
| `control` | off | `1` lists transaction and session control (`BEGIN`, `COMMIT`, `SET`) in place of the queries. |
| `limit` | 200 (1 to 5000) | How many shapes. |
| `offset` | 0 | Shapes to skip. |

Database calls grouped by shape, costliest (most total time) first. A bare
list. Only spans that count as queries are in it: a statement seen twice is
there once. Shortened here to two of three rows:

```json
[
  {
    "db_system": "postgresql",
    "fingerprint": "SELECT name FROM customers WHERE id = ?",
    "example": "SELECT name FROM customers WHERE id = $1",
    "calls": 18,
    "total_ms": 0.793,
    "avg_ms": 0.044055555555555556,
    "p50_ms": 0.0385,
    "p95_ms": 0.0679,
    "max_ms": 0.073,
    "errors": 0,
    "last_ms": 1791754259237.8062,
    "traces": 3,
    "max_per_trace": 6,
    "n_plus_one": true
  },
  {
    "db_system": "postgresql",
    "fingerprint": "SELECT id, customer_id FROM orders ORDER BY id LIMIT ?",
    "example": "SELECT id, customer_id FROM orders ORDER BY id LIMIT 25",
    "calls": 3,
    "total_ms": 0.154,
    "avg_ms": 0.051333333333333335,
    "p50_ms": 0.05,
    "p95_ms": 0.0689,
    "max_ms": 0.071,
    "errors": 0,
    "last_ms": 1791754259231.563,
    "traces": 3,
    "max_per_trace": 1,
    "n_plus_one": false
  }
]
```

| field | what |
| --- | --- |
| `db_system` | `postgresql`, `mysql`, `redis`, `mongodb`, ... as the span said |
| `fingerprint` | The shape: the statement with `?` where a value was. With `db_system` it names the query, for `/api/queries/detail`. |
| `example` | One real statement of that shape. |
| `calls` | How many times it ran. |
| `total_ms`, `avg_ms`, `max_ms` | Time in it: all calls, the mean, the slowest. |
| `p50_ms`, `p95_ms` | The median and the slow percentile of a call's duration. |
| `errors` | Calls that failed. |
| `last_ms` | When it last ran. |
| `traces` | How many traces ran it. |
| `max_per_trace` | The most times one trace ran it. |
| `n_plus_one` | `true` when `max_per_trace` is 5 or more: one request ran the same statement five times. |

**The N+1 rule** is one number, 5, here and everywhere a row has
`n_plus_one`. It is Leadline's.

`?control=1`, shortened to one row:

```json
[
  {
    "db_system": "postgresql",
    "fingerprint": "begin",
    "example": "begin",
    "calls": 1,
    "total_ms": 0.077,
    "avg_ms": 0.077,
    "p50_ms": 0.077,
    "p95_ms": 0.077,
    "max_ms": 0.077,
    "errors": 0,
    "last_ms": 1791754259262.804,
    "traces": 1,
    "max_per_trace": 1,
    "n_plus_one": false
  }
]
```

### GET /api/queries/detail

```
GET /api/queries/detail?buckets=4&db_system=postgresql&fingerprint=SELECT+name+FROM+customers+WHERE+id+%3D+%3F
```

| parameter | default | what |
| --- | --- | --- |
| `fingerprint` | required | The shape, as `/api/queries` gave it. |
| `db_system` | `""` | Its system, as `/api/queries` gave it. Give it: the two together name the query. |
| `since_ms`, `until_ms` | the last 15 minutes | The window. |
| `source`, `service` | all | Count only this worktree's, or this service's, calls. |
| `buckets` | 60 (1 to 2000) | How many buckets of time the series and the heatmap have. |

Everything about one query shape. A missing `fingerprint` is a `400`. A
shape that did not run in the window answers `200` with `"stats": null` and
empty lists. Shortened here: two of the histogram's seven bins, one of three
`traces`, two of three `n_plus_one`.

```json
{
  "stats": {
    "db_system": "postgresql",
    "fingerprint": "SELECT name FROM customers WHERE id = ?",
    "example": "SELECT name FROM customers WHERE id = $1",
    "calls": 18,
    "errors": 0,
    "total_ms": 0.793,
    "avg_ms": 0.044055555555555556,
    "p50_ms": 0.0385,
    "p95_ms": 0.0679,
    "max_ms": 0.073,
    "traces": 3,
    "max_per_trace": 6,
    "last_ms": 1791754259237.8062,
    "share": 0.6824440619621343,
    "n_plus_one": true
  },
  "series": {
    "bucket_ms": 225000,
    "t": [1791753359275.432, 1791753584275.432, 1791753809275.432, 1791754034275.432],
    "calls": [0, 0, 0, 18],
    "errors": [0, 0, 0, 0],
    "total_ms": [0, 0, 0, 0.793],
    "avg_ms": [null, null, null, 0.044055555555555556],
    "p95_ms": [null, null, null, 0.0679]
  },
  "histogram": {
    "per_decade": 10,
    "bins": [
      {
        "lo_ms": 0.0199526231496888,
        "hi_ms": 0.025118864315095794,
        "count": 2
      },
      {
        "lo_ms": 0.025118864315095794,
        "hi_ms": 0.031622776601683784,
        "count": 2
      }
    ]
  },
  "heatmap": {
    "bucket_ms": 225000,
    "t": [1791753359275.432, 1791753584275.432, 1791753809275.432, 1791754034275.432],
    "per_decade": 10,
    "x": [3, 3, 3, 3, 3, 3],
    "y": [-17, -16, -15, -14, -13, -12],
    "n": [2, 2, 6, 3, 3, 2],
    "y_min": -17,
    "y_max": -12,
    "max": 6
  },
  "endpoints": [
    {
      "service": "acme/fix-orders",
      "name": "GET /orders",
      "calls": 18,
      "requests": 3,
      "endpoint_requests": 3,
      "per_request": 6,
      "max_per_request": 6,
      "avg_ms": 0.044055555555555556,
      "total_ms": 0.793,
      "n_plus_one": true
    }
  ],
  "traces": [
    {
      "trace_id": "000000000000000000000000acee0000",
      "name": "GET /orders",
      "service": "acme/fix-orders",
      "start_ms": 1791754259167.573,
      "calls": 6,
      "errors": 0,
      "total_ms": 0.326,
      "slowest_ms": 0.073
    }
  ],
  "n_plus_one": [
    {
      "trace_id": "000000000000000000000000acee0000",
      "name": "GET /orders",
      "service": "acme/fix-orders",
      "start_ms": 1791754259167.573,
      "calls": 6,
      "errors": 0,
      "total_ms": 0.326,
      "slowest_ms": 0.073
    },
    {
      "trace_id": "000000000000000000000000acee0001",
      "name": "GET /orders",
      "service": "acme/fix-orders",
      "start_ms": 1791754259199.67,
      "calls": 6,
      "errors": 0,
      "total_ms": 0.288,
      "slowest_ms": 0.063
    }
  ]
}
```

- `stats`: the row of `/api/queries` for the window, with
  - `example`: the statement of the **slowest** call, the one worth reading;
  - `share`: this query's part of all database time in the window, 0 to 1.
- `series`: the query over time. `t[i]` is where bucket `i` starts and
  `bucket_ms` how long each is; `calls`, `errors`, `total_ms` are per bucket,
  and `avg_ms`, `p95_ms` are `null` for a bucket with no calls: there is
  nothing to draw there, and zero would be a lie.
- `histogram`: how its calls' durations are spread, in log-spaced bins,
  `per_decade` (10) bins to each factor of ten. Each bin is `lo_ms` up to
  `hi_ms` and a `count`; bins run from the first to the last that holds
  anything, with no gaps.
- `heatmap`: duration over time, as [`/api/heatmap`](#get-apiheatmap)
  describes, 10 bands to the decade.
- `endpoints`: the routes whose requests ran it, costliest first (at most
  30):
  - `service`, `name`: the endpoint.
  - `calls`: how many times its requests ran the query.
  - `requests`: how many of its requests ran it at all; `endpoint_requests`:
    all the requests it had.
  - `per_request`: `calls / endpoint_requests`. `max_per_request`: the most
    in one request. `n_plus_one`: `max_per_request` is 5 or more.
  - `avg_ms`, `total_ms`: of the query, inside this endpoint.

  The `source` and `service` filters apply to the query's own spans; the
  request that caused them may belong to any service.
- `traces`: the traces that spent the longest in the query (at most 12):
  `trace_id`; `name` and `service` of the request (empty when it began
  before the window); `start_ms` of the first call; `calls`; `errors`;
  `total_ms`; `slowest_ms`.
- `n_plus_one`: **the requests that show the N+1**: the traces that ran the
  query 5 times or more, the one that ran it most first (at most 20), in the
  same shape as `traces`. `[]` when `stats.n_plus_one` is false.

### GET /api/traces

```
GET /api/traces?limit=3&source=acme%2Ffix-orders
GET /api/traces?limit=3&source=acme%2Ffix-orders&before=1791754259229408000.000000000000000000000000000000d1
```

| parameter | default | what |
| --- | --- | --- |
| `source`, `service` | all | Keep the traces whose **root** is of this worktree, or this service. |
| `since_ms` | everything held | Only spans at or after this time are looked at: a trace that began before it is listed by the part of it that came after. |
| `q` | none | Keep the traces whose name contains this. |
| `errors` | off | `1` keeps the traces with a failed span. |
| `min_duration_ms` | 0 | Keep the traces at least this long. |
| `user` | none | Keep the traces with a span for this user (`user.id`). |
| `db_system`, `db_server` | all | Keep the traces that sent a database call to this system, or this server. |
| `limit` | 200 (1 to 5000) | How many traces. |
| `before` | none | The `cursor` of the last row of the page before. |

Traces, newest first; among traces that begin in the same nanosecond, by id,
descending. A bare list. A `before` that is not a cursor is a `400`.
Shortened here to two of the page's three rows:

```json
[
  {
    "trace_id": "000000000000000000000000acee0003",
    "name": "POST /checkout",
    "service": "acme/fix-orders",
    "start_ms": 1791754259262.6062,
    "duration_ms": 3.086,
    "spans": 6,
    "errors": 1,
    "queries": 1,
    "db_ms": 0.215,
    "user_id": "u_42",
    "cursor": "1791754259262606000.000000000000000000000000acee0003"
  },
  {
    "trace_id": "000000000000000000000000acee0002",
    "name": "GET /orders",
    "service": "acme/fix-orders",
    "start_ms": 1791754259231.5068,
    "duration_ms": 10.893,
    "spans": 15,
    "errors": 0,
    "queries": 7,
    "db_ms": 0.212,
    "user_id": "u_42",
    "cursor": "1791754259231507000.000000000000000000000000acee0002"
  }
]
```

The page after it, by the cursor of that page's last row, shortened to one
row:

```json
[
  {
    "trace_id": "000000000000000000000000acee0001",
    "name": "GET /orders",
    "service": "acme/fix-orders",
    "start_ms": 1791754259199.457,
    "duration_ms": 11.011,
    "spans": 15,
    "errors": 0,
    "queries": 7,
    "db_ms": 0.359,
    "user_id": "u_42",
    "cursor": "1791754259199457000.000000000000000000000000acee0001"
  }
]
```

| field | what |
| --- | --- |
| `trace_id` | For `/api/traces/{id}`. |
| `name`, `service` | Of the trace's root span; until the root arrives (it ends last, so it is exported last) the earliest span stands in. |
| `start_ms`, `duration_ms` | From its first span's start to its last span's end. |
| `spans` | How many spans it has. |
| `errors` | Spans that failed. A call seen twice failed once. |
| `queries` | Spans that are queries (`db_role` `query`). |
| `db_ms` | Time in the database: queries and control statements. |
| `user_id` | Who it ran for, when the app says. Left out otherwise. |
| `cursor` | This row's place in the list, for `before`. |

### GET /api/traces/{id}

```
GET /api/traces/000000000000000000000000acee0000
```

One trace opened. `{id}` is a `trace_id`, 32 hex digits. A trace that is not
there (or has aged out) is a `404`. Shortened here to three of its fifteen
spans, and to two of the root's seven children in the tree:

```json
{
  "trace_id": "000000000000000000000000acee0000",
  "spans": [
    {
      "span_id": "000000000000b000",
      "parent_id": "",
      "service": "acme/fix-orders",
      "name": "GET /orders",
      "kind": "server",
      "start_ms": 1791754259167.194,
      "duration_ms": 11.237,
      "status": "unset",
      "status_message": "",
      "db_system": "",
      "db_statement": "",
      "http_method": "GET",
      "http_route": "/orders",
      "http_status": 200,
      "attrs": {
        "http.request.method": "GET",
        "http.response.status_code": 200,
        "http.route": "/orders",
        "user.id": "u_42"
      },
      "entry": true,
      "depth": 0
    },
    {
      "span_id": "000000000000c000",
      "parent_id": "000000000000b000",
      "service": "acme/fix-orders",
      "name": "pg.query",
      "kind": "client",
      "start_ms": 1791754259167.194,
      "duration_ms": 0.254,
      "status": "unset",
      "status_message": "",
      "db_system": "postgresql",
      "db_statement": "SELECT id, customer_id FROM orders ORDER BY id LIMIT 25",
      "http_method": "",
      "http_route": "",
      "http_status": 0,
      "attrs": {
        "db.namespace": "acme",
        "db.query.text": "SELECT id, customer_id FROM orders ORDER BY id LIMIT 25",
        "db.system.name": "postgresql"
      },
      "db_role": "duplicate",
      "depth": 1
    },
    {
      "span_id": "36e50dc3352b64b7",
      "parent_id": "000000000000c000",
      "service": "acme/fix-orders",
      "name": "SELECT",
      "kind": "client",
      "start_ms": 1791754259167.366,
      "duration_ms": 0.05,
      "status": "ok",
      "status_message": "",
      "db_system": "postgresql",
      "db_statement": "SELECT id, customer_id FROM orders ORDER BY id LIMIT 25",
      "http_method": "",
      "http_route": "",
      "http_status": 0,
      "attrs": {
        "db.namespace": "acme",
        "db.rows": 1,
        "db.user": "app",
        "leadline.source": "pgproxy",
        "server.address": "localhost:50762"
      },
      "db_role": "query",
      "db_server": "127.0.0.1:50762",
      "depth": 2
    }
  ],
  "tree": [
    {
      "span_id": "000000000000b000",
      "children": [
        {
          "span_id": "000000000000c000",
          "children": [
            {
              "span_id": "36e50dc3352b64b7",
              "children": []
            }
          ]
        },
        {
          "span_id": "000000000000c001",
          "children": [
            {
              "span_id": "d6dfdbcfb7053d23",
              "children": []
            }
          ]
        }
      ]
    }
  ],
  "logs": [
    {
      "ts_ms": 1791754259178.431,
      "service": "acme/fix-orders",
      "severity": "info",
      "body": "listed 6 orders",
      "trace_id": "000000000000000000000000acee0000",
      "span_id": "000000000000b000",
      "attrs": {
        "orders": 6
      }
    }
  ]
}
```

- `spans`: every span of the trace, in start order.

  | field | what |
  | --- | --- |
  | `span_id`, `parent_id` | 16 hex digits each; `parent_id` is `""` for a root. |
  | `service`, `name`, `kind` | `kind` is `server`, `client`, `internal`, `producer` or `consumer`. |
  | `start_ms`, `duration_ms` | |
  | `status`, `status_message` | `status` is `ok`, `error` or `unset`. |
  | `db_system`, `db_statement` | `""` unless a database call. `db_statement` is **the SQL as it was sent**; the proxy's has ` …` at its end when it was cut at 16 KB. |
  | `http_method`, `http_route`, `http_status` | `""`, `""` and `0` unless the span said. |
  | `attrs` | Every attribute of the span, as an object. On a span the proxy recorded: `db.rows` (rows returned or changed), `db.query.parameters` (the values bound, as strings; `null` for NULL), `db.user`, `db.namespace` (the database), `server.address`, `leadline.source` (`"pgproxy"`), and when they apply `db.response.status_code` (the SQLSTATE of a failure), `db.failed_at` (`"parse"` or `"bind"`), `db.query.truncated`, `leadline.shared_timing` (several statements sent as one script and answered together share one stretch of time). On the proxy's `transaction` span: `db.transaction.statements` and `db.transaction.outcome` (`"commit"` or `"rollback"`). |
  | `db_role` | `"query"`, `"control"` or `"duplicate"`; left out for a span that is no database call. See [above](#a-statement-seen-twice-is-counted-once). |
  | `entry` | `true` when the span is a request; left out otherwise. |
  | `db_server` | The `host:port` a database call went to, when known; left out otherwise. |
  | `depth` | How far under a root it is in the tree: `0` for a root. |

- `tree`: the same spans as a tree, by id. Each node is
  `{"span_id": "...", "children": [...]}`, with children in start order. A
  span whose parent is not in the trace (not yet exported, or never) is a
  root, so there can be several. To draw a waterfall, walk the tree and look
  each `span_id` up in `spans`.
- `logs`: the log records written during the trace (those that carry its
  `trace_id`), oldest first, at most 1000. Each is a row of
  [`/api/logs`](#get-apilogs) without `kind`, `user_id` and `cursor`;
  `span_id` says which span wrote it.

### GET /api/logs

```
GET /api/logs?limit=2
GET /api/logs?source=acme/fix-orders&min_severity=17&q=declined
```

| parameter | default | what |
| --- | --- | --- |
| `source`, `service` | all | The worktree, or the one service, whose logs to list. |
| `since_ms` | everything held | Only records at or after this time. |
| `q` | none | Keep the records whose body contains this. |
| `min_severity` | 0 | Keep the records at or above this OTLP severity number: 1 trace, 5 debug, 9 info, 13 warn, 17 error, 21 fatal. |
| `kind` | none | Keep the records of one `leadline.kind`, as `browser.error`. |
| `user` | none | Keep the records written for this user (`user.id`). |
| `db_system`, `db_server` | all | Keep the records written during traces that sent a database call to this system, or this server. |
| `limit` | 500 (1 to 5000) | How many records. |
| `before` | none | The `cursor` of the last row of the page before. |

Log records, newest first; among records of one nanosecond, the one stored
last comes first, so the list read backwards is the order they were written
in. A bare list. A `before` that is not a cursor is a `400`.

```json
[
  {
    "ts_ms": 1791754259269.408,
    "service": "acme/add-refunds",
    "severity": "info",
    "body": "server listening on 3004",
    "trace_id": "",
    "span_id": "",
    "attrs": {},
    "cursor": "1791754259269408000.5"
  },
  {
    "ts_ms": 1791754259265.692,
    "service": "acme/fix-orders",
    "severity": "error",
    "body": "checkout failed: payment declined",
    "trace_id": "000000000000000000000000acee0003",
    "span_id": "000000000000b003",
    "attrs": {
      "orders": 0
    },
    "cursor": "1791754259265692000.4"
  }
]
```

| field | what |
| --- | --- |
| `ts_ms` | When it was written. |
| `service` | Who wrote it. |
| `severity` | `trace`, `debug`, `info`, `warn`, `error` or `fatal`. |
| `body` | The message. |
| `trace_id`, `span_id` | The trace and span it was written during; `""` for none. |
| `attrs` | Its attributes, as an object. |
| `kind` | Its `leadline.kind`, when it has one. Left out otherwise. |
| `user_id` | Its `user.id`, when it has one. Left out otherwise. |
| `cursor` | This row's place in the list, for `before`. |

### GET /api/endpoints

```
GET /api/endpoints?source=acme/fix-orders&spark=4
GET /api/endpoints?sort=queries&dir=desc&limit=50&offset=50
```

| parameter | default | what |
| --- | --- | --- |
| `since_ms`, `until_ms` | the last 15 minutes | The window. |
| `source`, `service` | all | The worktree, or the one service, whose requests to count. |
| `q` | none | Keep the endpoints whose name contains this. |
| `sort` | `time` | `time` (total time), `name`, `service`, `requests`, `errors` (the share that failed), `avg`, `p50`, `p95`, `max`, `queries` (per request), `db` (database time per request). Anything else is `time`. |
| `dir` | `desc` | `asc` for smallest first. |
| `limit` | 200 (1 to 1000) | How many endpoints. |
| `offset` | 0 | Endpoints to skip. |
| `spark` | none | Give it to get a sparkline for each row: how many buckets, 1 to 200 (`spark` alone, or out of range, is 24). |
| `db_system`, `db_server` | all | Count only the requests that sent a database call to this system, or this server. |

Per-route numbers. An **endpoint** is the requests that share a service and
a name (`GET /orders`, `job send-receipt`); a request is an `entry` span.
What a request did (its queries, its failed spans) is whatever started in
its trace while it ran. Shortened here to two of three rows:

```json
{
  "bucket_ms": 225000,
  "t": [1791753359282.049, 1791753584282.049, 1791753809282.049, 1791754034282.049],
  "total": 3,
  "rows": [
    {
      "service": "acme/fix-orders:worker",
      "name": "job send-receipt",
      "requests": 1,
      "errors": 0,
      "total_ms": 40,
      "avg_ms": 40,
      "p50_ms": 40,
      "p95_ms": 40,
      "max_ms": 40,
      "queries": 0,
      "db_ms": 0,
      "share": 0.5247484487123985,
      "last_ms": 1791754259229.408,
      "spark": [0, 0, 0, 1]
    },
    {
      "service": "acme/fix-orders",
      "name": "GET /orders",
      "requests": 3,
      "errors": 0,
      "total_ms": 33.141,
      "avg_ms": 11.047,
      "p50_ms": 11.011,
      "p95_ms": 11.2144,
      "max_ms": 11.237,
      "queries": 7,
      "db_ms": 0.3156666666666667,
      "share": 0.4347672084694399,
      "last_ms": 1791754259231.5068,
      "spark": [0, 0, 0, 3]
    }
  ]
}
```

- `total`: how many endpoints match, before `limit` and `offset`. It is `0`
  on a page past the end.
- `t`, `bucket_ms`: the sparklines' buckets (`[]` and the window's length
  without `spark`).
- `rows[]`:

  | field | what |
  | --- | --- |
  | `service`, `name` | The endpoint. For `/api/endpoints/detail` give them as `endpoint_service` and `name`. |
  | `requests` | How many requests. |
  | `errors` | Requests in which any span failed, or that answered 5xx. |
  | `total_ms`, `avg_ms`, `p50_ms`, `p95_ms`, `max_ms` | A request's duration: summed, the mean, the median, the slow percentile, the slowest. |
  | `queries` | Queries per request, on average. |
  | `db_ms` | Database time per request, on average. |
  | `share` | Its part of all request time in the window, 0 to 1. A search narrows the list, not what "all request time" means. |
  | `last_ms` | When its newest request began. |
  | `spark` | The request count in each of `t`'s buckets. Only with `spark`. |
  | `kind`, `action` | What a framework's plugin says the endpoint is (Next.js's `next.kind` and server action name), read off its newest request. Left out unless set. |

### GET /api/endpoints/detail

```
GET /api/endpoints/detail?endpoint_service=acme/fix-orders&name=GET+/orders&buckets=4
```

| parameter | default | what |
| --- | --- | --- |
| `name` | required | The endpoint's name. |
| `endpoint_service` | `""` | The endpoint's service. Give it: the two together name the endpoint. |
| `since_ms`, `until_ms` | the last 15 minutes | The window. |
| `source`, `service` | all | As on `/api/endpoints`. They narrow the window; `endpoint_service` names the endpoint. |
| `db_system`, `db_server` | all | As on `/api/endpoints`. |
| `buckets` | 60 (1 to 2000) | How many buckets of time the series and the heatmap have. |

Everything about one route. A missing `name` is a `400`. An endpoint with no
request in the window answers `200` with `"stats": null` and empty lists.
Shortened here to one of its two `queries` and one of its three `slowest`:

```json
{
  "stats": {
    "service": "acme/fix-orders",
    "name": "GET /orders",
    "requests": 3,
    "errors": 0,
    "total_ms": 33.141,
    "avg_ms": 11.047,
    "p50_ms": 11.011,
    "p95_ms": 11.2144,
    "max_ms": 11.237,
    "queries": 7,
    "db_ms": 0.3156666666666667,
    "share": 0.43476720846943995,
    "last_ms": 1791754259231.5068
  },
  "series": {
    "bucket_ms": 225000,
    "t": [1791753359283.319, 1791753584283.319, 1791753809283.319, 1791754034283.319],
    "requests": [0, 0, 0, 3],
    "errors": [0, 0, 0, 0],
    "p50_ms": [null, null, null, 11.011],
    "p95_ms": [null, null, null, 11.2144],
    "avg_ms": [null, null, null, 11.047],
    "queries": [null, null, null, 7],
    "db_ms": [null, null, null, 0.3156666666666667]
  },
  "histogram": {
    "per_decade": 10,
    "bins": [
      {
        "lo_ms": 10,
        "hi_ms": 12.589254117941675,
        "count": 3
      }
    ]
  },
  "heatmap": {
    "bucket_ms": 225000,
    "t": [1791753359283.319, 1791753584283.319, 1791753809283.319, 1791754034283.319],
    "per_decade": 10,
    "x": [3],
    "y": [10],
    "n": [3],
    "y_min": 10,
    "y_max": 10,
    "max": 3
  },
  "queries": [
    {
      "db_system": "postgresql",
      "fingerprint": "SELECT name FROM customers WHERE id = ?",
      "example": "SELECT name FROM customers WHERE id = $1",
      "calls": 18,
      "requests": 3,
      "per_request": 6,
      "max_per_request": 6,
      "avg_ms": 0.044055555555555556,
      "max_ms": 0.073,
      "total_ms": 0.793,
      "n_plus_one": true
    }
  ],
  "slowest": [
    {
      "trace_id": "000000000000000000000000acee0000",
      "start_ms": 1791754259167.194,
      "duration_ms": 11.237,
      "queries": 7,
      "db_ms": 0.376,
      "failed": false
    }
  ],
  "failing": []
}
```

- `stats`: its row of `/api/endpoints` (without `spark`, `kind`, `action`).
- `series`: the endpoint over time: `requests` and `errors` per bucket;
  `p50_ms`, `p95_ms`, `avg_ms`, `queries` (per request) and `db_ms` (per
  request) are `null` for a bucket with no requests.
- `histogram`, `heatmap`: of its requests' durations, as on
  [`/api/queries/detail`](#get-apiqueriesdetail).
- `queries`: the query shapes its requests ran, costliest first (at most
  50): `db_system`, `fingerprint`, `example`; `calls`; `requests` (how many
  of its requests ran the shape at all); `per_request` (`calls` over all of
  its requests: well above 1 is an N+1); `max_per_request`; `avg_ms`,
  `max_ms`, `total_ms`; `n_plus_one` (`max_per_request` is 5 or more).
- `slowest`: its slowest requests (at most 12), slowest first. `failing`:
  its failing requests (at most 12), newest first. Each is `trace_id`,
  `start_ms`, `duration_ms`, `queries`, `db_ms`, `failed`.

### GET /api/series

```
GET /api/series?buckets=4
GET /api/series?source=acme/fix-orders&since_ms=1791754000000&until_ms=1791754900000&buckets=60
```

| parameter | default | what |
| --- | --- | --- |
| `since_ms`, `until_ms` | the last 15 minutes | The window. |
| `source`, `service` | all | The worktree, or the one service. |
| `buckets` | 60 (1 to 2000) | How many buckets. |

The overview: everything in the window, bucketed. Every list has one value
for each bucket; `t[i]` is where bucket `i` starts.

```json
{
  "bucket_ms": 225000,
  "t": [1791753359285.973, 1791753584285.973, 1791753809285.973, 1791754034285.973],
  "requests": [0, 0, 0, 5],
  "errors": [0, 0, 0, 1],
  "queries": [0, 0, 0, 22],
  "p50_ms": [0, 0, 0, 11.011],
  "p95_ms": [0, 0, 0, 34.24739999999999],
  "db_ms": [0, 0, 0, 1.162],
  "logs": [0, 0, 0, 5],
  "log_errors": [0, 0, 0, 1]
}
```

`requests`: `entry` spans, the same requests the endpoints count. `errors`:
failed spans. `queries`: spans that are queries. `p50_ms`, `p95_ms`: of the
requests' durations, `0` for a bucket with none. `db_ms`: database time.
`logs`: log records; `log_errors`: those at severity 17 (error) and above.

### GET /api/heatmap

```
GET /api/heatmap?buckets=4
```

| parameter | default | what |
| --- | --- | --- |
| `since_ms`, `until_ms` | the last 15 minutes | The window. |
| `source`, `service` | all | The worktree, or the one service. |
| `buckets` | 90 (1 to 2000) | How many columns of time. |

Latency of every request over time: a grid with time across and log-spaced
latency bands up, holding how many requests fell in each cell. Only the
cells that hold something are sent: `x[i]`, `y[i]` and `n[i]` describe one
cell.

```json
{
  "bucket_ms": 225000,
  "t": [1791753359286.3481, 1791753584286.3481, 1791753809286.3481, 1791754034286.3481],
  "per_decade": 5,
  "x": [3, 3, 3],
  "y": [2, 5, 8],
  "n": [1, 3, 1],
  "y_min": 2,
  "y_max": 8,
  "max": 3
}
```

`x` is a column, an index into `t`. `y` is a band: band `y` covers
`10^(y/per_decade)` to `10^((y+1)/per_decade)` milliseconds, so with
`per_decade` 5, band 5 is 10 ms to 15.8 ms; bands below 1 ms are negative.
`y_min`, `y_max`: the lowest and highest band in use. `max`: the fullest
cell's count. Durations are clamped to 1 µs and 1 hour.

### GET /api/breakdown

```
GET /api/breakdown?by=status&buckets=4
```

| parameter | default | what |
| --- | --- | --- |
| `by` | required | `db_time` (ms in each database system), `db_calls` (queries to each database system), `status` (requests by HTTP status class: `2xx`, `4xx`, `5xx`, ...; `other` for requests that are not HTTP), `service` (requests arriving at each service), `service_time` (ms in each service's requests). Anything else is a `400` that lists them. |
| `since_ms`, `until_ms` | the last 15 minutes | The window. |
| `source`, `service` | all | The worktree, or the one service. |
| `buckets` | 60 (1 to 2000) | How many buckets. |
| `limit` | 6 (1 to 20) | How many groups, the largest. |

One measure over time, split by a dimension: a line for each group, largest
first.

```json
{
  "bucket_ms": 225000,
  "t": [1791753359286.58, 1791753584286.58, 1791753809286.58, 1791754034286.58],
  "groups": [
    {
      "key": "2xx",
      "total": 3,
      "values": [0, 0, 0, 3]
    },
    {
      "key": "5xx",
      "total": 1,
      "values": [0, 0, 0, 1]
    },
    {
      "key": "other",
      "total": 1,
      "values": [0, 0, 0, 1]
    }
  ]
}
```

Each group has its `key`, its `total` over the window and its `values`, one
for each bucket of `t`.

### GET /api/storage

No parameters.

```json
{
  "size_bytes": 65536,
  "file_bytes": 222488,
  "max_bytes": 2147483648,
  "retain_ms": 259200000,
  "oldest_ms": 1791754259167.194
}
```

`size_bytes`: the data held, which is what the size cap is measured against.
`file_bytes`: what the database file and its write-ahead log take on disk;
the two differ by what is waiting in the log. `max_bytes`: the cap
(`--max-size`), `0` for none. `retain_ms`: how long a row is kept
(`--retain`), `0` for ever. `oldest_ms`: the time of the oldest span or log
held, `0` for none.

Old rows are dropped once a minute: everything older than `retain_ms`; and,
when `size_bytes` is past `max_bytes`, the oldest share of rows that brings
it back under. The file gets smaller when they go.

### GET /api/events

```
GET /api/events
```

A [server-sent events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events)
stream that says "new data arrived". It carries no rows: refetch what is on
screen (at most once a second is plenty).

```
event: hello
data: {}

event: ingest
data: {}

: keepalive
```

`hello` comes once, when the stream opens: from then on nothing is missed.
`ingest` comes after an OTLP export was taken, after a proxy's statements
were stored, and after a proxy was opened or closed or a worktree detached;
a burst of them is one event. A comment line comes every 20 seconds to keep
the connection open. The stream ends when the helper stops.

### GET /api/proxies

```
GET /api/proxies
GET /api/proxies?source=acme/fix-orders&since_ms=1791754000000
```

| parameter | default | what |
| --- | --- | --- |
| `source` | all | Only the proxies opened for this worktree. |
| `since_ms` | 15 minutes ago | Where `window_statements` starts counting. |
| `limit` | 200 (1 to 1000) | How many proxies. |
| `offset` | 0 | Proxies to skip. |

Every proxy, oldest first.

```json
{
  "total": 1,
  "proxies": [
    {
      "id": "p_f46655d8b2",
      "kind": "postgres",
      "listen": "127.0.0.1:50764",
      "port": 50764,
      "upstream": "localhost:50762",
      "label": "acme/fix-orders",
      "origins": ["api"],
      "state": "listening",
      "owner": {
        "kind": "worktree",
        "via": "shipyard",
        "service": "acme/fix-orders",
        "project": "acme",
        "worktree": "fix-orders",
        "branch": "fix/orders-n-plus-one",
        "box": "devbox",
        "label": "from Shipyard, worktree fix-orders"
      },
      "connections_open": 0,
      "connections_total": 1,
      "statements": 24,
      "failed_statements": 0,
      "unread_connections": 0,
      "last_activity_ms": 1791754259266.1592,
      "window_statements": 24
    }
  ]
}
```

#### The proxy object

| field | what |
| --- | --- |
| `id` | For `DELETE /api/proxies/{id}`. It stays the same across restarts. |
| `kind` | `"postgres"`. |
| `listen`, `port` | Where it listens: `127.0.0.1:PORT`. What goes in the app's database URL in place of the database's address. It stays the same across restarts. |
| `upstream` | The Postgres it is in front of, as it was given. |
| `label` | The source it was opened for: everything through it is filed under that name, whatever a connection calls itself. `""` for one opened for nobody, which files under each connection's `application_name`, or the database's name. |
| `origins` | `["api"]` (opened with `POST /api/proxies`) or `["flag"]` (the helper's `--pg`). |
| `state` | `"listening"`, or `"error"` with `error` saying why (its port is taken). The helper tries again every 5 seconds. |
| `owner` | [An owner](#get-apisources): the worktree it is for. |
| `connections_open`, `connections_total` | Client connections now, and since the proxy opened. |
| `statements`, `failed_statements` | Statements recorded since it opened, and those that ended in an error. |
| `unread_connections` | Connections relayed without being understood: their statements were not recorded. |
| `last_activity_ms` | When a connection last arrived or a statement was last recorded; `0` if never. |
| `window_statements` | Statements recorded since `since_ms`, counted by the minute and kept for a day. |

The counts start again at zero when the helper restarts; what was recorded
is in the store.

### POST /api/proxies

```
POST /api/proxies
{"kind": "postgres", "upstream": "localhost:5432", "label": "acme/fix-orders",
 "project": "acme", "worktree": "fix-orders", "branch": "fix/orders-n-plus-one", "box": "devbox"}
```

| field | default | what |
| --- | --- | --- |
| `upstream` | required | Where the Postgres listens: `host:port`, a host alone (port 5432), a bare port (on `127.0.0.1`), or the path of a unix socket. Not a URL: a URL has a password in it, and the proxy needs only the address. It must be on this machine (`localhost`, a loopback address, `0.0.0.0`, or a socket). |
| `label` | `""` | The source to file its statements under: the worktree's service name. |
| `kind` | `"postgres"` | Nothing else can be proxied. |
| `port` | picked | Ask for one port in particular. Left out, the first free port of `--proxy-ports` is taken. |
| `project`, `worktree`, `branch`, `box` | none | What is known of the worktree, to show with the source. They are remembered for `label`. |

Opens a proxy for a source, or finds the one there is. **Asking again is
safe**: the same upstream (however its address is spelled: `localhost:5432`
and `127.0.0.1:5432` are one) with the same label gives the same proxy and
`"created": false`. Another label gets a proxy of its own: one port for each
worktree and server.

```json
{
  "created": true,
  "proxy": {
    "id": "p_f46655d8b2",
    "kind": "postgres",
    "listen": "127.0.0.1:50764",
    "port": 50764,
    "upstream": "localhost:50762",
    "label": "acme/fix-orders",
    "origins": ["api"],
    "state": "listening",
    "owner": {
      "kind": "worktree",
      "via": "shipyard",
      "service": "acme/fix-orders",
      "project": "acme",
      "worktree": "fix-orders",
      "branch": "fix/orders-n-plus-one",
      "box": "devbox",
      "label": "from Shipyard, worktree fix-orders"
    },
    "connections_open": 0,
    "connections_total": 0,
    "statements": 0,
    "failed_statements": 0,
    "unread_connections": 0,
    "last_activity_ms": 0,
    "window_statements": 0
  }
}
```

The proxy is saved (`proxies.json`) before the answer is sent, and opened
again on the same port at every start, so a dev server that was pointed at
it still reaches its database after the helper restarts. While the helper is
down, though, the port is closed and the app's database connections fail:
the proxy is in the path.

Errors: `bad_request` (400: no upstream, a URL, another `kind`, a `port`
that is no port), `capture_remote` (400: the upstream is not on this
machine), `too_many` (400), `port_in_use` (409: the `port` asked for is
taken), `no_port` (409: no port of the range is free), `not_saved` (500).

### DELETE /api/proxies/{id}

```
DELETE /api/proxies/p_f46655d8b2
```

Closes a proxy and forgets it. The connections through it end, and what it
had recorded is stored first.

```json
{
  "ok": true,
  "closed": true
}
```

An unknown id is a `404` (`not_found`). A proxy from the `--pg` flag is not
closed this way (`400`, `bad_request`).

### POST /api/attach

```
POST /api/attach
{"box": "devbox", "project": "acme", "worktree": "fix-orders", "branch": "fix/orders-n-plus-one",
 "path": "/home/dev/acme-fix-orders", "command": "web",
 "env": {"DATABASE_URL": "postgres://app:hunter2@localhost:5432/acme?sslmode=prefer",
         "OTEL_RESOURCE_ATTRIBUTES": "deployment.environment=dev"}}
```

| field | default | what |
| --- | --- | --- |
| `project` | required | The repository, as Shipyard names it. |
| `worktree` | `""` | The worktree's name; empty for the project's main checkout. |
| `branch`, `box` | none | For showing with the source. |
| `path` | none | Where the worktree is. Taken and not used. |
| `command` | `""` | The name of this command among several run in the worktree (a service's name in the project's config). |
| `env` | `{}` | The command's environment, or the part of it the caller shows: the helper reads the database URLs and the `OTEL_*` variables in it. |

Points a worktree's command at the helper, in one call: what the box daemon
asks before it starts a worktree's service. It is the
[attach helper](#the-attach-helper-go) and
[`POST /api/proxies`](#post-apiproxies) together, for a caller that knows
nothing of either. The answer's `env` is what to set in the command's
environment, over what it has.

```json
{
  "env": {
    "DATABASE_URL": "postgres://app:hunter2@127.0.0.1:59199/acme?sslmode=prefer",
    "OTEL_BLRP_SCHEDULE_DELAY": "500",
    "OTEL_BSP_SCHEDULE_DELAY": "500",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:59197",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
    "OTEL_RESOURCE_ATTRIBUTES": "deployment.environment=dev,shipyard.project=acme,shipyard.worktree=fix-orders,shipyard.branch=fix%2Forders-n-plus-one,shipyard.box=devbox",
    "OTEL_SERVICE_NAME": "acme/fix-orders:web"
  },
  "service": "acme/fix-orders:web",
  "source": "acme/fix-orders",
  "proxies": ["p_597c677fea"]
}
```

- `env`:
  - **Telemetry.** The six variables of the attach helper's `Env`, with the
    helper's own address as the endpoint (the `--otlp` address when it was
    started with one). What the project's `OTEL_RESOURCE_ATTRIBUTES` sets is
    kept and the worktree's four attributes are added to it. An
    `OTEL_SERVICE_NAME`, `OTEL_BSP_SCHEDULE_DELAY` or
    `OTEL_BLRP_SCHEDULE_DELAY` the project set itself is left out of the
    answer, so it stands.
  - **A project with a collector of its own keeps it.** When `env` sets
    `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` or
    `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`, the answer has no `OTEL_*` variable
    at all: the endpoint is never replaced, and its service name and
    resource are not changed for a collector that is not this one. Its
    queries still show, through the proxy.
  - **The database.** Each variable whose value `LocalPostgres` accepts (a
    Postgres on this machine, whatever the variable is called) comes back
    as the same URL through the worktree's proxy, which is opened if it is
    not there: one proxy for each worktree and server, the same port every
    time, so asking again when the service restarts changes nothing. A URL
    that cannot go through a proxy (a remote host, one that insists on TLS)
    is left out of the answer, and so is one that already names a proxy of
    this helper's, and one whose proxy could not be opened: the command
    then reaches its database directly, as it would have.
- `service`: the name the command's telemetry is filed under
  (`ServiceFor(command)`, or the project's own `OTEL_SERVICE_NAME`).
  `source`: the worktree's name, which the proxies are labelled with.
- `proxies`: the ids of the proxies the URLs in `env` go through.

**Asking again is safe**, and so is not asking: nothing is recorded of the
call itself. A missing `project` is a `400` (`bad_request`). Like every
route that changes something, it is taken from the box daemon and from
Shipyard's own window, not from another page.

### POST /api/detach

```
POST /api/detach
{"service": "acme/add-refunds", "keep_data": false}
```

| field | default | what |
| --- | --- | --- |
| `service` | required, or `project` | The source's name. |
| `project`, `worktree` | none | The worktree as [`/api/attach`](#post-apiattach) was told of it, in place of `service`: the source's name is worked out from them (`Service()`), with `worktree` empty for the project's main checkout. `service` wins when both are given. |
| `keep_data` | `false` | `true` closes the proxies and keeps what was recorded. |

The box daemon calls it when a worktree is removed (the plugin's manifest
names this path as its helper's `detach`), with what it said to attach,
less the command and its environment:

```
POST /api/detach
{"box": "devbox", "project": "acme", "worktree": "add-refunds", "branch": "feat/refunds", "path": "/home/dev/acme-add-refunds"}
```

`box`, `branch` and `path` are taken and not used. Neither `service` nor
`project` is a `400` (`bad_request`).

Has the helper forget a worktree, for when the worktree is removed: closes
every proxy opened for it (its own label and the names under it) and, unless
`keep_data`, deletes everything recorded under its name and the names under
it (`acme/add-refunds` and `acme/add-refunds:worker`, not
`acme/add-refunds-2`), with what was known of the worktree.

```json
{
  "service": "acme/add-refunds",
  "found": true,
  "proxies": 0,
  "kept_data": false,
  "deleted": {
    "spans": 0,
    "logs": 1
  }
}
```

`found`: the helper had anything of that name. Detaching a worktree it never
heard of is no error: `"found": false`. `proxies`: how many were closed.
`deleted`: the rows that went. Rows of that name that arrive after the call
are kept: detach is for a source that has stopped.

## The attach helper (Go)

Package `github.com/cosscom/shipyard/internal/leadline` (this folder's own
`attach.go`) is what points a dev server at the helper: the names, the
variables and the database URL. [`POST /api/attach`](#post-apiattach) is
these functions behind one call, which is how the box daemon uses them: it
asks the helper, and imports nothing of it. A caller that sets a command's
environment itself can use the package directly. It imports only the
standard library; the packages beside it bring SQLite and protobuf and must
not be imported by `berthd` or `berth`.

```go
// Worktree is one checkout of a project. Name is empty for the project's
// main checkout. Branch and Box are for showing.
type Worktree struct {
	Project, Name string
	Branch, Box   string
}

// Service is the name the worktree is filed under, and its source in the
// API: "<project>/<worktree>", or the project alone for its main checkout.
func (w Worktree) Service() string

// ServiceFor is the name for one command among several in the worktree:
// "<project>/<worktree>:<command>", or Service() when command is "".
func (w Worktree) ServiceFor(command string) string

// Helper is where a running berth-leadline listens. OTLP is a base URL
// that takes OTLP/HTTP: an entry of "otlp" in helper.json.
type Helper struct{ OTLP string }

type Var struct{ Name, Value string }

// Env returns the environment variables a dev server needs for the helper
// to see its traces and logs under the worktree's name. have is the value
// OTEL_RESOURCE_ATTRIBUTES already has in the command's environment.
func Env(w Worktree, command string, h Helper, have string) []Var

// LocalPostgres reports whether a value, as of DATABASE_URL, is the URL of
// a Postgres on this machine that a proxy can stand in front of, and where
// that Postgres listens: the "upstream" of POST /api/proxies.
func LocalPostgres(value string) (upstream string, ok bool)

// ThroughProxy returns the URL with the database's address replaced by
// proxy (the "listen" of a proxy), and everything else as it was written.
func ThroughProxy(value, proxy string) (rewritten string, ok bool)
```

**The names.** `Service()` joins the project and the worktree with `/`, and
`ServiceFor` adds `:` and the command. In each part `/`, `:`, `\`, white
space and control characters become `-`, leading and trailing `-` are
dropped, and it is cut at 60 bytes. A worktree named for its project, or
with no name, is the main checkout: the project alone.

| `Worktree` | command | name |
| --- | --- | --- |
| `{Project: "acme", Name: "fix-orders"}` | `""` | `acme/fix-orders` |
| `{Project: "acme", Name: "fix-orders"}` | `"worker"` | `acme/fix-orders:worker` |
| `{Project: "acme"}` or `{Project: "acme", Name: "acme"}` | `""` | `acme` |
| `{Project: "acme shop", Name: "feat/fix:orders"}` | `""` | `acme-shop/feat-fix-orders` |

**The variables.** `Env(Worktree{Project: "acme", Name: "fix-orders",
Branch: "fix/orders-n-plus-one", Box: "devbox"}, "worker",
Helper{OTLP: "http://127.0.0.1:4325"}, "deployment.environment=dev")`
returns, in this order:

| name | value |
| --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://127.0.0.1:4325` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` |
| `OTEL_SERVICE_NAME` | `acme/fix-orders:worker` |
| `OTEL_RESOURCE_ATTRIBUTES` | `deployment.environment=dev,shipyard.project=acme,shipyard.worktree=fix-orders,shipyard.branch=fix%2Forders-n-plus-one,shipyard.box=devbox` |
| `OTEL_BSP_SCHEDULE_DELAY` | `500` |
| `OTEL_BLRP_SCHEDULE_DELAY` | `500` |

Any OpenTelemetry SDK reads them, in any language. What `have` already sets
stays, and a key it sets is not set again. An attribute with nothing to say
(no branch, no box, the main checkout's worktree) is left out, and so is
the whole variable when there is nothing to add to an empty `have`.
`OTEL_SERVICE_NAME` is left out for a `Worktree` with no name at all. The
two delays have the SDK send within half a second; its own default is five
seconds, which in a tool watched while working reads as "nothing arrived".
`Env` only says what to set: it sets nothing, and whether to override what
the project sets is the caller's to decide. An app with no OpenTelemetry SDK
ignores them all, and still shows its queries through the proxy.

**The database.** For each variable of the dev server's environment:

```go
if upstream, ok := leadline.LocalPostgres(value); ok {
	// POST /api/proxies {"upstream": upstream, "label": w.Service(),
	//   "project": w.Project, "worktree": w.Name, "branch": w.Branch, "box": w.Box}
	// and with the answer's proxy.listen:
	value, _ = leadline.ThroughProxy(value, listen)
}
```

The label is `w.Service()`, the worktree's own name, for every command in
it: one proxy for each worktree and server.

`LocalPostgres` takes `postgres://`, `postgresql://`, a driver suffix
(`postgresql+psycopg://`) and a `jdbc:` prefix, on `localhost`,
`*.localhost`, a loopback address or `0.0.0.0` (dialled as `127.0.0.1`),
with port 5432 when none is written. It refuses, so that they are left
alone: a remote host, a unix socket, several hosts, `?host=` or `?hostaddr=`,
and a connection that insists on TLS (`sslmode=require`, `verify-ca`,
`verify-full`; `ssl=true`, `1` or `require`), which the proxy could not
read. The upstream it returns has no user and no password in it.

`ThroughProxy` cuts the address out of the string and puts the proxy's in:
user, password, database and options keep every byte they had.

| value | through `127.0.0.1:25432` |
| --- | --- |
| `postgres://app:hunter2@localhost:5432/acme` | `postgres://app:hunter2@127.0.0.1:25432/acme` |
| `postgres://app:p%40ss@[::1]:5433/acme?sslmode=prefer` | `postgres://app:p%40ss@127.0.0.1:25432/acme?sslmode=prefer` |
| `jdbc:postgresql://localhost/acme?user=app` | `jdbc:postgresql://127.0.0.1:25432/acme?user=app` |
| `postgres://app@db.acme.example/acme` | unchanged, `ok` false |

When the worktree is removed: `POST /api/detach {"service": w.Service()}`,
or with the worktree's `project` and `worktree`, which is how the box daemon
says it.

## What differs from Leadline's API

The routes Leadline has keep its names and its response shapes; a UI written
for Leadline reads these. What is different:

- **Added:** `source` on every list route; `offset` on `/api/queries` and
  `/api/endpoints`; `errors` and `min_duration_ms` on `/api/traces`;
  `n_plus_one` on a query row, a query's stats, an endpoint's query row and
  a query's endpoint row; the `n_plus_one` list of `/api/queries/detail`;
  `tree`, and `depth` on each span, in `/api/traces/{id}`; `api` in
  `/api/health`, whose `name` is `berth-leadline`.
- **`/api/sources`** has `sources` and `total`, and no `connections` or
  `attached`: there are no saved connections here. A service has `source`
  and no `metric_points`; its `through` has no `connection_ids`.
- **`/api/proxies`**: `POST` answers `{"created", "proxy"}` with the whole
  proxy object, takes the worktree's description, and is safe to repeat
  (Leadline counts uses, and `DELETE` gives one back; here `DELETE` closes).
  Proxies are kept across restarts. A proxy has no `connection_ids`.
  `GET` has `total`.
- **`/api/detach`** takes the same body, and a worktree's `project` and
  `worktree` in place of `service`; its answer has no `connections` and its
  `deleted` no `metric_points`.
- **`/api/attach`** is not Leadline's, which attaches a saved connection:
  here it takes a worktree's command and its environment, and answers with
  the variables to set. Nothing is saved but the proxy it opens.
- **Errors** under `/api/` are always the JSON object; Leadline answers its
  older routes' errors with a line of text.
- **Logs of one nanosecond** come last-stored first (Leadline: first-stored
  first), and a log's cursor is its time and its row's number.
- **`/api/endpoints`** has no `never_hit`: route lists are not collected.
- **Not here:** metrics (`/api/metrics*`), route lists (`/api/routes`),
  saved connections and their statistics (`/api/connections*`), Redis
  capture, runs (`/api/runs*`), discovery and setup, briefs, the MCP
  endpoint (`/mcp`), `/api/debug/memory`.
