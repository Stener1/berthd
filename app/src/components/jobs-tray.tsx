import { AlertTriangleIcon, CheckIcon, ChevronDownIcon, ChevronUpIcon, CircleStopIcon, MinusIcon, XIcon } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";

import { Tip } from "@/components/tip";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { cancelJob, dismissJob, FADE_MS, type Job, type JobItem, leftOver, outcomeOf, progressOf, setFolded, useJobs } from "@/lib/jobs";
import { cn } from "@/lib/utils";

// The background-jobs tray (lib/jobs.ts): bottom right, above the status
// bar (26px) and the loops panel (--berth-loops-h), with toasts stacked
// above it through --berth-jobs-h. Jobs stack newest on top. Folded, it is
// one line with the progress. With no jobs it is gone and reserves no room.
const STATUS_BAR = 26;
const GAP = 12;
const FADE = 700;

export function JobsTray() {
  const jobs = useJobs((s) => s.jobs);
  const folded = useJobs((s) => s.folded);
  const ref = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const root = document.documentElement;
    const el = ref.current;
    if (!el) {
      root.style.setProperty("--berth-jobs-h", "0px");
      return;
    }
    const sync = () => root.style.setProperty("--berth-jobs-h", `${el.offsetHeight + GAP}px`);
    sync();
    const ro = new ResizeObserver(sync);
    ro.observe(el);
    return () => {
      ro.disconnect();
      root.style.setProperty("--berth-jobs-h", "0px");
    };
  }, [jobs.length, folded]);

  if (!jobs.length) return null;
  const style = { bottom: `calc(var(--berth-status-h, ${STATUS_BAR}px) + ${GAP}px + var(--berth-loops-h, 0px))`, right: `calc(${GAP}px + var(--berth-dock-w, 0px))` };
  if (folded) {
    const running = jobs.filter((j) => outcomeOf(j) === "running");
    const report = jobs.some((j) => outcomeOf(j) === "report");
    const one = jobs.length === 1 ? jobs[0] : undefined;
    const p = jobs.reduce((a, j) => ({ settled: a.settled + progressOf(j).settled, total: a.total + progressOf(j).total }), { settled: 0, total: 0 });
    const label = one ? lineOf(one) : `${jobs.length} jobs${running.length ? ` · ${p.settled}/${p.total}` : ""}`;
    return (
      <div ref={ref} style={style} className="fixed z-40" role="region" aria-label="Background jobs">
        <Button size="sm" variant="outline" className="max-w-88 rounded-full bg-popover shadow-lg/5" onClick={() => setFolded(false)} aria-expanded={false}>
          {running.length ? <Spinner className="size-3.5" /> : report ? <AlertTriangleIcon className="text-warning-foreground" /> : <CheckIcon className="text-success" />}
          <span className="truncate tabular-nums">{label}</span>
          <ChevronUpIcon />
        </Button>
      </div>
    );
  }
  return (
    <div ref={ref} style={style} className="fixed z-40 flex max-h-[50vh] w-88 flex-col gap-2 overflow-y-auto" role="region" aria-label="Background jobs">
      {jobs.map((j) => (
        <JobCard key={j.id} job={j} />
      ))}
    </div>
  );
}

// lineOf is a job in one line: what it is doing and how far it got, or,
// once done, what it did.
function lineOf(j: Job) {
  if (j.summary !== undefined) return j.summary;
  const p = progressOf(j);
  return `${j.title} · ${p.settled}/${p.total}`;
}

function JobCard({ job: j }: { job: Job }) {
  const outcome = outcomeOf(j);
  const fading = useFading(j, outcome === "clean");
  // Running, every row; a report, only what didn't go as asked; clean,
  // just the summary.
  const rows = outcome === "running" ? j.items : outcome === "report" ? leftOver(j) : [];
  const groups = [...new Set(rows.map((i) => i.group))].sort();
  const unsent = j.items.some((i) => i.state === "queued");
  return (
    <div
      role="group"
      aria-label={j.title}
      className={cn(
        "rounded-xl border bg-popover p-3 text-body shadow-lg/5 transition-opacity",
        outcome === "report" && "border-warning/60",
        outcome === "clean" && "border-success/40",
        fading && "opacity-0",
      )}
      style={{ transitionDuration: `${FADE}ms` }}
    >
      <div className="flex items-center gap-2">
        {outcome === "running" ? <Spinner className="size-3.5 shrink-0" /> : outcome === "report" ? <AlertTriangleIcon className="size-3.5 shrink-0 text-warning-foreground" /> : <CheckIcon className="size-3.5 shrink-0 text-success" />}
        <span className="min-w-0 flex-1 truncate font-medium tabular-nums" data-slot="job-title">
          {lineOf(j)}
        </span>
        <Tip label="Fold">
          <button type="button" aria-label="Fold" aria-expanded className="inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground" onClick={() => setFolded(true)}>
            <ChevronDownIcon className="size-3.5" />
          </button>
        </Tip>
        {outcome !== "running" && (
          <Tip label="Dismiss">
            <button type="button" aria-label="Dismiss" className="inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground" onClick={() => dismissJob(j.id)}>
              <XIcon className="size-3.5" />
            </button>
          </Tip>
        )}
      </div>
      {rows.length > 0 && (
        <div className="mt-2 max-h-64 overflow-y-auto rounded-lg border">
          {groups.map((g) => {
            const group = rows.filter((i) => i.group === g);
            const all = j.items.filter((i) => i.group === g);
            return (
              <div key={g}>
                {groups.length > 1 || outcome === "running" ? (
                  <div className="sticky top-0 z-1 flex items-center gap-2 border-b bg-muted/80 px-3 py-1 font-mono text-meta text-muted-foreground backdrop-blur-sm">
                    <span className="flex-1">{g}</span>
                    {outcome === "running" && (
                      <span className="tabular-nums">
                        {progressOf({ ...j, items: all }).settled}/{all.length}
                      </span>
                    )}
                  </div>
                ) : null}
                <ul className="divide-y divide-border/60">
                  {group.map((i) => (
                    <JobRow key={i.key} item={i} />
                  ))}
                </ul>
              </div>
            );
          })}
        </div>
      )}
      {outcome === "running" && j.cancellable && unsent && (
        <div className="mt-2.5 flex items-center gap-1.5">
          <Button size="xs" variant="ghost" className="text-muted-foreground" disabled={j.cancelling} onClick={() => cancelJob(j.id)}>
            <CircleStopIcon />
            {j.cancelling ? "Stopping the rest…" : "Stop the rest"}
          </Button>
        </div>
      )}
    </div>
  );
}

// useFading starts a clean job's fade just before the store lets it go.
function useFading(j: Job, clean: boolean) {
  const [fading, setFading] = useState(false);
  useEffect(() => {
    if (!clean || j.finished === undefined) return;
    const t = setTimeout(() => setFading(true), Math.max(0, j.finished + FADE_MS - FADE - Date.now()));
    return () => clearTimeout(t);
  }, [clean, j.finished]);
  return fading;
}

function JobRow({ item: i }: { item: JobItem }) {
  const warn = i.state === "kept" || i.state === "cancelled";
  return (
    <li className="flex items-start gap-2.5 px-3 py-1.5">
      <span className="flex h-5 w-4 shrink-0 items-center justify-center">
        {i.state === "running" ? (
          <Spinner className="size-3.5" />
        ) : i.state === "done" ? (
          <CheckIcon className="size-3.5 text-success" />
        ) : i.state === "failed" ? (
          <XIcon className="size-3.5 text-destructive-foreground" />
        ) : warn ? (
          <AlertTriangleIcon className="size-3.5 text-warning-foreground" />
        ) : (
          <MinusIcon className="size-3.5 text-muted-foreground" />
        )}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate">{i.label}</span>
          {i.aside && <span className="shrink-0 text-muted-foreground text-meta">{i.aside}</span>}
        </span>
        {i.message && (
          <span className={cn("block whitespace-normal break-words text-meta", i.state === "failed" ? "text-destructive-foreground" : warn ? "text-warning-foreground" : "text-muted-foreground")}>{i.message}</span>
        )}
      </span>
    </li>
  );
}
