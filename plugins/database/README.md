# Database

A worktree's own development database, as a tab on the worktree: browse its
tables, edit rows and run SQL, read-only until unlocked. Not built in: it is
in the listing (Settings → Plugins → Browse), and off until you allow it.
The guide is [docs/guides/database.mdx](../../docs/guides/database.mdx).

- **Database**: a worktree panel. Tables by schema with row estimates; a
  table as a grid (sort, filters, paging, NULL told from the empty string,
  long values in a sheet, foreign keys that jump) or as its structure;
  edits that are staged, reviewed as old → new and applied as one; a lock
  on writes that lasts 15 minutes; and a SQL console with a history.
- **Database**: a screen with every box and what it has of the helper, the
  switch for each, and which databases show up.

It is its own plugin with its own helper. It doesn't need Leadline, and
imports nothing from it.

## The helper

The data comes from `berth-database`, a program the plugin's manifest
carries under `box.helper`. The laptop agent installs it on a box when the
person turns Database on there, and the box runs it; the plugin reads its
API through the box with `berth.helper.request`, so a real box sees
`helpers/database/api/api/db/…`. Every route and answer is in
[internal/database/API.md](../../internal/database/API.md), which is the
source of truth; `src/api.ts` types the six routes the panel uses:

| Route (from the helper's `/api/`) | What it is |
| --- | --- |
| `GET db/connections?source=S` | The worktree's databases, each with its engine's `capabilities` |
| `GET db/{conn}/schema` | Schemas, tables, columns, keys, indexes |
| `POST db/{conn}/rows` | A page of a table, ordered and filtered |
| `POST db/{conn}/query` | One statement |
| `POST db/{conn}/edit` | Staged changes, all or nothing |
| `POST db/{conn}/writes` | The lock |

A worktree's `source` is `<project>/<worktree>`, or the project alone for
its main checkout: the same rule as Leadline's helper, copied into
`src/source.ts` and tested on its own.

## Where a worktree's database comes from

The helper is told of a database by the box, never by the panel directly,
in one of three ways:

- **A service Shipyard starts.** Before the box starts one of the
  worktree's services it calls the helper's `/api/attach` with the
  service's `DATABASE_URL` and `POSTGRES_URL` (the manifest's
  `attach_env`), and the service's name as `command`.
- **The worktree's `.env` files.** A project whose dev servers Shipyard
  does not start never gets there. So when the panel opens and
  `db/connections` is empty for the worktree, it asks the box to look:
  `berth.helper.attachEnv(box, { location, worktree })`, which is
  `POST helpers/database/attach-env` on the box. The box reads `.env`,
  `.env.development`, `.env.local` and `.env.development.local` in the
  worktree's own folder (a later file winning), keeps those two variables
  and calls the same `/api/attach` with **`.env` as `command`**. The helper
  did not change for this: it files a database under the commands that
  claim it, so a look replaces what the last look found, and a database a
  service also has stays while the service has it.

- **By hand.** **Connect a database…** (in the empty state, and in the
  picker when the worktree has several) takes an address in a dialog
  (`src/connect.tsx`) and calls `attachEnv` with
  `env: { DATABASE_URL: address }`: the box reads no file and calls
  `/api/attach` with **`.manual` as `command`**. An empty `env` takes it
  back, which is what **Forget…** does. The address is in the dialog's
  field until it is sent and nowhere in the plugin after that; what is
  kept (in the plugin's storage, on this computer) is the id of the
  connection it became, which is how the panel knows a connection was
  added by hand and may be forgotten. The helper decides whether an
  address is a database on the box, and answers no connection when it is
  not; it does not say why in its answer, so the dialog states the rule.

The panel looks once each time it is opened (opening the tab is the person
asking), and again from **Look again** and from the refresh button above
the tables, when the worktree has no database or has one the files gave.
`src/dotenv.ts` is the rules, with no React in them: what a look's answer
means, what to keep of it (the connection ids and the file each variable
was in, in the plugin's storage, since the helper lists a connection by
its variable and not by who said so), and the sentences for a look that
found nothing to show. The box sends file and variable names, never a
value.

A box whose `berthd` is from before the route answers `box_outdated`, and
a Shipyard from before `attachEnv` has no such function: in both the panel
is as it was, with no error.

What the panel holds to, from `API.md`:

- **Errors are told apart by `code`, never by status.** `query_failed` may
  carry `position` (a character of the statement, from 1: the console marks
  it), `sqlstate` and `detail`; `not_found` on `db/connections` is a helper
  from before the route; `unauthorized` is a box whose `berthd` doesn't
  send the helper its token yet.
- **The next page starts at `offset + rows.length`.** A page can stop short
  of its limit with `more: true`.
- **`writes_until` is the box's clock.** The countdown starts when the
  answer to turning writes on arrives (`src/clock.ts`).
- **A cell goes back as it came.** An integer past 2^53 is a string of its
  digits, in a key too.
- **An edit conflicts only when a row is gone.** A row something else
  changed is overwritten, and Review says so.

## Engines

Postgres is the only engine for now, and nothing outside `src/engines/`
names it. Most of what differs between engines arrives with a connection,
as its `capabilities`, and the panel is built from that:

| Capability | What it decides |
| --- | --- |
| `dialect` | The console's highlighting and completion |
| `schemas` | Whether the list of tables has a schema level |
| `operators` | The comparisons a filter offers |
| `kinds` | The kinds of relation there are |
| `writes` | Whether the lock and the edit controls show at all |
| `transactions` | What Review says about applying |

What the helper can't say is in `src/engines/`: an engine's name and icon,
how its type names fall into families (`families.ts` decides how each
family is shown and typed in), how it quotes a name, and how it explains a
statement. To add one, write `src/engines/<id>.ts` after `postgres.ts`, add
it to `ENGINES` in `src/engines/index.ts`, and add its cases to
`app/src/lib/database.test.ts`.

An engine with no entry still shows: every value as the text the helper
sent, nothing editable in the grid, and a console that runs what you type.
Mock mode has one ("driftdb", on shop / checkout-fix's second connection)
to keep that true.

## Where things are

| File | What is in it |
| --- | --- |
| `src/panel.tsx` | The panel: the helper's state, the connections, the look in the worktree's `.env`, the lock, and the states with nothing to show |
| `src/dotenv.ts` | What a look in the worktree's `.env` files found, and what to say of it; the rules for an address given by hand |
| `src/connect.tsx` | **Connect a database…** and **Forget…**: the two dialogs |
| `src/browser.tsx` | The table list, a table's rows and structure, staging and Review |
| `src/grid.tsx` | The grid, shared with the console. Only the rows near the view are in the page |
| `src/console.tsx` | The console, on the app's `SqlEditor` |
| `src/staged.ts`, `src/filters.ts`, `src/statements.ts`, `src/rules.ts` | The rules, with no React in them: staged changes, filters and order, which statement Run means, what may be edited |

## Working on it

```sh
pnpm -C app typecheck:plugins
pnpm -C app build:marketplace     # site/plugins/database/, which is committed
pnpm -C app test                  # src/lib/database.test.ts
cd app && pnpm build && E2E_PORT=1427 npx playwright test e2e/database.spec.ts
```

Mock mode (`?mock=1`) runs the listing's own bundle, installed and allowed,
so rebuild the listing to see a change there. Its helper is
`app/src/lib/mock-database.ts`: a made-up "acme" shop kept in memory, with
filters, paging, edits, conflicts, the lock and a console that runs a small
set of statements. It answers in the real helper's fields, codes and
sentences. `?db=none`, `?db=refused`, `?db=old` and `?db=unauthorized` are
the states with nothing to show. `?db=env` is a project whose database is
only in a worktree's `.env`, found when the tab opens; `?db=env-none`,
`?db=env-refused` and `?db=env-old` are a look that found no variable, one
that found a variable it can't show, and a box that can't look. Connect a
database… works in memory in every one of them: the mock helper takes an
address on `localhost` or `127.0.0.1` and passes any other over.

To see what the real helper says, run it against a throwaway Postgres (on
a spare port, never 5432), attach a URL and ask it:

```sh
go build -o /tmp/berth-database ./cmd/berth-database
/tmp/berth-database serve --dir /tmp/berth-database-data --addr 127.0.0.1:4391
# helper.json in that folder has "api" and "token"; every route but
# /api/health needs Authorization: Bearer <token>.
```

Raise `version` in `berth-plugin.json` when the code changes. The
manifest's `assets` are empty here: the listing's build fills in each
platform's download and checksum.
