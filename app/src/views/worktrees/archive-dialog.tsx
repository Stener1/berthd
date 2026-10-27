import { AlertTriangleIcon, CheckIcon, XIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { AlertDialog, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogPopup, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { RowProgress } from "@/views/worktrees/use-bulk";
import type { Row } from "@/views/worktrees/use-worktrees";

// ArchiveDialog archives several worktrees after one confirmation that names
// every one, then stays open with each one's outcome: each one's archive
// script runs, then it goes, and its branch stays.
export function ArchiveDialog({ rows, progress, running, onRun, onClose }: { rows?: Row[]; progress: Record<string, RowProgress>; running: boolean; onRun(): void; onClose(): void }) {
  const [started, setStarted] = useState(false);
  useEffect(() => {
    if (rows) setStarted(false);
  }, [rows]);

  const targets = (rows ?? []).filter((r) => !r.main);
  const skipped = (rows ?? []).length - targets.length;
  const done = started && !running;
  const failed = targets.filter((r) => progress[r.key]?.state === "failed").length;

  return (
    <AlertDialog open={!!rows} onOpenChange={(o) => !o && !running && onClose()}>
      <AlertDialogPopup className="sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {done ? (failed ? `Archived ${targets.length - failed} of ${targets.length}` : `Archived ${targets.length} worktree${targets.length === 1 ? "" : "s"}`) : `Archive ${targets.length} worktree${targets.length === 1 ? "" : "s"}?`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {done
              ? failed
                ? "The rest were left as they were."
                : "Their branches are kept; their folders, services and sessions are gone."
              : "Each one's sessions and services stop, the repo's archive script runs, then its folder goes. Branches stay, so nothing committed is lost; one with uncommitted work is left as it is."}
            {skipped > 0 && !done && ` Main checkouts are skipped (${skipped}).`}
          </AlertDialogDescription>
        </AlertDialogHeader>

        <ul className="mx-6 mb-2 max-h-64 divide-y divide-border/60 overflow-y-auto rounded-lg border">
          {targets.map((r) => {
            const p = progress[r.key];
            return (
              <li key={r.key} className="flex items-center gap-2.5 px-3 py-1.5 text-body">
                <span className="flex w-4 justify-center">
                  {p?.state === "running" ? (
                    <Spinner className="size-3.5" />
                  ) : p?.state === "ok" ? (
                    <CheckIcon className="size-3.5 text-success" />
                  ) : p?.state === "failed" ? (
                    <XIcon className="size-3.5 text-destructive-foreground" />
                  ) : r.changed || r.untracked ? (
                    <AlertTriangleIcon className="size-3.5 text-warning" aria-label="Has uncommitted changes" />
                  ) : null}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{r.name}</span>
                  {p?.state === "failed" ? (
                    <span className="block whitespace-normal break-words text-meta text-destructive-foreground">
                      {p.message}
                    </span>
                  ) : (
                    <span className="block truncate font-mono text-meta text-muted-foreground">{r.branch}</span>
                  )}
                </span>
                {(r.changed > 0 || r.untracked > 0) && !p && (
                  <span className="shrink-0 text-meta text-warning-foreground tabular-nums">{[r.changed && `${r.changed} changed`, r.untracked && `${r.untracked} untracked`].filter(Boolean).join(", ")}</span>
                )}
                <span className="shrink-0 font-mono text-tag text-muted-foreground">{r.box}</span>
              </li>
            );
          })}
        </ul>

        <AlertDialogFooter>
          {done ? (
            <Button onClick={onClose}>Done</Button>
          ) : (
            <>
              <Button variant="ghost" disabled={running} onClick={onClose}>
                Cancel
              </Button>
              <Button
                loading={running}
                disabled={!targets.length}
                onClick={() => {
                  setStarted(true);
                  onRun();
                }}
              >
                Archive {targets.length}
              </Button>
            </>
          )}
        </AlertDialogFooter>
      </AlertDialogPopup>
    </AlertDialog>
  );
}
