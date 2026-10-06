import { CheckIcon, CircleAlertIcon, KeyRoundIcon, LockIcon, PlusIcon, RotateCwIcon, ServerIcon, SquareTerminalIcon } from "lucide-react";
import type { ReactNode } from "react";

import { SimpleSelect } from "@/components/simple-select";
import { Tip } from "@/components/tip";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { type GitHubState, plural, type TeamStatus, type TeamView } from "@/lib/team";
import { cn } from "@/lib/utils";
import { Avatar } from "@/views/team/team-parts";

// The page's right-hand side: before it runs, a three-line checklist
// (GitHub, the box, keys) that ends in the one button; while it runs, where
// it is and what to do next.

export type CheckState = "todo" | "active" | "done" | "warn";

function Check({ n, title, state, value, children, compact }: { n: number; title: string; state: CheckState; value?: ReactNode; children?: ReactNode; compact?: boolean }) {
  return (
    <div data-testid={`check-${title.toLowerCase()}`} data-state={state} className={cn("px-4 py-3", compact && "px-3 py-2.5")}>
      <div className="flex min-w-0 items-center gap-2.5">
        <span
          className={cn(
            "inline-flex size-5 shrink-0 items-center justify-center rounded-full border font-medium text-[11px]",
            state === "done" && "border-success bg-success text-white",
            state === "warn" && "border-warning bg-warning/15 text-warning-foreground",
            state === "active" && "border-foreground text-foreground",
            state === "todo" && "text-muted-foreground",
          )}
        >
          {state === "done" ? <CheckIcon className="size-3" strokeWidth={3} /> : state === "warn" ? "!" : n}
        </span>
        <span className={cn("shrink-0 font-medium text-[13px]", state === "todo" && "text-muted-foreground")}>{title}</span>
        {value && <span className="ml-auto flex min-w-0 items-center justify-end gap-1.5 truncate text-right text-xs">{value}</span>}
      </div>
      {children && <div className={cn("mt-2 ml-7.5", compact && "mt-1.5")}>{children}</div>}
    </div>
  );
}

export interface ChecklistProps {
  view: TeamView;
  github: GitHubState;
  boxes: { name: string; detail: string }[];
  box?: string;
  onBox(box: string): void;
  onAddBox(): void;
  keys: Record<string, string>;
  onKey(id: string, value: string): void;
  // How many of the picked repos will be set up.
  picked: number;
  sudo: number;
  // The button: its words, whether it can run, and why not.
  action: string;
  blocked?: string;
  busy?: boolean;
  onRun(): void;
  // At narrow widths, the same in less height, above the plan.
  compact?: boolean;
}

export function Checklist(p: ChecklistProps) {
  const { view, github, compact } = p;
  const asks = view.keys.ask.filter((k) => !k.set);
  const keyCount = view.keys.shared + asks.length;
  const missing = view.access.missing;
  const accessLine =
    view.state === "found" && view.access.total > 0 ? (
      missing.length === 0 ? (
        <p className="flex items-center gap-1.5 text-success-foreground text-xs">
          <CheckIcon className="size-3.5" /> You have access to {view.access.total === 1 ? "the repo" : `all ${view.access.total} repos`}
        </p>
      ) : (
        <p data-testid="access-partial" className="flex items-start gap-1.5 text-warning-foreground text-xs leading-relaxed">
          <LockIcon className="mt-0.5 size-3 shrink-0" />
          <span>
            You can't read <span className="whitespace-nowrap font-mono">{missing.join(", ")}</span>: ask an admin{view.setup?.contact ? ` (${view.setup.contact})` : ""} · Set up the other {view.access.readable}
          </span>
        </p>
      )
    ) : null;
  const boxPick = (
    <div className="flex items-center gap-1.5">
      {p.boxes.length > 0 ? (
        <SimpleSelect
          size="sm"
          className="min-w-0 flex-1"
          aria-labelledby="team-box-label"
          options={p.boxes.map((b) => ({ value: b.name, label: `${b.name}${b.detail ? ` · ${b.detail}` : ""}` }))}
          value={p.box ?? ""}
          onChange={p.onBox}
          placeholder="Pick a box"
        />
      ) : (
        <Button size="sm" className="flex-1" onClick={p.onAddBox}>
          <PlusIcon /> Add your box
        </Button>
      )}
      {p.boxes.length > 0 && (
        <Tip label="Add a box">
          <Button size="icon-sm" variant="ghost" aria-label="Add a box" onClick={p.onAddBox}>
            <PlusIcon />
          </Button>
        </Tip>
      )}
    </div>
  );
  const keyInputs = asks.length > 0 && (
    <div className="space-y-1.5">
      {asks.map((k) => {
        const id = `${k.project}/${k.key}`;
        return (
          <label key={id} className="block">
            <span className="mb-1 flex items-baseline gap-1.5 text-xs">
              <span className="font-mono">{k.key}</span>
              <span className="text-muted-foreground">yours alone</span>
            </span>
            <Input size="sm" type="password" autoComplete="off" placeholder="Paste it once" className="font-mono [&_input::placeholder]:font-sans" value={p.keys[id] ?? ""} onChange={(e) => p.onKey(id, e.target.value)} />
          </label>
        );
      })}
    </div>
  );
  const entered = asks.filter((k) => p.keys[`${k.project}/${k.key}`]).length;
  const keysValue = (
    <span data-testid="keys-line" className="text-muted-foreground">
      {[view.keys.shared ? `${view.keys.shared} from 1Password` : "", asks.length - entered ? `${asks.length - entered} to enter` : entered ? `${entered} entered` : ""].filter(Boolean).join(" · ")}
    </span>
  );

  const button = (
    <div className={cn("border-t bg-muted/30 p-4", compact && "flex flex-wrap items-center gap-x-4 gap-y-1.5 p-3")}>
      <Button data-testid="team-run" className={cn("w-full", compact && "w-auto shrink-0")} disabled={!!p.blocked || p.busy} onClick={p.onRun}>
        {p.busy && <Spinner className="size-3.5" />}
        {p.action}
      </Button>
      {p.blocked && <p className={cn("mt-2 text-center text-[11.5px] text-muted-foreground", compact && "mt-0 text-left")}>{p.blocked}</p>}
      <p className={cn("mt-2.5 flex items-start gap-1.5 text-[11.5px] text-muted-foreground leading-relaxed", compact && "mt-0 min-w-0 flex-1")}>
        <KeyRoundIcon className="mt-0.5 size-3 shrink-0 text-warning-foreground" />
        {p.sudo > 0 ? (
          <span>
            {plural(p.sudo, "step")} {p.sudo === 1 ? "asks" : "ask"} for your password on {p.box ?? "the box"}. You type it; Berth doesn't keep it.
          </span>
        ) : (
          <span>Nothing here asks for your password.</span>
        )}
      </p>
    </div>
  );

  if (compact) {
    return (
      <div data-testid="team-checklist" data-compact className="overflow-hidden rounded-xl border bg-card shadow-xs/5">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)] divide-x">
          <Check compact n={1} title="GitHub" state={missing.length ? "warn" : "done"} value={<GitHubWho github={github} />}>
            {accessLine}
          </Check>
          <div className="divide-y">
            <Check compact n={2} title="Box" state={p.box ? "done" : "active"} value={<span id="team-box-label" className="sr-only">Box</span>}>
              {boxPick}
            </Check>
            {keyCount > 0 && (
              <Check compact n={3} title="Keys" state={asks.length && asks.some((k) => !p.keys[`${k.project}/${k.key}`]) ? "active" : "done"} value={keysValue}>
                {keyInputs}
              </Check>
            )}
          </div>
        </div>
        {button}
      </div>
    );
  }

  return (
    <div data-testid="team-checklist" className="overflow-hidden rounded-xl border bg-card shadow-xs/5">
      <div className="divide-y">
        <Check n={1} title="GitHub" state={missing.length ? "warn" : "done"} value={<GitHubWho github={github} />}>
          {accessLine}
        </Check>
        <Check n={2} title="Box" state={p.box ? "done" : "active"} value={<span id="team-box-label" className="sr-only">Box</span>}>
          {boxPick}
          <p className="mt-1.5 text-[11.5px] text-muted-foreground leading-relaxed">It signs in to GitHub itself, in its own terminal: this computer's sign-in is never copied there.</p>
        </Check>
        {keyCount > 0 && (
          <Check n={3} title="Keys" state={asks.length && asks.some((k) => !p.keys[`${k.project}/${k.key}`]) ? "active" : "done"} value={keysValue}>
            {keyInputs}
            {asks.length > 0 && <p className="mt-1.5 text-[11px] text-muted-foreground">Kept on {p.box ?? "the box"} only, never in git. Leave it empty to skip.</p>}
          </Check>
        )}
      </div>
      {button}
    </div>
  );
}

function GitHubWho({ github }: { github: GitHubState }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <Avatar src={github.avatar_url} name={github.login ?? "?"} size={16} />
      <span className="truncate">{github.login}</span>
    </span>
  );
}

// RunCard is the rail while the setup runs: where it is, how far, and the
// one thing to do now.
export function RunCard({ run, github, os, onOpenTerminal, onRetry, onBackground, compact }: { run: TeamStatus; github?: GitHubState; os?: string; onOpenTerminal(): void; onRetry(from: string): void; onBackground(): void; compact?: boolean }) {
  const { title, detail, tone, pct, retryFrom } = runSummary(run);
  const icon = tone === "failed" ? <CircleAlertIcon className="size-4 shrink-0 text-destructive" /> : tone === "waiting" ? <KeyRoundIcon className="size-4 shrink-0 text-warning-foreground" /> : tone === "done" ? <CheckIcon className="size-4 shrink-0 text-success" /> : <Spinner className="size-4 shrink-0" />;
  const bar = (
    <div className="h-1.5 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuenow={Math.round(pct * 100)} aria-valuemin={0} aria-valuemax={100} aria-label="Setup progress">
      <div className={cn("h-full rounded-full transition-[width] duration-500", tone === "failed" ? "bg-destructive" : tone === "done" ? "bg-success" : "bg-foreground")} style={{ width: `${Math.max(4, pct * 100)}%` }} />
    </div>
  );
  const primary = retryFrom ? (
    <Button size="sm" className={compact ? undefined : "w-full"} onClick={() => onRetry(retryFrom.id)}>
      <RotateCwIcon /> Retry from {shortTitle(retryFrom.title)}
    </Button>
  ) : (
    <Button size="sm" variant="outline" className={compact ? undefined : "min-w-0 flex-1"} onClick={onOpenTerminal} disabled={!run.session}>
      <SquareTerminalIcon /> Open terminal
    </Button>
  );
  const background = (
    <Button size="sm" variant="ghost" className={compact ? undefined : "min-w-0 flex-1"} onClick={onBackground}>
      Run in background
    </Button>
  );
  if (compact) {
    // At narrow widths, one band above the plan: the plan stays in view.
    return (
      <div data-testid="team-runcard" data-phase={run.phase} data-compact className="overflow-hidden rounded-xl border bg-card px-4 py-3 shadow-xs/5">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
          <div className="min-w-0 flex-1 basis-64">
            <p className="flex items-center gap-2 font-medium text-sm">
              {icon}
              {title}
            </p>
            <p className="mt-0.5 text-muted-foreground text-xs leading-relaxed">{detail}</p>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            {primary}
            {background}
          </div>
        </div>
        <div className="mt-2.5">{bar}</div>
      </div>
    );
  }
  return (
    <div data-testid="team-runcard" data-phase={run.phase} className="overflow-hidden rounded-xl border bg-card shadow-xs/5">
      <div className="p-4">
        <p className="flex items-center gap-2 font-medium text-sm">
          {icon}
          {title}
        </p>
        <p className="mt-1 text-muted-foreground text-xs leading-relaxed">{detail}</p>
        <div className="mt-3">{bar}</div>
      </div>
      <div className="space-y-1.5 border-t px-4 py-3 text-xs">
        {github?.login && (
          <p className="flex items-center gap-2">
            <Avatar src={github.avatar_url} name={github.login} size={14} /> {github.login}
          </p>
        )}
        <p className="flex items-center gap-2">
          <ServerIcon className="size-3.5 text-muted-foreground" /> {run.box}
          {os ? <span className="text-muted-foreground">· {os}</span> : null}
        </p>
      </div>
      <div className="flex flex-wrap gap-2 border-t bg-muted/30 p-3">
        {primary}
        {background}
      </div>
    </div>
  );
}

// shortTitle is a step's name for a button: "Postgres 16 in Docker" → "Postgres 16".
export const shortTitle = (t: string) => t.replace(/ (with|in|and) .*/, "");

export function runSummary(run: TeamStatus): { title: string; detail: string; tone: "running" | "waiting" | "failed" | "done"; pct: number; retryFrom?: { id: string; title: string }; status: string } {
  const steps = run.steps;
  const doneSteps = steps.filter((s) => s.state === "done" || s.state === "skipped").length;
  const repos = run.projects.filter((p) => p.state !== "skipped");
  const ready = repos.filter((p) => p.state === "ready").length;
  const total = steps.length + repos.length;
  const pct = total ? (doneSteps + ready) / total : 0;
  const failedStep = steps.find((s) => s.state === "failed");
  const failedRepo = run.projects.find((p) => p.state === "failed");
  const waiting = steps.find((s) => s.state === "waiting");
  const running = steps.findIndex((s) => s.state === "running" || s.state === "waiting");
  if (failedStep)
    return {
      title: `Stopped at ${failedStep.title}`,
      detail: `${doneSteps} of ${steps.length} box steps done. Fix it, then retry from that step; the ones done are kept.`,
      tone: "failed",
      pct,
      retryFrom: failedStep,
      status: `stopped at ${failedStep.title} · Retry`,
    };
  if (failedRepo)
    return { title: `${failedRepo.id} didn't set up`, detail: failedRepo.error ?? "See its row for why.", tone: "failed", pct, retryFrom: { id: failedRepo.id, title: failedRepo.id }, status: `${failedRepo.id} failed · Retry` };
  if (run.phase === "done") return { title: "Set up", detail: `${run.box} matches ${run.name}'s team setup.`, tone: "done", pct: 1, status: "done" };
  if (waiting?.id === "github")
    return { title: "Sign the box in to GitHub", detail: `Enter the code at github.com/login/device. Box step ${running + 1} of ${steps.length}, then ${plural(repos.length, "repo")}.`, tone: "waiting", pct, status: "waiting for GitHub's code" };
  if (waiting) return { title: "Waiting for your password", detail: `sudo asks in ${run.box}'s terminal. Box step ${running + 1} of ${steps.length}, then ${plural(repos.length, "repo")}.`, tone: "waiting", pct, status: "waiting for your password" };
  if (run.phase === "projects") {
    const busy = repos.filter((p) => p.state !== "ready").map((p) => p.id);
    return {
      title: ready ? `${ready} of ${repos.length} repos ready` : "Cloning repos",
      detail: busy.length ? `The box is ready. ${busy.join(" and ")} ${busy.length === 1 ? "is" : "are"} still setting up; start on what's ready.` : "Finishing up.",
      tone: "running",
      pct,
      status: `${ready} of ${repos.length} repos ready`,
    };
  }
  const cur = steps[running];
  return { title: cur ? cur.title : "Starting", detail: `Box step ${Math.max(1, running + 1)} of ${steps.length}, then ${plural(repos.length, "repo")}.`, tone: "running", pct, status: cur ? `${cur.title.toLowerCase()} (${running + 1} of ${steps.length})` : "starting" };
}
