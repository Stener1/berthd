# Leadline

What your code did in development, as tabs on a worktree. Not built in: it
is in the listing (Settings → Plugins → Browse), and off until you allow
it. The guide is [docs/guides/leadline.mdx](../../docs/guides/leadline.mdx).

- **Queries**, **Traces**, **Logs** and **Endpoints**: four worktree
  panels, each about the worktree in front. They share one time window (15
  minutes unless changed) and read again every few seconds while on screen.
- **Leadline**: a screen with every box and what it has of the helper (off,
  installing, running, failed, can't run it), the switch for each, what a
  running one holds, and what an app needs before it shows up.
- **Send to the agent**: a slow query, an N+1 or a failing endpoint written
  up for the worktree's agent, shown before it is sent.

## The helper

The data comes from `berth-leadline` (`cmd/berth-leadline`,
`internal/leadline`), a program the plugin's manifest carries under
`box.helper`. The laptop agent installs it on a box when the person turns
Leadline on there, and the box runs it; the plugin reads its API through
the box with `berth.helper.request`. Every route and answer is in
[internal/leadline/API.md](../../internal/leadline/API.md), and
`src/api.ts` types the ones the panels read.

Two rules are the Go side's, copied to the letter in `src/source.ts`, with
its own test cases in `app/src/lib/leadline.test.ts`:

- **A worktree's source**: `<project>/<worktree>`, or the project alone for
  its main checkout, each part made safe as `attach.go` makes it. The
  project is the repository as the box names it (its location).
- **A statement's shape**: the helper's fingerprint. A trace carries each
  statement as it was sent, so the waterfall reduces it here before it is
  shown, and a span's parameters are left out.

## Working on it

```sh
pnpm -C app typecheck:plugins
pnpm -C app build:marketplace     # site/plugins/leadline/, which is committed
pnpm -C app test                  # src/lib/leadline.test.ts
```

Mock mode (`?mock=1`) runs the listing's own bundle, installed and allowed,
so rebuild the listing to see a change there. Its helper is
`app/src/lib/mock-helper.ts`: running on devl, off on gpu, answering with
`app/src/lib/fixtures/leadline.json`, which is what the real helper said
about made-up telemetry. To capture it again after `API.md` changes:

```sh
go build -o /tmp/berth-leadline ./cmd/berth-leadline
node app/scripts/capture-leadline-fixtures.mjs /tmp/berth-leadline
```

Raise `version` in `berth-plugin.json` when the code changes. The
manifest's `assets` are empty here: the listing's build fills in each
platform's download and checksum.
