import { AlertDialog, AlertDialogClose, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogPopup, AlertDialogTitle, Badge, Button, Icon, Tip } from "@berth/plugin/ui";
import { useState } from "react";

import { type Connection, type Fault, faultOf } from "./api";
import { WRITES_FOR_MS } from "./clock.ts";
import type { Engine } from "./engines/index.ts";
import { HELPER, left } from "./ui";

// The lock on writes. A database opens read-only: nothing the grid or the
// console does can change it until writes are unlocked, which asks once,
// lasts fifteen minutes and ends by itself. One lock for the connection,
// kept by the helper, governs both.

export interface Writes {
  // The engine takes writes at all.
  can: boolean;
  on: boolean;
  // When they lock again, while on.
  until?: number;
  // Asks, then unlocks.
  ask(): void;
  // The helper said they are locked when the panel thought otherwise.
  refused(): void;
}

const MINUTES = Math.round(WRITES_FOR_MS / 60_000);

export function Lock({ writes, now, busy, onLock }: { writes: Writes; now: number; busy: boolean; onLock(): void }) {
  if (!writes.can) {
    return (
      <Tip label="This database takes no writes through Shipyard: its tables can be read and queried, not changed.">
        <Badge variant="outline" size="sm" className="shrink-0 gap-1 font-normal" data-testid="database-lock" data-writes="never">
          <Icon name="Lock" className="size-3" />
          Read-only
        </Badge>
      </Tip>
    );
  }
  if (!writes.on) {
    return (
      <Button size="sm" variant="outline" className="shrink-0" onClick={writes.ask} data-testid="database-lock" data-writes="off">
        <Icon name="Lock" className="size-3.5" />
        Unlock writes…
      </Button>
    );
  }
  return (
    <span className="flex shrink-0 items-center gap-1.5" data-testid="database-lock" data-writes="on">
      <Tip label="Edits and statements can change this database until the time runs out">
        {/* Armed, said the way the app's destructive actions are: an outline, in red. Not amber, which is an agent asking for you. */}
        <Badge variant="outline" size="sm" className="gap-1 border-destructive/40 text-destructive-foreground tabular-nums" role="timer" aria-label={`Writes are on for ${left((writes.until ?? now) - now)} more`}>
          <Icon name="LockOpen" className="size-3" />
          Writes on · {left((writes.until ?? now) - now)}
        </Badge>
      </Tip>
      <Button size="sm" variant="outline" loading={busy} onClick={onLock}>
        Lock
      </Button>
    </span>
  );
}

// UnlockDialog asks before writes are turned on, naming the database and
// the box, and saying how long it lasts.
export function UnlockDialog({ open, conn, engine, box, worktree, onUnlock, onClose }: { open: boolean; conn: Connection; engine: Engine; box: string; worktree: string; onUnlock(): Promise<void>; onClose(): void }) {
  const [busy, setBusy] = useState(false);
  const [fault, setFault] = useState<Fault>();
  const go = async () => {
    setBusy(true);
    setFault(undefined);
    try {
      await onUnlock();
      onClose();
    } catch (err) {
      setFault(faultOf(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <AlertDialog
      open={open}
      onOpenChange={(o: boolean) => {
        if (!o) {
          setFault(undefined);
          onClose();
        }
      }}
    >
      <AlertDialogPopup className="sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>
            Unlock writes to {conn.database} on {box}?
          </AlertDialogTitle>
          <AlertDialogDescription>
            For {MINUTES} minutes, the changes you apply in the grid and the statements you run in the console can change <code>{conn.database}</code>: the {engine.name} database {worktree}'s services use on {box}
            {conn.host && ` (${conn.host}${conn.port ? `:${conn.port}` : ""})`}. That includes deleting rows and dropping tables. It locks again by itself after {MINUTES} minutes, or when <code>{HELPER}</code> restarts; <b>Lock</b> does it at once.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {fault && <p className="mx-6 whitespace-pre-wrap rounded-md bg-destructive/8 p-2 text-destructive-foreground text-xs">{fault.message}</p>}
        <AlertDialogFooter>
          <AlertDialogClose render={<Button variant="ghost" />}>Cancel</AlertDialogClose>
          <Button loading={busy} onClick={() => void go()}>
            Unlock for {MINUTES} minutes
          </Button>
        </AlertDialogFooter>
      </AlertDialogPopup>
    </AlertDialog>
  );
}
