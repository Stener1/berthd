# Threads

A box says who owns a thread and what became of work sent to it (`internal/box/workthreads.go`). A key is an opaque string. A session claims it (`--claim` at its start, or `berthd work claim`) or mentions it (the id rule on its title or worktree). Only a claim makes an owner: the newest live claimant, leaving out `router-*`. Delivery records follow prompts sent with `session send --key`. There is no view in the app: the API (`/v1/work/threads`, `/v1/work/deliveries`) and the CLI are the surface, and records stay per box.

## Sub-features

- `threads-claim`: `session new --claim`, `task new --claim`, `work claim`, `work unclaim`. A claim stays on its session when grouping moves it, and survives a restart. A new session under an old name inherits nothing.
- `threads-find`: `work find --key K` gives `record`, `owner`, `claimants`, `subscribers`, each one's live state, and `provenance` (`claimed` or `mentioned`), the same from API and CLI.
- `threads-owner`: newest live claimant; routers skipped; ✅ in a title is still live; an ended or exited agent isn't; no claimant means no owner.
- `threads-subscribe`: a session naming a key subscribes, never owns. `work subscribe` and `work unsubscribe`; an unsubscribed session stays off whatever its title says.
- `threads-delivery`: `session send --key K [--source S]`, `work deliveries`, `work delivery ID`. States `held`, `typed`, `cancelled`, `undone`, `handed_back`, `lost`; a retried `--idem` is the same delivery.
- `threads-undo`: `work undo` retracts a held prompt (`retracted: true`, never typed); one already typed only reads `undone`. `work hand-back ID --why` is for a typed one.
- `threads-lost`: a session ending with a prompt held marks the delivery `lost` and publishes `work.lost`: on the next read after a kill, and within a few seconds of a session ending by itself. A delivery reads its fate from the turns ledger (`SyncHeld`), so a prompt that can't be typed reads `lost` the same way.
- `threads-held`: `session queue NAME`, `session unqueue NAME TURN`.
- `threads-resume`: `session resume NAME` for an ended structured session (a terminal one gets the route's 409); `conversation resume ID` brings a gone session back under a new name, which takes on its claims.
- `threads-events`: `work.claimed`, `work.unclaimed`, `work.subscribed`, `work.unsubscribed`, `work.delivered` (at held or typed, and again when a held one is typed), `work.cancelled`, `work.undone`, `work.handed_back`, `work.lost`, none with a prompt in them.

## How to get to it (user POV)

Nothing in the app yet. From a box: `berthd work find --key K`, `berthd work deliveries --key K`.

## Driving it

A real box and real Claude Code, in both drivers. From a `/ship` worker unset every `BERTH_*` first, and use your own `VERIFY_BOX_DIR`:

```sh
export VERIFY_BOX_DIR=/tmp/vb-threads
.claude/skills/verify-berthd/scripts/dev-box.sh up
.claude/skills/verify-berthd/scripts/threads-live.sh     # ends in PASS threads
.claude/skills/verify-berthd/scripts/dev-box.sh down
```

It runs once per dev box, because its sessions have fixed names and it kills `ask` at the end. It took about a minute at load ~30, and spends several short Claude prompts. Its `sleep` prompts only keep the agent's turn open: the last run's turn lasted seconds, not the length of the sleep, which is enough to hold a prompt. Unit tests: `go test ./internal/box -run 'Title|Joining|FollowDiss|Grouping|TheOwner|AClaim|Subscrib|Unclaim|Carry|OlderFile|AFileThis|ClaimsAndDel|ADelivery|UndoAnd|DeliveriesFilter|ThreadKeys'`.

## Known limits

Resuming a conversation within about 2 seconds of killing its session gives a dead session with a 200: the old host still holds the socket. The script waits for the host to go.

## Not covered live

A session ending by itself with a prompt held (`TestADeliveryReadsItsFateFromTheLedger`, "gone unannounced"), an inbox prompt that can't be typed (same test), and `task new --claim` (`TestClaimAtStartAndSendWithAKey`; it reaches the same `claimNew` as `session new`).
