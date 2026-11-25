# A super-agent layer for Shipyard

Status: vision and step plan for this fork, decided 2026-11-02 and 2026-11-03. Fork-only: this branch and the work it describes don't go upstream unless the operator says so later.

## The problem

Agents got good enough that the limit moved. One person now runs dozens of sessions at once, and the bottleneck is their attention.

Measured on one operator's two boxes on 2026-11-02:

- 80 live agent sessions. 23 waiting on the person, and 13 of the 26 cards titled as questions are 3 or more days old.
- 17 cards marked done are still open, because the only automatic close-out needs a finished tracker issue named in the worktree.
- 32 of the 80 sessions name no tracker issue at all.

Sessions pile up because nothing drives them to closed. Boards sort them; nothing makes them leave.

The operator put the real cost this way: they keep an inventory of the work in their head and jump to what they remember. Whatever isn't in their thoughts right then goes unanswered. So a better list isn't the fix, because a list still relies on memory to go and look. **The system has to bring every open item to the person, and drive it to closed.**

Two more things they said shape the design:

- **Sessions stay small.** Separate sessions keep context and usage down. Grouping is visual; nothing merges sessions.
- **Not everything is equal right now.** Three to five areas matter most at any moment, and those change. A presentation or a budget beats a side project this week, not forever.

## The reference: DeepSeek's super-agent

[Exploring Super-agent Design](https://x.com/deepseekharness/status/2116857257245987236) (DeepSeek Harness, 2026-11-01) makes the same diagnosis: a person can follow 3 to 5 active sessions, so multi-tasking is limited by human attention, and "sessions should be an implementation detail of the system, not a mental burden on its users". Its [prototype](https://github.com/deepseek-ai/dsh-superagent-prototype) (MIT, research code) adds a domain model worth reading. Its results are from a synthetic workload and the authors call them indicative only.

The four traits it aims for: continuous (it always feels like one agent), productive (takes the next instruction before the last task ends), proactive (acts on events, quiet otherwise), persistent (always on, recovers by itself).

### The five modules, mapped to Shipyard

| Module | What it says | What Shipyard has | Gap |
|---|---|---|---|
| **1. Two levels** | A long-lived router handles people and dispatch and never executes (it can run with no Bash tool). Workers have isolated context and full tools, and report to the router. | Sessions are workers. A per-drop routing session exists privately, but it isn't long-lived. | A long-lived router |
| **2. Event-driven** | Code filters first (fast path), a model judges candidates (slow path). Events on one PR, topic or email chain form a Thread. A worker subscribes, so follow-ups pass straight through. The router handles only new threads and cross-task coordination. IDs link objects so context can be recalled hop by hop (Chain of Recall). Speculative work is delivered only if useful. | The box journal, webhook triggers and a `pr_merged` trigger. One-owner rules for some private work types. | Threads, subscriptions, recall, filters |
| **3. Full-duplex** | `start()` returns "Started" and the result arrives later as an event. Output suits each channel: short in chat, structured on PRs. | Mostly there: MCP tools return at once and report back with a notification. Question cards and PR bodies differ in style. | Per-channel norms |
| **4. Compute, state, environment** | Stateless compute, sessions in a database, a thin daemon beside the real environment. | The daemon keeps environments where they are. | Compute and state still live on the box. See Non-goals. |
| **5. Watchdog** | An independent session outside the system, on a schedule, that judges progress and drift as well as liveness. You can ask it why something failed. | A watchdog for unattended night workers only, kept privately. | One for day sessions |

### What the prototype adds

- **A Ticket is the unit of work, not a session.** It has its own state, and a session is just who's assigned. Closing a session doesn't end the work, and a new message resumes it. This is the work record below.
- **Several tickets may share a thread.** "At most one live ticket per thread" is on its rejected list, which matches keeping sessions small.
- **Looser links add context and never route.** A message attaches to existing work only when it points there explicitly.
- **Waiting for something is an event source.** A date or a merged PR is a source like any other.
- **Don't infer "waiting on a person" from free text.** Workers report through a structured field.
- **Its judgment step holds no state.** It gets a snapshot each time.
- **Start narrow.** Few settings; a label says whom a row waits for and what it needs; a count equals the length of the list it links to.

### What the article leaves open, and where this goes further

- **How to present work to people** (section 8.2). The prototype has a tabbed supervisor panel. The walk-through queue and generated cards below are this project's own design, so there's no blueprint to copy.
- **Long-term memory** (section 8.1) is called the main bottleneck. See The router.
- **Security** (section 8.4) gets its own section below.
- **Cost.** In their test the design used 23.2M tokens against 6 to 11M for a single session. Pass-through saved about 37% of tokens (31.8M to 23.2M) and didn't change task time (12.4 vs 12.6 minutes). So threads save tokens; their test didn't measure duplicates, which is the reason to build them here.

Caution on the figures: this document relies on the article's text and the results table. The copies of the figures on translation sites are redrawn, and some are wrong.

## Principles

1. **Everything comes to you.** Nothing depends on remembering to look.
2. **Unblock first.** Decisions come before manual tasks, because one answer can free a whole session.
3. **Everything leaves.** Each item ends closed, parked or waiting on something named, and the system pushes it there.
4. **Sessions are plumbing.** The unit you see is a piece of work.
5. **Spend attention carefully.** Each card, count and setting must be worth more than the attention it costs.
6. **Code before models.** Use a model only where a rule can't decide.

## Needs me

A menu item in the fork. Open it and one piece of work fills the screen. Answer or close it and the next comes up. A header says "7 left, about 9 min". A side list allows jumping, but you never need it.

**Order:**

1. Pinned decisions
2. Other decisions
3. Pinned tasks for you
4. Other tasks for you
5. Results to read
6. Close-out deck

Within a pass, longest-unseen first, so what you've forgotten surfaces. A new pinned question becomes the next card and never interrupts the current one.

The decision and task passes are the existing sub-states of Needs you ("Needs a decision", "Needs you to act").

**What enters:** agents waiting, sessions that ended on a question or a block without a form, and results not yet seen. Queued and parked sessions stay out. A card without a form shows the agent's last message, a reply box and Close.

**Pins:** pin a repo, a tracker project or one piece of work. A pin stays until removed, or until a pinned piece of work closes. More than 5 only warns.

**Getting out of the way:**

- **Read:** seen, nothing more to do.
- **Not now:** comes back tomorrow or on a date.
- **Waiting on:** who or what it waits for, and a check-back date.
- **Close:** ends the session. The transcript stays, so it can resume.

**Groups:** sessions on one piece of work appear together as a tree, with the orchestrator on top. A worker's question shows in its group and is answered directly, with no model in between. This departs from the article's "workers never talk to the user": at this stage the operator wants direct answers, and the UI still gives them one place to go. Group and Ungroup replace any idea of merging sessions.

**How groups form:** rules first (issue ids including ranges, the task file's session list, a shared worktree, the recorded parent session). Then a cheap model reads session digests and suggests groups as a one-tap card. Nothing groups without that tap.

## The work record

Two layers:

- **The issue tracker holds the plan:** initiative, project, milestone, issue.
- **A small record on the box holds the running work:** title, its sessions, status, pin, Not now, Waiting on, and a link to an issue and project when there is one.

Quick jobs don't need an issue, so they don't clutter the tracker, and reads stay local and fast. The record outlives its sessions. It starts as Needs me's store and becomes the thread registry in step 2.

New sessions also record which session started them. The box doesn't know this today, so existing sessions can only be grouped by rule.

## Close-out

A scheduled sweep on every box:

- **Closes by itself:** marked done, finished, nothing unpushed, quiet for 24 hours.
- **Everything else stale for 3 or more days** goes in the deck: Close, Park, Still needed.

Staleness is measured from the last human visit, because a session's `state_since` is reset by events other than activity.

## Generated cards

**Feasible, and mostly built.** The app already draws charts, tables, Mermaid and notes, runs pages in a sandbox with no network and no access to Shipyard, and has interactive question and secret-request cards and a before/after slider. What's missing is one format composing them, and a safe way for buttons to act.

**The approaches:** fixed templates; declarative specs composed from a catalog you control (A2UI, json-render); open-ended generated pages (HTML, MCP Apps).

**Decided: a block catalog, plus a sandboxed page as a read-only block.** A card is:

```jsonc
{
  "kind": "pr-approval",             // decision | task | result | close-out | group | plan | pr-approval | credentials | ui-edit
  "title": "Merge the checkout copy change?",
  "why": "It changes what customers read",
  "blocks": [
    { "type": "before-after", "before": "…", "after": "…" },
    { "type": "checks", "items": [{ "name": "staging verified", "state": "passed" }] }
  ],
  "actions": ["approve", "ask-for-changes", "not-now"]
}
```

This sketch isn't the final schema; the worker settles it.

**Blocks:** text, question form, table, chart, image, before/after, diff summary, checks, checklist of steps for you, command to copy, secret field, link, and a read-only sandboxed page.

**Actions are a fixed list of verbs, and the app decides what each does:** answer, reply, done, approve, ask for changes, read, not now, waiting on, close. Agents can't supply their own URLs or commands for a button, and links show their real host. **Approve tells the worker, and the worker merges under its usual gate**, so a card never touches production itself. The catalog is the safety model: agent text is rendered as text, and an agent can only draw what the catalog contains.

**Our own small schema, not an open standard.** Cards arrive whole, so A2UI's streaming isn't needed, it's still moving to v1.0, and neither it nor json-render knows these actions. The shape stays close to A2UI's so a later move stays cheap.

**The journey:**

| Step | Card |
|---|---|
| Intake | Question form, the model all of this comes from |
| Plan review | Text, decisions, approve |
| Task for you, credentials | Checklist, deep links, secret field, done |
| PR approval | Before/after, checks, diff summary, approve |
| UI edit | Before/after, preview link, ask for changes |
| Grouping | Suggested group, accept or reject |
| Close-out | One-line summary, close, park, still needed |

**Who composes them, in order:**

1. Shipyard, from known data (first slice)
2. Workers, through the report at the stop gate (step 2)
3. The router, including a combined view for right now (later)

## The stop gate

Inferring state from text misses things. So a worker reports when it tries to end its turn, with its state (`progress`, `waiting`, `ready`, `blocked`, `failed`, `dropped`), a summary and what it needs, optionally carrying a card.

A Claude Code Stop hook can refuse the end of a turn until a report exists; tested 2026-11-03. Chat-mode sessions and Codex are still untested.

- **It blocks twice, then lets go** and shows "didn't report", so a confused agent can't loop and burn usage. Inference stays as the fallback.
- An open question form counts as a report.
- Reporting before ending costs nothing extra, so taught agents are never blocked.
- The report becomes a journal event, so hooks and webhooks can subscribe.

## The router

It reads new drops and events, starts or assigns workers, and fills the Needs me queue. It never executes: no Bash tool. From day one it may start and assign, each with a visible Undo. It never closes a session, merges, deploys or answers for you.

**The hard problem is continuity without runaway usage.** The operator wants what a good always-on assistant feels like: it remembers across days, there's one place to talk, it comes to you, and it's fast on the phone. A continuous Claude Code session is expensive and compacts its way into losing detail, and the article doesn't solve this either.

**Three candidates, decided by a bake-off at step 4:**

| Candidate | How | For | Against |
|---|---|---|---|
| Fresh small wakes | One isolated call of about 15K tokens per event batch: fixed rules, snapshot of open work, last exchanges, new events, lookup tools. Bills to the subscription through the CLI. | Nothing accumulates, so nothing compacts. Cheapest. | May feel less continuous. Lookup tools add cost. |
| Rolling session | A real Sonnet session, restarted with a handoff at about 150K tokens. | Most capable and continuous. | Highest usage, especially when events are sparse. A restart can drop detail. |
| External assistant | An always-on assistant bot as the driver. | Already feels right to the operator. | Context leaves the boxes, its reach to tools and boxes is unproven, and it may be replaced. |

Claude Duet is a useful comparison (one thread, a morning brief, messages only when needed), but it's a US-only preview and checks apps every few hours, so it isn't a candidate yet.

**One contract makes them swappable:** read the board and events, output card specs and start or assign workers. Any driver plugs in, and the wall of text goes away for all of them, because Needs me renders the cards.

**The bake-off** replays two weeks of real events through each. The operator judges the decisions; usage is compared. They're scored on the four traits above.

Rough list-price estimates per wake: about $0.40 for a 150K context with an expired cache on Sonnet 5.5, $0.035 for a fresh 15K on Sonnet 5.5, $0.002 for a fresh 15K on Haiku 5.5. Real numbers come from the bake-off.

**Load isn't a concern:** about 300 agent state events a day, normally under 7 a minute, against the article's single router tested at about 40 a minute.

## Security

Events bring in text other people wrote, like PR comments and email, and workers run with broad access. A prompt injection must not be able to start work or reach production.

- Only the operator and listed sources can start work. Everything else is data.
- The router has no Bash and can't close, merge, deploy or answer.
- Agents never see secrets; they ask through the secret-request card and use them through `berthd secret exec`.
- Card text is rendered as text, and buttons are fixed verbs.
- Speculative work starts in a mode that only records what it would have done.

## Steps

The order is deliberate: **relief first, then foundations shaped by real use.** Each step's issues are chained, so the next starts when its blockers ship. The operator is only needed for decisions and blockers.

| Step | What | Why here |
|---|---|---|
| 0 | The chain that runs the project | So nothing needs a nudge |
| 1 | **Needs me**: work records, parent links, card format and renderer, walk-through queue, pins. **Close-out sweep.** **Group suggestions.** | Relief on today's data, and it defines what a thread is |
| 2 | **Threads and ownership:** an exact id or PR joins the owner (with Undo, and the owner can hand back), a likely match asks. **Stop gate and reports**, and workers compose cards. | Stops duplicates; a router stays cheap only once this exists |
| 3 | **Events on threads:** GitHub, CI and tracker events, tagged by thread, with a filter and source allowlist. Waits wake on events, like a PR merging. | What a router reads |
| 4 | **Router:** contract, bake-off, then live with Undo | Needs threads and events |
| 5 | **Combined view:** the router composes a view for right now | Needs a router |
| 6 | **Watchdog** for day sessions: stalled, drifting, finished. Then **filters, speculative work and Chain of Recall.** | Last, on stable ground |
| Later | **Phone:** Needs me on the phone, with short-reply norms | After living with the desktop view |

### What each step reuses

| Today | Becomes |
|---|---|
| Intake's question cards | The model for cards, on the app's own `QuestionCard`, with key choices and issue chips ported in |
| A one-card deck with a time estimate | The walk-through's shape |
| A plugin's last-visit tracking | The stale clock |
| Session digests from the card titler | Group suggestions and the router's snapshot |
| Task files with `waiting_on` and `check_back` | A work record's project and Waiting on |
| Single-owner rules for incidents and ideas | Thread ownership |
| The per-drop router | Grows into step 4, keeping its session types and Undo card |

## Relation to unattended night work

It stays. Night work runs queued items with nobody watching; this layer is how the operator works by day. They meet in two places: the router can queue night work, and questions parked overnight appear in Needs me.

## Non-goals, for now

- **Moving compute and state off the box** (module 4's first half). A box going down still stops its agents. This is a real gap, but it's the most expensive module and doesn't touch the attention problem. Revisit after step 6.
- **Agent-to-agent work across people** (section 8.3).
- **Merging sessions.**
- **Hard caps on starting work.** Pressure comes from the queue and close-out.

## How we'll know it works

Agents decide the exact measures when building, starting from:

- Nothing asks unanswered for 3 or more days without a decision to wait.
- A done session is closed within a day.
- Fewer live sessions, trending down.
- Fewer jumps through sessions before answering the one the operator meant to.
- No duplicate workers on one issue or PR.
