# Turn check's "working": an hourglass, not a live turn's spinner

Turn check (`internal/box/turncheck.go`) reads each finished turn. When it reads the work as still running without the agent (a delegated task the agent waits on, a build, a deploy, a watcher), the box marks the session `unfinished` and it counts as working (`sessionState()` in `app/src/lib/derive.ts`), so it sits in the dashboard's Working column. A handoff to a worker or session the agent won't follow ("you can close this session", "I turned off its notifications to this session") is done, not working.

## Sub-features

- `sidebar`: a worktree row whose running agents all ended their turn on their work (`allOnItsWork`) shows an hourglass in its status lane instead of the blue arc. Its tip: "Its turn ended while its work runs on: a delegated task, a build, a deploy or a watcher. It picks up by itself." A row with any live turn keeps the arc. The same goes for a folded project's header, a folded fork's chip, and Labs' two-column rows.
- `tab`: the tab (and the pane's header) of such a session shows the hourglass instead of the spinner, and its tooltip adds the same line.
- `rail`: the rail's hover card for the agent says the same line under its state.
- `dashboard`: the card says "Waiting on its work" with an hourglass (`views/dashboard/agent-card.tsx`), in the Working column. Unchanged.
- A new turn clears `unfinished` on the box (`internal/box/turns.go`), so the spinner comes back while it runs.

## How to get to it (user POV)

- Look at the sidebar: the mock's `devl / order-export` ("Add tests for the export job", `order-export-claude-3` with `unfinished: true`) has the hourglass, `devl / qa-deck` (a live Codex turn) the spinner. Hover the hourglass. Click the row and look at the tab.

## Driving it with drive.mjs

- `node .claude/skills/verify-berthd/scripts/drive.mjs turn-check-indicator --port $PORT` checks the sidebar rows and the tip, opens order-export and checks its tab and tooltip, then opens qa-deck and checks its spinner.
- `app/e2e/sidebar-turn-check.spec.ts` asserts the same; `app/e2e/dashboard-turn-check.spec.ts` the dashboard's columns.
- The verdict itself: `go test ./internal/box -run TestTurnCheckLive -v` with `BERTH_TURNCHECK_LIVE_KEY_FILE` (a box's `turncheck.key`) and `BERTH_TURNCHECK_LIVE_MODEL` asks the real provider about the fixtures (a handoff is done, a delegated task it waits on, a deploy and a background re-check are working, a scheduled check-in is parked). It skips without them, so CI never calls a provider. Models drift: run it a few times.

- The verdict on a real box, the way a real turn reaches it: `scripts/turn-check-replay.sh NAME TITLE PROMPT_FILE REPLY_FILE` makes a throwaway location and a stand-in `claude` session, writes the turn as its transcript, fires the UserPromptSubmit and Stop hooks through `berthd hook`, prints how the box then reads the session (`unfinished=True` is working, `parked=True` parked, neither done), and removes it all. It uses the berthd on PATH (`BERTHD=` for another, such as a `dev-box.sh` build) and the box's own turn check settings, so the turn goes to that provider.

## Gotchas

- An agent running in a chat-mode (structured) session has `BERTH_DRIVER=structured` set, and `berthd hook` does nothing under it. The replay script unsets it for its hooks.

- The drive opens with `?sidebar=list`. The default rail layout shows one project at a time.
- The sidebar's tips come from one shared tooltip (`RowLayer`), so they read as `[data-slot=tooltip-popup]`, not role `tooltip`. They fade in, so wait for opacity 1 before a screenshot.
