import type { LinearIssue, Project, Session } from "@berth/plugin";

import type { Run } from "./store";

// What the Linear side of Issues works out for itself: the worktree an issue
// gets, which worktrees are already for it, and its first prompt.

const slug = (s: string) =>
  s
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");

// worktreeOf names an issue's worktree after it ("web-12-checkout-charges-
// twice") and gives it the branch Linear suggests, so Linear links the pull
// request and moves the issue along by itself. Without a suggestion the
// branch is the worktree's name, which still carries the identifier.
export function worktreeOf(issue: Pick<LinearIssue, "identifier" | "title" | "branch">): { name: string; branch: string } {
  const id = issue.identifier.toLowerCase();
  const rest = slug(issue.title).slice(0, 36).replace(/-+$/, "");
  const name = rest ? `${id}-${rest}` : id;
  return { name, branch: issue.branch?.trim() || name };
}

// identifierIn finds a Linear identifier ("web-12", any case) in a branch or
// worktree name, as its own word: "robin/web-12-fix" and "WEB-12" have one,
// "web-123" is not WEB-12, and "issue-12" is GitHub's.
export function identifierIn(name: string | undefined, teams: string[]): string | undefined {
  if (!name || !teams.length) return undefined;
  const m = new RegExp(`(?:^|[/_-])(${teams.map((t) => t.toLowerCase()).join("|")})-(\\d+)(?=$|[/_-])`, "i").exec(name);
  return m ? `${m[1].toUpperCase()}-${m[2]}` : undefined;
}

const RANK: Record<string, number> = { waiting: 0, running: 1, finished: 2, idle: 3 };
const rank = (r: Run) => (r.session ? (RANK[r.session.agent_state ?? ""] ?? 4) : 5);

// runsByIdentifier finds every worktree made for a Linear issue, in any
// project, keyed by identifier, the liveliest first.
export function runsByIdentifier(projects: Project[], sessions: Record<string, Session[] | undefined>, teams: string[]): Map<string, Run[]> {
  const out = new Map<string, Run[]>();
  for (const p of projects) {
    for (const m of p.members) {
      for (const wt of m.location.worktrees ?? []) {
        if (wt.main) continue;
        const id = identifierIn(wt.branch, teams) ?? identifierIn(wt.name, teams);
        if (!id) continue;
        const loc = `${m.location.name}/${wt.name}`;
        const session = (sessions[m.box] ?? [])
          .filter((s) => s.agent && !s.exited && (s.location === loc || s.dir === wt.path))
          .sort((a, b) => b.created.localeCompare(a.created))[0];
        out.set(id, [...(out.get(id) ?? []), { box: m.box, location: m.location.name, worktree: wt.name, path: wt.path, branch: wt.branch, session }]);
      }
    }
  }
  for (const runs of out.values()) runs.sort((a, b) => rank(a) - rank(b));
  return out;
}

// What an agent is first told: {{id}}, {{title}}, {{body}}, {{url}} and
// {{team}} are filled in from the issue.
export const DEFAULT_TEMPLATE = "Work on {{id}}: {{title}}\n\n{{url}}\n\n{{body}}\n\nOpen a PR when done, with {{id}} in its title so Linear links it to the issue.";

export const TEMPLATE_VARIABLES = ["id", "title", "body", "url", "team"];

const BODY_LIMIT = 8000;

// Issue templates leave HTML comments in bodies; nobody wants to read them.
const stripComments = (s: string) => s.replace(/<!--[\s\S]*?-->/g, "");

export function renderPrompt(template: string, issue: Pick<LinearIssue, "identifier" | "title" | "url" | "team"> & { description?: string }): string {
  let body = stripComments(issue.description ?? "").trim();
  if (body.length > BODY_LIMIT) body = `${body.slice(0, BODY_LIMIT).trimEnd()}\n\n(The issue goes on; read the rest at ${issue.url}.)`;
  const vars: Record<string, string> = { id: issue.identifier, title: issue.title, body, url: issue.url, team: issue.team.name };
  return template
    .replace(/\{\{\s*(\w+)\s*\}\}/g, (whole, k: string) => vars[k] ?? whole)
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

// Urgent first, then high, medium and low; no priority last.
export const priorityRank = (p: number) => (p === 0 ? 5 : p);

export const PRIORITY_ICON: Record<number, string> = { 1: "CircleAlert", 2: "SignalHigh", 3: "SignalMedium", 4: "SignalLow" };

// The glyph for a workflow state's type, drawn in the state's own colour.
export const STATE_ICON: Record<string, string> = { triage: "CircleDotDashed", backlog: "CircleDashed", unstarted: "Circle", started: "CircleDot" };
