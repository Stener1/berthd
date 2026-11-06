# The Agent Dashboard's cards

With **Settings → Agents → Agent Dashboard → Live status cards** on (`prefs.dashboardCards: "status"`; off by default), each agent on the board is a live status card (`app/src/views/dashboard/status-card.tsx`). Off, it is the classic card (`agent-card.tsx`), which `dashboard-stop.md` and `turn-check-indicator.md` describe. What it says comes from the agent's own record, read the way its chat reads it (`views/dashboard/use-card-run.ts` → `lib/transcript-feed`), and only while the card is on screen. `lib/agent-run.ts` turns the record into steps (unit tests next to it). An agent without a record (a shell, a box too old to send one) keeps the old body: the last lines of its screen.

## Sub-features

- `cost`: it reads an agent's record only while its card is on screen: a working agent's as often as its chat would, a waiting or finished one on its events and every 30s besides. The step rows are memoised against the board's one-second clock, and the now line has no shimmer (a background-position animation repaints the line every frame). Measured on the mock board, its script time is within a few ms/s of the classic card's.
- `look`: the card follows devl's agent components (devl.dev, Sean's own library on coss ui like the app): Run List's status icon and clock at the head, Working Out Loud's rail of steps, and Go / No-go's approval. The app's own `Button` and `Kbd` throughout; none of devl's code is copied (its items are demos with scripted data).
- `title`: devl's status icon (a turning arc while it works, an amber alert while it waits on you, a green check once done, an hourglass while its work runs on or it is parked, an empty ring at rest), then the work's title as the card's main line, and the clock for its time in its state (`1:05`, `14:05`; past an hour `1h 15m`). On hover or focus the ⋯ menu takes the clock's place (a finished card has no clock).
- `meta`: where it works (place, box, location, branch, then the agent's mark and model last, the first to go in a narrow column) and "Step N of M" from its task list ("N of M steps" once done). Nothing shows without a task list: the count is the agent's own TodoWrite/TaskCreate/plan (`internal/transcript/signals.go`), never guessed.
- `pills`: one pill per task (shared out when there are more than 8): blue when done, pulsing on the one under way.
- `rail`: the steps it took this turn, the last 3, small and quiet with a soft green check each and how long each took (to the next timed step; a finished turn's last one to its end), then its last row (`card-now`): the step under way in the present tense with a turning mark and its own clock ("Running pnpm test payments 0:12"), "Thinking" between calls, "Waiting on its work" or "Parked: …" with an hourglass, what it waits on with an amber dot, or "Done in 13s · 6 files changed" with a green one. New words roll in from below. A step a full rail pushes off the top leaves at once, so a working card keeps its height as steps roll by; there is no height animation. A question or permission left open by a turn that ended isn't shown.
- `approval`: instead of the rail's last row, Go / No-go's block (`card-now`, `data-status=waiting`): "Claude Code wants to run a command" (`askOf` in `lib/agent-run.ts`) and the exact command or path in a mono block (up to 3 lines), with **Deny** `N` and **Approve** `Y` in the footer; Y and N answer while the card has focus.
- `controls`: working: **Pause** (Esc through `interrupt`, on boxes with `controls`) and **Stop** (asks first). Needs you: **Reply…** and **Stop**, plus the agent's numbered options as chips for a question that isn't a plain approval; a form of questions (AskUserQuestion) gets **Answer ↗** instead, which opens the agent where the form is. An approval swaps them for **Deny** `N` and **Approve** `Y` (the app's `Button` and `Kbd`), matched from its options by `permissionChoices`. The footer keeps its height. Ready: **Prompt…** and **Stop**. Done: only **Open run ↗**, flush right.

- `plugins`: `AgentStatusCard` in `@berth/plugin/ui` (`app/src/plugins/ui.tsx`, declared in `packages/plugin-sdk/src/ui.ts`, exported by name in `app/public/shims/berth-plugin-ui.js`) is this card on a plugin's own board, with the plugin's `title`, `mark`, `children` (lines under the meta), `actions` (buttons after the card's) and `onOpen`, and `highlighted`. It finds the session by box and name, draws nothing for one the box doesn't list, and loads the card's code on first use. It shows the live card whatever the Settings switch says: the plugin chose it.

## How to get to it (user POV)

- Turn the switch on in Settings › Agents, then ⌘J opens the Agent Dashboard. In the mock: `Tune the judge prompt` (working, 6 tasks), `Fix checkout webhook retries` (an approval), `Plan the checkout release` (questions), `Export orders as CSV from the admin` (done, timed).

## Driving it with drive.mjs

- `node .claude/skills/verify-berthd/scripts/drive.mjs dashboard-status-cards --port $PORT` turns the switch on through Settings, then checks each state above, that the approval's footer is as tall as a working card's, that Approve answers (toast "Approved"), and that Open run opens the agent.
- `app/e2e/dashboard-cards.spec.ts` asserts the same.

- `node .claude/skills/verify-berthd/scripts/drive.mjs plugin-status-card --port $PORT` loads a test plugin whose Board draws one with every extra, plus one for a session that doesn't exist, and checks both, the plugin's button and its Open. `app/e2e/plugin-status-card.spec.ts` asserts the same.

## Gotchas

- A new export of `@berth/plugin/ui` must also be listed in `app/public/shims/berth-plugin-ui.js`, or a plugin importing it fails to load ("does not provide an export named …").
- A plugin's screen is under the sidebar's More; the driver needs `params = { sidebar: "list" }` to see it, and `menuitem` names match by substring ("Board" is in "Agent Dashboard"), so match exactly.

- The mock's turns are `seedTranscript` (`lib/mock-conversation.ts`); the card seeds the same conversation its chat would, so opening the chat from the card shows the same steps.
- The judge agent's 6 tasks are in `mockSignals` (`lib/chat-controls.ts`).
- `window.__berthTurn.play(box, session, prompt)` (mock only) plays a scripted turn into an agent already at work, so steps arrive one by one. Don't `await` it from `page.evaluate`: it resolves when the turn ends. `app/e2e/dashboard-cards.spec.ts` samples the card's height through one.
- A finished mock turn with an extra chat after its prompt (`search-perf`, the artifacts story) has no times: it reads "Done", not "Done in …".
- Light themes: the app's theme is `berth.ui` `{ themeId }` in localStorage, not the system's colour scheme.
