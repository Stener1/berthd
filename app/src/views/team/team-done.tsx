import { ArrowRightIcon, GitBranchPlusIcon, LockIcon, Settings2Icon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { startWork } from "@/lib/start-work";
import { useStore } from "@/lib/store";
import { sudoCount, type TeamStatus, type TeamView } from "@/lib/team";
import { GitHubMark, OrgAvatar, SourceTag, SudoTag } from "@/views/team/team-parts";

// TeamDone is "You're set up": the team's suggested first task, ready to
// start, and each repo with the two things you do next.
export function TeamDone({ view, run, onShowPlan, onReviewUpdate }: { view: TeamView; run: TeamStatus; onShowPlan(): void; onReviewUpdate(): void }) {
  const name = view.setup?.name ?? view.org.name;
  const first = view.projects.find((p) => p.first_task);
  const firstLoc = first && run.projects.find((p) => p.id === first.id)?.location;
  const [task, setTask] = useState(first?.first_task ?? "");
  const [busy, setBusy] = useState(false);
  const sudo = sudoCount(view);
  const ready = run.projects.filter((p) => p.state === "ready");
  const go = async () => {
    if (!firstLoc) return;
    setBusy(true);
    await startWork({ text: task, box: run.box, location: firstLoc, where: "new", picks: [{ agent: "claude", model: "", effort: "" }] });
    setBusy(false);
  };
  return (
    <div data-testid="team-done">
      <div className="border-b bg-gradient-to-b from-success/8 to-transparent">
        <div className="mx-auto max-w-4xl px-8 pt-8 pb-6 @max-[819px]:px-5 @max-[819px]:pt-6">
          <div className="flex items-center gap-4">
            <OrgAvatar org={view.org} size={44} />
            <div className="min-w-0">
              <h1 className="font-semibold text-2xl tracking-tight">You're set up for {name}</h1>
              <p className="mt-0.5 text-muted-foreground text-sm">
                {run.box} matches {name}'s team setup at <span className="font-mono">{run.commit}</span>
                {view.source?.kind === "link" && (
                  <>
                    , loaded from <span className="font-mono">{view.source.label}</span>
                  </>
                )}
                . Its repos are in the sidebar.
              </p>
            </div>
          </div>
          {view.update && (
            <div className="mt-4 flex flex-wrap items-center gap-3 rounded-lg border border-info/30 bg-info/6 px-3 py-2 text-sm">
              <span className="min-w-0 flex-1">
                {name}'s setup changed: {view.update.changes.length} changes in {view.update.commits} commits by {view.update.author}
                {view.update.sudo.length ? ", one asks for your password" : ""}.
              </span>
              <Button size="sm" onClick={onReviewUpdate}>
                Review update
              </Button>
            </div>
          )}
        </div>
      </div>
      <div className="mx-auto max-w-4xl px-8 pt-6 pb-16 @max-[819px]:px-5">
        {first && firstLoc && (
          <section className="rounded-xl border bg-card p-4" aria-label="Your first task">
            <p className="font-medium text-sm">Your first task, suggested by {name}</p>
            <Textarea className="mt-2 text-sm" value={task} onChange={(e) => setTask(e.target.value)} aria-label="First task" />
            <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
              <span className="text-muted-foreground text-xs">
                on {first.id} · Claude Code · a new worktree on {run.box}
              </span>
              <Button data-testid="start-first-task" disabled={busy || !task.trim()} onClick={go}>
                Start the agent <ArrowRightIcon />
              </Button>
            </div>
          </section>
        )}
        <div className="mt-5 grid grid-cols-2 gap-3 @max-[819px]:grid-cols-1">
          {ready.map((r) => {
            const p = view.projects.find((x) => x.id === r.id);
            return (
              <div key={r.id} className="rounded-xl border bg-card p-4">
                <div className="flex items-center gap-2">
                  <span className="font-medium font-mono text-sm">{r.repo}</span>
                  {p?.private && <LockIcon className="size-3 text-muted-foreground" />}
                </div>
                <div className="mt-1.5 flex flex-wrap items-center gap-2 text-muted-foreground text-xs">
                  {p && <SourceTag source={p.source} />}
                  {p?.services.join(", ")}
                </div>
                <div className="mt-3 flex gap-2">
                  <Button size="xs" variant="outline" onClick={() => r.location && useStore.getState().openNewWorktree({ box: run.box, location: r.location })}>
                    <GitBranchPlusIcon /> New task
                  </Button>
                  <Button size="xs" variant="ghost" onClick={() => r.location && useStore.getState().setView({ kind: "project", box: run.box, location: r.location })}>
                    <Settings2Icon /> Project settings
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
        <div className="mt-5 flex flex-wrap items-center gap-x-4 gap-y-2 text-muted-foreground text-xs">
          {sudo > 0 && (
            <span className="flex items-center gap-2">
              <SudoTag /> sudo asked for your password on {run.box} for {sudo} steps. Berth never had it, and nothing kept it.
            </span>
          )}
          <button type="button" onClick={onShowPlan} className="inline-flex items-center gap-1.5 underline-offset-4 hover:text-foreground hover:underline">
            <GitHubMark className="size-3" /> See what ran
          </button>
        </div>
      </div>
    </div>
  );
}
