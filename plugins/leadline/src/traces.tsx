import type { WorktreePanelProps } from "@berth/plugin";
import { Badge, Button, FilterChip, Icon, Tip, cn } from "@berth/plugin/ui";
import { useMemo, useState } from "react";

import type { Span, TraceDetail, TraceRow, TreeNode } from "./api";
import { clock, count, ms, plural } from "./format.ts";
import { useDebounced, useJump, useLive, usePaged } from "./live";
import { shapeOf } from "./source.ts";
import { Command, DetailHeader, Meter, More, None, Nothing, Notice, type PanelContext, PanelShell, Search, Split, StaleNote, Toolbar, Waiting } from "./ui";

// Traces: the worktree's requests, newest first, and one of them opened as
// a waterfall: every span in the order it ran, the SQL each database call
// sent (by its shape) and the logs written while it ran.

const PAGE = 100;

export function TracesPanel(props: WorktreePanelProps) {
  return (
    <PanelShell {...props} panel="traces">
      {(ctx) => <Traces ctx={ctx} />}
    </PanelShell>
  );
}

function Traces({ ctx }: { ctx: PanelContext }) {
  const [search, setSearch] = useState("");
  const q = useDebounced(search);
  const [errors, setErrors] = useState(false);
  const [open, setOpen] = useState<string>();
  useJump("traces", ctx.box, ctx.source, setOpen);

  const list = usePaged<TraceRow>((before) => ctx.api.traces(ctx.scope(), { q, errors, limit: PAGE, before }), [ctx.box, ctx.source, ctx.window.value, q, errors].join("\n"), {
    size: PAGE,
    every: ctx.window.every,
    active: ctx.active,
    id: (t) => t.trace_id,
    cursor: (t) => t.cursor,
    time: (t) => t.start_ms,
    sinceMs: () => ctx.scope().sinceMs,
    onFault: ctx.onFault,
  });
  const rows = list.rows ?? [];
  const max = useMemo(() => rows.reduce((m, t) => Math.max(m, t.duration_ms), 1e-9), [rows]);

  return (
    <>
      <Toolbar ctx={ctx} note={list.rows ? `${count(rows.length)} ${list.more ? "newest" : rows.length === 1 ? "trace" : "traces"}` : undefined}>
        <Search value={search} onChange={setSearch} placeholder="Filter by name" />
        <FilterChip pressed={errors} onPressedChange={setErrors}>
          Failed
        </FilterChip>
      </Toolbar>
      <StaleNote fault={list.rows ? list.fault : undefined} />
      <Split
        list={
          !list.rows ? (
            <Waiting fault={list.fault} />
          ) : rows.length === 0 ? (
            <Nothing ctx={ctx} what="traces" filtered={!!q || errors} />
          ) : (
            <div className="min-h-0 flex-1 overflow-auto" data-testid="leadline-list">
              <ul>
                {rows.map((t) => (
                  <li key={t.trace_id} className="border-b">
                    <button
                      type="button"
                      data-testid="leadline-row"
                      aria-current={t.trace_id === open ? "true" : undefined}
                      className={cn("flex h-9 w-full items-center gap-3 px-3 text-left outline-none hover:bg-accent/50 focus-visible:bg-accent/60", t.trace_id === open && "bg-accent/60")}
                      onClick={() => setOpen(t.trace_id === open ? undefined : t.trace_id)}
                    >
                      <span className="shrink-0 font-mono text-muted-foreground text-xs tabular-nums">{clock(t.start_ms)}</span>
                      <span role="img" aria-label={t.errors > 0 ? "Failed" : "Ok"} className={cn("size-1.5 shrink-0 rounded-full", t.errors > 0 ? "bg-destructive" : "bg-success")} />
                      <span className="min-w-0 flex-1 truncate">{t.name}</span>
                      <Command service={t.service} />
                      {t.queries > 0 && <span className="hidden shrink-0 text-muted-foreground text-xs tabular-nums @md:block">{plural(t.queries, "query", "queries")}</span>}
                      {!open && (
                        <span className="hidden w-20 shrink-0 @xl:flex">
                          <Meter value={t.duration_ms} max={max} tone={t.errors > 0 ? "bad" : undefined} />
                        </span>
                      )}
                      <span className="w-16 shrink-0 text-right tabular-nums">{ms(t.duration_ms)}</span>
                    </button>
                  </li>
                ))}
              </ul>
              {list.more && (
                <More onClick={list.loadMore} loading={list.loadingMore}>
                  Show older traces
                </More>
              )}
            </div>
          )
        }
        detail={open && <TraceView key={open} ctx={ctx} id={open} onClose={() => setOpen(undefined)} />}
      />
    </>
  );
}

// A statement seen twice, and BEGIN and its like, are in the trace but are
// not queries of their own.
const ROLE: Record<string, { word: string; why: string }> = {
  duplicate: { word: "seen twice", why: "The app's own report of a statement Leadline also recorded off the wire. It is counted once, on the recorded span." },
  control: { word: "control", why: "Transaction and session control (BEGIN, COMMIT, SET). In the trace and in database time, not counted among the queries." },
};

// waterfall is the trace's spans in the order they are drawn: the tree
// walked from each root, children in start order.
function waterfall(d: TraceDetail): { span: Span; depth: number }[] {
  const byId = new Map(d.spans.map((s) => [s.span_id, s]));
  const out: { span: Span; depth: number }[] = [];
  const walk = (nodes: TreeNode[], depth: number) => {
    for (const n of nodes) {
      const span = byId.get(n.span_id);
      if (span) out.push({ span, depth });
      walk(n.children ?? [], depth + 1);
    }
  };
  walk(d.tree ?? [], 0);
  return out.length ? out : d.spans.map((span) => ({ span, depth: span.depth ?? 0 }));
}

function TraceView({ ctx, id, onClose }: { ctx: PanelContext; id: string; onClose(): void }) {
  // A trace grows while its request runs: spans are exported as they end.
  const live = useLive(() => ctx.api.trace(ctx.box, id), `${ctx.box}\n${id}`, { every: ctx.window.every, active: ctx.active, onFault: ctx.onFault });
  const [picked, setPicked] = useState<string>();
  const d = live.data;
  const rows = useMemo(() => (d ? waterfall(d) : []), [d]);

  if (!d) {
    return (
      <>
        <DetailHeader label="Close the trace" onClose={onClose}>
          <span className="truncate font-medium">Trace</span>
        </DetailHeader>
        {live.fault?.code === "not_found" ? <Notice icon="Hourglass" title="This trace is no longer held" body="Leadline drops what is older than it keeps, and this trace has gone." /> : <Waiting fault={live.fault} onRetry={live.refresh} />}
      </>
    );
  }

  const start = Math.min(...d.spans.map((s) => s.start_ms));
  const end = Math.max(...d.spans.map((s) => s.start_ms + s.duration_ms));
  const total = Math.max(end - start, 1e-6);
  const root = rows[0]?.span;
  const queries = d.spans.filter((s) => s.db_role === "query").length;
  const span = d.spans.find((s) => s.span_id === picked);

  return (
    <>
      <DetailHeader
        label="Close the trace"
        onClose={onClose}
        actions={
          <Tip label={`Trace ${id}`}>
            <span className="hidden shrink-0 font-mono text-muted-foreground text-xs @lg:block">{id.slice(0, 12)}</span>
          </Tip>
        }
      >
        <span className="truncate font-medium">{root?.name ?? "Trace"}</span>
        <span className="shrink-0 tabular-nums">{ms(total)}</span>
        <span className="hidden truncate text-muted-foreground text-xs @md:block">
          {plural(d.spans.length, "span")}
          {queries > 0 && `, ${plural(queries, "query", "queries")}`}, {plural(d.logs.length, "log")}
        </span>
      </DetailHeader>
      <div className="min-h-0 flex-1 overflow-auto" data-testid="leadline-detail">
        <ol aria-label="Spans, in the order they ran">
          {rows.map(({ span: s, depth }) => {
            const mark = s.db_role && ROLE[s.db_role];
            const label = s.db_statement ? shapeOf(s.db_statement) : s.name;
            return (
              <li key={s.span_id}>
                <button
                  type="button"
                  data-testid="leadline-span"
                  aria-expanded={s.span_id === picked}
                  className={cn("flex h-7 w-full items-center gap-2 px-3 text-left outline-none hover:bg-accent/50 focus-visible:bg-accent/60", s.span_id === picked && "bg-accent/60")}
                  onClick={() => setPicked(s.span_id === picked ? undefined : s.span_id)}
                >
                  <span className={cn("flex w-[46%] min-w-0 shrink-0 items-center gap-1.5", mark && "text-muted-foreground")} style={{ paddingLeft: Math.min(depth, 8) * 12 }}>
                    {s.db_system && <Icon name="Database" className={cn("size-3 shrink-0 text-info-foreground", mark && "opacity-60")} />}
                    <span className={cn("truncate", s.db_system && "font-mono text-xs")}>{label}</span>
                    {mark && (
                      <Tip label={mark.why}>
                        <span className="shrink-0 rounded-sm border px-1 text-[10px] leading-[14px]">{mark.word}</span>
                      </Tip>
                    )}
                  </span>
                  <span className="relative h-2.5 min-w-0 flex-1" aria-hidden>
                    <span
                      className={cn("absolute top-0 h-full rounded-[2px]", s.status === "error" ? "bg-destructive" : s.db_system ? "bg-info" : "bg-primary/70", mark && "opacity-40")}
                      style={{ left: `${((s.start_ms - start) / total) * 100}%`, width: `max(2px, ${(s.duration_ms / total) * 100}%)` }}
                    />
                  </span>
                  <span className={cn("w-16 shrink-0 text-right text-xs tabular-nums", s.status === "error" ? "text-destructive-foreground" : "text-muted-foreground")}>{ms(s.duration_ms)}</span>
                </button>
                {s.span_id === picked && span && <SpanInfo span={span} />}
              </li>
            );
          })}
        </ol>
        <section className="mt-2 border-t px-3 py-3">
          <h3 className="mb-1.5 font-medium text-sm">Logs written during this trace</h3>
          {d.logs.length === 0 ? (
            <None>It wrote no logs.</None>
          ) : (
            <ul className="space-y-0.5 font-mono text-xs" data-testid="leadline-trace-logs">
              {d.logs.map((l, i) => (
                <li key={i} className="flex gap-3">
                  <span className="w-16 shrink-0 text-right text-muted-foreground tabular-nums">+{ms(Math.max(0, l.ts_ms - start))}</span>
                  <span className={cn("w-10 shrink-0", severityTone(l.severity))}>{l.severity}</span>
                  <span className="min-w-0 break-words">{l.body}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </>
  );
}

export const severityTone = (severity: string) => (severity === "error" || severity === "fatal" ? "text-destructive-foreground" : severity === "warn" ? "text-warning-foreground" : severity === "info" ? "text-info-foreground" : "text-muted-foreground");

// The attributes that hold a statement's values or its text as sent: the
// shape is shown in their place.
const HIDDEN = new Set(["db.query.parameters", "db.query.text", "db.statement", "db.query.parameter"]);

function SpanInfo({ span }: { span: Span }) {
  const attrs = Object.entries(span.attrs ?? {})
    .filter(([k]) => !HIDDEN.has(k) && !k.startsWith("db.query.parameter."))
    .sort(([a], [b]) => a.localeCompare(b));
  const facts = [span.service, span.kind, ms(span.duration_ms), span.entry && "counted as a request", span.db_role === "query" && "counted as a query", span.db_server && `to ${span.db_server}`].filter(Boolean);
  return (
    <div className="border-y bg-muted/30 px-3 py-2.5 text-xs" data-testid="leadline-span-info">
      <div className="mb-1.5 flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="font-medium">{span.name}</span>
        <span className="text-muted-foreground">{facts.join(" · ")}</span>
        {span.http_status > 0 && (
          <Badge variant={span.http_status >= 500 ? "error" : "outline"} size="sm">
            {span.http_status}
          </Badge>
        )}
      </div>
      {span.status === "error" && <p className="mb-1.5 text-destructive-foreground">{span.status_message || "It ended in an error."}</p>}
      {span.db_statement && (
        <>
          <pre className="mb-1 whitespace-pre-wrap break-words rounded-md border bg-background p-2 font-mono">{shapeOf(span.db_statement)}</pre>
          <p className="mb-2 text-muted-foreground">Shown by its shape: a <code>?</code> is where a value was.</p>
        </>
      )}
      {attrs.length > 0 && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5 font-mono">
          {attrs.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="min-w-0 break-all">{typeof v === "string" ? v : JSON.stringify(v)}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

// TraceLink is the way from a row elsewhere to its trace.
export function TraceLink({ onClick }: { onClick(): void }) {
  return (
    <Button size="xs" variant="ghost" className="shrink-0 font-sans text-muted-foreground" onClick={onClick}>
      Trace
      <Icon name="ArrowUpRight" className="size-3" />
    </Button>
  );
}
