import { Button, Empty, EmptyDescription, EmptyHeader, EmptyTitle, type HelperCopy, Icon, Skeleton, cn } from "@berth/plugin/ui";
import type { ReactNode } from "react";

import type { Fault } from "./api";

// The program this plugin installs on a box, as its manifest names it.
export const HELPER = "berth-database";

export const GUIDE = "https://docs.berthd.app/guides/database";

// What Database says of its helper where the app's HelperBox draws a box's
// state and its switch (the Database screen's rows, and the panel on a box
// where it is off).
export const COPY: HelperCopy = {
  name: "Database",
  program: HELPER,
  icon: "table-2",
  does: (box) =>
    `It notes the development database of each worktree on that box: a DATABASE_URL or POSTGRES_URL that points at a database on the box itself, in the environment of a service Shipyard starts there, in the worktree's own .env files, which the box reads when you open the worktree's Database tab, or in an address you paste for it. It keeps that address in a folder on the box, and runs what you ask of it here. It changes nothing in a service's environment, and only reads until you unlock writes. Nothing it keeps leaves ${box}.`,
  about: (box) => (
    <>
      It shows a worktree's own development database: its tables and rows, and a console for SQL, read-only until you unlock it. Turning it on installs and runs <code>{HELPER}</code> on {box}; the database's address stays there.
    </>
  ),
  off: (box) => `No worktree's database is shown while it is off. Turning it on installs ${HELPER} on ${box} and keeps it running there; the addresses it kept before, if any, stay on the box until you remove them.`,
  remove: {
    title: (box) => `Remove what Database kept on ${box}?`,
    body: () => "deletes what it kept there: the address and password of each worktree's development database. The databases themselves aren't touched. Turn Database on again and open a worktree's Database tab, or start its services, to see its database here again; one you connected by pasting its address needs connecting again.",
    done: (box) => `Removed Database's data on ${box}`,
  },
};

export function Notice({ icon, title, body, actions }: { icon: ReactNode; title: string; body?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex h-full min-h-60 flex-1 items-center justify-center p-6" data-testid="database-notice">
      <Empty>
        <EmptyHeader>
          {typeof icon === "string" ? <Icon name={icon} className="mx-auto mb-2 size-5 text-muted-foreground" /> : icon}
          <EmptyTitle>{title}</EmptyTitle>
          {body && <EmptyDescription className="max-w-md text-pretty">{body}</EmptyDescription>}
        </EmptyHeader>
        {actions && <div className="flex flex-wrap items-center justify-center gap-2">{actions}</div>}
      </Empty>
    </div>
  );
}

// What the helper said went wrong, and what it suggests.
export function FaultText({ fault }: { fault: Fault }) {
  return (
    <>
      {fault.message}
      {fault.hint && <span className="mt-1 block text-muted-foreground">{fault.hint}</span>}
    </>
  );
}

// Waiting is a list before its first answer: rows at their height, or why
// there is no answer.
export function Waiting({ fault, onRetry, title = "Couldn't read from the database" }: { fault?: Fault; onRetry?(): void; title?: string }) {
  if (fault) {
    return (
      <Notice
        icon="TriangleAlert"
        title={title}
        body={<FaultText fault={fault} />}
        actions={
          onRetry && (
            <Button size="sm" variant="outline" onClick={onRetry}>
              Try again
            </Button>
          )
        }
      />
    );
  }
  return (
    <div className="flex-1 space-y-2 p-3" aria-busy="true">
      {[0, 1, 2, 3, 4, 5].map((i) => (
        <Skeleton key={i} className="h-7 w-full" />
      ))}
    </div>
  );
}

// One line over what is shown: something needs saying ("note"), or went
// wrong ("bad"). Never amber: in Shipyard that means an agent needs you.
export function Line({ tone = "note", children, actions, testId }: { tone?: "bad" | "note"; children: ReactNode; actions?: ReactNode; testId?: string }) {
  const tones = { bad: "bg-destructive/8 text-destructive-foreground", note: "bg-muted/50 text-muted-foreground" };
  return (
    <div role="status" data-testid={testId} className={cn("flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-1.5 text-xs", tones[tone])}>
      <span className="min-w-0 flex-1 text-pretty">{children}</span>
      {actions}
    </div>
  );
}

// count writes a row estimate: 950, 1.2k, 34k, 3.4M.
export function count(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "–";
  if (n < 1000) return String(Math.round(n));
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0).replace(/\.0$/, "")}k`;
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
}

// ms writes a duration: 412µs, 3.21ms, 12.3ms, 840ms, 1.24s.
export function ms(n: number): string {
  if (!Number.isFinite(n)) return "–";
  if (n < 1) return `${Math.round(n * 1000)}µs`;
  if (n < 10) return `${n.toFixed(2)}ms`;
  if (n < 100) return `${n.toFixed(1)}ms`;
  if (n < 1000) return `${Math.round(n)}ms`;
  return `${(n / 1000).toFixed(2)}s`;
}

// left writes how long writes stay on: 14:32, 0:07.
export function left(msLeft: number): string {
  const s = Math.max(0, Math.ceil(msLeft / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
