# Issues

A screen with the open GitHub issues of your projects, and an agent on any
of them in one step. On by default; it sits under **More** in the sidebar
(drag it out to keep it in view), and in the palette as "Show issues" and
"Refresh issues".

- **One project or all of them.** The picker lists every project whose
  origin is on GitHub (one repository across boxes is one project). It
  starts on the project of the worktree in front, and remembers your choice.
- **Fast filters.** Search (`/`) by title, number, author, label or
  assignee; All / Mine / Unassigned ("mine" is whoever gh is logged in as on
  the box); label chips that narrow together; sort by recently updated,
  newest or most discussed.
- **The thread.** The issue's body and its last 50 comments as Markdown,
  pull requests that will close it, and who's assigned.
- **Start an agent** (`S`). A sheet prefilled from the issue: the worktree
  name the box's resolver gives it (`issue-<n>-<slug-of-title>`), the box
  (the project's default box, or another one it's on), the agent, and the
  first prompt with the issue's body in it. Everything can be changed before
  you start. The agent opens in a new tab of its worktree, or a toast offers
  to open it.
- **Live state.** From then on the issue's row shows its agent: running,
  needs you, done, or only the worktree left. Click it to open the worktree.
  Any worktree whose branch or name is `issue-<n>` or `issue-<n>-…` counts,
  however it was made.
- **Batches.** Tick issues (`X`, or the checkbox on hover) and start one
  agent per issue. They run one after another, each with its own worktree
  and the same prompt template, and the sheet ends with a summary. Issues
  that already have a worktree are skipped.
- **Comment.** Comments are posted with gh on the project's box, as whoever
  is logged in there. Every comment is confirmed first.

Keys: `J`/`K` move, `X` ticks, `S` starts (the ticked issues, or the one
open), `O` opens it on GitHub, `/` searches, `Esc` clears the ticks.

## Linear

The switch at the top right turns the screen to Linear, once a personal API
key is in **Settings → Integrations** (`docs/guides/linear.mdx`):

- **Assigned to me, or a team.** Up to 100 issues that are not done or
  cancelled; search, All / Mine / Unassigned within a team, label chips,
  and sort by recently updated, newest or priority.
- **The issue.** State, priority, labels, assignee, what Linear linked to
  it, the description and its last 50 comments.
- **Start an agent.** A Linear issue names no repository, so the first
  start asks which project; the choice is kept. The worktree is named for
  the issue (`web-412-…`) and takes the branch Linear suggests, so Linear
  links the pull request by itself. Any worktree whose branch or name
  carries the identifier counts as the issue's.
- **Home widget.** "Linear": what is assigned to you, the most urgent first.

The plugin never holds the key. It reads through `berth.linear`, which the
laptop agent answers (`internal/agent/linear.go`): the key is in a file
only the person can read, and the app's window may only talk to the agent.
`src/linear.ts` is the part with no React in it (names, the prompt, which
worktrees are an issue's) and is tested in
`app/src/lib/linear-issues.test.ts`.

## Where the issues come from

Nothing here talks to GitHub from the laptop. The plugin runs `gh` on a box
that has the repository (the project's default box when it's online), in
its main checkout, through `orchestrate.exec`:

- the list is one `gh api graphql` query for the 100 most recently updated
  open issues, with labels, assignees, comment counts and closing pull
  requests, shaped by `--jq` on the box so it fits well inside exec's 64 KB;
- an issue's thread is a second query, with long bodies cut short on the box;
- a comment is `gh issue comment --body-file -`, with the text passed as
  base64 so the shell never reads it.

When that can't work, the screen says why: gh missing or logged out on the
box, issues turned off for the repository, no box online, or an origin that
isn't on GitHub.

Avatars are a login's initial on a colour; no images are fetched.

## What it shows of the SDK

- `addScreen`, `addSidebarItem`, `addCommand`, `openUrl`, `openWorktree`,
  `notify`, `useStorage` (project, filters, last agent, prompt template).
- `useProjects()`: projects across boxes and their default box, added to
  the SDK for this plugin.
- `useSessions(box)` for each box the projects are on, to follow agents.
- `orchestrate.exec` to run gh where the code is.
- `api.request` for the box's resolver (`POST locations/{name}/resolve`),
  `api.info` for a box's agents, and `api.createTask` with `open: "tab"`
  (now part of the SDK's `TaskRequest`).
- From `@berth/plugin/ui`: `Sheet`, `Checkbox`, and the app's own
  `PickOne`, `AgentPicker` and `AgentIcon`, added to the kit so a plugin's
  pickers look like the app's. Also `AlertDialog` for the comment confirm,
  `Menu`, `Tooltip`, `Kbd`, `Skeleton`, `Textarea`.
- A small Markdown renderer (`src/markdown.tsx`) that builds React elements
  and never sets HTML: an issue's text can only ever be text, and only
  http(s) links become links.

In mock mode (`?mock=1`) the gh calls are answered by
`app/src/lib/mock-issues.ts`, with invented issues; comments go nowhere.
