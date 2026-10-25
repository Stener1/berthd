# Chat: a live turn, and a turn that ended while its work runs on

The chat's foot and its reply box say whether the agent is in a turn. In a live turn (`agent_state: running`) the last row is the agent's spinner word (its own, or a harbour word such as "Making way…" when its screen shows none) with a ticking timer, the reply box queues what you type ("Claude is working: it reads this at its next step", **Queue**), and **Stop** sits under it. A finished turn that turn check read as work still running (`agent_state: finished`, `unfinished: true`: a delegated task, a build, a deploy, a watcher) is between turns: the last row is an hourglass and **Waiting on its work**, with the Agent Dashboard card's tooltip, no spinner word and no timer; the reply box shows **Send** and starts a new turn, and there is no Stop. Everywhere else (the sidebar, the board's counts) that session still counts as running (`lib/derive.ts` `onItsWork`, `chatState`, `replyMode`; `conversation-pane.tsx`; `conversation-view.tsx`).

## Sub-features

- `live-turn`: the spinner word and timer, **Queue** and **Stop** for a running agent.
- `on-its-work`: the hourglass row, its tooltip ("Its turn ended while its work runs on: …"), **Send**, no Stop, for a finished + unfinished one.
- `new-turn`: a prompt sent then starts a turn, and the chat is a live turn's again (the box resets `unfinished` on the turn's start).

## How to get to it (user POV)

- Open a worktree's agent and switch the pane to **Conversation**.
- On the mock: `devl/order-export`'s tab **Add tests for the export job** is on its work; `gpu/judge-v2` is mid-turn.
- On a real box: a turn that ends saying its build, deploy or background task is still running, with turn check on.

## Driving it with drive.mjs

- On the mock: `scenarios/chat-turn-state.mjs` opens both and asserts the literal row text, the tooltip, and the buttons (**Send** and no **Stop …** on its work; **Stop Claude** and **Queue** in a live turn). `app/e2e/chat.spec.ts` ("a turn that ended while its work runs on …") asserts the same.
- On a real box: `scenarios/chat-turn-state-live.mjs` (`export const live = true`), on a dev box of this checkout with turn check on (the commands in [chat-ask-card.md](chat-ask-card.md)). It has Claude Code start `sleep 600` in the background and end its turn, waits for the box to list the session `finished` with `unfinished: true`, and asserts the same as the mock; then a foreground `sleep 45` for a live turn (`agent_state: running`, the timer, **Stop Claude**, **Queue**). Second view: the box's `sessions` through the agent (`call`).
- Turn check's verdicts themselves are Go's: `TestTurnCheckReplays` in `internal/box/turncheck_test.go` replays recorded AI Gateway answers (`internal/box/testdata/turncheck/`), each tied to its exact request by a sha256. A changed prompt fails it until it is recorded again, which asks the gateway: `BERTH_TURNCHECK_RECORD_KEY_FILE=<key file> go test ./internal/box -run TestTurnCheckReplays`.

## Gotchas

- The tab strip and the sidebar still draw a spinner for a session on its work; giving them the hourglass is a separate change.
- On a real box the hourglass needs the box's turn check to say `working` at 0.65 or more. A reply that only reports another session's progress and offers to follow it up is `done`, so it ends as a plain finished turn.
- A helper (subagent) left running in the background keeps using tools after the agent's Stop. Claude Code marks those hooks with `agent_id`, and the box ignores them while no turn is open, so they don't bring back the live turn. One that asks for a permission then still makes the session wait, and its tool running once that is granted ends the wait.
