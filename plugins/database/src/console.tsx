import { Button, Icon, Kbd, Menu, MenuItem, MenuPopup, MenuSeparator, MenuTrigger, Spinner, SqlEditor, Tip } from "@berth/plugin/ui";
import { useEffect, useMemo, useRef, useState } from "react";

import { type Fault, faultOf, type QueryResult, QUERY_LIMIT, type Schema } from "./api";
import type { Ctx } from "./context";
import { Grid, type GridColumn, type GridRow } from "./grid";
import { ValueSheet, type Viewed } from "./sheet";
import { lineAndColumn, markAt, oneLine, type Ran, remember, type Statement, statementAt } from "./statements.ts";
import { Line, ms, Notice, plural } from "./ui";

// The SQL console: an editor, Run for the statement under the cursor (or
// the selection), and what came back in the same grid the tables use. It is
// under the same lock as the grid: a statement that writes is refused until
// writes are unlocked. What was run is remembered for the worktree, by its
// text alone: never what it returned.

interface Run {
  state: "running" | "done" | "failed";
  statement: Statement;
  // What was sent: the statement, or it wrapped (EXPLAIN …).
  sent: string;
  result?: QueryResult;
  fault?: Fault;
}

const ago = (at: number, now: number) => {
  const s = Math.max(0, now - at) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86_400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86_400)}d ago`;
};

export function Console({ ctx, schemas, put }: { ctx: Ctx; schemas: Schema[]; put?: { sql: string; n: number } }) {
  const { api, berth, box, conn, engine, caps, writes } = ctx;
  const historyKey = `history:${box}/${ctx.source}`;
  const [history, setHistory] = useState<Ran[]>(() => berth.storage.get<Ran[]>(historyKey, []));
  const [text, setText] = useState("");
  const selection = useRef({ from: 0, to: 0 });
  const [run, setRun] = useState<Run>();
  const [viewed, setViewed] = useState<Viewed>();
  const asked = useRef(0);

  // A statement handed over (a table's Query): it takes the editor.
  useEffect(() => {
    if (!put) return;
    setText(put.sql);
    selection.current = { from: 0, to: 0 };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [put?.n]);

  // Completion: every table by its name, and by its schema's where it has one.
  const names = useMemo(() => {
    const out: Record<string, string[]> = {};
    for (const s of schemas) {
      for (const t of s.tables) {
        const cols = t.columns.map((c) => c.name);
        if (!caps.schemas || !s.name || s.name === engine.defaultSchema) out[t.name] = cols;
        if (caps.schemas && s.name) out[`${s.name}.${t.name}`] = cols;
      }
    }
    return out;
  }, [schemas, caps.schemas, engine.defaultSchema]);

  const go = async (at: { from: number; to: number }, wrap?: (statement: string) => string) => {
    const statement = statementAt(text, at);
    if (!statement) return;
    const sent = wrap ? wrap(statement.text) : statement.text;
    const mine = ++asked.current;
    setRun({ state: "running", statement, sent });
    const next = remember(history, sent, Date.now());
    setHistory(next);
    berth.storage.set(historyKey, next);
    try {
      const result = await api.query(box, conn.id, sent, QUERY_LIMIT);
      if (asked.current === mine) setRun({ state: "done", statement, sent, result });
    } catch (err) {
      const fault = faultOf(err);
      if (fault.code === "read_only" && writes.on) writes.refused();
      ctx.onFault(fault);
      if (asked.current === mine) setRun({ state: "failed", statement, sent, fault });
    }
  };

  // Where the database stopped, in the editor: it counts from the start of
  // what was sent, which may begin with a word the console added.
  const mark = useMemo(() => {
    if (run?.state !== "failed") return undefined;
    const from = markAt(run.statement, run.sent, run.fault?.position);
    return from === undefined ? undefined : { from };
  }, [run]);
  const where = useMemo(() => (mark ? lineAndColumn(text, mark.from) : undefined), [mark, text]);

  const result = run?.result;
  const columns = useMemo<GridColumn[]>(() => (result?.columns ?? []).map((c) => ({ name: c.name, type: c.type, family: engine.family(c.type) })), [result, engine]);
  const rows = useMemo<GridRow[]>(() => {
    const cuts = new Map<number, Set<number>>();
    for (const [r, c] of result?.cut ?? []) cuts.set(r, (cuts.get(r) ?? new Set()).add(c));
    return (result?.rows ?? []).map((cells, i) => ({ id: String(i), ref: i, cells, cut: cuts.get(i) }));
  }, [result]);
  const now = Date.now();
  const running = run?.state === "running";

  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="database-console">
      <SqlEditor
        className="h-44 shrink-0 border-b"
        label="SQL to run"
        value={text}
        onChange={setText}
        onSelect={(s) => (selection.current = s)}
        onRun={(s) => void go(s)}
        schema={names}
        defaultSchema={caps.schemas ? engine.defaultSchema : undefined}
        dialect={caps.dialect}
        error={mark}
        placeholder={`A statement to run against ${conn.database}`}
      />
      <div className="flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b px-3 py-1.5">
        <Tip label="Run the statement under the cursor, or what is selected">
          <Button size="xs" disabled={!text.trim() || running} onClick={() => void go(selection.current)}>
            <Icon name="Play" className="size-3.5" />
            Run
            <Kbd className="ml-0.5 border-primary-foreground/30 bg-transparent text-primary-foreground">⌘↵</Kbd>
          </Button>
        </Tip>
        {engine.explain && (
          <Tip label="Show the statement's plan, without running it">
            <Button size="xs" variant="outline" disabled={!text.trim() || running} onClick={() => void go(selection.current, engine.explain)}>
              Explain
            </Button>
          </Tip>
        )}
        <Menu>
          <MenuTrigger render={<Button size="xs" variant="ghost" disabled={history.length === 0} />}>
            <Icon name="History" className="size-3.5" />
            History
          </MenuTrigger>
          <MenuPopup align="start" className="max-h-96 w-[30rem] max-w-[90vw]" data-testid="database-history">
            {history.slice(0, 25).map((h) => (
              <MenuItem key={h.sql} onClick={() => setText(h.sql)}>
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{oneLine(h.sql)}</span>
                <span className="ml-3 shrink-0 text-muted-foreground text-xs">{ago(h.at, now)}</span>
              </MenuItem>
            ))}
            <MenuSeparator />
            <MenuItem
              onClick={() => {
                setHistory([]);
                berth.storage.set(historyKey, []);
              }}
            >
              <span className="text-muted-foreground">Forget what was run in {ctx.worktree}</span>
            </MenuItem>
          </MenuPopup>
        </Menu>
        <span className="ml-auto flex items-center gap-2 text-muted-foreground text-xs tabular-nums" data-testid="database-result" role="status">
          {running ? (
            <>
              <Spinner className="size-3" /> Running…
            </>
          ) : result ? (
            <>
              <span className="font-medium font-mono text-foreground">{result.command}</span>
              <span>·</span>
              <span>{result.columns.length ? plural(result.row_count, "row") : `${plural(result.row_count, "row")} changed`}</span>
              <span>·</span>
              <span>{ms(result.duration_ms)}</span>
            </>
          ) : !writes.can ? (
            "Read-only"
          ) : !writes.on ? (
            "Reads only, until writes are unlocked"
          ) : (
            "Writes are on"
          )}
        </span>
      </div>
      {run?.state === "failed" && run.fault && (
        <div role="alert" data-testid="database-error" className="max-h-40 shrink-0 space-y-1 overflow-auto border-b bg-destructive/8 px-3 py-2 text-xs">
          {run.fault.code === "read_only" ? (
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-destructive-foreground">
              <span className="min-w-0 flex-1">
                <b>Not run:</b> {writes.can ? `this statement writes, and writes to ${conn.database} are locked.` : `this statement writes, and ${conn.database} takes no writes through Shipyard.`}
                {run.fault.detail && <span className="mt-0.5 block font-mono text-muted-foreground">{run.fault.detail}</span>}
              </span>
              {writes.can && (
                <Button size="xs" variant="outline" onClick={writes.ask}>
                  Unlock writes…
                </Button>
              )}
            </div>
          ) : (
            // The helper's own sentence, the database's detail, where it
            // stopped, and what the helper suggests.
            <>
              <p className="whitespace-pre-wrap font-mono text-destructive-foreground">{run.fault.message}</p>
              {run.fault.detail && <p className="whitespace-pre-wrap font-mono text-muted-foreground">{run.fault.detail}</p>}
              {where && <p className="text-muted-foreground">At {where}, underlined in the editor.</p>}
              {run.fault.hint && <p className="whitespace-pre-wrap text-muted-foreground">{run.fault.hint}</p>}
            </>
          )}
        </div>
      )}
      {result?.truncated && (
        <Line testId="database-truncated">
          Only the first {plural(result.rows.length, "row")} are shown: the statement returned more than the console keeps. Narrow it with WHERE or LIMIT to see the others.
        </Line>
      )}
      {result && result.notices.length > 0 && (
        <Line tone="note" testId="database-notices">
          {result.notices.map((notice, i) => (
            <span key={i} className="block font-mono">
              {notice}
            </span>
          ))}
        </Line>
      )}
      {!run || (run.state === "failed" && !result) ? (
        run ? (
          <div className="flex-1" />
        ) : (
          <Notice
            icon="SquareTerminal"
            title="Nothing run yet"
            body={
              <>
                Write a statement above and press <Kbd>⌘↵</Kbd>. With several in the editor, the one the cursor is in runs; select text to run just that. {writes.can ? `${conn.database} is read-only until you unlock writes.` : `${conn.database} is read-only.`}
              </>
            }
          />
        )
      ) : !result ? (
        <div className="flex-1" aria-busy="true" />
      ) : result.columns.length === 0 ? (
        <Notice icon="Check" title={`${result.command}: ${plural(result.row_count, "row")} changed`} body="The statement returned no rows to show." />
      ) : rows.length === 0 ? (
        <Notice icon="table-2" title="No rows" body={`${result.command} returned ${plural(result.columns.length, "column")} and no rows.`} />
      ) : (
        <Grid
          label="Results"
          columns={columns}
          rows={rows}
          onView={(row, col) => setViewed({ column: columns[col].name, type: columns[col].type, family: columns[col].family, row: `Result row ${(row.ref ?? 0) + 1}`, cell: row.cells[col] ?? null, cut: row.cut?.has(col) })}
        />
      )}
      <ValueSheet viewed={viewed} onClose={() => setViewed(undefined)} />
    </div>
  );
}
