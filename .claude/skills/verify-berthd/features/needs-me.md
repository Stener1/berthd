# Needs me

A menu item with a count. One piece of work fills the screen; answer or close it and the next comes up. The rules are in `app/src/lib/needs-me.ts`, the card format in `app/src/lib/cards.ts`, the view in `app/src/views/needs-me/`. It reads sessions and each box's work records (see [work-records.md](work-records.md)).

## Sub-features

- `needs-me-order`: pinned decisions, other decisions, pinned tasks, other tasks, results, the close-out deck, then suggested groups. Within a pass, longest unseen first. A count anywhere is the list's length.
- `needs-me-entry`: waiting agents, sessions that ended on a question or block (a title starting with ❓ or ⛔) with no form, and results not seen since they ended. Parked sessions stay out, and so do queued ones, unless a question on the agent's own screen is holding up what's queued. A line that ended stays history when a new session reuses its name. A session no record holds yet still enters while it waits.
- `needs-me-walk`: a card in front never moves. A new pinned question sorts in right behind it and becomes next. A card passed over waits behind the rest.
- `needs-me-keys`: number keys pick, Enter submits a form or opens a session, `j`/`n` next, `k` back, `e` Read, `s` Not now, `w` Waiting on, `d` Close, `p` Pin, `r` the reply box.
- `needs-me-actions`: Read, Not now (tomorrow or a date), Waiting on (who or what, and a check-back date), Close, Pin (this work, its repo or its project). Not now and Waiting on come back by themselves when due and sit under Later until then, with a label saying whom it waits for. Read on a question that still waits moves it to the back of its pass.
- `needs-me-close`: Close ends the sessions (their transcripts stay) and keeps the record. It re-reads the box first: it asks if a session is working now, and leaves a new session under an old name alone.
- `needs-me-stale`: a reply is never forced, so it waits rather than landing in a question the agent has reached since. A permission's key is checked against the wait on the card, and refused if it has moved on. A change the box refuses keeps the card where it is.
- `needs-me-deck`: stale 3+ days since `last_seen`, with nothing running, queued or parked: Close, Park, Still needed.
- `needs-me-groups`: a piece of work's sessions as a tree from `started_by`, orchestrator on top. A worker's question is answered directly. Ungroup, Group with, and suggested groups as a card with Accept and Reject.
- `needs-me-cards`: every card is built as JSON and validated before it draws. A button with a URL or a command is refused. Agent text is drawn as text.

## How to get to it (user POV)

**Needs me** in the sidebar, pinned under Home the first time it appears, or ⌘K, "Needs me".

## Driving it

Mock, `?scene=needs-me`, a fleet with one of everything it sorts. `window.__berthMockWork` stands in for what a box or the clock does: `arrive(name, title)`, `pass(id)`, `clear()`, `refuse()`, `moveOn(session)`, `record(id)`, `sent`.

```sh
node .claude/skills/verify-berthd/scripts/drive.mjs needs-me --port $PORT
cd app && E2E_PORT=$PORT npx playwright test e2e/needs-me.spec.ts --workers=1
node --experimental-strip-types --test src/lib/cards.test.ts src/lib/needs-me.test.ts
```

A form answered on a real box, by real Claude Code in its terminal driver. From a `/ship` worker unset every `BERTH_*` first, and use your own `VERIFY_BOX_DIR`:

```sh
export VERIFY_BOX_DIR=/tmp/vb-needs-me
.claude/skills/verify-berthd/scripts/dev-box.sh up
eval "$(.claude/skills/verify-berthd/scripts/dev-box.sh env)"
node .claude/skills/verify-berthd/scripts/drive.mjs needs-me-live --port $PORT --agent http://127.0.0.1:9378
.claude/skills/verify-berthd/scripts/dev-box.sh down
```

Second view: the agent's own record (`sessions/<name>/transcript`) has the answer and Claude says it back; `GET work/<id>` shows `last_seen` moved.

## Gotchas

- A wait on the agent's own screen with nothing to read from it (no form, no choices) offers no reply, only Open session. A permission's forced key still has a small window after the check, as the chat's and the board's do: the box has no expected-wait check.
- A real session's finished turn comes back as a result once Claude replies, because the person hasn't read that reply. That's correct, and the live scenario relies on it.
- Not drawn yet, though valid in the format: chart, image, before/after, diff summary, checks, checklist, command, secret and page. A card with one says so. Nothing Shipyard builds today uses them; workers composing cards (step 2) will.
- The question form takes attachments only inside a chat (`ChatFiles`, from the `question-card-images` branch); Needs me doesn't wrap it, so there's no paste or drop on its form yet.
- The mock accepts every box as having the `work` capability, so a box without records shows up only in the unit tests.

## Not covered

The phone, and the sweep that closes finished work by itself (separate issues). Real groups, real pins and a real count against a live fleet are checked on the channel build, not here.
