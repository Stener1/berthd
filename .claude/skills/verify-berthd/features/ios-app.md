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
- **Session:** the screen text, quick keys (Enter, Esc, arrows, Tab, 1-4, Ctrl-C) and
  a message box. `agent_waiting` is explained in words. URLs open in Safari.
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

## Prove it on a box

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
