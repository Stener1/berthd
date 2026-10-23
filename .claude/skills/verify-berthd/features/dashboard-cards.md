# The Agent Dashboard's cards

Each agent on the board is a live status card (`app/src/views/dashboard/agent-card.tsx`). What it says comes from the agent's own record, read the way its chat reads it (`views/dashboard/use-card-run.ts` → `lib/transcript-feed`), and only while the card is on screen. `lib/agent-run.ts` turns the record into steps (unit tests next to it). An agent without a record (a shell, a box too old to send one) keeps the old body: the last lines of its screen.

## Sub-features

- `headline`: the agent's mark in a ring that says its state (a turning arc while it works, a pulse while it waits, a check once done), the icon of what it does now, and the words: the step under way in the present tense ("Running pnpm test payments"), "Thinking" between calls, "Waiting on its work" or "Parked: …" from turn check, the approval it waits on ("Needs your approval to run git push"), or "Done in 13s · 6 files changed". A new headline rolls in from below. The clock on the right counts the time in its state; a finished card has none.
- `subheader`: the model (`modelLabel` of its signals; the agent's name without a record), then the work's title (its place, box and branch in the tip), and "Step N of M" from its task list ("N of M steps" once done; "N steps" without a list).
- `pills`: one pill per task (shared out when there are more than 8): blue when done, pulsing on the one under way.
- `log`: this turn's last 4 steps. A finished step is in the past tense with how long it took (to the next timed step; a finished turn's last one to its end). The live one is in the present tense with its clock running. A permission it waits on is a blue-tinted row. Steps fade in, and the card's height follows on a spring (none with reduced motion). Prompts and edits carry their time from newer boxes (`Item.At`, `internal/transcript`), so a turn counts from its prompt.
- `controls`: working: **Pause** (Esc through `interrupt`, on boxes with `controls`) and **Stop** (asks first). Needs you: **Reply…** and **Stop**, plus the agent's numbered options as chips for a question that isn't a plain approval. An approval swaps them for **Approve** (solid blue) and **Deny**, matched from its options by `permissionChoices`. The footer keeps its height. Ready: **Prompt…** and **Stop**. Done: only **Open run ↗**. The ⋯ menu (orchestrate, Stop and remove) shows on hover and focus.

## How to get to it (user POV)

- ⌘J opens the Agent Dashboard. In the mock: `Tune the judge prompt` (working, 6 tasks), `Fix checkout webhook retries` (an approval), `Plan the checkout release` (questions), `Export orders as CSV from the admin` (done, timed).

## Driving it with drive.mjs

- `node .claude/skills/verify-berthd/scripts/drive.mjs dashboard-status-cards --port $PORT` checks each state above, that the approval's footer is as tall as a working card's, that Approve answers (toast "Approved"), and that Open run opens the agent.
- `app/e2e/dashboard-cards.spec.ts` asserts the same.

## Gotchas

- The mock's turns are `seedTranscript` (`lib/mock-conversation.ts`); the card seeds the same conversation its chat would, so opening the chat from the card shows the same steps.
- The judge agent's 6 tasks are in `mockSignals` (`lib/chat-controls.ts`).
- A finished mock turn with an extra chat after its prompt (`search-perf`, the artifacts story) has no times: it reads "Done", not "Done in …".
- Light themes: the app's theme is `berth.ui` `{ themeId }` in localStorage, not the system's colour scheme.
