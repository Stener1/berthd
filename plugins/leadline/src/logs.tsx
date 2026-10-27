import type { WorktreePanelProps } from "@berth/plugin";
import { PickOne, cn } from "@berth/plugin/ui";
import { useState } from "react";

import type { LogRow } from "./api";
import { clock, count } from "./format.ts";
import { jumpTo, useDebounced, usePaged } from "./live";
import { severityTone, TraceLink } from "./traces";
import { Command, More, Nothing, type PanelContext, PanelShell, Search, StaleNote, Toolbar, Waiting } from "./ui";

// Logs: what the worktree's app logged, newest first. A line opens to its
// whole text and attributes, and a line written during a request is a click
// from that request's trace.

const PAGE = 200;

// OTLP severity numbers: the lowest of each level.
const LEVELS = [
  { value: "0", label: "All" },
  { value: "9", label: "Info" },
  { value: "13", label: "Warn" },
  { value: "17", label: "Error" },
];

export function LogsPanel(props: WorktreePanelProps) {
  return (
    <PanelShell {...props} panel="logs">
      {(ctx) => <Logs ctx={ctx} />}
    </PanelShell>
  );
}

// A log has no id: its cursor, the time and its place among the logs of
// that instant, names it.
const idOf = (l: LogRow) => l.cursor ?? `${l.ts_ms}\n${l.body}`;

function Logs({ ctx }: { ctx: PanelContext }) {
  const [search, setSearch] = useState("");
  const q = useDebounced(search);
  const [min, setMin] = useState("0");
  const [open, setOpen] = useState<string>();

  const list = usePaged<LogRow>((before) => ctx.api.logs(ctx.scope(), { q, min_severity: Number(min) || undefined, limit: PAGE, before }), [ctx.box, ctx.source, ctx.window.value, q, min].join("\n"), {
    size: PAGE,
    every: ctx.window.every,
    active: ctx.active,
    id: idOf,
    cursor: (l) => l.cursor ?? "",
    time: (l) => l.ts_ms,
    sinceMs: () => ctx.scope().sinceMs,
    onFault: ctx.onFault,
  });
  const rows = list.rows ?? [];

  return (
    <>
      <Toolbar ctx={ctx} note={list.rows ? `${count(rows.length)} ${list.more ? "newest" : rows.length === 1 ? "line" : "lines"}` : undefined}>
        <Search value={search} onChange={setSearch} placeholder="Filter by message" />
        <PickOne label="Lowest level" value={min} options={LEVELS} onChange={setMin} />
      </Toolbar>
      <StaleNote fault={list.rows ? list.fault : undefined} />
      {!list.rows ? (
        <Waiting fault={list.fault} />
      ) : rows.length === 0 ? (
        <Nothing ctx={ctx} what="logs" filtered={!!q || min !== "0"} />
      ) : (
        <div className="min-h-0 flex-1 overflow-auto" data-testid="leadline-list">
          <ul className="font-mono text-xs">
            {rows.map((l) => {
              const id = idOf(l);
              const on = id === open;
              const attrs = Object.entries(l.attrs ?? {});
              return (
                <li key={id} className={cn("border-b", on && "bg-accent/40")}>
                  <div className="flex min-h-7 items-center gap-1 pr-2">
                    <button type="button" data-testid="leadline-row" aria-expanded={on} className="flex min-w-0 flex-1 items-center gap-3 px-3 py-1 text-left outline-none hover:bg-accent/50 focus-visible:bg-accent/60" onClick={() => setOpen(on ? undefined : id)}>
                      <span className="shrink-0 text-muted-foreground tabular-nums">{clock(l.ts_ms)}</span>
                      <span className={cn("w-10 shrink-0", severityTone(l.severity))}>{l.severity}</span>
                      <span className={cn("min-w-0 flex-1", on ? "whitespace-pre-wrap break-words" : "truncate")}>{l.body}</span>
                    </button>
                    <Command service={l.service} />
                    {l.trace_id && <TraceLink onClick={() => jumpTo(ctx.berth, { panel: "traces", box: ctx.box, source: ctx.source, key: l.trace_id })} />}
                  </div>
                  {on && (
                    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5 px-3 pb-2 text-muted-foreground" data-testid="leadline-log-info">
                      <dt>service</dt>
                      <dd className="text-foreground">{l.service}</dd>
                      {attrs.map(([k, v]) => (
                        <div key={k} className="contents">
                          <dt>{k}</dt>
                          <dd className="min-w-0 break-all text-foreground">{typeof v === "string" ? v : JSON.stringify(v)}</dd>
                        </div>
                      ))}
                      {!l.trace_id && (
                        <>
                          <dt>trace</dt>
                          <dd>none: it was written outside any request</dd>
                        </>
                      )}
                    </dl>
                  )}
                </li>
              );
            })}
          </ul>
          {list.more && (
            <More onClick={list.loadMore} loading={list.loadingMore}>
              Show older logs
            </More>
          )}
        </div>
      )}
    </>
  );
}
