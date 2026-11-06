# Work records and session parents

A box keeps a record of each piece of running work (`internal/box/work.go`, `work.json`): title, sessions, status, Not now, Waiting on, last_seen and suggested groups, with the box's pins beside them. A record defines its own state: it stays when its sessions end. [Needs me](needs-me.md) is its view in the app; the API (`/v1/work…`) and the CLI (`berthd work …`) are the rest of the surface. A new session records the session that started it as `started_by` (a tmux option, `@berth_started_by`).

## Sub-features

- `work-started-by`: a session started from a session (`session new`, `task new`, the MCP tools, a run they start) shows `started_by` in its JSON, kept across a daemon restart. A paired computer can't claim one.
- `work-rules`: an agent session joins a record by issue id (ranges and lists: `X-1..5`, `X-1/2`), then by shared worktree, then by parent; else it gets its own. Shells and service terminals get none. A session naming several records joins the oldest and suggests the rest.
- `work-id-pattern`: `berthd work settings --id-pattern RE` is the box's rule for an id.
- `work-crud`: create, update (status, Not now, Waiting on, check back), group, ungroup, accept and reject a suggestion, from API and CLI, reading the same from both.
- `work-pins`: a repo, a project or a record, kept until removed. A record that closes loses its pin.
- `work-last-seen`: counts a person's prompt or answer (app, phone, a laptop's `berth`, a terminal) and `work seen`; not an agent's send, a `/loop` wakeup or a restart.
- `work-events`: changes publish `work.*`, without titles or what someone typed.

## How to get to it (user POV)

**Needs me** in the app. From a box: `berthd work list`, `berthd work get ID`, `berthd sessions --json | jq '.[].started_by'`.

## Driving it

A real box and real Claude Code, in its terminal driver. From a `/ship` worker unset every `BERTH_*` first, and use your own `VERIFY_BOX_DIR`:

```sh
export VERIFY_BOX_DIR=/tmp/vb-work
.claude/skills/verify-berthd/scripts/dev-box.sh up
.claude/skills/verify-berthd/scripts/work-records-live.sh     # ends in PASS work-records
.claude/skills/verify-berthd/scripts/dev-box.sh down
```

It takes a few minutes and spends a few short Claude prompts. Unit tests: `go test ./internal/box -run 'Issue|RecordsForm|LastSeen|Ungroup|Closing'`.

## Not covered live

A `/loop` wakeup (`TestLastSeenIgnoresAgentsAndWakeups`, `TestHumanPromptTimes`) and the phone (the same event as the app's send, `from: "phone"`).
