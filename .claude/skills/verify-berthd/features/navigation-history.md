# Back and forward

The app keeps a trail of the places you've been, as a browser does (`app/src/lib/nav-history.ts`, its pure part `nav-history-core.ts`). A place is a view (the Agent Dashboard, Review, Worktrees, Settings and its section, a plugin's screen, …), and in the workspace the worktree in front (none is Home; a box's home terminals count) with its tab showing. Switching tabs changes the place you're on rather than adding one. The trail follows the two stores that say where you are (`useStore.view`, `useWorkspaces.current`), so nothing that moves you has to tell it. It holds 50 places and lasts until the window closes.

## Sub-features

- `history-arrows`: Back and Forward in the window strip at the sidebar's top (rail and list layouts, `components/sidebar/back-forward.tsx`), before the bell and the fold. Disabled with nowhere to go.
- `history-keys`: ⌘[ and ⌘] (Ctrl+[ and Ctrl+] in the Linux app; in a terminal there they stay the shell's, Ctrl+Shift+[ and ] are the app's). In a File tab's editor they stay outdent and indent. Also Go → Back and Go → Forward in the Mac app's menu bar, ⌘K's Back and Forward, and the Keyboard shortcuts sheet (all from `lib/shortcuts.json`).
- `history-mouse`: a mouse's back (button 3) and forward (4) buttons, on `mouseup`.
- `history-skip`: Back and Forward pass over a worktree that has gone (archived or removed) and over the place you're on already.
- `history-gone`: when the worktree in front is archived or removed and that would leave you on Home, you go back to the last place before it that is still there instead (the Agent Dashboard, say). With Labs tab groups, when another group comes forward it stays in front, as before.
- `dashboard-scroll`: the Agent Dashboard keeps where it was scrolled to until the window closes, so Back (or ⌘J) finds the card you left from.

## How to get to it (user POV)

- Open the Agent Dashboard (⌘J), click a card, then the ← at the sidebar's top, or ⌘[.
- Close the session's tab, then **Archive this worktree…** on the launcher: you land on the dashboard.

## Driving it with drive.mjs

- `node .claude/skills/verify-berthd/scripts/drive.mjs navigation-history --port $PORT` starts on Home (both arrows disabled), opens the dashboard with ⌘J and scrolls it down, clicks "Speed up product search" (worktree `search-perf` on `devl`), then: Back to the dashboard at the same `scrollTop`, Forward to the session, ⌘[ and ⌘] the same, mouse buttons 3 and 4 the same, ⌘W to close its tab, **Archive this worktree…** and **Archive** in the confirm: it asserts the dashboard shows (not Home), the card is gone and Forward is disabled.
- With Labs on (tab groups, the harbour Home): a two-line scenario that re-exports `run` from `navigation-history.mjs` with `export const params = { labs: "1" }` passes the same steps.
- Hooks: `history-back`, `history-forward` (the arrows, in a group named "History"), `dashboard-board` (the board's scroller). The worktree in front is the page's `h1` (App.tsx `WorkspaceHeading`, or the launcher's title).

## Gotchas

- Playwright's mouse has no back or forward button: the scenario dispatches `mouseup` with `button` 3 and 4 on the window, which is what the handler listens for. A real mouse in the Mac app (WKWebView) needs a person.
- The Mac app's menu items and the ⌘[ that reaches a File tab's editor while the menu holds the key can't be shown on the mock; `hooks/use-shortcuts.ts` hands the key to the editor when it has the keyboard.
- The folded sidebar, zen and Settings have no arrows; the keys, the mouse, ⌘K and the menu still work there.
- The mock's `shop` project has an archive script: the box answers at once, the launcher shows Archiving…, and the worktree goes (and you go back) 1.5s later, when `worktree.removed` arrives. Remove worktree, from the sidebar's ⋯, goes the same way, through `forgetWorktree`.
