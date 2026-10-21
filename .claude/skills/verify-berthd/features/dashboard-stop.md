# Stopping agents from the Agent Dashboard

Stopping ends an agent's session on its box (`app/src/views/dashboard/stop.tsx`). Its worktree and files stay unless the bulk dialog is told to archive them. The archive goes through `removeWorktreeOnBox(…, "archive")` (`lib/remove-worktree.ts`), the same path as the sidebar's Archive… and the Worktrees table's bulk archive. `lib/archive-after-stop.ts` decides which worktrees to archive and which to keep (unit tests next to it). The box refuses a worktree with uncommitted or untracked files before it stops anything or runs the archive script (`dropWorktree` in `internal/box/api.go`), in git's words, so every Archive path keeps dirty work up front.

## Sub-features

- `stop-one`: a card's ⋯ → **Stop agent…** (asks first) and **Stop and remove worktree** (removes it without asking; git keeps a dirty one, and a toast offers Remove anyway).
- `stop-picked`: **Select**, click cards, **Stop N…** (or ⌫). Also a column's ⋯ → **Stop all N in <column>…**. The dialog lists them by box, each with a checkbox. A busy one says "working" or "needs you".
- `cleanup`: **Clean up…** in the header, or a Done/Ready column's ⋯ → **Clean up idle agents…**. It ticks the ones idle longer than 1 hour / 4 hours / 1 day (remembered in `berth.dashboard.cleanupAfter`). A parked agent is listed but never ticked for you.
- `stop-archive`: in both bulk dialogs, **Archive their worktrees where it's safe**. It shows only when a ticked row has a worktree that isn't the main checkout, starts unticked, and is remembered (`berth.dashboard.stopArchives`). Ticked, the agents stop first. Then each of their worktrees is archived once: one at a time on each box, the boxes side by side. A row reads `Stopped · Archived; its branch is kept`, `Stopped · Archiving: its archive script runs, then it goes` (the location has an archive script), `Stopped · Kept: uncommitted changes` (git refused), `Stopped · Kept: another agent is still running there` (an agent session outside the batch is live in it or in a folder under it, by the box's own fresh listing; plain shells and services don't count), `Stopped · Kept: an agent there didn't stop`, `Stopped · Kept: couldn't check for other agents on its box` (its box didn't answer the listing), or `Stopped · Couldn't archive: …`. A kept or failed one holds the dialog open on "Stopped N agents · archived/archiving M worktrees". Otherwise it closes with a toast saying the same.

## How to get to it (user POV)

- ⌘J opens the Agent Dashboard. **Select**, click cards, then **Stop N…** in the bar at the bottom.

## Driving it with drive.mjs

- `node .claude/skills/verify-berthd/scripts/drive.mjs dashboard-stop-archive --port $PORT` picks four agents. It covers each outcome: search-perf archives through shop's archive script, ci-flake on gpu archives at once, checkout-fix is kept for uncommitted changes, and order-export is kept because two more agents run there. It ticks the box, stops them, checks each row, then checks the Worktrees table (two gone, two still there), and that **Clean up…** opens with the box ticked.
- On a real box: `scripts/dev-box.sh up`, then `scripts/archive-dirty-live.sh` archives a dirty and a clean worktree of acme-shop under an archive script, through the agent's API. The dirty one is refused at once (400, "contains modified or untracked files"), and its file stays while the script doesn't run. The clean one answers 202, the script runs in it, and it goes. `dev-box.sh down` afterwards.
- `app/e2e/dashboard-stop-archive.spec.ts` asserts the same, plus unticked (nothing archived, the old copy and toast) and a main checkout alone (no box).

## Gotchas

- With the default prefs the sidebar is the rail layout, which shows one project at a time. Read the outcome in the Worktrees table, or open with `labs: false` prefs, as the e2e does, to use the sidebar's `worktree-row`s.
- In the mock, checkout-fix's agent is in Needs you, not Done: it is the only dirty worktree in the fixtures.
- The mock's archive script takes 1.5s, so search-perf leaves the table a moment after the dialog says Archiving.
- axe over the open dialog flags the board's cards behind it (`.opacity-70` meta text), not the dialog.
