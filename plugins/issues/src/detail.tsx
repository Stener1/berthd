import type { BerthPluginContext, Project } from "@berth/plugin";
import {
  AgentIcon,
  AlertDialog,
  AlertDialogClose,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogPopup,
  AlertDialogTitle,
  Button,
  Icon,
  Kbd,
  Skeleton,
  Spinner,
  Textarea,
  Tip,
  Tooltip,
  TooltipPopup,
  TooltipTrigger,
  cn,
} from "@berth/plugin/ui";
import { useEffect, useState } from "react";

import * as gh from "./gh";
import { Markdown } from "./markdown";
import { type Run, describeProblem, detailOf, loadDetail, postComment, runnerOf, useIssuesStore } from "./store";

// The right-hand pane: one issue, its thread, the agents on it, and
// "Start an agent".

export interface Row extends gh.Issue {
  key: string;
  repo: string;
  project: Project;
}

export function Detail({ berth, row, runs, viewer, onStart }: { berth: BerthPluginContext; row: Row; runs: Run[]; viewer?: string; onStart(): void }) {
  useIssuesStore();
  const load = detailOf(row.repo, row.number);
  useEffect(() => {
    void loadDetail(berth, row.project, row.number);
  }, [berth, row.project, row.number]);
  const detail = load?.status === "ok" ? load.value : load?.status === "loading" ? load.prev : undefined;
  const url = gh.issueUrl(row.repo, row.number);
  const open = (u: string) => berth.openUrl(u);

  return (
    <article className="mx-auto flex max-w-3xl flex-col gap-4 px-6 py-5">
      <header className="flex flex-col gap-2">
        <p className="flex flex-wrap items-center gap-x-1.5 text-muted-foreground text-xs">
          <span className="inline-flex items-center gap-1 rounded-full bg-success/12 px-1.5 py-px font-medium text-[11px] text-success-foreground">
            <Icon name="CircleDot" className="size-3" />
            Open
          </span>
          <span className="font-mono">
            {row.repo}#{row.number}
          </span>
          <span>·</span>
          <span>
            opened {gh.since(row.createdAt)} by <b className="font-medium text-foreground">{row.author ?? "ghost"}</b>
          </span>
        </p>
        <h2 className="font-semibold text-lg leading-snug tracking-tight">{row.title}</h2>
        {(row.labels.length > 0 || row.assignees.length > 0) && (
          <div className="flex flex-wrap items-center gap-1.5">
            {row.labels.map((l) => (
              <LabelChip key={l.name} label={l} />
            ))}
            {row.assignees.length > 0 && (
              <span className="ml-1 inline-flex items-center gap-1.5 text-muted-foreground text-xs">
                <Icon name="UserRound" className="size-3" />
                {row.assignees.map((a) => (a === viewer ? "you" : a)).join(", ")}
              </span>
            )}
          </div>
        )}
        <div className="mt-1 flex flex-wrap items-center gap-2">
          <Button size="sm" onClick={onStart}>
            <Icon name="Bot" className="size-3.5" />
            Start an agent
            <Kbd className="ml-1 h-4.5 bg-primary-foreground/15 text-[10px] text-primary-foreground">S</Kbd>
          </Button>
          <Button size="sm" variant="outline" onClick={() => open(url)}>
            <Icon name="ExternalLink" className="size-3.5" />
            Open on GitHub
          </Button>
          <Tooltip>
            <TooltipTrigger render={<Button size="icon-sm" variant="ghost" aria-label="Refresh this issue" onClick={() => void loadDetail(berth, row.project, row.number, true)} />}>
              <Icon name="RefreshCw" className={cn("size-3.5", load?.status === "loading" && "animate-spin")} />
            </TooltipTrigger>
            <TooltipPopup>Refresh this issue</TooltipPopup>
          </Tooltip>
        </div>
      </header>

      {runs.length > 0 && <Runs berth={berth} runs={runs} />}

      {detail && detail.prs.length > 0 && (
        <section className="flex flex-col rounded-lg border">
          <h3 className="border-b px-3 py-1.5 font-medium text-[11px] text-muted-foreground">Pull requests that close it</h3>
          {detail.prs.map((pr) => (
            <button key={pr.number} type="button" onClick={() => open(pr.url ?? gh.pullUrl(row.repo, pr.number))} className="flex items-center gap-2 px-3 py-2 text-left text-[13px] hover:bg-accent/50">
              <PrIcon pr={pr} />
              <span className="font-mono text-muted-foreground text-xs">#{pr.number}</span>
              <span className="min-w-0 flex-1 truncate">{pr.title}</span>
              <span className="text-muted-foreground text-xs">{pr.draft ? "Draft" : pr.state === "OPEN" ? "Open" : pr.state === "MERGED" ? "Merged" : "Closed"}</span>
            </button>
          ))}
        </section>
      )}

      {load?.status === "problem" && !detail ? (
        <Problem problem={load.problem} box={runnerOf(row.project)?.box} />
      ) : !detail ? (
        <div className="flex flex-col gap-2 rounded-lg border p-4">
          <Skeleton className="h-3.5 w-1/3" />
          <Skeleton className="h-3.5 w-full" />
          <Skeleton className="h-3.5 w-5/6" />
          <Skeleton className="h-3.5 w-2/3" />
        </div>
      ) : (
        <>
          <Post author={detail.author} at={detail.createdAt} viewer={viewer} badge="Author">
            {detail.body.trim() ? <Markdown source={detail.body} repo={row.repo} onLink={open} /> : <p className="text-muted-foreground text-[13px] italic">No description.</p>}
          </Post>
          {detail.totalComments > detail.comments.length && (
            <button type="button" onClick={() => open(url)} className="self-center text-muted-foreground text-xs hover:text-foreground">
              {detail.totalComments - detail.comments.length} earlier comments on GitHub
            </button>
          )}
          {detail.comments.map((c, i) => (
            <Post key={c.url ?? i} author={c.author} at={c.createdAt} viewer={viewer} badge={c.author && c.author === detail.author ? "Author" : undefined}>
              <Markdown source={c.body} repo={row.repo} onLink={open} />
            </Post>
          ))}
          <Composer berth={berth} row={row} viewer={viewer} />
        </>
      )}
    </article>
  );
}

export function Runs({ berth, runs }: { berth: BerthPluginContext; runs: Run[] }) {
  return (
    <section className="flex flex-col rounded-lg border">
      <h3 className="border-b px-3 py-1.5 font-medium text-[11px] text-muted-foreground">Agents on this issue</h3>
      {runs.map((r) => (
        <div key={`${r.box}:${r.path}`} className="flex items-center gap-2.5 px-3 py-2 text-[13px]">
          {r.session ? <AgentIcon agent={r.session.agent} /> : <Icon name="GitBranch" className="size-3.5 text-muted-foreground" />}
          <span className="min-w-0 flex-1 truncate">
            <span className="font-medium">{r.worktree}</span>
            <span className="text-muted-foreground"> · {r.box}</span>
          </span>
          <StatePill run={r} />
          <Button size="xs" variant="outline" onClick={() => berth.openWorktree({ box: r.box, location: r.location, worktree: r.worktree, path: r.path })}>
            Open
          </Button>
        </div>
      ))}
    </section>
  );
}

const STATES: Record<string, { label: string; dot: string; text: string }> = {
  running: { label: "Running", dot: "bg-info animate-pulse", text: "text-info-foreground" },
  waiting: { label: "Needs you", dot: "bg-warning", text: "text-warning-foreground" },
  finished: { label: "Done", dot: "bg-success", text: "text-success-foreground" },
  idle: { label: "Idle", dot: "bg-muted-foreground/50", text: "text-muted-foreground" },
};

// StatePill is how an issue's agent is doing, or that only its worktree is left.
export function StatePill({ run, compact }: { run: Run; compact?: boolean }) {
  const s = run.session ? (STATES[run.session.agent_state ?? ""] ?? STATES.idle) : undefined;
  if (!s) {
    return (
      <span className="inline-flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
        <Icon name="GitBranch" className="size-3" />
        {compact ? "" : "No agent"}
      </span>
    );
  }
  return (
    <span className={cn("inline-flex shrink-0 items-center gap-1.5 font-medium text-[11px]", s.text)}>
      <span className={cn("size-1.5 rounded-full", s.dot)} />
      {s.label}
    </span>
  );
}

export function Post({ author, at, viewer, badge, children }: { author?: string; at: string; viewer?: string; badge?: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-3">
      <Avatar login={author} className="mt-0.5 size-6 text-[10px]" />
      <div className="min-w-0 flex-1 rounded-lg border">
        <div className="flex items-center gap-1.5 border-b bg-muted/40 px-3 py-1.5 text-xs">
          <b className="font-medium">{author ?? "ghost"}</b>
          {author && author === viewer && <span className="text-muted-foreground">(you)</span>}
          <span className="text-muted-foreground">· {gh.since(at)}</span>
          {badge && <span className="ml-auto rounded-full border px-1.5 text-[10px] text-muted-foreground">{badge}</span>}
        </div>
        <div className="px-3 py-2.5">{children}</div>
      </div>
    </div>
  );
}

// Comments go out as whoever gh is logged in as on the box, publicly, so
// they are always confirmed first.
function Composer({ berth, row, viewer }: { berth: BerthPluginContext; row: Row; viewer?: string }) {
  const [text, setText] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [posting, setPosting] = useState(false);
  const [error, setError] = useState<string>();
  const box = runnerOf(row.project)?.box;

  async function post() {
    setPosting(true);
    setError(undefined);
    try {
      await postComment(berth, row.project, row.number, text.trim());
      setText("");
      setConfirming(false);
      berth.notify(`Commented on #${row.number}`, row.title);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setConfirming(false);
    } finally {
      setPosting(false);
    }
  }

  return (
    <div className="flex gap-3">
      <Avatar login={viewer} className="mt-0.5 size-6 text-[10px]" />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <Textarea
          className="min-h-20 text-[13px]"
          placeholder="Leave a comment (Markdown)"
          value={text}
          onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setText(e.target.value)}
          onKeyDown={(e: React.KeyboardEvent) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && text.trim()) {
              e.preventDefault();
              setConfirming(true);
            }
          }}
        />
        {error && <p className="text-destructive text-xs">{error}</p>}
        <div className="flex items-center justify-end gap-2">
          <span className="mr-auto text-muted-foreground text-xs">{viewer && box ? `Posts as @${viewer}, with gh on ${box}.` : null}</span>
          <Button size="sm" variant="outline" disabled={!text.trim() || posting} onClick={() => setConfirming(true)}>
            {posting ? <Spinner className="size-3.5" /> : <Icon name="MessageSquare" className="size-3.5" />}
            Comment
          </Button>
        </div>
      </div>
      <AlertDialog open={confirming} onOpenChange={(o: boolean) => !posting && setConfirming(o)}>
        <AlertDialogPopup>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Comment on {row.repo}#{row.number}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              It's posted publicly{viewer ? ` as @${viewer}` : ""} and everyone watching the issue is notified.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className="mx-6 mb-5 max-h-48 overflow-y-auto rounded-md border bg-muted/40 px-3 py-2">
            <Markdown source={text} repo={row.repo} onLink={() => undefined} />
          </div>
          <AlertDialogFooter>
            <AlertDialogClose render={<Button variant="ghost" disabled={posting} />}>Cancel</AlertDialogClose>
            <Button onClick={() => void post()} disabled={posting}>
              {posting && <Spinner className="size-3.5" />}
              Post comment
            </Button>
          </AlertDialogFooter>
        </AlertDialogPopup>
      </AlertDialog>
    </div>
  );
}

export function Problem({ problem, box, className }: { problem: gh.Problem; box?: string; className?: string }) {
  const d = describeProblem(problem, box);
  return (
    <div className={cn("flex items-start gap-3 rounded-lg border bg-muted/30 px-4 py-3", className)}>
      <Icon name={d.icon} className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0">
        <p className="font-medium text-[13px]">{d.title}</p>
        <p className="mt-0.5 whitespace-pre-wrap text-muted-foreground text-xs">{d.body}</p>
      </div>
    </div>
  );
}

// LabelChip shows an issue's label in its colour. Filtering by labels is
// FilterChip's, in the toolbar.
export function LabelChip({ label }: { label: gh.Label }) {
  return (
    <span className="inline-flex max-w-44 shrink-0 items-center gap-1.5 rounded-full border px-2 py-px text-[11px] text-foreground/80 leading-[18px]">
      <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: `#${label.color}` }} />
      <span className="truncate">{label.name}</span>
    </span>
  );
}

function PrIcon({ pr }: { pr: gh.LinkedPR }) {
  if (pr.state === "MERGED") return <Icon name="GitMerge" className="size-3.5 shrink-0 text-[#8250df] dark:text-[#a371f7]" />;
  if (pr.state === "CLOSED") return <Icon name="GitPullRequestClosed" className="size-3.5 shrink-0 text-destructive" />;
  if (pr.draft) return <Icon name="GitPullRequestDraft" className="size-3.5 shrink-0 text-muted-foreground" />;
  return <Icon name="GitPullRequest" className="size-3.5 shrink-0 text-success" />;
}

export { PrIcon };

// Avatar is a login's initial on a colour of its own: no images are
// fetched, so nothing about who looks at what leaves the app.
export function Avatar({ login, className }: { login?: string; className?: string }) {
  const name = login ?? "?";
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
  return (
    <Tip label={login}>
      <span
        role="img"
        aria-label={login ?? "Unknown"}
        className={cn("grid size-5 shrink-0 place-items-center rounded-full font-semibold text-[9px] text-white uppercase", className)}
        style={{ backgroundColor: `hsl(${h} 45% 45%)` }}
      >
        {name.replace(/^app\//, "").slice(0, 1)}
      </span>
    </Tip>
  );
}
