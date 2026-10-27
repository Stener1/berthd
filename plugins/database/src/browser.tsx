import { Badge, Button, Card, CardHeader, CardPanel, Icon, Input, Menu, MenuItem, MenuPopup, MenuTrigger, PickOne, Sheet, SheetDescription, SheetFooter, SheetHeader, SheetPanel, SheetPopup, SheetTitle, Tip, cn } from "@berth/plugin/ui";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { type Cell, type Change, type Edited, type Fault, faultOf, MOST, MOST_CHANGES, PAGE, type Order, type Rows, type Schema, type Table } from "./api";
import type { Ctx } from "./context";
import { quote } from "./engines/families.ts";
import { tableName } from "./engines/index.ts";
import { type Draft, filtersOf, jumpFilters, nextOrder, opLabel, opsFor, takesValue } from "./filters.ts";
import { Grid, type GridColumn, type GridRow } from "./grid";
import { useRead } from "./live";
import { kindOf, rulesFor } from "./rules.ts";
import { ValueSheet, type Viewed } from "./sheet";
import { addRow, changes, count as countOf, dropRows, keyed, keyOf, keyText, NOTHING, removeNew, removeRows, review, type Row, rowId, setCell, setNew, type Staged, summary, undo } from "./staged.ts";
import { count, Line, Notice, plural, Waiting } from "./ui";

// The table browser: the connection's tables on the left, and the one that
// is open as a grid of its rows or as its structure.

interface Open {
  schema: string;
  table: string;
  // The filters it opens with: a foreign key's jump.
  filters?: Draft[];
  // A new number for each opening, so the same table opens afresh.
  n: number;
}

const tableKey = (schema: string, table: string) => `${schema}\n${table}`;

// The change a refusal is about: the helper counts them from 1 ("Change 2: …").
const changeOf = (message: string) => Number(/^Change (\d+)\b/.exec(message)?.[1] ?? 0);
const keyOfChange = (c?: Change) => (c && c.op !== "insert" ? c.key : undefined);

export function Browser({ ctx, schemas, onReload, toConsole }: { ctx: Ctx; schemas: Schema[]; onReload(): void; toConsole(sql: string): void }) {
  const [open, setOpen] = useState<Open>();
  // What is staged, by table: it stays while another table is looked at.
  const [staged, setStaged] = useState<Record<string, Staged>>({});
  const table = open && schemas.find((s) => s.name === open.schema)?.tables.find((t) => t.name === open.table);
  const key = open ? tableKey(open.schema, open.table) : "";
  const setOne = useCallback((k: string, next: Staged) => setStaged((all) => ({ ...all, [k]: next })), []);
  const go = useCallback((schema: string, name: string, filters?: Draft[]) => setOpen((o) => ({ schema, table: name, filters, n: (o?.n ?? 0) + 1 })), []);

  return (
    <div className="flex min-h-0 flex-1">
      <Tree ctx={ctx} schemas={schemas} open={open} staged={staged} onOpen={go} onReload={onReload} />
      <div className={cn("min-h-0 min-w-0 flex-1 flex-col", open ? "flex" : "hidden @2xl:flex")}>
        {open && table ? (
          <TableView key={`${key}\n${open.n}`} ctx={ctx} schema={open.schema} table={table} initial={open.filters} staged={staged[key] ?? NOTHING} setStaged={(s) => setOne(key, s)} onBack={() => setOpen(undefined)} onJump={go} toConsole={toConsole} />
        ) : open ? (
          <Notice icon="table-2" title={`${open.table} isn't there any more`} body="The table was dropped or renamed since the list was read." actions={<Button size="sm" variant="outline" onClick={() => setOpen(undefined)}>Back to the tables</Button>} />
        ) : (
          <Notice icon="table-2" title="Pick a table" body={`Its rows open here. ${ctx.conn.database} is read-only until you unlock writes.`} />
        )}
      </div>
    </div>
  );
}

function Tree({ ctx, schemas, open, staged, onOpen, onReload }: { ctx: Ctx; schemas: Schema[]; open?: Open; staged: Record<string, Staged>; onOpen(schema: string, table: string): void; onReload(): void }) {
  const [text, setText] = useState("");
  const q = text.trim().toLowerCase();
  // Narrowed, a schema with nothing left is not shown; otherwise an empty one is listed, as the database has it.
  const shown = schemas.map((s) => ({ ...s, tables: s.tables.filter((t) => !q || t.name.toLowerCase().includes(q) || (ctx.caps.schemas && `${s.name}.${t.name}`.toLowerCase().includes(q))) })).filter((s) => s.tables.length > 0 || (!q && ctx.caps.schemas));
  const total = schemas.reduce((n, s) => n + s.tables.length, 0);
  return (
    <nav aria-label="Tables" data-testid="database-tree" className={cn("min-h-0 w-full shrink-0 flex-col @2xl:w-60 @2xl:border-r", open ? "hidden @2xl:flex" : "flex")}>
      <div className="flex shrink-0 items-center gap-1.5 border-b p-2">
        <Input className="min-w-0 flex-1" size="sm" type="search" placeholder="Filter tables" aria-label="Filter tables" value={text} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setText(e.target.value)} />
        <Tip label="Read the tables again">
          <Button size="icon-sm" variant="ghost" aria-label="Read the tables again" onClick={onReload}>
            <Icon name="RefreshCw" className="size-3.5" />
          </Button>
        </Tip>
      </div>
      <div className="min-h-0 flex-1 overflow-auto py-1">
        {total === 0 ? (
          <p className="px-3 py-6 text-center text-muted-foreground text-xs">{ctx.conn.database} has no tables yet.</p>
        ) : shown.length === 0 ? (
          <p className="px-3 py-6 text-center text-muted-foreground text-xs">No table matches “{text.trim()}”.</p>
        ) : (
          shown.map((s) => (
            <section key={s.name} className="pb-1">
              {/* An engine without schemas has one list, with no heading. */}
              {ctx.caps.schemas && <h3 className="px-3 pt-2 pb-1 font-medium text-muted-foreground text-xs">{s.name}</h3>}
              {s.tables.length === 0 && <p className="px-3 py-1 text-muted-foreground text-xs">No tables.</p>}
              <ul>
                {s.tables.map((t) => {
                  const on = open?.schema === s.name && open.table === t.name;
                  const kind = kindOf(t.kind);
                  const n = countOf(staged[tableKey(s.name, t.name)] ?? NOTHING);
                  return (
                    <li key={t.name}>
                      <button
                        type="button"
                        data-testid="database-table"
                        aria-current={on ? "true" : undefined}
                        onClick={() => onOpen(s.name, t.name)}
                        className={cn("flex h-7 w-full items-center gap-2 px-3 text-left outline-none hover:bg-accent/50 focus-visible:bg-accent/60", on && "bg-accent/60 font-medium")}
                      >
                        <Icon name={kind.icon} className="size-3.5 shrink-0 text-muted-foreground" />
                        <span className="min-w-0 flex-1 truncate">{t.name}</span>
                        {n > 0 && (
                          <Tip label={`${plural(n, "staged change")}, not applied`}>
                            <span role="img" aria-label={`${plural(n, "staged change")}, not applied`} className="size-1.5 shrink-0 rounded-full bg-info" />
                          </Tip>
                        )}
                        <span className="shrink-0 text-muted-foreground text-xs tabular-nums">{t.kind === "table" ? (t.rows_estimate >= 0 ? `~${count(t.rows_estimate)}` : "") : kind.short}</span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </section>
          ))
        )}
      </div>
    </nav>
  );
}

const NO_ROWS = new Set<string>();

function TableView({ ctx, schema, table, initial, staged, setStaged, onBack, onJump, toConsole }: { ctx: Ctx; schema: string; table: Table; initial?: Draft[]; staged: Staged; setStaged(s: Staged): void; onBack(): void; onJump(schema: string, table: string, filters?: Draft[]): void; toConsole(sql: string): void }) {
  const { api, box, conn, engine, caps, writes } = ctx;
  const [mode, setMode] = useState<"rows" | "structure">("rows");
  const [offset, setOffset] = useState(0);
  // Where each page before this one started: a page can stop short of its
  // size, so the way back is remembered, not worked out.
  const [trail, setTrail] = useState<number[]>([]);
  const [size, setSize] = useState(PAGE);
  const [order, setOrder] = useState<Order[]>([]);
  const [drafts, setDrafts] = useState<Draft[]>(initial ?? []);
  const nextId = useRef((initial?.length ?? 0) + 1);
  const [picked, setPicked] = useState<ReadonlySet<string>>(NO_ROWS);
  const [viewed, setViewed] = useState<Viewed>();
  const [reviewing, setReviewing] = useState(false);
  const [applying, setApplying] = useState(false);
  const [refused, setRefused] = useState<Fault>();
  // The staged rows an edit found gone.
  const [gone, setGone] = useState<string[]>([]);
  const [done, setDone] = useState<Edited>();

  const name = tableName(engine, schema, table.name);
  const rules = useMemo(() => rulesFor(table, engine, caps), [table, engine, caps]);
  const familyOf = useCallback((column: string) => engine.family(table.columns.find((c) => c.name === column)?.type ?? ""), [engine, table]);
  const filters = useMemo(() => filtersOf(drafts, familyOf), [drafts, familyOf]);
  const request = useMemo(() => ({ schema, table: table.name, limit: size, offset, order_by: order, filters }), [schema, table.name, size, offset, order, filters]);
  const read = useRead<Rows>(() => api.rows(box, conn.id, request), JSON.stringify([conn.id, request]), { active: ctx.active && mode === "rows" });
  const data = read.data;
  useEffect(() => {
    if (read.fault) ctx.onFault(read.fault);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [read.fault]);

  const pk = data?.primary_key ?? table.primary_key;
  const columns = useMemo<GridColumn[]>(
    () =>
      (data?.columns ?? table.columns).map((c) => {
        const known = table.columns.find((k) => k.name === c.name);
        return { name: c.name, type: c.type, family: engine.family(c.type), key: pk.includes(c.name), nullable: known?.nullable ?? true, fk: table.foreign_keys.find((f) => f.columns.includes(c.name)), generated: known?.default?.startsWith("generated always") };
      }),
    [data?.columns, table, engine, pk],
  );
  // The rows as they were read, by column, and what each is filed under.
  const originals = useMemo<Row[]>(() => (data?.rows ?? []).map((r) => Object.fromEntries(columns.map((c, i) => [c.name, r[i] ?? null]))), [data?.rows, columns]);
  const cuts = useMemo(() => {
    const out = new Map<number, Set<number>>();
    for (const [r, c] of data?.cut ?? []) out.set(r, (out.get(r) ?? new Set()).add(c));
    return out;
  }, [data?.cut]);
  const keyColumns = useMemo(() => pk.map((k) => columns.findIndex((c) => c.name === k)), [pk, columns]);

  const rows = useMemo<GridRow[]>(() => {
    const fresh = staged.inserts.map((i): GridRow => ({ id: i.id, state: "new", cells: columns.map((c) => i.set[c.name] ?? null), unset: new Set(columns.flatMap((c, at) => (c.name in i.set ? [] : [at]))), changed: new Set(columns.flatMap((c, at) => (c.name in i.set ? [at] : []))) }));
    const loaded = (data?.rows ?? []).map((cells, at): GridRow => {
      // Without a key, a row is its place on the page.
      const id = pk.length ? rowId(originals[at], pk) : `row-${at}`;
      const cut = cuts.get(at);
      if (staged.deletes[id]) return { id, ref: at, cells, cut, state: gone.includes(id) ? "gone" : "deleted" };
      const update = staged.updates[id];
      if (!update) return { id, ref: at, cells, cut };
      return { id, ref: at, cut, state: gone.includes(id) ? "gone" : undefined, cells: cells.map((v, i) => (columns[i].name in update.set ? update.set[columns[i].name] : v)), changed: new Set(columns.flatMap((c, i) => (c.name in update.set ? [i] : []))) };
    });
    return [...fresh, ...loaded];
  }, [data?.rows, staged, columns, originals, cuts, pk, gone]);

  const editing = writes.on && (rules.rows || rules.inserts);
  const canEdit = useCallback(
    (row: GridRow, col: number) => {
      // A column the database always generates takes nothing from anyone.
      if (!editing || columns[col]?.generated) return false;
      if (row.state === "new") return rules.inserts;
      // A row whose key arrived cut can't be named.
      return rules.rows && row.state !== "deleted" && !row.cut?.has(col) && !keyColumns.some((k) => row.cut?.has(k));
    },
    [editing, rules, keyColumns, columns],
  );
  const onEdit = useCallback(
    (row: GridRow, col: number, value: Cell) => {
      setDone(undefined);
      if (row.state === "new") setStaged(setNew(staged, row.id, columns[col].name, value));
      else if (row.ref !== undefined) setStaged(setCell(staged, pk, originals[row.ref], columns[col].name, value));
    },
    [staged, setStaged, columns, pk, originals],
  );
  const titleOf = useCallback((row: GridRow) => (row.state === "new" ? "New row" : row.ref !== undefined && pk.length ? keyText(keyOf(originals[row.ref], pk)) : `Row ${offset + (row.ref ?? 0) + 1}`), [originals, pk, offset]);
  const onView = useCallback(
    (row: GridRow, col: number) => {
      const c = columns[col];
      setViewed({ column: c.name, type: c.type, family: c.family, row: `${name}, ${titleOf(row)}`, cell: row.cells[col] ?? null, cut: row.cut?.has(col), nullable: c.nullable, unset: row.unset?.has(col), onStage: canEdit(row, col) ? (v) => onEdit(row, col, v) : undefined });
    },
    [columns, name, titleOf, canEdit, onEdit],
  );
  const jump = useCallback(
    (row: GridRow, col: number) => {
      const fk = columns[col].fk;
      const to = fk && row.ref !== undefined && jumpFilters(fk, originals[row.ref]);
      if (fk && to) onJump(fk.ref_schema, fk.ref_table, to);
    },
    [columns, originals, onJump],
  );

  const first = () => {
    setPicked(NO_ROWS);
    setTrail([]);
    setOffset(0);
  };
  // The next page starts after the rows that came, however many that was.
  const next = () => {
    if (!data) return;
    setPicked(NO_ROWS);
    setTrail([...trail, offset]);
    setOffset(offset + data.rows.length);
  };
  const previous = () => {
    setPicked(NO_ROWS);
    setOffset(trail[trail.length - 1] ?? 0);
    setTrail(trail.slice(0, -1));
  };
  const narrow = (next: Draft[]) => {
    setDrafts(next);
    first();
  };
  const addFilter = (column: string) => {
    const ops = opsFor(caps.operators, familyOf(column));
    if (ops.length === 0) return;
    setMode("rows");
    setDrafts((d) => [...d, { id: nextId.current++, column, op: ops[0] }]);
  };

  const n = countOf(staged);
  const removePicked = () => {
    const ids = [...picked];
    const fresh = ids.filter((id) => staged.inserts.some((i) => i.id === id));
    const loaded = rows.filter((r) => r.ref !== undefined && picked.has(r.id)).map((r) => originals[r.ref!]);
    setStaged(removeRows(removeNew(staged, fresh), pk, rules.rows ? loaded : []));
    setPicked(NO_ROWS);
    setDone(undefined);
  };

  // Which of the rows an edit names are no longer there: each is asked for
  // by its key. The helper says only that one is missing.
  const missing = async (): Promise<string[]> => {
    const asked = keyed(staged).slice(0, 40);
    const found = await Promise.all(
      asked.map((k) =>
        api
          .rows(box, conn.id, { schema, table: table.name, limit: 1, filters: Object.entries(k.key).map(([column, value]) => ({ column, op: "=", value })) })
          .then((r) => r.rows.length > 0)
          .catch(() => true),
      ),
    );
    return asked.filter((_, i) => !found[i]).map((k) => k.id);
  };

  const apply = async () => {
    setRefused(undefined);
    setGone([]);
    if (countOf(staged) > MOST_CHANGES) {
      setRefused({ message: `One edit takes at most ${MOST_CHANGES.toLocaleString("en-US")} changes, and ${countOf(staged).toLocaleString("en-US")} are staged.`, hint: "Take some back in Review, apply, and stage the rest again." });
      setReviewing(false);
      return;
    }
    setApplying(true);
    try {
      const result = await api.edit(box, conn.id, schema, table.name, changes(staged));
      setStaged(NOTHING);
      setReviewing(false);
      setPicked(NO_ROWS);
      setDone(result);
      read.reload();
    } catch (err) {
      const fault = faultOf(err);
      if (fault.code === "read_only") writes.refused();
      if (fault.code === "row_conflict") {
        const found = await missing();
        // The helper names one change by its place in what was sent ("Change
        // 2: …"): that row at least, when asking for each found none.
        const named = keyed(staged).find((k) => JSON.stringify(k.key) === JSON.stringify(keyOfChange(changes(staged)[changeOf(fault.message) - 1])));
        setGone(found.length ? found : named ? [named.id] : []);
      }
      setRefused(fault);
      setReviewing(false);
      ctx.onFault(fault);
    } finally {
      setApplying(false);
    }
  };

  const kind = kindOf(table.kind);
  const lines = useMemo(() => review(staged), [staged]);
  const goneKeys = keyed(staged).filter((k) => gone.includes(k.id));
  const from = offset + 1;
  const to = offset + (data?.rows.length ?? 0);

  return (
    <>
      <div className="flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b px-3 py-1.5" data-testid="database-table-bar">
        <Tip label="Back to the tables">
          <Button size="icon-xs" variant="ghost" aria-label="Back to the tables" className="@2xl:hidden" onClick={onBack}>
            <Icon name="ArrowLeft" className="size-3.5" />
          </Button>
        </Tip>
        <Icon name={kind.icon} className="size-4 shrink-0 text-muted-foreground" />
        <h2 className="truncate font-medium">{name}</h2>
        {table.kind !== "table" && (
          <Badge variant="outline" size="sm" className="shrink-0 font-normal">
            {kind.label}
          </Badge>
        )}
        <PickOne
          label="What to show of the table"
          value={mode}
          onChange={(v) => setMode(v as "rows" | "structure")}
          options={[
            { value: "rows", label: "Rows" },
            { value: "structure", label: "Structure" },
          ]}
        />
        <span className="ml-auto flex flex-wrap items-center gap-1.5">
          {mode === "rows" && editing && picked.size > 0 && (
            <Button size="xs" variant="destructive-outline" onClick={removePicked}>
              <Icon name="trash-2" className="size-3.5" />
              Delete {plural(picked.size, "row")}
            </Button>
          )}
          {mode === "rows" && editing && rules.inserts && (
            <Button size="xs" variant="outline" onClick={() => (setStaged(addRow(staged)[0]), setDone(undefined))}>
              <Icon name="Plus" className="size-3.5" />
              Add row
            </Button>
          )}
          {mode === "rows" && caps.operators.length > 0 && (
            <Menu>
              <MenuTrigger render={<Button size="xs" variant="outline" />}>
                <Icon name="ListFilter" className="size-3.5" />
                Filter
              </MenuTrigger>
              <MenuPopup align="end" className="max-h-80 min-w-44">
                {table.columns.map((c) => (
                  <MenuItem key={c.name} onClick={() => addFilter(c.name)}>
                    <span className="font-mono text-xs">{c.name}</span>
                  </MenuItem>
                ))}
              </MenuPopup>
            </Menu>
          )}
          <Tip label="Write a SELECT for this table in the console">
            <Button size="xs" variant="ghost" onClick={() => toConsole(`SELECT * FROM ${tableName(engine, schema, table.name, true)} LIMIT 100;`)}>
              <Icon name="SquareTerminal" className="size-3.5" />
              Query
            </Button>
          </Tip>
          {mode === "rows" && (
            <Tip label="Read the rows again">
              <Button size="icon-xs" variant="ghost" aria-label="Read the rows again" loading={read.loading && !!data} onClick={read.reload}>
                <Icon name="RefreshCw" className="size-3.5" />
              </Button>
            </Tip>
          )}
        </span>
      </div>
      {rules.why && (
        <Line tone="note" testId="database-browse-only">
          <Icon name="Lock" className="mr-1.5 inline size-3 align-[-1px]" />
          {rules.rows || rules.inserts ? "" : "Browse only. "}
          {rules.why}
        </Line>
      )}
      {mode === "structure" ? (
        <Structure table={table} schema={schema} ctx={ctx} />
      ) : (
        <>
          {drafts.length > 0 && (
            <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 border-b px-3 py-1.5" data-testid="database-filters">
              {drafts.map((d) => (
                <FilterRow key={d.id} draft={d} columns={table.columns.map((c) => c.name)} ops={opsFor(caps.operators, familyOf(d.column))} opsOf={(column) => opsFor(caps.operators, familyOf(column))} onChange={(next) => narrow(drafts.map((x) => (x.id === d.id ? next : x)))} onRemove={() => narrow(drafts.filter((x) => x.id !== d.id))} />
              ))}
              {drafts.length > 1 && (
                <Button size="xs" variant="ghost" className="text-muted-foreground" onClick={() => narrow([])}>
                  Clear all
                </Button>
              )}
            </div>
          )}
          {refused && (
            <Line
              tone="bad"
              testId="database-refused"
              actions={
                <>
                  {refused.code === "row_conflict" && (
                    <Button size="xs" variant="outline" onClick={read.reload}>
                      Reload the rows
                    </Button>
                  )}
                  {goneKeys.length > 0 && (
                    <Button
                      size="xs"
                      variant="outline"
                      onClick={() => {
                        setStaged(dropRows(staged, gone));
                        setGone([]);
                        setRefused(undefined);
                      }}
                    >
                      Drop {goneKeys.length === 1 ? "that change" : `those ${goneKeys.length} changes`}
                    </Button>
                  )}
                  {refused.code === "read_only" && (
                    <Button size="xs" variant="outline" onClick={writes.ask}>
                      Unlock writes…
                    </Button>
                  )}
                  <Button size="xs" variant="ghost" onClick={() => (setRefused(undefined), setGone([]))}>
                    Dismiss
                  </Button>
                </>
              }
            >
              {refused.code === "row_conflict" ? (
                <>
                  <b>Nothing was applied.</b>{" "}
                  {goneKeys.length > 0
                    ? `${goneKeys.length === 1 ? "A row you changed is" : `${goneKeys.length} rows you changed are`} no longer there: ${goneKeys
                        .slice(0, 4)
                        .map((k) => keyText(k.key))
                        .join("; ")}${goneKeys.length > 4 ? `, and ${goneKeys.length - 4} more` : ""}. Something else deleted or re-keyed ${goneKeys.length === 1 ? "it" : "them"} since you read ${name}.`
                    : `${refused.message} Something else changed ${name} since you read it.`}{" "}
                  Your changes are still staged.
                </>
              ) : refused.code === "read_only" ? (
                <>
                  <b>Nothing was applied:</b> writes are locked. Your changes are still staged.
                </>
              ) : (
                <>
                  <b>Nothing was applied.</b> {refused.message}
                  {refused.detail && ` ${refused.detail}`}
                  {refused.hint && refused.code !== "query_failed" && ` ${refused.hint}`} Your changes are still staged.
                </>
              )}
            </Line>
          )}
          {done && !refused && n === 0 && (
            <Line tone="note" testId="database-applied" actions={<Button size="xs" variant="ghost" onClick={() => setDone(undefined)}>Dismiss</Button>}>
              Applied to {name}: {[done.updated && `${plural(done.updated, "row")} updated`, done.inserted && `${plural(done.inserted, "row")} added`, done.deleted && `${plural(done.deleted, "row")} deleted`].filter(Boolean).join(", ") || "nothing changed"}.
            </Line>
          )}
          {!data ? (
            <Waiting fault={read.fault} onRetry={read.reload} title={`Couldn't read ${name}`} />
          ) : (
            <>
              {read.fault && <Line tone="bad">Couldn't read again: {read.fault.message}</Line>}
              {rows.length === 0 ? (
                <Notice icon={filters.length ? "Search" : "table-2"} title={filters.length ? "No rows match" : offset > 0 ? "No more rows" : `${name} is empty`} body={filters.length ? "Nothing in the table matches the filters above." : offset > 0 ? "The page before was the last." : rules.inserts && !editing ? "Unlock writes to add a row." : undefined} />
              ) : (
                <Grid label={`Rows of ${name}`} columns={columns} rows={rows} offset={offset} order={order} onSort={(column) => (setOrder(nextOrder(order, column)), first())} onFilter={caps.operators.length ? addFilter : undefined} canEdit={canEdit} onEdit={editing ? onEdit : undefined} onView={onView} onJump={jump} picked={picked} canPick={(r) => r.state === "new" || (rules.rows && r.state !== "deleted")} onPick={editing ? (ids, on) => setPicked((p) => new Set(on ? [...p, ...ids] : [...p].filter((id) => !ids.includes(id)))) : undefined} />
              )}
            </>
          )}
          {n > 0 && (
            <div className="flex shrink-0 flex-wrap items-center gap-2 border-t bg-muted/50 px-3 py-1.5" data-testid="database-staged" role="status">
              <Icon name="PencilLine" className="size-3.5 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 text-sm">
                <b className="font-medium">{plural(n, "staged change")}</b> <span className="text-muted-foreground">in {name}: {summary(staged)}. Nothing is written until you apply.</span>
              </span>
              <Button size="xs" variant="ghost" onClick={() => (setStaged(NOTHING), setGone([]), setRefused(undefined))}>
                Discard
              </Button>
              <Button size="xs" variant="outline" onClick={() => setReviewing(true)}>
                Review…
              </Button>
              {writes.on ? (
                <Button size="xs" loading={applying} onClick={() => void apply()}>
                  Apply
                </Button>
              ) : (
                <Tip label="Writes are locked: unlock them to apply what is staged">
                  <Button size="xs" onClick={writes.ask}>
                    Unlock to apply…
                  </Button>
                </Tip>
              )}
            </div>
          )}
          {data && (
            <div className="flex min-h-9 shrink-0 items-center gap-2 border-t px-3 py-1 text-muted-foreground text-xs" data-testid="database-pages">
              <span className="tabular-nums" data-testid="database-range">
                {data.rows.length === 0 ? "No rows" : `Rows ${from.toLocaleString("en-US")}–${to.toLocaleString("en-US")}`}
                {filters.length === 0 && data.rows_estimate > 0 && ` of about ${data.rows_estimate.toLocaleString("en-US")}`}
              </span>
              <span className="ml-auto flex items-center gap-1">
                <PickOne
                  label="Rows a page"
                  className="mr-2"
                  value={String(size)}
                  onChange={(v) => (setSize(Number(v)), first())}
                  options={[
                    { value: String(PAGE), label: String(PAGE) },
                    { value: String(MOST), label: String(MOST) },
                  ]}
                />
                <Button size="xs" variant="ghost" disabled={offset === 0} onClick={previous}>
                  <Icon name="ChevronLeft" className="size-3.5" />
                  Previous
                </Button>
                <Button size="xs" variant="ghost" disabled={!data.more || data.rows.length === 0} onClick={next}>
                  Next
                  <Icon name="ChevronRight" className="size-3.5" />
                </Button>
              </span>
            </div>
          )}
        </>
      )}
      <ValueSheet viewed={viewed} onClose={() => setViewed(undefined)} />
      <Sheet open={reviewing} onOpenChange={setReviewing}>
        <SheetPopup side="right" className="max-w-xl">
          <div className="flex min-h-0 flex-1 flex-col" data-testid="database-review">
            <SheetHeader className="gap-1.5 pb-3">
              <SheetTitle className="text-base">Review changes to {name}</SheetTitle>
              <SheetDescription className="text-[13px]">
                {caps.transactions ? `${plural(n, "change")}, sent together: all of them apply, or none do.` : `${plural(n, "change")}. ${engine.name} has no transactions, so they apply one after another, and one that fails leaves the ones before it applied.`} A row is found by its key: if something else changed it since you read it, your values replace theirs without a word. Only a row that is gone stops the edit.
              </SheetDescription>
            </SheetHeader>
            <SheetPanel className="min-h-0 flex-1 space-y-2 overflow-auto">
              {lines.map((l) => (
                // One card a change: its kind is the badge's to say.
                <Card key={`${l.kind}\n${l.id}`} className="overflow-hidden rounded-xl before:rounded-[calc(var(--radius-xl)-1px)]" data-testid="database-change" data-kind={l.kind}>
                  {/* A compact header: the card's own is spaced for a page's section. */}
                  <CardHeader className="flex items-center gap-2 border-b px-3 py-1.5 in-[[data-slot=card]:has(>[data-slot=card-panel])]:pb-1.5">
                    {/* Delete in the app's destructive text on its outline badge, as its Delete buttons are: the filled and tinted red badges are under 4.5:1 on a sheet in the dark themes. */}
                    <Badge variant={l.kind === "insert" ? "success" : "outline"} size="sm" className={cn("shrink-0", l.kind === "delete" && "text-destructive-foreground")}>
                      {l.kind === "delete" ? "Delete" : l.kind === "insert" ? "Add" : "Update"}
                    </Badge>
                    <span className="min-w-0 flex-1 truncate font-mono text-xs">{l.title}</span>
                    {gone.includes(l.id) && (
                      <Badge variant="outline" size="sm" className="shrink-0">
                        No longer there
                      </Badge>
                    )}
                    <Tip label="Take this change back">
                      <Button size="icon-xs" variant="ghost" aria-label={`Take back the ${l.kind} of ${l.title}`} onClick={() => setStaged(undo(staged, l))}>
                        <Icon name="undo-2" className="size-3.5" />
                      </Button>
                    </Tip>
                  </CardHeader>
                  <CardPanel className="p-0">
                  {l.kind === "delete" ? (
                    <p className="px-3 py-2 text-muted-foreground text-xs">The whole row goes.</p>
                  ) : l.fields.length === 0 ? (
                    <p className="px-3 py-2 text-muted-foreground text-xs">Every column takes its default.</p>
                  ) : (
                    <dl className="divide-y">
                      {l.fields.map((f) => (
                        <div key={f.column} className="flex items-baseline gap-2 px-3 py-1.5 font-mono text-xs">
                          <dt className="w-32 shrink-0 truncate text-muted-foreground">{f.column}</dt>
                          <dd className="min-w-0 flex-1 break-words">
                            {l.kind === "update" && (
                              <>
                                <span className="text-muted-foreground line-through">{quote(f.was ?? null)}</span>
                                <span className="mx-1.5 text-muted-foreground" aria-label="becomes">
                                  →
                                </span>
                              </>
                            )}
                            <span className="font-medium">{quote(f.now)}</span>
                          </dd>
                        </div>
                      ))}
                    </dl>
                  )}
                  </CardPanel>
                </Card>
              ))}
              {lines.length === 0 && <p className="rounded-lg border border-dashed px-3 py-6 text-center text-muted-foreground text-sm">Nothing is staged.</p>}
            </SheetPanel>
            <SheetFooter>
              <Button variant="ghost" onClick={() => setReviewing(false)}>
                Close
              </Button>
              {writes.on ? (
                <Button loading={applying} disabled={n === 0} onClick={() => void apply()}>
                  Apply {plural(n, "change")}
                </Button>
              ) : (
                <Button disabled={n === 0} onClick={writes.ask}>
                  Unlock to apply…
                </Button>
              )}
            </SheetFooter>
          </div>
        </SheetPopup>
      </Sheet>
    </>
  );
}

// FilterRow is one filter as it is written: its column, how it compares,
// and what with. It narrows the rows once its value is entered.
function FilterRow({ draft, columns, ops, opsOf, onChange, onRemove }: { draft: Draft; columns: string[]; ops: string[]; opsOf(column: string): string[]; onChange(next: Draft): void; onRemove(): void }) {
  const [text, setText] = useState(draft.value ?? "");
  const input = useRef<HTMLInputElement>(null);
  // A filter that was just added is ready to be typed in.
  useEffect(() => {
    if (draft.value === undefined) input.current?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const enter = () => text !== draft.value && onChange({ ...draft, value: text });
  return (
    <span className="flex items-center gap-1" data-testid="database-filter" data-column={draft.column}>
      <Menu>
        <MenuTrigger render={<Button size="xs" variant="outline" className="font-mono" aria-label={`Filtered column: ${draft.column}`} />}>{draft.column}</MenuTrigger>
        <MenuPopup align="start" className="max-h-80 min-w-44">
          {columns.map((c) => (
            <MenuItem
              key={c}
              onClick={() => {
                const allowed = opsOf(c);
                onChange({ ...draft, column: c, op: allowed.includes(draft.op) ? draft.op : (allowed[0] ?? draft.op) });
              }}
            >
              <span className="font-mono text-xs">{c}</span>
            </MenuItem>
          ))}
        </MenuPopup>
      </Menu>
      <Menu>
        <MenuTrigger render={<Button size="xs" variant="outline" aria-label={`How ${draft.column} is compared: ${opLabel(draft.op)}`} />}>{opLabel(draft.op)}</MenuTrigger>
        <MenuPopup align="start" className="min-w-36">
          {ops.map((op) => (
            <MenuItem key={op} onClick={() => onChange({ ...draft, op, value: takesValue(op) ? draft.value : undefined })}>
              {opLabel(op)}
            </MenuItem>
          ))}
        </MenuPopup>
      </Menu>
      {takesValue(draft.op) && (
        <Input
          ref={input}
          size="sm"
          className="w-36"
          aria-label={`Value for ${draft.column}`}
          placeholder={draft.value === undefined ? "value, then ↵" : "empty string"}
          value={text}
          onChange={(e: React.ChangeEvent<HTMLInputElement>) => setText(e.target.value)}
          onKeyDown={(e: React.KeyboardEvent) => e.key === "Enter" && enter()}
          onBlur={() => (draft.value !== undefined || text !== "") && enter()}
        />
      )}
      <Tip label="Remove this filter">
        <Button size="icon-xs" variant="ghost" aria-label={`Remove the filter on ${draft.column}`} onClick={onRemove}>
          <Icon name="X" className="size-3.5" />
        </Button>
      </Tip>
    </span>
  );
}

const th = "h-8 whitespace-nowrap border-b px-3 text-left font-medium text-muted-foreground text-xs";
const td = "border-b px-3 py-1.5 align-top";

// Structure is a table's shape: its columns, its key, its indexes and what
// it points at.
function Structure({ table, schema, ctx }: { table: Table; schema: string; ctx: Ctx }) {
  const name = tableName(ctx.engine, schema, table.name);
  return (
    <div className="min-h-0 flex-1 space-y-5 overflow-auto p-4" data-testid="database-structure">
      <Part title="Columns" hint={plural(table.columns.length, "column")}>
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full border-separate border-spacing-0 text-sm">
            <thead>
              <tr>
                <th scope="col" className={th}>
                  Name
                </th>
                <th scope="col" className={th}>
                  Type
                </th>
                <th scope="col" className={th}>
                  NULL
                </th>
                <th scope="col" className={th}>
                  Default
                </th>
              </tr>
            </thead>
            <tbody className="[&_tr:last-child_td]:border-b-0">
              {table.columns.map((c) => (
                <tr key={c.name}>
                  <td className={cn(td, "font-mono text-xs")}>
                    <span className="flex items-center gap-1.5">
                      {c.name}
                      {(c.primary_key || table.primary_key.includes(c.name)) && (
                        <Tip label="Part of the primary key">
                          <span role="img" aria-label="Part of the primary key">
                            <Icon name="KeyRound" className="size-3 text-muted-foreground" />
                          </span>
                        </Tip>
                      )}
                    </span>
                  </td>
                  <td className={cn(td, "font-mono text-muted-foreground text-xs")}>{c.type}</td>
                  <td className={cn(td, "text-xs")}>{c.nullable ? "allowed" : <span className="text-muted-foreground">never</span>}</td>
                  <td className={cn(td, "font-mono text-muted-foreground text-xs")}>{c.default ?? ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Part>
      <Part title="Primary key">
        {table.primary_key.length ? (
          <p className="rounded-lg border px-3 py-2 font-mono text-xs">{table.primary_key.join(", ")}</p>
        ) : (
          <Nothing>{name} has no primary key{table.kind === "table" ? ": its rows can be added here, not edited or deleted." : "."}</Nothing>
        )}
      </Part>
      <Part title="Indexes">
        {table.indexes.length === 0 ? (
          <Nothing>None.</Nothing>
        ) : (
          <ul className="divide-y rounded-lg border">
            {table.indexes.map((i) => (
              <li key={i.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-1.5 font-mono text-xs">
                <span>{i.name}</span>
                <span className="text-muted-foreground">({i.columns.join(", ")})</span>
                {i.unique && (
                  <Badge variant="outline" size="sm" className="ml-auto font-normal font-sans">
                    Unique
                  </Badge>
                )}
              </li>
            ))}
          </ul>
        )}
      </Part>
      <Part title="Foreign keys">
        {table.foreign_keys.length === 0 ? (
          <Nothing>None: {name} points at no other table.</Nothing>
        ) : (
          <ul className="divide-y rounded-lg border">
            {table.foreign_keys.map((f) => (
              <li key={f.columns.join(",")} className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-1.5 font-mono text-xs">
                <span>({f.columns.join(", ")})</span>
                <span className="text-muted-foreground" aria-label="points at">
                  →
                </span>
                <span>
                  {tableName(ctx.engine, f.ref_schema, f.ref_table)} ({f.ref_columns.join(", ")})
                </span>
              </li>
            ))}
          </ul>
        )}
      </Part>
    </div>
  );
}

function Part({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section>
      <h3 className="mb-1.5 flex items-baseline gap-2 font-medium text-sm">
        {title}
        {hint && <span className="font-normal text-muted-foreground text-xs">{hint}</span>}
      </h3>
      {children}
    </section>
  );
}

function Nothing({ children }: { children: React.ReactNode }) {
  return <p className="rounded-lg border border-dashed px-3 py-3 text-muted-foreground text-xs">{children}</p>;
}
