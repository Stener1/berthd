# The iPhone app (`ios/`)

A SwiftUI client of the phone API (`/phone/v1`, port 1379). It is not part of the
web app and not driven by `drive.mjs`: it runs in an iOS simulator and is driven
with `simctl` and Maestro. Unit tests are `make ios-test`.

## What a person sees

- **Pairing:** first launch shows *Scan the pairing code* and a paste field. One
  pairing link pairs every box. A token that stops working shows *Re-pair this box*.
- **Agents (board):** every agent session of every box in one list, waiting first
  (*Needs you*, then *Working*, *Done*, *Ready*, *Exited*). A box that is off shows
  *Offline* at the top and the rest keep listing. Pull to refresh; it polls every 4 s.
- **Session:** a chat (box lists `transcript` in `GET /phone/v1/info`): bubbles for
  your prompts and the agent's replies, tool groups as small cards, published pages as
  links (Safari), and a waiting agent's ask as a card at the foot: a permission or plan
  has a button per `ask.choices` (sent as the key, no Enter) and a words field; an
  AskUserQuestion form has its options plus *Other*, answered through
  `POST /phone/v1/sessions/{name}/answer` (a lone single-choice question is answered by
  tapping the option; several questions or a multiple choice wait for *Send answers*).
  The *Chat | Screen* toggle switches to the screen text, quick keys (Enter, Esc, arrows,
  Tab, 1-4, Ctrl-C) and the same message box. Without the capability, or with
  `source: none` (terminal-only agent), only the screen shows. `agent_waiting` is
  explained in words. URLs open in Safari.
- **Runs:** runs of every box; a run at a gate shows its candidates, Approve/Reject
  (or pick one) and *Cancel run*.
- **Boxes:** rename, remove (forgets the token), add.

## Drive it

Needs the simulator booted, a pairing link in a file only you can read
(`umask 077`, `/tmp/shipyard-pair.txt`), and **always** `--device <udid>`: Maestro
without it picks any booted simulator, including other people's.

```sh
.claude/skills/verify-berthd/scripts/ios-drive.sh pair|session|key|gate
```

The script builds nothing; run `make ios-build` first. Notes that cost time:

- A custom-scheme `simctl openurl` raises a system "Open in Shipyard?" prompt that
  neither Maestro nor `simctl` can tap. Debug builds therefore read a launch
  argument instead: `simctl launch $SIM dev.shipyard.phone -ShipyardOpenURL
  "shipyard://pair#pair=..."` pairs, `shipyard://box/<box>/session/<name>` opens a
  session (the same path a notification tap takes).
- Maestro text-typing works into the message box but not into the pairing field.
- Cards are one accessibility element: match with `.*title.*`.
- Do not read the screen of a `Reject` by text; tap its point, then read the run back
  with `berthd run get RUN` on the box.

## Prove it on a box (the chat)

macOS has no `dev-box.sh` (it needs `ss`, `/proc`); run a second berthd by hand, apart
from the real one (which owns 1379): `BERTH_HOME=/tmp/x/box-home BERTH_USER_DIR=/tmp/x/user
TMUX_TMPDIR=/tmp/x/tmux BERTH_PHONE_PORT=2379 berthd serve --listen 127.0.0.1:7545`, with
`{"enabled":true,"token":"..."}` in `/tmp/x/box-home/box/phone.json` first, and check
`~/.claude/settings.json` is unchanged afterwards. Turn structured agents on over the
socket (`PUT /v1/agents/defaults {"structured":true}` on `box-home/box/berthd.sock`), add
a throwaway repo with `berthd location add`, then `berthd session new LOC --name chat-ask
--agent claude --prompt "Use AskUserQuestion to ask 'Which rounding mode?' with options
'Half up' and 'Half even', then reply ANSWER=<my answer>"`. Pair the simulator with a link
for `http://<tailnet ip>:2379`, run `ios-drive.sh chat BOX NAME`, then read the answer back
with `GET /phone/v1/sessions/NAME/transcript` (the agent's `ANSWER=Half even`). Clean up:
kill your berthd by PID, `session kill`, delete `/tmp/x`, uninstall the app.

## Prove it on a box (screen, runs)

1. A throwaway worktree and agent: `berthd worktree new LOC/ios-verify-tmp --no-setup`,
   `berthd session new LOC/ios-verify-tmp --name ios-verify-session --agent claude
   --prompt "Reply with just the word PONG" --title "ios-verify test session"`.
2. A run at a gate: `berthd run start --template attempts --param location=LOC
   --param name=iosverify --param 'attempts=["claude","claude"]' --param prompt=...`.
3. In the app: the card's title equals `berthd sessions`' TITLE; send a message
   (`berthd session turns` then shows a turn FROM `phone`); press Esc (`^[` appears
   in `berthd session screen`); decide the gate (`berthd run get` shows it).
4. Clean up: `berthd worktree rm LOC/ios-verify-tmp --force`, kill the session,
   `simctl uninstall $SIM dev.shipyard.phone` (drops the Keychain tokens), delete the
   pairing file and `ios/build`.
