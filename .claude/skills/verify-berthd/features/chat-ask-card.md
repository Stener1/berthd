# Chat: the question card and the agent's own screen

When an agent waits for you, its chat shows what to answer. A permission prompt gets a card with Allow, Always allow and Deny. A form of questions (AskUserQuestion) gets the question form. A menu of numbered options gets "Needs your answer" with those options. A dialog only keys answer (the trust question, Remote Control's menu) opens the agent's own screen inline. A wait that has none of those draws no card. Turn check's wait (the agent's last message asks in words, `soft_wait` on the session) is answered from the reply box, and the mode menu still works then. The card never makes up Yes / No (`conversation-pane.tsx`, `conversation-view.tsx`, `lib/screen.ts` `askIn`).

## Sub-features

- `ask-permission`: a permission prompt's card, its options read off the screen as Allow / Always allow / Deny.
- `ask-form`: AskUserQuestion's form, with its options as radios.
- `ask-menu`: "Needs your answer" with the numbered options the screen shows, and the line above them as the question.
- `ask-none`: no card for a wait with no options on screen: turn check's wait, a screen at the agent's prompt (whatever list its last message has), Remote Control's menu, the footer's `/rc`.
- `mode-at-soft-wait`: the mode menu switches modes during turn check's wait, and stays blocked at a permission prompt.
- `board-answers`: the Agent Dashboard's card offers a menu's options as answer chips, and never a numbered list from the agent's last message (`views/dashboard/agent-card.tsx`, `use-screen-tail.ts`).

## How to get to it (user POV)

- Open a worktree's agent and switch the pane to **Conversation**.
- Type `/rc` in the reply box; pick a mode from the mode chip under it (**Permission mode: …**).
- Ask the agent something that makes it end its turn on a question in words; ask it to run a command in **Ask before edits**; ask it to use AskUserQuestion.

## Driving it with drive.mjs

- On the mock fixtures (no real box) the e2e spec `app/e2e/ask-card.spec.ts` covers `ask-none`, `ask-menu` and `board-answers` on a stand-in box, with the screens from `internal/box/testdata/screens/`. With `BERTH_SHOTS=DIR` it saves the chat and the board as screenshots.
- On a real box: `scenarios/chat-ask-card.mjs` (`export const live = true`), on a dev box of this checkout (`../scripts/dev-box.sh`, as [chat-questions.md](chat-questions.md) runs it). It needs turn check on for that box. The dev box reads turn check's settings from its own user dir, so copy yours in once it is up; they are read at each check, so no restart is needed:

```sh
.claude/skills/verify-berthd/scripts/dev-box.sh up
eval "$(.claude/skills/verify-berthd/scripts/dev-box.sh env)"   # the token stays in env
cp ~/.berth/turncheck.json /tmp/verify-berthd-box/box-user/        # turn check: on, provider, model
install -m 600 "$BERTH_HOME/box/turncheck.key" /tmp/verify-berthd-box/box-home/box/   # your box's key; dev-box.sh down deletes it
node .claude/skills/verify-berthd/scripts/drive.mjs chat-ask-card --port $PORT --agent http://127.0.0.1:9378
.claude/skills/verify-berthd/scripts/dev-box.sh down
```

  It drives `/rc`, a switch to bypass, a turn that ends asking which of two numbered items to fix (it waits for `soft_wait: true` on the session), then `/rc` and a switch to **Ask before edits** during that wait, asserting no "Needs your answer" and no Yes button each time. Then a `touch` that asks permission (the card, then Allow) and AskUserQuestion (the form, then Esc). Second view: the box's own API through the agent (`call`): `sessions` (`agent_state`, `soft_wait`, `ask.tool`), `sessions/ask/screen` and `/controls`.

## Gotchas

- `/rc` turns Remote Control on and prints a line. Once it is on, `/rc` opens a menu a second or so later (it asks claude.ai first), and Esc closes it without a word.
- The chat follows the box a moment after the box's API changes: wait for a card to leave the chat before typing, or Enter goes to the menu that the chat still shows.
- Esc on AskUserQuestion declines it, but Claude Code sends no hook for that, so the box keeps the wait (and the reply box stays blocked) until the next prompt. Keep that check last.
- Turn check scores the turn: a scripted "reply word for word" prompt scored under its 0.65 bar. Ask for a real decision.
- Claude Code 2.1's footer calls the default mode "manual mode".
