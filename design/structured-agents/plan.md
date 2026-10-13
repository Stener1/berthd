# Structured agents: plan

Status: 2026-10-13. Phase 0 (spike) done: go. Phases 1 (driver interface) and 2 (agent-host + log) merged. Phase 3 (Claude driver, behind Labs) merged. Phase 4a (terminal and back) in review; 4b (resume, conversation index) next. The spike's recordings join the repo as test fixtures in phase 3, once reviewed.

## Why

Today every agent is its TUI in tmux, and the conversation view is rebuilt from
the agent's own record files (`~/.claude/projects/*.jsonl`, Codex rollouts),
stat-polled, plus screen-scraping (`screen-status.ts`, `screen.ts`) and
keystroke-driving (`answer.go`, `controls.go`, `orchestrate.go` paste+Enter).

Every comparable app (t3code, Vibe Kanban, Sculptor, Paseo, Superset, Nimbalyst,
Orca's structured mode) instead drives the agent over a structured channel and
keeps its own record:

- Claude: `claude -p` stream-json in/out (Vibe Kanban, Sculptor) or the Agent SDK
  on top of the same protocol (t3code, Paseo, Superset).
- Codex: `codex app-server` (JSON-RPC over stdio), unanimously.
- Others: ACP (Agent Client Protocol).
- Own store (SQLite / log); only the agent's session id is kept, for `--resume`.
- CLI transcripts are read only for history import, never as the live feed.

Policy (checked 2026-10-13): running the real, unmodified `claude` binary with the
user's own login, including `claude -p` and stream-json, is allowed and draws
from subscription limits (support article 15036540, updates 2026-06-15 and
2026-10-07). Never read, copy or proxy the user's credentials. Risk: the paused
June change would have moved `-p`/SDK usage off subscription limits; if it comes
back, the TUI path is the fallback, so the driver stays a setting.

## Decisions (from the user)

| | |
|---|---|
| Default | Chat first; "Open in terminal" hands the same session to the TUI via `--resume` |
| Claude link | Raw CLI stream-json spoken by berthd in Go (no Node, no SDK) |
| v1 scope | Claude + Codex (app-server) + a generic ACP driver for the rest |
| ACP agents | Gemini CLI, opencode, Cursor/Copilot/others: one config-driven driver |
| History | berthd's own event log per box; agent session id kept for resume |
| Approvals | Default bypass (berthd auto-allows each request in default mode; works as root); a global setting (repo/kit can override) |
| Rollout | Spike first, then phased PRs behind Labs |

## Shape

```
app ──SSE/API──▶ berthd ──unix socket──▶ berthd agent-host (in tmux) ──stdio──▶ claude -p / codex app-server / ACP agent
                   │                            │
                   └── index (SQLite)           └── events.jsonl (append-only, berth's own format)
```

- The **driver** turns agent protocol into `transcript.Item` upserts (the model
  the app already renders) plus live state (busy/idle/waiting, partial text,
  status) and requests (approval, question, plan).
- The **agent-host** owns the agent's stdio inside a tmux session, so an agent
  survives a berthd restart/upgrade or a dropped link (none of the desktop apps
  solve this; it's the Berth-specific part). berthd reconnects and replays the
  log from an offset.

## Phases

### 0. Spike (throwaway, scratch dir, against claude 2.1.296 and the boxes' codex)

Answer, with recorded protocol fixtures (sanitised) kept for tests:

- [ ] Long-lived multi-turn `claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages`
- [ ] Control requests: interrupt, set permission mode, set model
- [ ] `--permission-mode bypassPermissions --permission-prompt-tool stdio`: do AskUserQuestion and ExitPlanMode still reach us (Vibe Kanban relies on this)?
- [ ] Bypass when the box user is root (VPS): does Claude refuse, and what does it need?
- [ ] `/compact`, custom commands, skills; `system/init.slash_commands`; which built-ins refuse
- [ ] Images/attachments as content blocks
- [ ] berth's installed hooks: do they fire under `-p`? (avoid double turn events)
- [ ] Session hand-off: stream session → `claude --resume <id>` TUI → back; `--fork-session`; `--resume-session-at <uuid>`
- [ ] Kill mid-turn, resume: what's lost
- [ ] Codex `app-server`: thread/start, turn/start, turn/interrupt, approvals, resume
- [ ] ACP handshake with `gemini --experimental-acp` and `opencode acp`

Exit: a go/no-go note per item; any no-go changes the phases below.

### 1. Driver interface (no behaviour change)

- `internal/agentdrv`: `Driver{Start, Send(prompt, attachments), Interrupt, SetMode, SetModel, Respond(requestID, decision), Stop, Events()}`.
- Wrap today's path as `ptyDriver` (tmux keys + transcript tail) so the
  conversation pane talks to one API regardless of driver.
- Box capability `driver`; the app keeps today's code path for older boxes.

### 2. Agent-host + event log

- `berthd agent-host` in the agent's tmux session: spawns the agent, owns stdio,
  appends to `<state>/conversations/<id>/events.jsonl` (versioned), serves a
  unix socket. Lock: one agent process per conversation, ever.
- Keep streamed partial text in the log (a killed agent loses it). Stop =
  interrupt, then SIGTERM, then kill the process tree (SIGKILL leaves Bash
  children running).
- The SQLite index (conversation id, location/worktree, agent, driver, agent
  session id, title, status, cost, timestamps) comes with resume and the
  terminal hand-off (phase 4), the first thing that looks conversations up
  after their session is gone.
- Push to the app via the events bus (items in the event, not a "changed" ping);
  no stat-polling for driver agents.
- Attaching to the tmux session shows a readable live render, so `berth attach` keeps working.

### 3. Claude driver

- Spawn: the flags above + `--resume <id>` when continuing. Always
  `--permission-mode default --permission-prompt-tool stdio`; "bypass" is berthd
  auto-allowing each request (Claude refuses the bypass flag as root without
  `IS_SANDBOX=1`). Questions and plans always come to us.
- Set an env var (e.g. `BERTH_DRIVER=structured`) so `berthd hook` stays silent
  for these sessions; the user's other hooks keep running.
- Read commands/models from the `initialize` control request; follow
  `session_id` changes (`/clear` starts a new one). Merge assistant messages by
  `message.id` (one per content block).
- Map messages → Items, reusing `internal/transcript/claude.go` where shapes
  match; partial messages → live text (replaces `screen-status.ts` scraping).
- Approvals, AskUserQuestion, ExitPlanMode → cards (the `question` item exists);
  no keystroke form-driving for these agents.
- Subagents via `parent_tool_use_id` → crew items. Usage/cost from `result` →
  usage plugin.
- Slash commands from init; terminal-only ones offer "Open in terminal".
- Fork (`--fork-session`) and rewind (`--resume-session-at`).

### 4. Terminal hand-off

- "Open in terminal": at idle (or after interrupt) the host stops the driver
  and runs `claude --resume <id>` in the same tmux session; the pane shows the
  terminal and the conversation falls back to the transcript tail.
- "Back to chat": the TUI exits, the driver resumes the session; turns made in
  the TUI are imported once from the transcript into the event log.

### 5. Codex driver: `codex app-server`

thread/start|resume, turn/start|interrupt, approvals → cards, items → Items.

### 6. Generic ACP driver

- Agents defined in config (box, kit, repo):
  `{ "gemini": "gemini --acp", "opencode": "opencode acp", "cursor": "cursor-agent acp" }`.
- Per-agent quirks: `authenticate` when asked (cursor), resume via
  `session/resume`, pick permission options by `kind`, usage optional, ignore
  unknown vendor requests (`cursor/create_plan`).
- session/new|load, session/prompt, session/update → Items,
  session/request_permission → cards (or auto-allow in bypass), session/cancel.
- Agents without ACP stay terminal-only.

### 7. Flows onto the driver

Judges, reviewers and `.ask` steps (`headless.go`) use the same driver, so they
get the event log, live view and cost. Read-only mode = a tool allow/deny list.

### 8. Make it the default

- New agents open in chat; Labs toggle removed.
- Settings: approval mode (global, default bypass), default driver
  (structured | terminal: the fallback if `-p` billing changes).
- Transcript tailing stays for terminal-mode agents and importing old sessions.

## Cross-cutting

- **Protocol drift:** stream-json control messages are undocumented. Contract
  tests on recorded fixtures, plus a scheduled CI job against the latest
  `claude`/`codex` that fails loudly on unknown message types.
- **Bypass default:** this is a change from today (berth never adds
  skip-permissions). Worktrees limit the blast radius; show the mode on every
  agent; repo/kit can force `ask`.
- **Credentials:** never touch `~/.claude`/`~/.codex` auth; login stays the
  user running `claude`/`codex` themselves (terminal hand-off covers `/login`).
- **Naming:** the Claude name/logo can't be part of the product's name or logo.
