# The Agent Dashboard's cards

With **Settings → Agents → Agent Dashboard → Live status cards** on (`prefs.dashboardCards: "status"`; off by default), each agent on the board is a live status card (`app/src/views/dashboard/agent-card.tsx`). Off, it is the classic card (`classic-card.tsx`), which `dashboard-stop.md` and `turn-check-indicator.md` describe. What it says comes from the agent's own record, read the way its chat reads it (`views/dashboard/use-card-run.ts` → `lib/transcript-feed`), and only while the card is on screen. `lib/agent-run.ts` turns the record into steps (unit tests next to it). An agent without a record (a shell, a box too old to send one) keeps the old body: the last lines of its screen.

## Sub-features

- `title`: the agent's mark in a ring that says its state (a turning arc while it works, a pulse while it waits, a check once done), then the work's title as the card's main line, as the board is scanned by it, and the clock for its time in its state. On hover or focus the ⋯ menu takes the clock's place (a finished card has no clock, so the corner holds only the menu).
- `meta`: where it works (place, box, location, branch, then the model last, the first to go in a narrow column) and "Step N of M" from its task list ("N of M steps" once done). Nothing shows without a task list: the count is the agent's own TodoWrite/TaskCreate/plan (`internal/transcript/signals.go`), never guessed.
- `now`: one line for what it does now, its icon in the mark's column: the step under way in the present tense with its own clock ("Running pnpm test payments 12s"), "Thinking" between calls, "Waiting on its work" or "Parked: …" from turn check, the approval it waits on (tinted blue), or "Done in 13s · 6 files changed" (green check). New words roll in from below. A plain ready agent has no line.
- `pills`: one pill per task (shared out when there are more than 8): blue when done, pulsing on the one under way.
- `log`: the steps it took before the one it is on, the last 3, small and quiet: past tense with how long each took (to the next timed step; a finished turn's last one to its end). A question or permission left open by a turn that ended isn't a step. Steps fade in, and the card's height follows on a spring (none with reduced motion). Prompts and edits carry their time from newer boxes (`Item.At`, `internal/transcript`), so a turn counts from its prompt.
- `controls`: working: **Pause** (Esc through `interrupt`, on boxes with `controls`) and **Stop** (asks first). Needs you: **Reply…** and **Stop**, plus the agent's numbered options as chips for a question that isn't a plain approval; a form of questions (AskUserQuestion) gets **Answer ↗** instead, which opens the agent where the form is. An approval swaps them for **Approve** (solid blue) and **Deny**, matched from its options by `permissionChoices`. The footer keeps its height. Ready: **Prompt…** and **Stop**. Done: only **Open run ↗**, flush right.

## How to get to it (user POV)

- Turn the switch on in Settings › Agents, then ⌘J opens the Agent Dashboard. In the mock: `Tune the judge prompt` (working, 6 tasks), `Fix checkout webhook retries` (an approval), `Plan the checkout release` (questions), `Export orders as CSV from the admin` (done, timed).

## Driving it with drive.mjs

- `node .claude/skills/verify-berthd/scripts/drive.mjs dashboard-status-cards --port $PORT` turns the switch on through Settings, then checks each state above, that the approval's footer is as tall as a working card's, that Approve answers (toast "Approved"), and that Open run opens the agent.
- `app/e2e/dashboard-cards.spec.ts` asserts the same.

## Gotchas

- The mock's turns are `seedTranscript` (`lib/mock-conversation.ts`); the card seeds the same conversation its chat would, so opening the chat from the card shows the same steps.
- The judge agent's 6 tasks are in `mockSignals` (`lib/chat-controls.ts`).
- A finished mock turn with an extra chat after its prompt (`search-perf`, the artifacts story) has no times: it reads "Done", not "Done in …".
- Light themes: the app's theme is `berth.ui` `{ themeId }` in localStorage, not the system's colour scheme.
