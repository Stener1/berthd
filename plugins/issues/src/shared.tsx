import { type Session, useSessions } from "@berth/plugin";
import { Kbd, Skeleton } from "@berth/plugin/ui";
import { type RefObject, useEffect, useRef } from "react";

// What the GitHub and the Linear side of Issues both use: the keys that
// drive a list, the agents on a box, and the list's small parts.

// SessionFeed hands a box's live sessions up, so rows can follow agents on
// every box the shown projects are on.
export function SessionFeed({ box, onSessions }: { box: string; onSessions(box: string, s?: Session[]): void }) {
  const s = useSessions(box);
  useEffect(() => onSessions(box, s), [box, s, onSessions]);
  return null;
}

export interface ListKeys<T extends { key: string }> {
  list: T[];
  selected?: T;
  ticked: T[];
  // A sheet is open over the list: its keys are its own.
  blocked: boolean;
  search: RefObject<HTMLInputElement | null>;
  select(key: string): void;
  toggle(key: string): void;
  start(rows: T[]): void;
  open(row: T): void;
  clear(): void;
}

// useListKeys: J/K move, X ticks, S starts, O opens the issue where it
// lives, / searches, Esc clears the ticks. A row's element is #issue-<key>.
export function useListKeys<T extends { key: string }>(opts: ListKeys<T>) {
  const now = useRef(opts);
  now.current = opts;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const o = now.current;
      const t = e.target as HTMLElement | null;
      if (e.metaKey || e.ctrlKey || e.altKey || o.blocked) return;
      if (t && (t.closest("input, textarea, [contenteditable=true], [role=dialog], [role=menu]") || t.isContentEditable)) {
        if (e.key === "Escape" && t === o.search.current) o.search.current?.blur();
        return;
      }
      const i = o.selected ? o.list.indexOf(o.selected) : -1;
      const move = (d: number) => {
        const next = o.list[Math.min(o.list.length - 1, Math.max(0, i + d))];
        if (!next) return;
        o.select(next.key);
        document.getElementById(`issue-${next.key}`)?.scrollIntoView({ block: "nearest" });
      };
      if (e.key === "j" || e.key === "ArrowDown") move(1);
      else if (e.key === "k" || e.key === "ArrowUp") move(-1);
      else if (e.key === "x" && o.selected) o.toggle(o.selected.key);
      else if (e.key === "s") o.start(o.ticked.length ? o.ticked : o.selected ? [o.selected] : []);
      else if (e.key === "o" && o.selected) o.open(o.selected);
      else if (e.key === "/") o.search.current?.focus();
      else if (e.key === "Escape") o.clear();
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}

export function RowSkeleton() {
  return (
    <li className="flex gap-2.5 px-2.5 py-2.5">
      <Skeleton className="mt-0.5 size-3.5 rounded-full" />
      <div className="flex flex-1 flex-col gap-1.5">
        <Skeleton className="h-3.5 w-4/5" />
        <Skeleton className="h-3 w-2/5" />
      </div>
    </li>
  );
}

// KeyHints is the list's footer when nothing is ticked: the keys, and how
// many issues show. where names what O opens ("GitHub", "Linear").
export function KeyHints({ where, count }: { where: string; count: string }) {
  return (
    <footer className="flex shrink-0 items-center gap-2.5 overflow-hidden whitespace-nowrap border-t px-3 py-2 text-[11px] text-muted-foreground">
      {(
        [
          ["J K", "move"],
          ["X", "select"],
          ["S", "start agent"],
          ["O", where],
        ] as const
      ).map(([k, label]) => (
        <span key={label} className="inline-flex items-center gap-1">
          {k.split(" ").map((x) => (
            <Kbd key={x} className="h-4 min-w-4 px-1 text-[10px]">
              {x}
            </Kbd>
          ))}
          {label}
        </span>
      ))}
      <span className="ml-auto tabular-nums">{count}</span>
    </footer>
  );
}
