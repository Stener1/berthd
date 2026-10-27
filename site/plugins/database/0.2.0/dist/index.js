// src/index.tsx
import { definePlugin } from "@berth/plugin";
import { Empty as Empty3, EmptyDescription as EmptyDescription3, EmptyHeader as EmptyHeader3, EmptyTitle as EmptyTitle3, Icon as Icon9 } from "@berth/plugin/ui";

// src/panel.tsx
import { useBoxes } from "@berth/plugin";
import { Badge as Badge4, Button as Button7, HelperBox, Icon as Icon7, Menu as Menu3, MenuItem as MenuItem3, MenuPopup as MenuPopup3, MenuSeparator as MenuSeparator2, MenuTrigger as MenuTrigger3, PickOne as PickOne2, Tip as Tip6, useHelperState } from "@berth/plugin/ui";
import { useCallback as useCallback4, useEffect as useEffect5, useMemo as useMemo4, useRef as useRef5, useState as useState8 } from "react";

// src/dotenv.ts
var FILES = [".env", ".env.development", ".env.local", ".env.development.local"];
var cannotLook = (f) => f.code === "box_outdated" || f.code === "helper_no_attach" || f.status === 404 || f.status === 405;
function originOf(look) {
  const ids = (look.answer?.connections ?? []).filter((id) => typeof id === "string");
  if (ids.length === 0) return void 0;
  const files = {};
  for (const v of look.variables ?? []) if (v.sent) files[v.name] = v.file;
  return { ids, files };
}
function fromFile(origin, conn) {
  if (!origin?.ids?.includes(conn.id)) return void 0;
  return origin.files?.[conn.label] ?? FILES[0];
}
function lookAgain(origin, conns, byHand = []) {
  return conns.every((c) => byHand.includes(c.id)) || conns.some((c) => !!origin?.ids?.includes(c.id));
}
var BY_HAND_VARIABLE = "DATABASE_URL";
var BY_HAND = ".manual";
var EXAMPLE = "postgres://user:password@localhost:5432/database";
function addressProblem(text) {
  const address = text.trim();
  if (!address) return "Paste the database's address.";
  if (/\s/.test(address)) return "An address has no spaces or line breaks in it. Paste the one address alone.";
  if (!/^(jdbc:)?postgres(ql)?(\+[a-z0-9]+)?:\/\//i.test(address)) return `That isn't a Postgres address. One looks like ${EXAMPLE}.`;
  return void 0;
}
function connected(look) {
  if (look.command !== BY_HAND) return { old: true };
  const id = (look.answer?.connections ?? []).find((x) => typeof x === "string");
  return id ? { id } : { refused: true };
}
function list(names) {
  if (names.length < 2) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}
var SKIPPED = {
  outside: "is a link to a file outside the worktree, which isn't followed",
  not_a_file: "isn't a plain file",
  too_large: "is larger than 256 KB, so it wasn't read",
  not_text: "isn't text",
  unreadable: "couldn't be read"
};
function said(look, box) {
  const out = [];
  const files = look.files ?? [];
  const skipped = look.skipped ?? [];
  const variables = look.variables ?? [];
  if (files.length === 0 && skipped.length === 0) {
    out.push(`Looked for ${list(FILES)} in this worktree on ${box}: there is none.`);
  } else if (files.length > 0 && variables.length === 0) {
    out.push(`Looked in ${list(files)}: no DATABASE_URL or POSTGRES_URL in ${files.length === 1 ? "it" : "them"}.`);
  }
  for (const v of variables) {
    if (v.sent) out.push(`${v.name} in ${v.file} doesn't point at a Postgres on ${box} itself, so it isn't opened.`);
    else if (v.reason === "secret") out.push(`${v.name} in ${v.file} is a reference to a secret, not an address, so it isn't read.`);
    else if (v.reason === "unresolved") out.push(`${v.name} in ${v.file} refers to a variable that these files don't set.`);
    else if (v.reason === "empty") out.push(`${v.name} in ${v.file} is empty.`);
    else out.push(`${v.name} in ${v.file} is too long to be an address.`);
  }
  for (const s of skipped) out.push(`${s.file} ${SKIPPED[s.reason] ?? SKIPPED.unreadable}.`);
  return out;
}

// src/api.ts
var NO_CAPABILITIES = { dialect: "", schemas: true, operators: ["="], kinds: ["table"], writes: false, transactions: false };
var PAGE = 100;
var MOST = 500;
var QUERY_LIMIT = 1e3;
var CUT_AT = 4e3;
var MOST_CHANGES = 1e3;
function database(berth) {
  const on = (conn, route) => `db/${encodeURIComponent(conn)}/${route}`;
  const post = (box, path, body) => berth.helper.request(box, "POST", path, body);
  return {
    // The databases the helper knows for a worktree. None when its services
    // have not been started since the helper was turned on, or use no
    // database the helper can reach.
    connections: (box, source) => berth.helper.request(box, "GET", `db/connections?${new URLSearchParams({ source })}`).then((r) => r.connections ?? []),
    // Whether this Shipyard can have a box look in a worktree's .env files.
    canLook: typeof berth.helper.attachEnv === "function",
    // The box looks in the worktree's .env files and tells the helper of
    // the database it finds there (dotenv.ts). worktree "" is the main
    // checkout. This is the box's own route, not one of the helper's.
    look: (box, location, worktree) => berth.helper.attachEnv(box, { location, worktree }),
    // Tells the helper of a database the person gave the address of, in
    // place of the one they gave before (dotenv.ts: "by hand"). The address
    // goes to the box in the request and is not kept here.
    connect: (box, location, worktree, address) => berth.helper.attachEnv(box, { location, worktree, env: { [BY_HAND_VARIABLE]: address } }),
    // Takes back the database that was given by hand.
    forget: (box, location, worktree) => berth.helper.attachEnv(box, { location, worktree, env: {} }),
    schema: (box, conn) => berth.helper.request(box, "GET", on(conn, "schema")).then((r) => r.schemas ?? []),
    rows: (box, conn, req) => post(box, on(conn, "rows"), { ...req, schema: req.schema || void 0 }),
    query: (box, conn, sql, limit = QUERY_LIMIT) => post(box, on(conn, "query"), { sql, limit }),
    edit: (box, conn, schema, table, changes2) => post(box, on(conn, "edit"), { schema: schema || void 0, table, changes: changes2 }),
    writes: (box, conn, turnOn) => post(box, on(conn, "writes"), { on: turnOn })
  };
}
function faultOf(err) {
  const e = err ?? {};
  const inner = e.detail?.error && typeof e.detail.error === "object" ? e.detail.error : {};
  const text = (v) => typeof v === "string" && v ? v : void 0;
  return {
    message: typeof e.message === "string" && e.message ? e.message : String(err),
    code: typeof e.code === "string" ? e.code : void 0,
    status: typeof e.status === "number" ? e.status : void 0,
    hint: text(inner.hint),
    position: typeof inner.position === "number" && inner.position >= 1 ? inner.position : void 0,
    sqlstate: text(inner.sqlstate),
    detail: text(inner.detail)
  };
}
var isOldHelper = (f) => f.code === "not_found";
var isOldBox = (f) => f.code === "unauthorized";
var lostHelper = (f) => f.code === "helper_off" || f.code === "helper_unreachable" || f.status === 503 && !f.code;

// src/clock.ts
var WRITES_FOR_MS = 15 * 6e4;
function skewOf(writesUntil, arrivedAt) {
  return writesUntil - (arrivedAt + WRITES_FOR_MS);
}
function untilHere(writesUntil, skew, now) {
  return Math.min(writesUntil - skew, now + WRITES_FOR_MS);
}

// src/browser.tsx
import { Badge as Badge2, Button as Button3, Card, CardHeader, CardPanel, Icon as Icon3, Input, Menu, MenuItem, MenuPopup, MenuTrigger, PickOne, Sheet as Sheet2, SheetDescription as SheetDescription2, SheetFooter as SheetFooter2, SheetHeader as SheetHeader2, SheetPanel as SheetPanel2, SheetPopup as SheetPopup2, SheetTitle as SheetTitle2, Tip as Tip2, cn as cn3 } from "@berth/plugin/ui";
import { useCallback as useCallback3, useEffect as useEffect3, useMemo as useMemo2, useRef as useRef3, useState as useState4 } from "react";

// src/engines/families.ts
var CELL = 240;
function show(cell, family) {
  if (cell === null) return { kind: "null", text: "NULL", right: false };
  const right = family === "integer" || family === "decimal";
  if (typeof cell === "boolean") return { kind: "value", text: cell ? "true" : "false", right: false };
  if (typeof cell === "number") return { kind: "value", text: String(cell), right };
  if (cell === "") return { kind: "empty", text: "empty", right: false };
  const line = (cell.length > CELL ? cell.slice(0, CELL) : cell).replace(/\r?\n/g, "\u21B5").replace(/\t/g, "\u2192");
  return { kind: "value", text: line, right };
}
var isLong = (cell) => typeof cell === "string" && (cell.length > 80 || cell.includes("\n"));
function editText(cell) {
  if (cell === null) return "";
  return typeof cell === "string" ? cell : String(cell);
}
function pretty(cell, family) {
  if (cell === null) return "NULL";
  if (typeof cell !== "string") return String(cell);
  if (family === "json") {
    try {
      return JSON.stringify(JSON.parse(cell), null, 2);
    } catch {
      return cell;
    }
  }
  return cell;
}
var no = (why) => ({ ok: false, why });
var TRUE = /* @__PURE__ */ new Set(["true", "t", "yes", "y", "on", "1"]);
var FALSE = /* @__PURE__ */ new Set(["false", "f", "no", "n", "off", "0"]);
function parseInput(text, family) {
  if (family === "text") return { ok: true, value: text };
  const t = text.trim();
  if (!t) return no("Type a value, or set NULL: only text can be empty.");
  switch (family) {
    case "integer":
      if (!/^[+-]?\d+$/.test(t)) return no("Type a whole number.");
      return { ok: true, value: Number.isSafeInteger(Number(t)) ? Number(t) : t.replace(/^\+/, "") };
    case "decimal":
      if (!/^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/i.test(t) && !/^[+-]?(nan|infinity)$/i.test(t)) return no("Type a number.");
      return { ok: true, value: t };
    case "boolean":
      if (TRUE.has(t.toLowerCase())) return { ok: true, value: true };
      if (FALSE.has(t.toLowerCase())) return { ok: true, value: false };
      return no("Type true or false.");
    case "uuid":
      if (!/^[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}$/i.test(t)) return no("Type a UUID: 32 hex digits.");
      return { ok: true, value: t };
    case "json":
      try {
        JSON.parse(t);
      } catch {
        return no("That isn't JSON yet.");
      }
      return { ok: true, value: text };
    case "binary":
      if (!/^\\x([0-9a-f]{2})*$/i.test(t)) return no("Type bytes as \\x and pairs of hex digits.");
      return { ok: true, value: t };
    default:
      return { ok: true, value: text };
  }
}
function sameCell(a, b) {
  if (a === b) return true;
  if (a === null || b === null) return false;
  if (typeof a === "number" && typeof b === "string") return String(a) === b;
  if (typeof b === "number" && typeof a === "string") return String(b) === a;
  return false;
}
function quote(cell, max = 60) {
  if (cell === null) return "NULL";
  if (typeof cell !== "string") return String(cell);
  const line = cell.replace(/\s+/g, " ");
  return `'${line.length > max ? `${line.slice(0, max)}\u2026` : line}'`;
}

// src/engines/postgres.ts
var FAMILIES = {
  int2: "integer",
  int4: "integer",
  int8: "integer",
  smallint: "integer",
  integer: "integer",
  int: "integer",
  bigint: "integer",
  smallserial: "integer",
  serial: "integer",
  bigserial: "integer",
  oid: "integer",
  numeric: "decimal",
  decimal: "decimal",
  float4: "decimal",
  float8: "decimal",
  real: "decimal",
  "double precision": "decimal",
  bool: "boolean",
  boolean: "boolean",
  text: "text",
  varchar: "text",
  "character varying": "text",
  character: "text",
  char: "text",
  bpchar: "text",
  citext: "text",
  name: "text",
  date: "time",
  time: "time",
  timetz: "time",
  timestamp: "time",
  timestamptz: "time",
  "time without time zone": "time",
  "time with time zone": "time",
  "timestamp without time zone": "time",
  "timestamp with time zone": "time",
  interval: "time",
  json: "json",
  jsonb: "json",
  bytea: "binary",
  uuid: "uuid"
};
var postgres = {
  id: "postgres",
  name: "PostgreSQL",
  icon: "Database",
  edits: true,
  family(type) {
    const t = type.trim().toLowerCase();
    if (t.endsWith("[]") || t.startsWith("_") || t.endsWith(" array")) return "array";
    return FAMILIES[t.replace(/\s*\([^)]*\)/g, "")] ?? "other";
  },
  ident: (name) => `"${name.replaceAll('"', '""')}"`,
  defaultSchema: "public",
  // Without ANALYZE: the plan, and nothing is run.
  explain: (statement) => `EXPLAIN ${statement}`
};

// src/engines/index.ts
var ENGINES = { [postgres.id]: postgres };
var generic = (id) => ({
  id,
  name: id || "Database",
  icon: "Database",
  edits: false,
  family: () => "other",
  ident: (name) => `"${name.replaceAll('"', '""')}"`
});
function engineOf(id) {
  return ENGINES[id] ?? generic(id);
}
function tableName(engine, schema, table, quoted = false) {
  const q = (s) => quoted && !/^[a-z_][a-z0-9_]*$/.test(s) ? engine.ident(s) : s;
  return !schema || schema === engine.defaultSchema ? q(table) : `${q(schema)}.${q(table)}`;
}

// src/filters.ts
var OPERATORS = {
  "=": { label: "=", value: true },
  "!=": { label: "\u2260", value: true },
  "<": { label: "<", value: true },
  "<=": { label: "\u2264", value: true },
  ">": { label: ">", value: true },
  ">=": { label: "\u2265", value: true },
  like: { label: "like", value: true },
  ilike: { label: "like, any case", value: true },
  is_null: { label: "is NULL", value: false },
  not_null: { label: "is not NULL", value: false }
};
var opLabel = (op) => OPERATORS[op]?.label ?? op;
var takesValue = (op) => OPERATORS[op]?.value ?? true;
var PATTERNS = /* @__PURE__ */ new Set(["like", "ilike"]);
var ORDERED = /* @__PURE__ */ new Set(["<", "<=", ">", ">="]);
function opsFor(operators, family) {
  return operators.filter((op) => {
    if (PATTERNS.has(op)) return family === "text" || family === "other" || family === "uuid";
    if (ORDERED.has(op)) return family !== "json" && family !== "boolean" && family !== "binary" && family !== "array";
    return true;
  });
}
function filtersOf(drafts, family) {
  const out = [];
  for (const d of drafts) {
    if (!takesValue(d.op)) {
      out.push({ column: d.column, op: d.op });
      continue;
    }
    if (d.value === void 0) continue;
    const parsed = PATTERNS.has(d.op) ? void 0 : parseInput(d.value, family(d.column));
    out.push({ column: d.column, op: d.op, value: parsed?.ok ? parsed.value : d.value });
  }
  return out;
}
function nextOrder(order, column) {
  const now = order.find((o) => o.column === column);
  if (!now) return [{ column }];
  return now.desc ? [] : [{ column, desc: true }];
}
function jumpFilters(fk, row) {
  const out = [];
  for (let i = 0; i < fk.columns.length; i++) {
    const v = row[fk.columns[i]];
    if (v === null || v === void 0) return void 0;
    out.push({ id: i + 1, column: fk.ref_columns[i], op: "=", value: String(v) });
  }
  return out;
}

// src/grid.tsx
import { Icon, Tip, cn } from "@berth/plugin/ui";
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { jsx, jsxs } from "react/jsx-runtime";
var ROW = 30;
var HEAD = 44;
var MARGIN = 52;
var SPARE = 10;
var WIDTH = { integer: 96, decimal: 116, boolean: 88, uuid: 292, time: 232, json: 280, text: 232, binary: 208, array: 208, other: 168 };
var widthOf = (c) => Math.round(Math.min(380, Math.max(WIDTH[c.family], c.name.length * 7.8 + 64, c.type.length * 6.4 + 28)));
function Grid({ label, columns, rows, offset = 0, order, onSort, onFilter, canEdit, onEdit, onView, onJump, picked, canPick, onPick }) {
  const scroller = useRef(null);
  const [view, setView] = useState({ top: 0, height: 600 });
  const [sel, setSel] = useState();
  const [editing, setEditing] = useState(false);
  const follow = useRef(false);
  const widths = useMemo(() => columns.map(widthOf), [columns]);
  const total = MARGIN + widths.reduce((a, b) => a + b, 0);
  const pickable = !!onPick;
  const latest = useRef({ rows, columns, canEdit, onEdit, onView, onJump, onPick });
  latest.current = { rows, columns, canEdit, onEdit, onView, onJump, onPick };
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const read = () => setView((v) => v.top === el.scrollTop && v.height === el.clientHeight ? v : { top: el.scrollTop, height: el.clientHeight });
    read();
    const ro = new ResizeObserver(read);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const shape = `${rows.length}:${columns.length}:${rows[0]?.id ?? ""}`;
  useEffect(() => {
    setSel(void 0);
    setEditing(false);
    if (scroller.current) scroller.current.scrollTop = 0;
  }, [shape]);
  const first = Math.max(0, Math.floor(view.top / ROW) - SPARE);
  const last = Math.min(rows.length, Math.ceil((view.top + view.height) / ROW) + SPARE);
  const reveal = useCallback(
    (s) => {
      const el = scroller.current;
      if (!el) return;
      const top = s.row * ROW;
      if (top < el.scrollTop) el.scrollTop = top;
      else if (top + ROW > el.scrollTop + el.clientHeight - HEAD) el.scrollTop = top + ROW - el.clientHeight + HEAD;
      const left2 = widths.slice(0, s.col).reduce((a, b) => a + b, MARGIN);
      if (left2 - MARGIN < el.scrollLeft) el.scrollLeft = left2 - MARGIN;
      else if (left2 + widths[s.col] > el.scrollLeft + el.clientWidth) el.scrollLeft = left2 + widths[s.col] - el.clientWidth;
    },
    [widths]
  );
  useEffect(() => {
    if (!sel || editing || !follow.current) return;
    follow.current = false;
    scroller.current?.querySelector(`[data-cell="${sel.row}:${sel.col}"]`)?.focus({ preventScroll: true });
  }, [sel, editing, first, last]);
  const move = (to) => {
    const next = { row: Math.max(0, Math.min(rows.length - 1, to.row)), col: Math.max(0, Math.min(columns.length - 1, to.col)) };
    follow.current = true;
    reveal(next);
    setSel(next);
  };
  const act = useCallback((s) => {
    const l = latest.current;
    const row = l.rows[s.row];
    if (!row) return;
    if (l.onEdit && l.canEdit?.(row, s.col)) setEditing(true);
    else l.onView(row, s.col);
  }, []);
  const onKeyDown = (e) => {
    if (editing || !sel || !e.target.matches("[data-cell]")) return;
    const page = Math.max(1, Math.floor((view.height - HEAD) / ROW) - 1);
    const steps = {
      ArrowUp: { row: sel.row - 1, col: sel.col },
      ArrowDown: { row: sel.row + 1, col: sel.col },
      ArrowLeft: { row: sel.row, col: sel.col - 1 },
      ArrowRight: { row: sel.row, col: sel.col + 1 },
      PageUp: { row: sel.row - page, col: sel.col },
      PageDown: { row: sel.row + page, col: sel.col },
      Home: { row: e.metaKey || e.ctrlKey ? 0 : sel.row, col: 0 },
      End: { row: e.metaKey || e.ctrlKey ? rows.length - 1 : sel.row, col: columns.length - 1 }
    };
    if (steps[e.key]) {
      e.preventDefault();
      move(steps[e.key]);
    } else if (e.key === "Enter" || e.key === "F2") {
      e.preventDefault();
      act(sel);
    } else if (e.key === " ") {
      e.preventDefault();
      const row = rows[sel.row];
      if (row) onView(row, sel.col);
    }
  };
  const select = useCallback((row, col) => {
    setEditing(false);
    setSel({ row, col });
  }, []);
  const open = useCallback(
    (row, col) => {
      setSel({ row, col });
      act({ row, col });
    },
    [act]
  );
  const commit = useCallback((row, col, value) => {
    const l = latest.current;
    if (l.rows[row]) l.onEdit?.(l.rows[row], col, value);
    follow.current = true;
    setEditing(false);
  }, []);
  const cancel = useCallback(() => {
    follow.current = true;
    setEditing(false);
  }, []);
  const view1 = useCallback((row, col) => latest.current.rows[row] && latest.current.onView(latest.current.rows[row], col), []);
  const jump = useCallback((row, col) => latest.current.rows[row] && latest.current.onJump?.(latest.current.rows[row], col), []);
  const pick = useCallback((row, on) => latest.current.rows[row] && latest.current.onPick?.([latest.current.rows[row].id], on), []);
  const pickables = useMemo(() => pickable ? rows.filter((r) => canPick?.(r) ?? true).map((r) => r.id) : [], [rows, pickable, canPick]);
  const allPicked = pickables.length > 0 && pickables.every((id) => picked?.has(id));
  return /* @__PURE__ */ jsx("div", { ref: scroller, className: "min-h-0 flex-1 overflow-auto overscroll-contain bg-background", "data-testid": "database-grid", onScroll: (e) => setView({ top: e.currentTarget.scrollTop, height: e.currentTarget.clientHeight }), children: /* @__PURE__ */ jsxs("table", { role: "grid", "aria-label": label, "aria-readonly": !onEdit, "aria-rowcount": rows.length + 1, "aria-colcount": columns.length + 1, className: "border-separate border-spacing-0 text-sm", style: { tableLayout: "fixed", width: total }, children: [
    /* @__PURE__ */ jsxs("colgroup", { children: [
      /* @__PURE__ */ jsx("col", { style: { width: MARGIN } }),
      widths.map((w, i) => /* @__PURE__ */ jsx("col", { style: { width: w } }, columns[i].name + i))
    ] }),
    /* @__PURE__ */ jsx("thead", { children: /* @__PURE__ */ jsxs("tr", { "aria-rowindex": 1, style: { height: HEAD }, children: [
      /* @__PURE__ */ jsxs("th", { scope: "col", className: "sticky top-0 left-0 z-30 border-r border-b bg-background px-2 text-center font-normal text-muted-foreground text-xs", children: [
        pickable && pickables.length > 0 ? /* @__PURE__ */ jsx("input", { type: "checkbox", className: "size-3.5 cursor-pointer align-middle accent-primary", "aria-label": allPicked ? "Pick no rows" : "Pick every row on this page", checked: allPicked, onChange: (e) => onPick?.(pickables, e.target.checked) }) : /* @__PURE__ */ jsx("span", { "aria-hidden": true, children: "#" }),
        !pickable && /* @__PURE__ */ jsx("span", { className: "sr-only", children: "Row" })
      ] }),
      columns.map((c, i) => {
        const by = order?.find((o) => o.column === c.name);
        return /* @__PURE__ */ jsxs("th", { scope: "col", "aria-colindex": i + 2, "aria-sort": by ? by.desc ? "descending" : "ascending" : onSort ? "none" : void 0, className: "group/head sticky top-0 z-20 border-r border-b bg-background px-2 text-left align-middle font-normal", children: [
          /* @__PURE__ */ jsxs("div", { className: "flex items-center gap-1", children: [
            onSort ? /* @__PURE__ */ jsxs("button", { type: "button", onClick: () => onSort(c.name), className: cn("flex min-w-0 items-center gap-1 rounded-sm font-medium text-[13px] outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring", by ? "text-foreground" : "text-foreground/90"), children: [
              c.key && /* @__PURE__ */ jsx(Icon, { name: "KeyRound", className: "size-3 shrink-0 text-muted-foreground" }),
              /* @__PURE__ */ jsx("span", { className: "truncate", children: c.name }),
              by && /* @__PURE__ */ jsx(Icon, { name: by.desc ? "ArrowDown" : "ArrowUp", className: "size-3 shrink-0" })
            ] }) : /* @__PURE__ */ jsx("span", { className: "truncate font-medium text-[13px]", children: c.name }),
            onFilter && /* @__PURE__ */ jsx(Tip, { label: `Filter by ${c.name}`, children: /* @__PURE__ */ jsx("button", { type: "button", "aria-label": `Filter by ${c.name}`, onClick: () => onFilter(c.name), className: "-mr-1 ml-auto flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground opacity-0 outline-none hover:bg-accent hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-hover/head:opacity-100", children: /* @__PURE__ */ jsx(Icon, { name: "ListFilter", className: "size-3" }) }) })
          ] }),
          /* @__PURE__ */ jsxs("div", { className: "truncate font-mono text-[11px] text-muted-foreground leading-4", children: [
            c.type,
            c.fk && /* @__PURE__ */ jsxs("span", { children: [
              " \u2192 ",
              c.fk.ref_table
            ] })
          ] })
        ] }, c.name + i);
      })
    ] }) }),
    /* @__PURE__ */ jsxs("tbody", { onKeyDown, children: [
      first > 0 && /* @__PURE__ */ jsx("tr", { "aria-hidden": true, style: { height: first * ROW }, children: /* @__PURE__ */ jsx("td", { colSpan: columns.length + 1 }) }),
      rows.slice(first, last).map((r, i) => {
        const at = first + i;
        const on = sel?.row === at;
        return /* @__PURE__ */ jsx(
          RowView,
          {
            at,
            number: r.state === "new" ? 0 : offset + (r.ref ?? at) + 1,
            row: r,
            columns,
            sel: on ? sel.col : -1,
            tab: !sel && at === first ? 0 : -1,
            editing: on && editing,
            picked: pickable && (canPick?.(r) ?? true) ? !!picked?.has(r.id) : void 0,
            select,
            open,
            view: view1,
            jump: onJump ? jump : void 0,
            pick,
            commit,
            cancel
          },
          r.id
        );
      }),
      last < rows.length && /* @__PURE__ */ jsx("tr", { "aria-hidden": true, style: { height: (rows.length - last) * ROW }, children: /* @__PURE__ */ jsx("td", { colSpan: columns.length + 1 }) })
    ] })
  ] }) });
}
var RowView = memo(function RowView2({ at, number, row, columns, sel, tab, editing, picked, select, open, view, jump, pick, commit, cancel }) {
  const gone = row.state === "deleted" || row.state === "gone";
  return /* @__PURE__ */ jsxs("tr", { "aria-rowindex": at + 2, "data-testid": "database-row", "data-row": row.id, "data-state": row.state, style: { height: ROW }, className: cn(row.state === "new" && "bg-success/8", gone && "bg-destructive/8"), children: [
    /* @__PURE__ */ jsx(
      "td",
      {
        role: "rowheader",
        className: cn(
          "sticky left-0 z-10 border-r border-b bg-background px-2 text-center text-muted-foreground text-xs tabular-nums",
          row.state === "new" && "bg-[color-mix(in_oklab,var(--success)_8%,var(--background))]",
          // A row that is gone, by this edit or by something else's, looks the same: it is not there.
          gone && "bg-[color-mix(in_oklab,var(--destructive)_8%,var(--background))]"
        ),
        children: picked !== void 0 ? /* @__PURE__ */ jsx("input", { type: "checkbox", className: "size-3.5 cursor-pointer align-middle accent-primary", "aria-label": row.state === "new" ? "Pick the new row" : `Pick row ${number}`, checked: picked, onChange: (e) => pick(at, e.target.checked) }) : row.state === "new" ? /* @__PURE__ */ jsx("span", { className: "font-medium text-success-foreground", children: "new" }) : number
      }
    ),
    columns.map((c, col) => {
      const on = sel === col;
      const cell = row.cells[col] ?? null;
      const unset = row.unset?.has(col);
      const cut = row.cut?.has(col);
      const shown = show(cell, c.family);
      const changed = row.changed?.has(col);
      const more = cut || isLong(cell);
      const points = !!jump && !!c.fk && cell !== null && row.state !== "new";
      return /* @__PURE__ */ jsx(
        "td",
        {
          role: "gridcell",
          "aria-colindex": col + 2,
          "aria-selected": on,
          "data-cell": `${at}:${col}`,
          "data-col": c.name,
          "data-changed": changed || void 0,
          tabIndex: on ? editing ? void 0 : 0 : col === 0 ? tab : -1,
          onClick: () => select(at, col),
          onFocus: () => !on && select(at, col),
          onDoubleClick: () => open(at, col),
          className: cn(
            "relative border-r border-b px-2 font-mono text-xs outline-none",
            // Changed, as the File tab marks a changed line: the whole cell, tinted.
            changed && "bg-info/14",
            on && "z-[5] shadow-[inset_0_0_0_2px_var(--ring)]",
            shown.right && "text-right tabular-nums",
            gone && "text-muted-foreground line-through"
          ),
          children: on && editing ? /* @__PURE__ */ jsx(CellEditor, { initial: unset ? "" : editText(cell), column: c, onCommit: (v) => commit(at, col, v), onCancel: cancel }) : /* @__PURE__ */ jsxs("span", { className: "flex items-center gap-1", children: [
            /* @__PURE__ */ jsx("span", { className: cn("min-w-0 flex-1 truncate", (shown.kind !== "value" || unset) && "font-sans text-[11px] text-muted-foreground italic"), children: unset ? c.generated ? "generated" : "default" : shown.text }),
            more && /* @__PURE__ */ jsx(Tip, { label: cut ? "Cut at 4,000 characters: show what was read" : "Show the whole value", children: /* @__PURE__ */ jsx("button", { type: "button", tabIndex: on ? 0 : -1, "aria-label": `Show the whole value of ${c.name}`, onClick: (e) => (e.stopPropagation(), view(at, col)), className: "-mr-1 flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring", children: /* @__PURE__ */ jsx(Icon, { name: cut ? "Scissors" : "maximize-2", className: "size-3" }) }) }),
            points && /* @__PURE__ */ jsx(Tip, { label: `Open the row in ${c.fk.ref_table}`, children: /* @__PURE__ */ jsx("button", { type: "button", tabIndex: on ? 0 : -1, "aria-label": `Open the row in ${c.fk.ref_table} that ${c.name} points at`, onClick: (e) => (e.stopPropagation(), jump(at, col)), className: "-mr-1 flex size-6 shrink-0 items-center justify-center rounded-sm text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring", children: /* @__PURE__ */ jsx(Icon, { name: "ArrowUpRight", className: "size-3" }) }) })
          ] })
        },
        c.name + col
      );
    })
  ] });
});
function CellEditor({ initial, column, onCommit, onCancel }) {
  const [text, setText] = useState(initial);
  const [why, setWhy] = useState();
  const done = useRef(false);
  const input = useRef(null);
  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);
  const keep = (leaving) => {
    if (done.current) return;
    const parsed = parseInput(text, column.family);
    if (parsed.ok) {
      done.current = true;
      onCommit(parsed.value);
    } else if (leaving) {
      done.current = true;
      onCancel();
    } else setWhy(parsed.why);
  };
  return /* @__PURE__ */ jsxs("span", { className: "-mx-1 flex items-center gap-1", children: [
    /* @__PURE__ */ jsx(
      "input",
      {
        ref: input,
        value: text,
        "aria-label": `${column.name}, ${column.type}`,
        "aria-invalid": !!why,
        spellCheck: false,
        autoComplete: "off",
        onChange: (e) => (setText(e.target.value), setWhy(void 0)),
        onBlur: () => keep(true),
        onKeyDown: (e) => {
          e.stopPropagation();
          if (e.key === "Enter") keep(false);
          else if (e.key === "Escape") {
            done.current = true;
            onCancel();
          }
        },
        className: "h-6 min-w-0 flex-1 rounded-sm border border-input bg-background px-1 font-mono text-xs outline-none focus:border-ring aria-invalid:border-destructive"
      }
    ),
    column.nullable && /* @__PURE__ */ jsx(
      "button",
      {
        type: "button",
        onMouseDown: (e) => e.preventDefault(),
        onClick: () => {
          done.current = true;
          onCommit(null);
        },
        className: "h-6 shrink-0 rounded-sm border border-input bg-background px-1.5 font-sans text-[11px] text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring",
        children: "Set NULL"
      }
    ),
    why && /* @__PURE__ */ jsx("span", { role: "alert", className: "absolute top-full left-0 z-20 mt-px whitespace-nowrap rounded-md border bg-popover px-2 py-1 font-sans text-destructive-foreground text-xs shadow-md", children: why })
  ] });
}

// src/live.ts
import { useCallback as useCallback2, useEffect as useEffect2, useRef as useRef2, useState as useState2 } from "react";
function useVisible() {
  const [el, setEl] = useState2(null);
  const [inView, setInView] = useState2(false);
  const [shown, setShown] = useState2(() => !document.hidden);
  useEffect2(() => {
    if (!el) return;
    const io = new IntersectionObserver((entries) => setInView(entries.some((e) => e.isIntersecting)));
    io.observe(el);
    return () => io.disconnect();
  }, [el]);
  useEffect2(() => {
    const on = () => setShown(!document.hidden);
    document.addEventListener("visibilitychange", on);
    return () => document.removeEventListener("visibilitychange", on);
  }, []);
  return [setEl, inView && shown];
}
function useRead(load, key, opts) {
  const [state, setState] = useState2({});
  const [stamp, setStamp] = useState2(0);
  const [busy, setBusy] = useState2(false);
  const latest = useRef2(load);
  latest.current = load;
  const { active, every } = opts;
  useEffect2(() => {
    if (!active) return;
    let live = true;
    let timer = 0;
    const run = async () => {
      setBusy(true);
      try {
        const data = await latest.current();
        if (live) setState({ key, data });
      } catch (err) {
        if (live) setState((s) => s.key === key ? { ...s, fault: faultOf(err) } : { key, fault: faultOf(err) });
      }
      if (!live) return;
      setBusy(false);
      if (every) timer = window.setTimeout(() => void run(), every);
    };
    void run();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [key, active, every, stamp]);
  const reload = useCallback2(() => setStamp((n) => n + 1), []);
  return { data: state.data, fault: state.key === key ? state.fault : void 0, loading: busy || state.key !== key && active, fresh: state.key === key, reload };
}
function useNow(on, every = 1e3) {
  const [now, setNow] = useState2(() => Date.now());
  useEffect2(() => {
    if (!on) return;
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [on, every]);
  return now;
}

// src/rules.ts
var KINDS = {
  table: { label: "Table", short: "table", icon: "table-2" },
  view: { label: "View", short: "view", icon: "Eye", why: "is a view: its rows come from a query, so they can't be changed here" },
  materialized_view: { label: "Materialized view", short: "mat. view", icon: "Layers", why: "is a materialized view: its rows are a query's saved result, so they can't be changed here" }
};
function kindOf(kind) {
  return KINDS[kind] ?? { label: kind.replaceAll("_", " ") || "Relation", short: kind.replaceAll("_", " ") || "relation", icon: "table-2", why: `is a ${kind.replaceAll("_", " ") || "relation"}, which Shipyard only knows how to read` };
}
function rulesFor(table, engine, caps) {
  const none = (why) => ({ rows: false, inserts: false, why });
  if (!caps.writes) return none(`${engine.name} takes no writes through Shipyard, so ${table.name} can be read and not changed.`);
  if (!engine.edits) return none(`Shipyard doesn't know ${engine.name}'s types well enough to edit rows in the grid. The console still runs what you type.`);
  const kind = kindOf(table.kind);
  if (kind.why) return none(`${table.name} ${kind.why}.`);
  if (table.primary_key.length === 0) return { rows: false, inserts: true, why: `${table.name} has no primary key, so one row can't be told from another like it: rows can be added, not edited or deleted.` };
  return { rows: true, inserts: true };
}

// src/sheet.tsx
import { Badge, Button, Sheet, SheetDescription, SheetFooter, SheetHeader, SheetPanel, SheetPopup, SheetTitle, Textarea } from "@berth/plugin/ui";
import { useState as useState3 } from "react";
import { Fragment, jsx as jsx2, jsxs as jsxs2 } from "react/jsx-runtime";
function ValueSheet({ viewed, onClose }) {
  const [last, setLast] = useState3(viewed);
  if (viewed && viewed !== last) setLast(viewed);
  const v = viewed ?? last;
  return /* @__PURE__ */ jsx2(Sheet, { open: !!viewed, onOpenChange: (open) => !open && onClose(), children: /* @__PURE__ */ jsx2(SheetPopup, { side: "right", className: "max-w-xl", children: v && /* @__PURE__ */ jsx2(Value, { v, onClose }, `${v.row}
${v.column}`) }) });
}
function Value({ v, onClose }) {
  const editable = !!v.onStage && !v.cut;
  const [text, setText] = useState3(() => v.unset ? "" : v.family === "json" && v.cell !== null ? pretty(v.cell, v.family) : editText(v.cell));
  const [why, setWhy] = useState3();
  const [copied, setCopied] = useState3(false);
  const [opened] = useState3(text);
  const stage = () => {
    if (text === opened && !v.unset && v.cell !== null) return onClose();
    const parsed = parseInput(text, v.family);
    if (!parsed.ok) return setWhy(parsed.why);
    v.onStage?.(parsed.value);
    onClose();
  };
  const size = typeof v.cell === "string" ? v.cell.length : void 0;
  return /* @__PURE__ */ jsxs2("div", { className: "flex min-h-0 flex-1 flex-col", "data-testid": "database-value", children: [
    /* @__PURE__ */ jsxs2(SheetHeader, { className: "gap-1.5 pb-3", children: [
      /* @__PURE__ */ jsxs2(SheetTitle, { className: "flex items-center gap-2 text-base", children: [
        /* @__PURE__ */ jsx2("span", { className: "truncate font-mono", children: v.column }),
        /* @__PURE__ */ jsx2(Badge, { variant: "outline", size: "sm", className: "shrink-0 font-mono font-normal", children: v.type })
      ] }),
      /* @__PURE__ */ jsxs2(SheetDescription, { className: "text-[13px]", children: [
        v.row,
        size !== void 0 && !v.cut && ` \xB7 ${size.toLocaleString("en-US")} characters`
      ] })
    ] }),
    /* @__PURE__ */ jsxs2(SheetPanel, { className: "flex min-h-0 flex-1 flex-col gap-3", children: [
      v.cut && /* @__PURE__ */ jsxs2("p", { role: "status", className: "rounded-md bg-muted px-3 py-2 text-pretty text-foreground text-xs", "data-testid": "database-cut", children: [
        "Only the first ",
        CUT_AT.toLocaleString("en-US"),
        " characters were read: the value is longer. It can't be edited here, because saving would keep only this much. Read or change the rest from the console."
      ] }),
      editable ? /* @__PURE__ */ jsxs2(Fragment, { children: [
        /* @__PURE__ */ jsx2(
          Textarea,
          {
            "aria-label": `${v.column}, ${v.type}`,
            "aria-invalid": !!why,
            spellCheck: false,
            className: "min-h-48 flex-1 font-mono text-xs",
            value: text,
            placeholder: v.cell === null || v.unset ? v.unset ? "Nothing typed: the column's default" : "NULL" : v.family === "text" ? "The empty string" : void 0,
            onChange: (e) => (setText(e.target.value), setWhy(void 0))
          }
        ),
        why && /* @__PURE__ */ jsx2("p", { role: "alert", className: "text-destructive-foreground text-xs", children: why }),
        /* @__PURE__ */ jsx2("p", { className: "text-muted-foreground text-xs", children: v.family === "text" ? "Left empty, this is the empty string. NULL is its own button." : "NULL is its own button: only text can be empty." })
      ] }) : v.cell === null ? /* @__PURE__ */ jsx2("p", { className: "rounded-lg border border-dashed px-3 py-6 text-center text-muted-foreground text-sm italic", children: v.unset ? "Nothing typed: the column's default" : "NULL" }) : v.cell === "" ? /* @__PURE__ */ jsx2("p", { className: "rounded-lg border border-dashed px-3 py-6 text-center text-muted-foreground text-sm italic", children: "The empty string" }) : (
        // The sheet's panel scrolls it; nothing here takes the keyboard
        // first, so a long value opens at its top.
        /* @__PURE__ */ jsx2("pre", { className: "whitespace-pre-wrap break-words rounded-lg border bg-muted/40 p-3 font-mono text-xs", children: pretty(v.cell, v.cut ? "other" : v.family) })
      )
    ] }),
    /* @__PURE__ */ jsxs2(SheetFooter, { children: [
      v.cell !== null && /* @__PURE__ */ jsx2(
        Button,
        {
          variant: "ghost",
          className: "mr-auto",
          onClick: () => {
            void navigator.clipboard?.writeText(editText(v.cell)).then(() => setCopied(true));
          },
          children: copied ? "Copied" : "Copy"
        }
      ),
      editable && v.nullable && /* @__PURE__ */ jsx2(
        Button,
        {
          variant: "outline",
          onClick: () => {
            v.onStage?.(null);
            onClose();
          },
          children: "Set NULL"
        }
      ),
      editable ? /* @__PURE__ */ jsx2(Button, { onClick: stage, children: "Stage change" }) : /* @__PURE__ */ jsx2(Button, { variant: "outline", onClick: onClose, children: "Close" })
    ] })
  ] });
}

// src/staged.ts
var NOTHING = { updates: {}, deletes: {}, inserts: [], next: 1 };
function keyOf(row, pk) {
  return Object.fromEntries(pk.map((c) => [c, row[c] ?? null]));
}
function rowId(row, pk) {
  return JSON.stringify(pk.map((c) => row[c] ?? null));
}
function keyText(key) {
  return Object.entries(key).map(([c, v]) => `${c} = ${quote(v, 40)}`).join(", ");
}
var without = (map, id) => {
  const { [id]: _, ...rest } = map;
  return rest;
};
function setCell(staged, pk, row, column, value) {
  const id = rowId(row, pk);
  if (staged.deletes[id]) return staged;
  const had = staged.updates[id] ?? { key: keyOf(row, pk), was: {}, set: {} };
  const { [column]: _w, ...was } = had.was;
  const { [column]: _s, ...set } = had.set;
  if (!sameCell(row[column] ?? null, value)) {
    was[column] = row[column] ?? null;
    set[column] = value;
  }
  if (Object.keys(set).length === 0) return { ...staged, updates: without(staged.updates, id) };
  return { ...staged, updates: { ...staged.updates, [id]: { key: had.key, was, set } } };
}
function addRow(staged) {
  const id = `new-${staged.next}`;
  return [{ ...staged, inserts: [...staged.inserts, { id, set: {} }], next: staged.next + 1 }, id];
}
function setNew(staged, id, column, value) {
  return {
    ...staged,
    inserts: staged.inserts.map((i) => {
      if (i.id !== id) return i;
      const { [column]: _, ...set } = i.set;
      return { id, set: value === void 0 ? set : { ...set, [column]: value } };
    })
  };
}
function removeRows(staged, pk, rows) {
  let { updates, deletes } = staged;
  for (const row of rows) {
    const id = rowId(row, pk);
    updates = without(updates, id);
    deletes = { ...deletes, [id]: { key: keyOf(row, pk) } };
  }
  return { ...staged, updates, deletes };
}
function removeNew(staged, ids) {
  return { ...staged, inserts: staged.inserts.filter((i) => !ids.includes(i.id)) };
}
function review(staged) {
  return [
    ...Object.entries(staged.deletes).map(([id, d]) => ({ id, kind: "delete", title: keyText(d.key), fields: [] })),
    ...Object.entries(staged.updates).map(([id, u]) => ({ id, kind: "update", title: keyText(u.key), fields: Object.keys(u.set).map((column) => ({ column, was: u.was[column], now: u.set[column] })) })),
    ...staged.inserts.map((i) => ({ id: i.id, kind: "insert", title: "New row", fields: Object.entries(i.set).map(([column, now]) => ({ column, now })) }))
  ];
}
function undo(staged, line) {
  if (line.kind === "insert") return removeNew(staged, [line.id]);
  if (line.kind === "delete") return { ...staged, deletes: without(staged.deletes, line.id) };
  return { ...staged, updates: without(staged.updates, line.id) };
}
function count(staged) {
  return Object.keys(staged.updates).length + Object.keys(staged.deletes).length + staged.inserts.length;
}
function changes(staged) {
  return [
    ...Object.values(staged.deletes).map((d) => ({ op: "delete", key: d.key })),
    ...Object.values(staged.updates).map((u) => ({ op: "update", key: u.key, set: u.set })),
    ...staged.inserts.map((i) => ({ op: "insert", set: i.set }))
  ];
}
function keyed(staged) {
  return [...Object.entries(staged.deletes), ...Object.entries(staged.updates)].map(([id, c]) => ({ id, key: c.key }));
}
function dropRows(staged, ids) {
  let { updates, deletes } = staged;
  for (const id of ids) {
    updates = without(updates, id);
    deletes = without(deletes, id);
  }
  return { ...staged, updates, deletes };
}
function summary(staged) {
  const parts = [
    [Object.keys(staged.updates).length, "edited"],
    [staged.inserts.length, "added"],
    [Object.keys(staged.deletes).length, "deleted"]
  ];
  return parts.filter(([n]) => n > 0).map(([n, what]) => `${n} ${what}`).join(", ");
}

// src/ui.tsx
import { Button as Button2, Empty, EmptyDescription, EmptyHeader, EmptyTitle, Icon as Icon2, Skeleton, cn as cn2 } from "@berth/plugin/ui";
import { Fragment as Fragment2, jsx as jsx3, jsxs as jsxs3 } from "react/jsx-runtime";
var HELPER = "berth-database";
var GUIDE = "https://docs.berthd.app/guides/database";
var COPY = {
  name: "Database",
  program: HELPER,
  icon: "table-2",
  does: (box) => `It notes the development database of each worktree on that box: a DATABASE_URL or POSTGRES_URL that points at a database on the box itself, in the environment of a service Shipyard starts there, in the worktree's own .env files, which the box reads when you open the worktree's Database tab, or in an address you paste for it. It keeps that address in a folder on the box, and runs what you ask of it here. It changes nothing in a service's environment, and only reads until you unlock writes. Nothing it keeps leaves ${box}.`,
  about: (box) => /* @__PURE__ */ jsxs3(Fragment2, { children: [
    "It shows a worktree's own development database: its tables and rows, and a console for SQL, read-only until you unlock it. Turning it on installs and runs ",
    /* @__PURE__ */ jsx3("code", { children: HELPER }),
    " on ",
    box,
    "; the database's address stays there."
  ] }),
  off: (box) => `No worktree's database is shown while it is off. Turning it on installs ${HELPER} on ${box} and keeps it running there; the addresses it kept before, if any, stay on the box until you remove them.`,
  remove: {
    title: (box) => `Remove what Database kept on ${box}?`,
    body: () => "deletes what it kept there: the address and password of each worktree's development database. The databases themselves aren't touched. Turn Database on again and open a worktree's Database tab, or start its services, to see its database here again; one you connected by pasting its address needs connecting again.",
    done: (box) => `Removed Database's data on ${box}`
  }
};
function Notice({ icon, title, body, actions }) {
  return /* @__PURE__ */ jsx3("div", { className: "flex h-full min-h-60 flex-1 items-center justify-center p-6", "data-testid": "database-notice", children: /* @__PURE__ */ jsxs3(Empty, { children: [
    /* @__PURE__ */ jsxs3(EmptyHeader, { children: [
      typeof icon === "string" ? /* @__PURE__ */ jsx3(Icon2, { name: icon, className: "mx-auto mb-2 size-5 text-muted-foreground" }) : icon,
      /* @__PURE__ */ jsx3(EmptyTitle, { children: title }),
      body && /* @__PURE__ */ jsx3(EmptyDescription, { className: "max-w-md text-pretty", children: body })
    ] }),
    actions && /* @__PURE__ */ jsx3("div", { className: "flex flex-wrap items-center justify-center gap-2", children: actions })
  ] }) });
}
function FaultText({ fault }) {
  return /* @__PURE__ */ jsxs3(Fragment2, { children: [
    fault.message,
    fault.hint && /* @__PURE__ */ jsx3("span", { className: "mt-1 block text-muted-foreground", children: fault.hint })
  ] });
}
function Waiting({ fault, onRetry, title = "Couldn't read from the database" }) {
  if (fault) {
    return /* @__PURE__ */ jsx3(
      Notice,
      {
        icon: "TriangleAlert",
        title,
        body: /* @__PURE__ */ jsx3(FaultText, { fault }),
        actions: onRetry && /* @__PURE__ */ jsx3(Button2, { size: "sm", variant: "outline", onClick: onRetry, children: "Try again" })
      }
    );
  }
  return /* @__PURE__ */ jsx3("div", { className: "flex-1 space-y-2 p-3", "aria-busy": "true", children: [0, 1, 2, 3, 4, 5].map((i) => /* @__PURE__ */ jsx3(Skeleton, { className: "h-7 w-full" }, i)) });
}
function Line({ tone = "note", children, actions, testId }) {
  const tones = { bad: "bg-destructive/8 text-destructive-foreground", note: "bg-muted/50 text-muted-foreground" };
  return /* @__PURE__ */ jsxs3("div", { role: "status", "data-testid": testId, className: cn2("flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-1.5 text-xs", tones[tone]), children: [
    /* @__PURE__ */ jsx3("span", { className: "min-w-0 flex-1 text-pretty", children }),
    actions
  ] });
}
function count2(n) {
  if (!Number.isFinite(n) || n < 0) return "\u2013";
  if (n < 1e3) return String(Math.round(n));
  if (n < 1e6) return `${(n / 1e3).toFixed(n < 1e4 ? 1 : 0).replace(/\.0$/, "")}k`;
  return `${(n / 1e6).toFixed(1).replace(/\.0$/, "")}M`;
}
function ms(n) {
  if (!Number.isFinite(n)) return "\u2013";
  if (n < 1) return `${Math.round(n * 1e3)}\xB5s`;
  if (n < 10) return `${n.toFixed(2)}ms`;
  if (n < 100) return `${n.toFixed(1)}ms`;
  if (n < 1e3) return `${Math.round(n)}ms`;
  return `${(n / 1e3).toFixed(2)}s`;
}
function left(msLeft) {
  const s = Math.max(0, Math.ceil(msLeft / 1e3));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}
var plural = (n, one, many = `${one}s`) => `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;

// src/browser.tsx
import { Fragment as Fragment3, jsx as jsx4, jsxs as jsxs4 } from "react/jsx-runtime";
var tableKey = (schema, table) => `${schema}
${table}`;
var changeOf = (message) => Number(/^Change (\d+)\b/.exec(message)?.[1] ?? 0);
var keyOfChange = (c) => c && c.op !== "insert" ? c.key : void 0;
function Browser({ ctx, schemas, onReload, toConsole }) {
  const [open, setOpen] = useState4();
  const [staged, setStaged] = useState4({});
  const table = open && schemas.find((s) => s.name === open.schema)?.tables.find((t) => t.name === open.table);
  const key = open ? tableKey(open.schema, open.table) : "";
  const setOne = useCallback3((k, next) => setStaged((all) => ({ ...all, [k]: next })), []);
  const go = useCallback3((schema, name, filters) => setOpen((o) => ({ schema, table: name, filters, n: (o?.n ?? 0) + 1 })), []);
  return /* @__PURE__ */ jsxs4("div", { className: "flex min-h-0 flex-1", children: [
    /* @__PURE__ */ jsx4(Tree, { ctx, schemas, open, staged, onOpen: go, onReload }),
    /* @__PURE__ */ jsx4("div", { className: cn3("min-h-0 min-w-0 flex-1 flex-col", open ? "flex" : "hidden @2xl:flex"), children: open && table ? /* @__PURE__ */ jsx4(TableView, { ctx, schema: open.schema, table, initial: open.filters, staged: staged[key] ?? NOTHING, setStaged: (s) => setOne(key, s), onBack: () => setOpen(void 0), onJump: go, toConsole }, `${key}
${open.n}`) : open ? /* @__PURE__ */ jsx4(Notice, { icon: "table-2", title: `${open.table} isn't there any more`, body: "The table was dropped or renamed since the list was read.", actions: /* @__PURE__ */ jsx4(Button3, { size: "sm", variant: "outline", onClick: () => setOpen(void 0), children: "Back to the tables" }) }) : /* @__PURE__ */ jsx4(Notice, { icon: "table-2", title: "Pick a table", body: `Its rows open here. ${ctx.conn.database} is read-only until you unlock writes.` }) })
  ] });
}
function Tree({ ctx, schemas, open, staged, onOpen, onReload }) {
  const [text, setText] = useState4("");
  const q = text.trim().toLowerCase();
  const shown = schemas.map((s) => ({ ...s, tables: s.tables.filter((t) => !q || t.name.toLowerCase().includes(q) || ctx.caps.schemas && `${s.name}.${t.name}`.toLowerCase().includes(q)) })).filter((s) => s.tables.length > 0 || !q && ctx.caps.schemas);
  const total = schemas.reduce((n, s) => n + s.tables.length, 0);
  return /* @__PURE__ */ jsxs4("nav", { "aria-label": "Tables", "data-testid": "database-tree", className: cn3("min-h-0 w-full shrink-0 flex-col @2xl:w-60 @2xl:border-r", open ? "hidden @2xl:flex" : "flex"), children: [
    /* @__PURE__ */ jsxs4("div", { className: "flex shrink-0 items-center gap-1.5 border-b p-2", children: [
      /* @__PURE__ */ jsx4(Input, { className: "min-w-0 flex-1", size: "sm", type: "search", placeholder: "Filter tables", "aria-label": "Filter tables", value: text, onChange: (e) => setText(e.target.value) }),
      /* @__PURE__ */ jsx4(Tip2, { label: "Read the tables again", children: /* @__PURE__ */ jsx4(Button3, { size: "icon-sm", variant: "ghost", "aria-label": "Read the tables again", onClick: onReload, children: /* @__PURE__ */ jsx4(Icon3, { name: "RefreshCw", className: "size-3.5" }) }) })
    ] }),
    /* @__PURE__ */ jsx4("div", { className: "min-h-0 flex-1 overflow-auto py-1", children: total === 0 ? /* @__PURE__ */ jsxs4("p", { className: "px-3 py-6 text-center text-muted-foreground text-xs", children: [
      ctx.conn.database,
      " has no tables yet."
    ] }) : shown.length === 0 ? /* @__PURE__ */ jsxs4("p", { className: "px-3 py-6 text-center text-muted-foreground text-xs", children: [
      "No table matches \u201C",
      text.trim(),
      "\u201D."
    ] }) : shown.map((s) => /* @__PURE__ */ jsxs4("section", { className: "pb-1", children: [
      ctx.caps.schemas && /* @__PURE__ */ jsx4("h3", { className: "px-3 pt-2 pb-1 font-medium text-muted-foreground text-xs", children: s.name }),
      s.tables.length === 0 && /* @__PURE__ */ jsx4("p", { className: "px-3 py-1 text-muted-foreground text-xs", children: "No tables." }),
      /* @__PURE__ */ jsx4("ul", { children: s.tables.map((t) => {
        const on = open?.schema === s.name && open.table === t.name;
        const kind = kindOf(t.kind);
        const n = count(staged[tableKey(s.name, t.name)] ?? NOTHING);
        return /* @__PURE__ */ jsx4("li", { children: /* @__PURE__ */ jsxs4(
          "button",
          {
            type: "button",
            "data-testid": "database-table",
            "aria-current": on ? "true" : void 0,
            onClick: () => onOpen(s.name, t.name),
            className: cn3("flex h-7 w-full items-center gap-2 px-3 text-left outline-none hover:bg-accent/50 focus-visible:bg-accent/60", on && "bg-accent/60 font-medium"),
            children: [
              /* @__PURE__ */ jsx4(Icon3, { name: kind.icon, className: "size-3.5 shrink-0 text-muted-foreground" }),
              /* @__PURE__ */ jsx4("span", { className: "min-w-0 flex-1 truncate", children: t.name }),
              n > 0 && /* @__PURE__ */ jsx4(Tip2, { label: `${plural(n, "staged change")}, not applied`, children: /* @__PURE__ */ jsx4("span", { role: "img", "aria-label": `${plural(n, "staged change")}, not applied`, className: "size-1.5 shrink-0 rounded-full bg-info" }) }),
              /* @__PURE__ */ jsx4("span", { className: "shrink-0 text-muted-foreground text-xs tabular-nums", children: t.kind === "table" ? t.rows_estimate >= 0 ? `~${count2(t.rows_estimate)}` : "" : kind.short })
            ]
          }
        ) }, t.name);
      }) })
    ] }, s.name)) })
  ] });
}
var NO_ROWS = /* @__PURE__ */ new Set();
function TableView({ ctx, schema, table, initial, staged, setStaged, onBack, onJump, toConsole }) {
  const { api, box, conn, engine, caps, writes } = ctx;
  const [mode, setMode] = useState4("rows");
  const [offset, setOffset] = useState4(0);
  const [trail, setTrail] = useState4([]);
  const [size, setSize] = useState4(PAGE);
  const [order, setOrder] = useState4([]);
  const [drafts, setDrafts] = useState4(initial ?? []);
  const nextId = useRef3((initial?.length ?? 0) + 1);
  const [picked, setPicked] = useState4(NO_ROWS);
  const [viewed, setViewed] = useState4();
  const [reviewing, setReviewing] = useState4(false);
  const [applying, setApplying] = useState4(false);
  const [refused, setRefused] = useState4();
  const [gone, setGone] = useState4([]);
  const [done, setDone] = useState4();
  const name = tableName(engine, schema, table.name);
  const rules = useMemo2(() => rulesFor(table, engine, caps), [table, engine, caps]);
  const familyOf = useCallback3((column) => engine.family(table.columns.find((c) => c.name === column)?.type ?? ""), [engine, table]);
  const filters = useMemo2(() => filtersOf(drafts, familyOf), [drafts, familyOf]);
  const request = useMemo2(() => ({ schema, table: table.name, limit: size, offset, order_by: order, filters }), [schema, table.name, size, offset, order, filters]);
  const read = useRead(() => api.rows(box, conn.id, request), JSON.stringify([conn.id, request]), { active: ctx.active && mode === "rows" });
  const data = read.data;
  useEffect3(() => {
    if (read.fault) ctx.onFault(read.fault);
  }, [read.fault]);
  const pk = data?.primary_key ?? table.primary_key;
  const columns = useMemo2(
    () => (data?.columns ?? table.columns).map((c) => {
      const known = table.columns.find((k) => k.name === c.name);
      return { name: c.name, type: c.type, family: engine.family(c.type), key: pk.includes(c.name), nullable: known?.nullable ?? true, fk: table.foreign_keys.find((f) => f.columns.includes(c.name)), generated: known?.default?.startsWith("generated always") };
    }),
    [data?.columns, table, engine, pk]
  );
  const originals = useMemo2(() => (data?.rows ?? []).map((r) => Object.fromEntries(columns.map((c, i) => [c.name, r[i] ?? null]))), [data?.rows, columns]);
  const cuts = useMemo2(() => {
    const out = /* @__PURE__ */ new Map();
    for (const [r, c] of data?.cut ?? []) out.set(r, (out.get(r) ?? /* @__PURE__ */ new Set()).add(c));
    return out;
  }, [data?.cut]);
  const keyColumns = useMemo2(() => pk.map((k) => columns.findIndex((c) => c.name === k)), [pk, columns]);
  const rows = useMemo2(() => {
    const fresh = staged.inserts.map((i) => ({ id: i.id, state: "new", cells: columns.map((c) => i.set[c.name] ?? null), unset: new Set(columns.flatMap((c, at) => c.name in i.set ? [] : [at])), changed: new Set(columns.flatMap((c, at) => c.name in i.set ? [at] : [])) }));
    const loaded = (data?.rows ?? []).map((cells, at) => {
      const id = pk.length ? rowId(originals[at], pk) : `row-${at}`;
      const cut = cuts.get(at);
      if (staged.deletes[id]) return { id, ref: at, cells, cut, state: gone.includes(id) ? "gone" : "deleted" };
      const update = staged.updates[id];
      if (!update) return { id, ref: at, cells, cut };
      return { id, ref: at, cut, state: gone.includes(id) ? "gone" : void 0, cells: cells.map((v, i) => columns[i].name in update.set ? update.set[columns[i].name] : v), changed: new Set(columns.flatMap((c, i) => c.name in update.set ? [i] : [])) };
    });
    return [...fresh, ...loaded];
  }, [data?.rows, staged, columns, originals, cuts, pk, gone]);
  const editing = writes.on && (rules.rows || rules.inserts);
  const canEdit = useCallback3(
    (row, col) => {
      if (!editing || columns[col]?.generated) return false;
      if (row.state === "new") return rules.inserts;
      return rules.rows && row.state !== "deleted" && !row.cut?.has(col) && !keyColumns.some((k) => row.cut?.has(k));
    },
    [editing, rules, keyColumns, columns]
  );
  const onEdit = useCallback3(
    (row, col, value) => {
      setDone(void 0);
      if (row.state === "new") setStaged(setNew(staged, row.id, columns[col].name, value));
      else if (row.ref !== void 0) setStaged(setCell(staged, pk, originals[row.ref], columns[col].name, value));
    },
    [staged, setStaged, columns, pk, originals]
  );
  const titleOf = useCallback3((row) => row.state === "new" ? "New row" : row.ref !== void 0 && pk.length ? keyText(keyOf(originals[row.ref], pk)) : `Row ${offset + (row.ref ?? 0) + 1}`, [originals, pk, offset]);
  const onView = useCallback3(
    (row, col) => {
      const c = columns[col];
      setViewed({ column: c.name, type: c.type, family: c.family, row: `${name}, ${titleOf(row)}`, cell: row.cells[col] ?? null, cut: row.cut?.has(col), nullable: c.nullable, unset: row.unset?.has(col), onStage: canEdit(row, col) ? (v) => onEdit(row, col, v) : void 0 });
    },
    [columns, name, titleOf, canEdit, onEdit]
  );
  const jump = useCallback3(
    (row, col) => {
      const fk = columns[col].fk;
      const to2 = fk && row.ref !== void 0 && jumpFilters(fk, originals[row.ref]);
      if (fk && to2) onJump(fk.ref_schema, fk.ref_table, to2);
    },
    [columns, originals, onJump]
  );
  const first = () => {
    setPicked(NO_ROWS);
    setTrail([]);
    setOffset(0);
  };
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
  const narrow = (next2) => {
    setDrafts(next2);
    first();
  };
  const addFilter = (column) => {
    const ops = opsFor(caps.operators, familyOf(column));
    if (ops.length === 0) return;
    setMode("rows");
    setDrafts((d) => [...d, { id: nextId.current++, column, op: ops[0] }]);
  };
  const n = count(staged);
  const removePicked = () => {
    const ids = [...picked];
    const fresh = ids.filter((id) => staged.inserts.some((i) => i.id === id));
    const loaded = rows.filter((r) => r.ref !== void 0 && picked.has(r.id)).map((r) => originals[r.ref]);
    setStaged(removeRows(removeNew(staged, fresh), pk, rules.rows ? loaded : []));
    setPicked(NO_ROWS);
    setDone(void 0);
  };
  const missing = async () => {
    const asked = keyed(staged).slice(0, 40);
    const found = await Promise.all(
      asked.map(
        (k) => api.rows(box, conn.id, { schema, table: table.name, limit: 1, filters: Object.entries(k.key).map(([column, value]) => ({ column, op: "=", value })) }).then((r) => r.rows.length > 0).catch(() => true)
      )
    );
    return asked.filter((_, i) => !found[i]).map((k) => k.id);
  };
  const apply = async () => {
    setRefused(void 0);
    setGone([]);
    if (count(staged) > MOST_CHANGES) {
      setRefused({ message: `One edit takes at most ${MOST_CHANGES.toLocaleString("en-US")} changes, and ${count(staged).toLocaleString("en-US")} are staged.`, hint: "Take some back in Review, apply, and stage the rest again." });
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
  const lines = useMemo2(() => review(staged), [staged]);
  const goneKeys = keyed(staged).filter((k) => gone.includes(k.id));
  const from = offset + 1;
  const to = offset + (data?.rows.length ?? 0);
  return /* @__PURE__ */ jsxs4(Fragment3, { children: [
    /* @__PURE__ */ jsxs4("div", { className: "flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b px-3 py-1.5", "data-testid": "database-table-bar", children: [
      /* @__PURE__ */ jsx4(Tip2, { label: "Back to the tables", children: /* @__PURE__ */ jsx4(Button3, { size: "icon-xs", variant: "ghost", "aria-label": "Back to the tables", className: "@2xl:hidden", onClick: onBack, children: /* @__PURE__ */ jsx4(Icon3, { name: "ArrowLeft", className: "size-3.5" }) }) }),
      /* @__PURE__ */ jsx4(Icon3, { name: kind.icon, className: "size-4 shrink-0 text-muted-foreground" }),
      /* @__PURE__ */ jsx4("h2", { className: "truncate font-medium", children: name }),
      table.kind !== "table" && /* @__PURE__ */ jsx4(Badge2, { variant: "outline", size: "sm", className: "shrink-0 font-normal", children: kind.label }),
      /* @__PURE__ */ jsx4(
        PickOne,
        {
          label: "What to show of the table",
          value: mode,
          onChange: (v) => setMode(v),
          options: [
            { value: "rows", label: "Rows" },
            { value: "structure", label: "Structure" }
          ]
        }
      ),
      /* @__PURE__ */ jsxs4("span", { className: "ml-auto flex flex-wrap items-center gap-1.5", children: [
        mode === "rows" && editing && picked.size > 0 && /* @__PURE__ */ jsxs4(Button3, { size: "xs", variant: "destructive-outline", onClick: removePicked, children: [
          /* @__PURE__ */ jsx4(Icon3, { name: "trash-2", className: "size-3.5" }),
          "Delete ",
          plural(picked.size, "row")
        ] }),
        mode === "rows" && editing && rules.inserts && /* @__PURE__ */ jsxs4(Button3, { size: "xs", variant: "outline", onClick: () => (setStaged(addRow(staged)[0]), setDone(void 0)), children: [
          /* @__PURE__ */ jsx4(Icon3, { name: "Plus", className: "size-3.5" }),
          "Add row"
        ] }),
        mode === "rows" && caps.operators.length > 0 && /* @__PURE__ */ jsxs4(Menu, { children: [
          /* @__PURE__ */ jsxs4(MenuTrigger, { render: /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "outline" }), children: [
            /* @__PURE__ */ jsx4(Icon3, { name: "ListFilter", className: "size-3.5" }),
            "Filter"
          ] }),
          /* @__PURE__ */ jsx4(MenuPopup, { align: "end", className: "max-h-80 min-w-44", children: table.columns.map((c) => /* @__PURE__ */ jsx4(MenuItem, { onClick: () => addFilter(c.name), children: /* @__PURE__ */ jsx4("span", { className: "font-mono text-xs", children: c.name }) }, c.name)) })
        ] }),
        /* @__PURE__ */ jsx4(Tip2, { label: "Write a SELECT for this table in the console", children: /* @__PURE__ */ jsxs4(Button3, { size: "xs", variant: "ghost", onClick: () => toConsole(`SELECT * FROM ${tableName(engine, schema, table.name, true)} LIMIT 100;`), children: [
          /* @__PURE__ */ jsx4(Icon3, { name: "SquareTerminal", className: "size-3.5" }),
          "Query"
        ] }) }),
        mode === "rows" && /* @__PURE__ */ jsx4(Tip2, { label: "Read the rows again", children: /* @__PURE__ */ jsx4(Button3, { size: "icon-xs", variant: "ghost", "aria-label": "Read the rows again", loading: read.loading && !!data, onClick: read.reload, children: /* @__PURE__ */ jsx4(Icon3, { name: "RefreshCw", className: "size-3.5" }) }) })
      ] })
    ] }),
    rules.why && /* @__PURE__ */ jsxs4(Line, { tone: "note", testId: "database-browse-only", children: [
      /* @__PURE__ */ jsx4(Icon3, { name: "Lock", className: "mr-1.5 inline size-3 align-[-1px]" }),
      rules.rows || rules.inserts ? "" : "Browse only. ",
      rules.why
    ] }),
    mode === "structure" ? /* @__PURE__ */ jsx4(Structure, { table, schema, ctx }) : /* @__PURE__ */ jsxs4(Fragment3, { children: [
      drafts.length > 0 && /* @__PURE__ */ jsxs4("div", { className: "flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 border-b px-3 py-1.5", "data-testid": "database-filters", children: [
        drafts.map((d) => /* @__PURE__ */ jsx4(FilterRow, { draft: d, columns: table.columns.map((c) => c.name), ops: opsFor(caps.operators, familyOf(d.column)), opsOf: (column) => opsFor(caps.operators, familyOf(column)), onChange: (next2) => narrow(drafts.map((x) => x.id === d.id ? next2 : x)), onRemove: () => narrow(drafts.filter((x) => x.id !== d.id)) }, d.id)),
        drafts.length > 1 && /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "ghost", className: "text-muted-foreground", onClick: () => narrow([]), children: "Clear all" })
      ] }),
      refused && /* @__PURE__ */ jsx4(
        Line,
        {
          tone: "bad",
          testId: "database-refused",
          actions: /* @__PURE__ */ jsxs4(Fragment3, { children: [
            refused.code === "row_conflict" && /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "outline", onClick: read.reload, children: "Reload the rows" }),
            goneKeys.length > 0 && /* @__PURE__ */ jsxs4(
              Button3,
              {
                size: "xs",
                variant: "outline",
                onClick: () => {
                  setStaged(dropRows(staged, gone));
                  setGone([]);
                  setRefused(void 0);
                },
                children: [
                  "Drop ",
                  goneKeys.length === 1 ? "that change" : `those ${goneKeys.length} changes`
                ]
              }
            ),
            refused.code === "read_only" && /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "outline", onClick: writes.ask, children: "Unlock writes\u2026" }),
            /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "ghost", onClick: () => (setRefused(void 0), setGone([])), children: "Dismiss" })
          ] }),
          children: refused.code === "row_conflict" ? /* @__PURE__ */ jsxs4(Fragment3, { children: [
            /* @__PURE__ */ jsx4("b", { children: "Nothing was applied." }),
            " ",
            goneKeys.length > 0 ? `${goneKeys.length === 1 ? "A row you changed is" : `${goneKeys.length} rows you changed are`} no longer there: ${goneKeys.slice(0, 4).map((k) => keyText(k.key)).join("; ")}${goneKeys.length > 4 ? `, and ${goneKeys.length - 4} more` : ""}. Something else deleted or re-keyed ${goneKeys.length === 1 ? "it" : "them"} since you read ${name}.` : `${refused.message} Something else changed ${name} since you read it.`,
            " ",
            "Your changes are still staged."
          ] }) : refused.code === "read_only" ? /* @__PURE__ */ jsxs4(Fragment3, { children: [
            /* @__PURE__ */ jsx4("b", { children: "Nothing was applied:" }),
            " writes are locked. Your changes are still staged."
          ] }) : /* @__PURE__ */ jsxs4(Fragment3, { children: [
            /* @__PURE__ */ jsx4("b", { children: "Nothing was applied." }),
            " ",
            refused.message,
            refused.detail && ` ${refused.detail}`,
            refused.hint && refused.code !== "query_failed" && ` ${refused.hint}`,
            " Your changes are still staged."
          ] })
        }
      ),
      done && !refused && n === 0 && /* @__PURE__ */ jsxs4(Line, { tone: "note", testId: "database-applied", actions: /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "ghost", onClick: () => setDone(void 0), children: "Dismiss" }), children: [
        "Applied to ",
        name,
        ": ",
        [done.updated && `${plural(done.updated, "row")} updated`, done.inserted && `${plural(done.inserted, "row")} added`, done.deleted && `${plural(done.deleted, "row")} deleted`].filter(Boolean).join(", ") || "nothing changed",
        "."
      ] }),
      !data ? /* @__PURE__ */ jsx4(Waiting, { fault: read.fault, onRetry: read.reload, title: `Couldn't read ${name}` }) : /* @__PURE__ */ jsxs4(Fragment3, { children: [
        read.fault && /* @__PURE__ */ jsxs4(Line, { tone: "bad", children: [
          "Couldn't read again: ",
          read.fault.message
        ] }),
        rows.length === 0 ? /* @__PURE__ */ jsx4(Notice, { icon: filters.length ? "Search" : "table-2", title: filters.length ? "No rows match" : offset > 0 ? "No more rows" : `${name} is empty`, body: filters.length ? "Nothing in the table matches the filters above." : offset > 0 ? "The page before was the last." : rules.inserts && !editing ? "Unlock writes to add a row." : void 0 }) : /* @__PURE__ */ jsx4(Grid, { label: `Rows of ${name}`, columns, rows, offset, order, onSort: (column) => (setOrder(nextOrder(order, column)), first()), onFilter: caps.operators.length ? addFilter : void 0, canEdit, onEdit: editing ? onEdit : void 0, onView, onJump: jump, picked, canPick: (r) => r.state === "new" || rules.rows && r.state !== "deleted", onPick: editing ? (ids, on) => setPicked((p) => new Set(on ? [...p, ...ids] : [...p].filter((id) => !ids.includes(id)))) : void 0 })
      ] }),
      n > 0 && /* @__PURE__ */ jsxs4("div", { className: "flex shrink-0 flex-wrap items-center gap-2 border-t bg-muted/50 px-3 py-1.5", "data-testid": "database-staged", role: "status", children: [
        /* @__PURE__ */ jsx4(Icon3, { name: "PencilLine", className: "size-3.5 shrink-0 text-muted-foreground" }),
        /* @__PURE__ */ jsxs4("span", { className: "min-w-0 flex-1 text-sm", children: [
          /* @__PURE__ */ jsx4("b", { className: "font-medium", children: plural(n, "staged change") }),
          " ",
          /* @__PURE__ */ jsxs4("span", { className: "text-muted-foreground", children: [
            "in ",
            name,
            ": ",
            summary(staged),
            ". Nothing is written until you apply."
          ] })
        ] }),
        /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "ghost", onClick: () => (setStaged(NOTHING), setGone([]), setRefused(void 0)), children: "Discard" }),
        /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "outline", onClick: () => setReviewing(true), children: "Review\u2026" }),
        writes.on ? /* @__PURE__ */ jsx4(Button3, { size: "xs", loading: applying, onClick: () => void apply(), children: "Apply" }) : /* @__PURE__ */ jsx4(Tip2, { label: "Writes are locked: unlock them to apply what is staged", children: /* @__PURE__ */ jsx4(Button3, { size: "xs", onClick: writes.ask, children: "Unlock to apply\u2026" }) })
      ] }),
      data && /* @__PURE__ */ jsxs4("div", { className: "flex min-h-9 shrink-0 items-center gap-2 border-t px-3 py-1 text-muted-foreground text-xs", "data-testid": "database-pages", children: [
        /* @__PURE__ */ jsxs4("span", { className: "tabular-nums", "data-testid": "database-range", children: [
          data.rows.length === 0 ? "No rows" : `Rows ${from.toLocaleString("en-US")}\u2013${to.toLocaleString("en-US")}`,
          filters.length === 0 && data.rows_estimate > 0 && ` of about ${data.rows_estimate.toLocaleString("en-US")}`
        ] }),
        /* @__PURE__ */ jsxs4("span", { className: "ml-auto flex items-center gap-1", children: [
          /* @__PURE__ */ jsx4(
            PickOne,
            {
              label: "Rows a page",
              className: "mr-2",
              value: String(size),
              onChange: (v) => (setSize(Number(v)), first()),
              options: [
                { value: String(PAGE), label: String(PAGE) },
                { value: String(MOST), label: String(MOST) }
              ]
            }
          ),
          /* @__PURE__ */ jsxs4(Button3, { size: "xs", variant: "ghost", disabled: offset === 0, onClick: previous, children: [
            /* @__PURE__ */ jsx4(Icon3, { name: "ChevronLeft", className: "size-3.5" }),
            "Previous"
          ] }),
          /* @__PURE__ */ jsxs4(Button3, { size: "xs", variant: "ghost", disabled: !data.more || data.rows.length === 0, onClick: next, children: [
            "Next",
            /* @__PURE__ */ jsx4(Icon3, { name: "ChevronRight", className: "size-3.5" })
          ] })
        ] })
      ] })
    ] }),
    /* @__PURE__ */ jsx4(ValueSheet, { viewed, onClose: () => setViewed(void 0) }),
    /* @__PURE__ */ jsx4(Sheet2, { open: reviewing, onOpenChange: setReviewing, children: /* @__PURE__ */ jsx4(SheetPopup2, { side: "right", className: "max-w-xl", children: /* @__PURE__ */ jsxs4("div", { className: "flex min-h-0 flex-1 flex-col", "data-testid": "database-review", children: [
      /* @__PURE__ */ jsxs4(SheetHeader2, { className: "gap-1.5 pb-3", children: [
        /* @__PURE__ */ jsxs4(SheetTitle2, { className: "text-base", children: [
          "Review changes to ",
          name
        ] }),
        /* @__PURE__ */ jsxs4(SheetDescription2, { className: "text-[13px]", children: [
          caps.transactions ? `${plural(n, "change")}, sent together: all of them apply, or none do.` : `${plural(n, "change")}. ${engine.name} has no transactions, so they apply one after another, and one that fails leaves the ones before it applied.`,
          " A row is found by its key: if something else changed it since you read it, your values replace theirs without a word. Only a row that is gone stops the edit."
        ] })
      ] }),
      /* @__PURE__ */ jsxs4(SheetPanel2, { className: "min-h-0 flex-1 space-y-2 overflow-auto", children: [
        lines.map((l) => (
          // One card a change: its kind is the badge's to say.
          /* @__PURE__ */ jsxs4(Card, { className: "overflow-hidden rounded-xl before:rounded-[calc(var(--radius-xl)-1px)]", "data-testid": "database-change", "data-kind": l.kind, children: [
            /* @__PURE__ */ jsxs4(CardHeader, { className: "flex items-center gap-2 border-b px-3 py-1.5 in-[[data-slot=card]:has(>[data-slot=card-panel])]:pb-1.5", children: [
              /* @__PURE__ */ jsx4(Badge2, { variant: l.kind === "insert" ? "success" : "outline", size: "sm", className: cn3("shrink-0", l.kind === "delete" && "text-destructive-foreground"), children: l.kind === "delete" ? "Delete" : l.kind === "insert" ? "Add" : "Update" }),
              /* @__PURE__ */ jsx4("span", { className: "min-w-0 flex-1 truncate font-mono text-xs", children: l.title }),
              gone.includes(l.id) && /* @__PURE__ */ jsx4(Badge2, { variant: "outline", size: "sm", className: "shrink-0", children: "No longer there" }),
              /* @__PURE__ */ jsx4(Tip2, { label: "Take this change back", children: /* @__PURE__ */ jsx4(Button3, { size: "icon-xs", variant: "ghost", "aria-label": `Take back the ${l.kind} of ${l.title}`, onClick: () => setStaged(undo(staged, l)), children: /* @__PURE__ */ jsx4(Icon3, { name: "undo-2", className: "size-3.5" }) }) })
            ] }),
            /* @__PURE__ */ jsx4(CardPanel, { className: "p-0", children: l.kind === "delete" ? /* @__PURE__ */ jsx4("p", { className: "px-3 py-2 text-muted-foreground text-xs", children: "The whole row goes." }) : l.fields.length === 0 ? /* @__PURE__ */ jsx4("p", { className: "px-3 py-2 text-muted-foreground text-xs", children: "Every column takes its default." }) : /* @__PURE__ */ jsx4("dl", { className: "divide-y", children: l.fields.map((f) => /* @__PURE__ */ jsxs4("div", { className: "flex items-baseline gap-2 px-3 py-1.5 font-mono text-xs", children: [
              /* @__PURE__ */ jsx4("dt", { className: "w-32 shrink-0 truncate text-muted-foreground", children: f.column }),
              /* @__PURE__ */ jsxs4("dd", { className: "min-w-0 flex-1 break-words", children: [
                l.kind === "update" && /* @__PURE__ */ jsxs4(Fragment3, { children: [
                  /* @__PURE__ */ jsx4("span", { className: "text-muted-foreground line-through", children: quote(f.was ?? null) }),
                  /* @__PURE__ */ jsx4("span", { className: "mx-1.5 text-muted-foreground", "aria-label": "becomes", children: "\u2192" })
                ] }),
                /* @__PURE__ */ jsx4("span", { className: "font-medium", children: quote(f.now) })
              ] })
            ] }, f.column)) }) })
          ] }, `${l.kind}
${l.id}`)
        )),
        lines.length === 0 && /* @__PURE__ */ jsx4("p", { className: "rounded-lg border border-dashed px-3 py-6 text-center text-muted-foreground text-sm", children: "Nothing is staged." })
      ] }),
      /* @__PURE__ */ jsxs4(SheetFooter2, { children: [
        /* @__PURE__ */ jsx4(Button3, { variant: "ghost", onClick: () => setReviewing(false), children: "Close" }),
        writes.on ? /* @__PURE__ */ jsxs4(Button3, { loading: applying, disabled: n === 0, onClick: () => void apply(), children: [
          "Apply ",
          plural(n, "change")
        ] }) : /* @__PURE__ */ jsx4(Button3, { disabled: n === 0, onClick: writes.ask, children: "Unlock to apply\u2026" })
      ] })
    ] }) }) })
  ] });
}
function FilterRow({ draft, columns, ops, opsOf, onChange, onRemove }) {
  const [text, setText] = useState4(draft.value ?? "");
  const input = useRef3(null);
  useEffect3(() => {
    if (draft.value === void 0) input.current?.focus();
  }, []);
  const enter = () => text !== draft.value && onChange({ ...draft, value: text });
  return /* @__PURE__ */ jsxs4("span", { className: "flex items-center gap-1", "data-testid": "database-filter", "data-column": draft.column, children: [
    /* @__PURE__ */ jsxs4(Menu, { children: [
      /* @__PURE__ */ jsx4(MenuTrigger, { render: /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "outline", className: "font-mono", "aria-label": `Filtered column: ${draft.column}` }), children: draft.column }),
      /* @__PURE__ */ jsx4(MenuPopup, { align: "start", className: "max-h-80 min-w-44", children: columns.map((c) => /* @__PURE__ */ jsx4(
        MenuItem,
        {
          onClick: () => {
            const allowed = opsOf(c);
            onChange({ ...draft, column: c, op: allowed.includes(draft.op) ? draft.op : allowed[0] ?? draft.op });
          },
          children: /* @__PURE__ */ jsx4("span", { className: "font-mono text-xs", children: c })
        },
        c
      )) })
    ] }),
    /* @__PURE__ */ jsxs4(Menu, { children: [
      /* @__PURE__ */ jsx4(MenuTrigger, { render: /* @__PURE__ */ jsx4(Button3, { size: "xs", variant: "outline", "aria-label": `How ${draft.column} is compared: ${opLabel(draft.op)}` }), children: opLabel(draft.op) }),
      /* @__PURE__ */ jsx4(MenuPopup, { align: "start", className: "min-w-36", children: ops.map((op) => /* @__PURE__ */ jsx4(MenuItem, { onClick: () => onChange({ ...draft, op, value: takesValue(op) ? draft.value : void 0 }), children: opLabel(op) }, op)) })
    ] }),
    takesValue(draft.op) && /* @__PURE__ */ jsx4(
      Input,
      {
        ref: input,
        size: "sm",
        className: "w-36",
        "aria-label": `Value for ${draft.column}`,
        placeholder: draft.value === void 0 ? "value, then \u21B5" : "empty string",
        value: text,
        onChange: (e) => setText(e.target.value),
        onKeyDown: (e) => e.key === "Enter" && enter(),
        onBlur: () => (draft.value !== void 0 || text !== "") && enter()
      }
    ),
    /* @__PURE__ */ jsx4(Tip2, { label: "Remove this filter", children: /* @__PURE__ */ jsx4(Button3, { size: "icon-xs", variant: "ghost", "aria-label": `Remove the filter on ${draft.column}`, onClick: onRemove, children: /* @__PURE__ */ jsx4(Icon3, { name: "X", className: "size-3.5" }) }) })
  ] });
}
var th = "h-8 whitespace-nowrap border-b px-3 text-left font-medium text-muted-foreground text-xs";
var td = "border-b px-3 py-1.5 align-top";
function Structure({ table, schema, ctx }) {
  const name = tableName(ctx.engine, schema, table.name);
  return /* @__PURE__ */ jsxs4("div", { className: "min-h-0 flex-1 space-y-5 overflow-auto p-4", "data-testid": "database-structure", children: [
    /* @__PURE__ */ jsx4(Part, { title: "Columns", hint: plural(table.columns.length, "column"), children: /* @__PURE__ */ jsx4("div", { className: "overflow-x-auto rounded-lg border", children: /* @__PURE__ */ jsxs4("table", { className: "w-full border-separate border-spacing-0 text-sm", children: [
      /* @__PURE__ */ jsx4("thead", { children: /* @__PURE__ */ jsxs4("tr", { children: [
        /* @__PURE__ */ jsx4("th", { scope: "col", className: th, children: "Name" }),
        /* @__PURE__ */ jsx4("th", { scope: "col", className: th, children: "Type" }),
        /* @__PURE__ */ jsx4("th", { scope: "col", className: th, children: "NULL" }),
        /* @__PURE__ */ jsx4("th", { scope: "col", className: th, children: "Default" })
      ] }) }),
      /* @__PURE__ */ jsx4("tbody", { className: "[&_tr:last-child_td]:border-b-0", children: table.columns.map((c) => /* @__PURE__ */ jsxs4("tr", { children: [
        /* @__PURE__ */ jsx4("td", { className: cn3(td, "font-mono text-xs"), children: /* @__PURE__ */ jsxs4("span", { className: "flex items-center gap-1.5", children: [
          c.name,
          (c.primary_key || table.primary_key.includes(c.name)) && /* @__PURE__ */ jsx4(Tip2, { label: "Part of the primary key", children: /* @__PURE__ */ jsx4("span", { role: "img", "aria-label": "Part of the primary key", children: /* @__PURE__ */ jsx4(Icon3, { name: "KeyRound", className: "size-3 text-muted-foreground" }) }) })
        ] }) }),
        /* @__PURE__ */ jsx4("td", { className: cn3(td, "font-mono text-muted-foreground text-xs"), children: c.type }),
        /* @__PURE__ */ jsx4("td", { className: cn3(td, "text-xs"), children: c.nullable ? "allowed" : /* @__PURE__ */ jsx4("span", { className: "text-muted-foreground", children: "never" }) }),
        /* @__PURE__ */ jsx4("td", { className: cn3(td, "font-mono text-muted-foreground text-xs"), children: c.default ?? "" })
      ] }, c.name)) })
    ] }) }) }),
    /* @__PURE__ */ jsx4(Part, { title: "Primary key", children: table.primary_key.length ? /* @__PURE__ */ jsx4("p", { className: "rounded-lg border px-3 py-2 font-mono text-xs", children: table.primary_key.join(", ") }) : /* @__PURE__ */ jsxs4(Nothing, { children: [
      name,
      " has no primary key",
      table.kind === "table" ? ": its rows can be added here, not edited or deleted." : "."
    ] }) }),
    /* @__PURE__ */ jsx4(Part, { title: "Indexes", children: table.indexes.length === 0 ? /* @__PURE__ */ jsx4(Nothing, { children: "None." }) : /* @__PURE__ */ jsx4("ul", { className: "divide-y rounded-lg border", children: table.indexes.map((i) => /* @__PURE__ */ jsxs4("li", { className: "flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-1.5 font-mono text-xs", children: [
      /* @__PURE__ */ jsx4("span", { children: i.name }),
      /* @__PURE__ */ jsxs4("span", { className: "text-muted-foreground", children: [
        "(",
        i.columns.join(", "),
        ")"
      ] }),
      i.unique && /* @__PURE__ */ jsx4(Badge2, { variant: "outline", size: "sm", className: "ml-auto font-normal font-sans", children: "Unique" })
    ] }, i.name)) }) }),
    /* @__PURE__ */ jsx4(Part, { title: "Foreign keys", children: table.foreign_keys.length === 0 ? /* @__PURE__ */ jsxs4(Nothing, { children: [
      "None: ",
      name,
      " points at no other table."
    ] }) : /* @__PURE__ */ jsx4("ul", { className: "divide-y rounded-lg border", children: table.foreign_keys.map((f) => /* @__PURE__ */ jsxs4("li", { className: "flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-1.5 font-mono text-xs", children: [
      /* @__PURE__ */ jsxs4("span", { children: [
        "(",
        f.columns.join(", "),
        ")"
      ] }),
      /* @__PURE__ */ jsx4("span", { className: "text-muted-foreground", "aria-label": "points at", children: "\u2192" }),
      /* @__PURE__ */ jsxs4("span", { children: [
        tableName(ctx.engine, f.ref_schema, f.ref_table),
        " (",
        f.ref_columns.join(", "),
        ")"
      ] })
    ] }, f.columns.join(","))) }) })
  ] });
}
function Part({ title, hint, children }) {
  return /* @__PURE__ */ jsxs4("section", { children: [
    /* @__PURE__ */ jsxs4("h3", { className: "mb-1.5 flex items-baseline gap-2 font-medium text-sm", children: [
      title,
      hint && /* @__PURE__ */ jsx4("span", { className: "font-normal text-muted-foreground text-xs", children: hint })
    ] }),
    children
  ] });
}
function Nothing({ children }) {
  return /* @__PURE__ */ jsx4("p", { className: "rounded-lg border border-dashed px-3 py-3 text-muted-foreground text-xs", children });
}

// src/connect.tsx
import { AlertDialog, AlertDialogClose, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogPopup, AlertDialogTitle, Button as Button4, Icon as Icon4, Input as Input2, Tip as Tip3 } from "@berth/plugin/ui";
import { useId, useState as useState5 } from "react";
import { jsx as jsx5, jsxs as jsxs5 } from "react/jsx-runtime";
function ConnectDialog({ open, api, at, name, onConnected, onClose }) {
  const [address, setAddress] = useState5("");
  const [shown, setShown] = useState5(false);
  const [busy, setBusy] = useState5(false);
  const [problem, setProblem] = useState5();
  const field = useId();
  const { box } = at;
  const close = () => {
    setAddress("");
    setShown(false);
    setProblem(void 0);
    onClose();
  };
  const go = async () => {
    const wrong = addressProblem(address);
    if (wrong) {
      setProblem(wrong);
      return;
    }
    setBusy(true);
    setProblem(void 0);
    try {
      const made = connected(await api.connect(box, at.location, at.worktree, address.trim()));
      if ("id" in made) {
        onConnected(made.id);
        close();
      } else if ("old" in made) {
        setProblem(`${box}'s berthd is from before it could take an address. Update the box in Settings \u2192 Boxes, then connect the database again.`);
      } else {
        setProblem(`${HELPER} on ${box} didn't take this address. It opens a Postgres on ${box} itself and nothing else: one at localhost, 127.0.0.1 or a Unix socket. Nothing was kept.`);
      }
    } catch (err) {
      const fault = faultOf(err);
      setProblem(cannotLook(fault) ? `${box}'s berthd is from before it could take an address. Update the box in Settings \u2192 Boxes, then connect the database again.` : fault.message);
    } finally {
      setBusy(false);
    }
  };
  return /* @__PURE__ */ jsx5(
    AlertDialog,
    {
      open,
      onOpenChange: (o) => {
        if (!o) close();
      },
      children: /* @__PURE__ */ jsxs5(AlertDialogPopup, { className: "sm:max-w-lg", "data-testid": "database-connect", children: [
        /* @__PURE__ */ jsxs5(AlertDialogHeader, { children: [
          /* @__PURE__ */ jsxs5(AlertDialogTitle, { children: [
            "Connect a database to ",
            name,
            " on ",
            box
          ] }),
          /* @__PURE__ */ jsxs5(AlertDialogDescription, { children: [
            "Paste the address of ",
            name,
            "'s development database. It has to be a Postgres on ",
            box,
            " itself (",
            /* @__PURE__ */ jsx5("code", { children: "localhost" }),
            ", ",
            /* @__PURE__ */ jsx5("code", { children: "127.0.0.1" }),
            " or a Unix socket): ",
            /* @__PURE__ */ jsx5("code", { children: HELPER }),
            " opens no other, and a database on another machine is not taken."
          ] })
        ] }),
        /* @__PURE__ */ jsxs5(
          "form",
          {
            className: "mx-6 space-y-2 pb-4",
            autoComplete: "off",
            onSubmit: (e) => {
              e.preventDefault();
              void go();
            },
            children: [
              /* @__PURE__ */ jsx5("label", { htmlFor: field, className: "block font-medium text-sm", children: "Address" }),
              /* @__PURE__ */ jsxs5("div", { className: "flex items-center gap-1.5", children: [
                /* @__PURE__ */ jsx5(
                  Input2,
                  {
                    id: field,
                    type: shown ? "text" : "password",
                    value: address,
                    onChange: (e) => {
                      setAddress(e.target.value);
                      setProblem(void 0);
                    },
                    placeholder: EXAMPLE,
                    autoComplete: "off",
                    autoCorrect: "off",
                    autoCapitalize: "off",
                    spellCheck: false,
                    "data-1p-ignore": true,
                    "data-lpignore": "true",
                    "aria-invalid": problem ? true : void 0,
                    "aria-describedby": `${field}-kept`,
                    className: "min-w-0 flex-1 font-mono",
                    autoFocus: true
                  }
                ),
                /* @__PURE__ */ jsx5(Tip3, { label: shown ? "Hide the address" : "Show the address", children: /* @__PURE__ */ jsx5(Button4, { type: "button", size: "icon-sm", variant: "ghost", "aria-label": shown ? "Hide the address" : "Show the address", "aria-pressed": shown, onClick: () => setShown((s) => !s), children: /* @__PURE__ */ jsx5(Icon4, { name: shown ? "EyeOff" : "Eye", className: "size-3.5" }) }) })
              ] }),
              problem && /* @__PURE__ */ jsx5("p", { role: "alert", className: "whitespace-pre-wrap rounded-md bg-destructive/8 p-2 text-destructive-foreground text-xs", "data-testid": "database-connect-problem", children: problem }),
              /* @__PURE__ */ jsxs5("p", { id: `${field}-kept`, className: "text-pretty text-muted-foreground text-xs", children: [
                "The address is kept on ",
                box,
                ", in a file only you can read there, until ",
                name,
                " is removed, you forget the connection, or Database's data on ",
                box,
                " is removed. It isn't kept on this computer, and isn't shown again: the connection shows as its database, host, port and user. It opens read-only, like any other."
              ] })
            ]
          }
        ),
        /* @__PURE__ */ jsxs5(AlertDialogFooter, { children: [
          /* @__PURE__ */ jsx5(AlertDialogClose, { render: /* @__PURE__ */ jsx5(Button4, { variant: "ghost" }), children: "Cancel" }),
          /* @__PURE__ */ jsx5(Button4, { loading: busy, onClick: () => void go(), children: "Connect" })
        ] })
      ] })
    }
  );
}
function ForgetDialog({ open, api, at, name, conn, onForgotten, onClose }) {
  const [busy, setBusy] = useState5(false);
  const [problem, setProblem] = useState5();
  const { box } = at;
  const go = async () => {
    setBusy(true);
    setProblem(void 0);
    try {
      await api.forget(box, at.location, at.worktree);
      onForgotten();
      onClose();
    } catch (err) {
      setProblem(faultOf(err).message);
    } finally {
      setBusy(false);
    }
  };
  return /* @__PURE__ */ jsx5(
    AlertDialog,
    {
      open,
      onOpenChange: (o) => {
        if (!o) {
          setProblem(void 0);
          onClose();
        }
      },
      children: /* @__PURE__ */ jsxs5(AlertDialogPopup, { className: "sm:max-w-lg", children: [
        /* @__PURE__ */ jsxs5(AlertDialogHeader, { children: [
          /* @__PURE__ */ jsxs5(AlertDialogTitle, { children: [
            "Forget ",
            conn.database,
            " for ",
            name,
            "?"
          ] }),
          /* @__PURE__ */ jsxs5(AlertDialogDescription, { children: [
            /* @__PURE__ */ jsx5("code", { children: HELPER }),
            " on ",
            box,
            " forgets the address you gave it for ",
            /* @__PURE__ */ jsx5("code", { children: conn.database }),
            conn.host && ` (${conn.host}${conn.port ? `:${conn.port}` : ""})`,
            ", and the connection goes from this tab with anything you had staged for it. The database itself isn't touched. To see it here again, connect it again with its address."
          ] })
        ] }),
        problem && /* @__PURE__ */ jsx5("p", { className: "mx-6 whitespace-pre-wrap rounded-md bg-destructive/8 p-2 text-destructive-foreground text-xs", children: problem }),
        /* @__PURE__ */ jsxs5(AlertDialogFooter, { children: [
          /* @__PURE__ */ jsx5(AlertDialogClose, { render: /* @__PURE__ */ jsx5(Button4, { variant: "ghost" }), children: "Cancel" }),
          /* @__PURE__ */ jsx5(Button4, { variant: "destructive", loading: busy, onClick: () => void go(), children: "Forget this connection" })
        ] })
      ] })
    }
  );
}

// src/console.tsx
import { Button as Button5, Icon as Icon5, Kbd, Menu as Menu2, MenuItem as MenuItem2, MenuPopup as MenuPopup2, MenuSeparator, MenuTrigger as MenuTrigger2, Spinner, SqlEditor, Tip as Tip4 } from "@berth/plugin/ui";
import { useEffect as useEffect4, useMemo as useMemo3, useRef as useRef4, useState as useState6 } from "react";

// src/statements.ts
function split(sql) {
  const out = [];
  let start = 0;
  let solid = false;
  const close = (end, semi) => {
    if (solid) {
      const raw = sql.slice(start, end);
      const from = start + (raw.length - raw.trimStart().length);
      const text = raw.trim();
      out.push({ text, from, to: from + text.length, end: semi ? end + 1 : end });
    }
    start = semi ? end + 1 : end;
    solid = false;
  };
  let i = 0;
  while (i < sql.length) {
    const c = sql[i];
    const two = sql.slice(i, i + 2);
    if (two === "--") {
      const nl = sql.indexOf("\n", i);
      i = nl < 0 ? sql.length : nl;
    } else if (two === "/*") {
      const end = sql.indexOf("*/", i + 2);
      i = end < 0 ? sql.length : end + 2;
    } else if (c === "'" || c === '"') {
      solid = true;
      i++;
      while (i < sql.length) {
        if (sql[i] === c) {
          if (sql[i + 1] === c) i++;
          else break;
        }
        i++;
      }
      i++;
    } else if (c === "$") {
      const tag = /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/.exec(sql.slice(i, i + 66));
      solid = true;
      if (tag) {
        const end = sql.indexOf(tag[0], i + tag[0].length);
        i = end < 0 ? sql.length : end + tag[0].length;
      } else i++;
    } else if (c === ";") {
      close(i, true);
      i++;
    } else {
      if (!/\s/.test(c)) solid = true;
      i++;
    }
  }
  close(sql.length, false);
  return out;
}
function statementAt(sql, selection) {
  if (selection.to > selection.from) {
    const raw = sql.slice(selection.from, selection.to);
    const lead = raw.length - raw.trimStart().length;
    const text = raw.trim().replace(/;\s*$/, "").trimEnd();
    return text ? { text, from: selection.from + lead, to: selection.from + lead + text.length } : void 0;
  }
  const pieces = split(sql);
  if (pieces.length === 0) return void 0;
  const at = selection.from;
  let found = pieces[0];
  for (const p of pieces) {
    if (p.from > at) break;
    found = p;
  }
  return { text: found.text, from: found.from, to: found.to };
}
function markAt(statement, sent, position) {
  if (position === void 0 || position < 1) return void 0;
  const lead = sent.length - statement.text.length;
  const at = statement.from + (position - 1) - lead;
  return at >= statement.from && at <= statement.to ? at : void 0;
}
function lineAndColumn(text, at) {
  const before = text.slice(0, at).split("\n");
  return `line ${before.length}, column ${before[before.length - 1].length + 1}`;
}
var HISTORY = 50;
function remember(history, sql, at, max = HISTORY) {
  const text = sql.trim();
  if (!text) return history;
  return [{ sql: text, at }, ...history.filter((h) => h.sql !== text)].slice(0, max);
}
var oneLine = (sql, max = 90) => {
  const line = sql.replace(/\s+/g, " ").trim();
  return line.length > max ? `${line.slice(0, max)}\u2026` : line;
};

// src/console.tsx
import { Fragment as Fragment4, jsx as jsx6, jsxs as jsxs6 } from "react/jsx-runtime";
var ago = (at, now) => {
  const s = Math.max(0, now - at) / 1e3;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
};
function Console({ ctx, schemas, put }) {
  const { api, berth, box, conn, engine, caps, writes } = ctx;
  const historyKey = `history:${box}/${ctx.source}`;
  const [history, setHistory] = useState6(() => berth.storage.get(historyKey, []));
  const [text, setText] = useState6("");
  const selection = useRef4({ from: 0, to: 0 });
  const [run, setRun] = useState6();
  const [viewed, setViewed] = useState6();
  const asked = useRef4(0);
  useEffect4(() => {
    if (!put) return;
    setText(put.sql);
    selection.current = { from: 0, to: 0 };
  }, [put?.n]);
  const names = useMemo3(() => {
    const out = {};
    for (const s of schemas) {
      for (const t of s.tables) {
        const cols = t.columns.map((c) => c.name);
        if (!caps.schemas || !s.name || s.name === engine.defaultSchema) out[t.name] = cols;
        if (caps.schemas && s.name) out[`${s.name}.${t.name}`] = cols;
      }
    }
    return out;
  }, [schemas, caps.schemas, engine.defaultSchema]);
  const go = async (at, wrap) => {
    const statement = statementAt(text, at);
    if (!statement) return;
    const sent = wrap ? wrap(statement.text) : statement.text;
    const mine = ++asked.current;
    setRun({ state: "running", statement, sent });
    const next = remember(history, sent, Date.now());
    setHistory(next);
    berth.storage.set(historyKey, next);
    try {
      const result2 = await api.query(box, conn.id, sent, QUERY_LIMIT);
      if (asked.current === mine) setRun({ state: "done", statement, sent, result: result2 });
    } catch (err) {
      const fault = faultOf(err);
      if (fault.code === "read_only" && writes.on) writes.refused();
      ctx.onFault(fault);
      if (asked.current === mine) setRun({ state: "failed", statement, sent, fault });
    }
  };
  const mark = useMemo3(() => {
    if (run?.state !== "failed") return void 0;
    const from = markAt(run.statement, run.sent, run.fault?.position);
    return from === void 0 ? void 0 : { from };
  }, [run]);
  const where = useMemo3(() => mark ? lineAndColumn(text, mark.from) : void 0, [mark, text]);
  const result = run?.result;
  const columns = useMemo3(() => (result?.columns ?? []).map((c) => ({ name: c.name, type: c.type, family: engine.family(c.type) })), [result, engine]);
  const rows = useMemo3(() => {
    const cuts = /* @__PURE__ */ new Map();
    for (const [r, c] of result?.cut ?? []) cuts.set(r, (cuts.get(r) ?? /* @__PURE__ */ new Set()).add(c));
    return (result?.rows ?? []).map((cells, i) => ({ id: String(i), ref: i, cells, cut: cuts.get(i) }));
  }, [result]);
  const now = Date.now();
  const running = run?.state === "running";
  return /* @__PURE__ */ jsxs6("div", { className: "flex min-h-0 flex-1 flex-col", "data-testid": "database-console", children: [
    /* @__PURE__ */ jsx6(
      SqlEditor,
      {
        className: "h-44 shrink-0 border-b",
        label: "SQL to run",
        value: text,
        onChange: setText,
        onSelect: (s) => selection.current = s,
        onRun: (s) => void go(s),
        schema: names,
        defaultSchema: caps.schemas ? engine.defaultSchema : void 0,
        dialect: caps.dialect,
        error: mark,
        placeholder: `A statement to run against ${conn.database}`
      }
    ),
    /* @__PURE__ */ jsxs6("div", { className: "flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b px-3 py-1.5", children: [
      /* @__PURE__ */ jsx6(Tip4, { label: "Run the statement under the cursor, or what is selected", children: /* @__PURE__ */ jsxs6(Button5, { size: "xs", disabled: !text.trim() || running, onClick: () => void go(selection.current), children: [
        /* @__PURE__ */ jsx6(Icon5, { name: "Play", className: "size-3.5" }),
        "Run",
        /* @__PURE__ */ jsx6(Kbd, { className: "ml-0.5 border-primary-foreground/30 bg-transparent text-primary-foreground", children: "\u2318\u21B5" })
      ] }) }),
      engine.explain && /* @__PURE__ */ jsx6(Tip4, { label: "Show the statement's plan, without running it", children: /* @__PURE__ */ jsx6(Button5, { size: "xs", variant: "outline", disabled: !text.trim() || running, onClick: () => void go(selection.current, engine.explain), children: "Explain" }) }),
      /* @__PURE__ */ jsxs6(Menu2, { children: [
        /* @__PURE__ */ jsxs6(MenuTrigger2, { render: /* @__PURE__ */ jsx6(Button5, { size: "xs", variant: "ghost", disabled: history.length === 0 }), children: [
          /* @__PURE__ */ jsx6(Icon5, { name: "History", className: "size-3.5" }),
          "History"
        ] }),
        /* @__PURE__ */ jsxs6(MenuPopup2, { align: "start", className: "max-h-96 w-[30rem] max-w-[90vw]", "data-testid": "database-history", children: [
          history.slice(0, 25).map((h) => /* @__PURE__ */ jsxs6(MenuItem2, { onClick: () => setText(h.sql), children: [
            /* @__PURE__ */ jsx6("span", { className: "min-w-0 flex-1 truncate font-mono text-xs", children: oneLine(h.sql) }),
            /* @__PURE__ */ jsx6("span", { className: "ml-3 shrink-0 text-muted-foreground text-xs", children: ago(h.at, now) })
          ] }, h.sql)),
          /* @__PURE__ */ jsx6(MenuSeparator, {}),
          /* @__PURE__ */ jsx6(
            MenuItem2,
            {
              onClick: () => {
                setHistory([]);
                berth.storage.set(historyKey, []);
              },
              children: /* @__PURE__ */ jsxs6("span", { className: "text-muted-foreground", children: [
                "Forget what was run in ",
                ctx.worktree
              ] })
            }
          )
        ] })
      ] }),
      /* @__PURE__ */ jsx6("span", { className: "ml-auto flex items-center gap-2 text-muted-foreground text-xs tabular-nums", "data-testid": "database-result", role: "status", children: running ? /* @__PURE__ */ jsxs6(Fragment4, { children: [
        /* @__PURE__ */ jsx6(Spinner, { className: "size-3" }),
        " Running\u2026"
      ] }) : result ? /* @__PURE__ */ jsxs6(Fragment4, { children: [
        /* @__PURE__ */ jsx6("span", { className: "font-medium font-mono text-foreground", children: result.command }),
        /* @__PURE__ */ jsx6("span", { children: "\xB7" }),
        /* @__PURE__ */ jsx6("span", { children: result.columns.length ? plural(result.row_count, "row") : `${plural(result.row_count, "row")} changed` }),
        /* @__PURE__ */ jsx6("span", { children: "\xB7" }),
        /* @__PURE__ */ jsx6("span", { children: ms(result.duration_ms) })
      ] }) : !writes.can ? "Read-only" : !writes.on ? "Reads only, until writes are unlocked" : "Writes are on" })
    ] }),
    run?.state === "failed" && run.fault && /* @__PURE__ */ jsx6("div", { role: "alert", "data-testid": "database-error", className: "max-h-40 shrink-0 space-y-1 overflow-auto border-b bg-destructive/8 px-3 py-2 text-xs", children: run.fault.code === "read_only" ? /* @__PURE__ */ jsxs6("div", { className: "flex flex-wrap items-center gap-x-3 gap-y-1 text-destructive-foreground", children: [
      /* @__PURE__ */ jsxs6("span", { className: "min-w-0 flex-1", children: [
        /* @__PURE__ */ jsx6("b", { children: "Not run:" }),
        " ",
        writes.can ? `this statement writes, and writes to ${conn.database} are locked.` : `this statement writes, and ${conn.database} takes no writes through Shipyard.`,
        run.fault.detail && /* @__PURE__ */ jsx6("span", { className: "mt-0.5 block font-mono text-muted-foreground", children: run.fault.detail })
      ] }),
      writes.can && /* @__PURE__ */ jsx6(Button5, { size: "xs", variant: "outline", onClick: writes.ask, children: "Unlock writes\u2026" })
    ] }) : (
      // The helper's own sentence, the database's detail, where it
      // stopped, and what the helper suggests.
      /* @__PURE__ */ jsxs6(Fragment4, { children: [
        /* @__PURE__ */ jsx6("p", { className: "whitespace-pre-wrap font-mono text-destructive-foreground", children: run.fault.message }),
        run.fault.detail && /* @__PURE__ */ jsx6("p", { className: "whitespace-pre-wrap font-mono text-muted-foreground", children: run.fault.detail }),
        where && /* @__PURE__ */ jsxs6("p", { className: "text-muted-foreground", children: [
          "At ",
          where,
          ", underlined in the editor."
        ] }),
        run.fault.hint && /* @__PURE__ */ jsx6("p", { className: "whitespace-pre-wrap text-muted-foreground", children: run.fault.hint })
      ] })
    ) }),
    result?.truncated && /* @__PURE__ */ jsxs6(Line, { testId: "database-truncated", children: [
      "Only the first ",
      plural(result.rows.length, "row"),
      " are shown: the statement returned more than the console keeps. Narrow it with WHERE or LIMIT to see the others."
    ] }),
    result && result.notices.length > 0 && /* @__PURE__ */ jsx6(Line, { tone: "note", testId: "database-notices", children: result.notices.map((notice, i) => /* @__PURE__ */ jsx6("span", { className: "block font-mono", children: notice }, i)) }),
    !run || run.state === "failed" && !result ? run ? /* @__PURE__ */ jsx6("div", { className: "flex-1" }) : /* @__PURE__ */ jsx6(
      Notice,
      {
        icon: "SquareTerminal",
        title: "Nothing run yet",
        body: /* @__PURE__ */ jsxs6(Fragment4, { children: [
          "Write a statement above and press ",
          /* @__PURE__ */ jsx6(Kbd, { children: "\u2318\u21B5" }),
          ". With several in the editor, the one the cursor is in runs; select text to run just that. ",
          writes.can ? `${conn.database} is read-only until you unlock writes.` : `${conn.database} is read-only.`
        ] })
      }
    ) : !result ? /* @__PURE__ */ jsx6("div", { className: "flex-1", "aria-busy": "true" }) : result.columns.length === 0 ? /* @__PURE__ */ jsx6(Notice, { icon: "Check", title: `${result.command}: ${plural(result.row_count, "row")} changed`, body: "The statement returned no rows to show." }) : rows.length === 0 ? /* @__PURE__ */ jsx6(Notice, { icon: "table-2", title: "No rows", body: `${result.command} returned ${plural(result.columns.length, "column")} and no rows.` }) : /* @__PURE__ */ jsx6(
      Grid,
      {
        label: "Results",
        columns,
        rows,
        onView: (row, col) => setViewed({ column: columns[col].name, type: columns[col].type, family: columns[col].family, row: `Result row ${(row.ref ?? 0) + 1}`, cell: row.cells[col] ?? null, cut: row.cut?.has(col) })
      }
    ),
    /* @__PURE__ */ jsx6(ValueSheet, { viewed, onClose: () => setViewed(void 0) })
  ] });
}

// src/lock.tsx
import { AlertDialog as AlertDialog2, AlertDialogClose as AlertDialogClose2, AlertDialogDescription as AlertDialogDescription2, AlertDialogFooter as AlertDialogFooter2, AlertDialogHeader as AlertDialogHeader2, AlertDialogPopup as AlertDialogPopup2, AlertDialogTitle as AlertDialogTitle2, Badge as Badge3, Button as Button6, Icon as Icon6, Tip as Tip5 } from "@berth/plugin/ui";
import { useState as useState7 } from "react";
import { jsx as jsx7, jsxs as jsxs7 } from "react/jsx-runtime";
var MINUTES = Math.round(WRITES_FOR_MS / 6e4);
function Lock({ writes, now, busy, onLock }) {
  if (!writes.can) {
    return /* @__PURE__ */ jsx7(Tip5, { label: "This database takes no writes through Shipyard: its tables can be read and queried, not changed.", children: /* @__PURE__ */ jsxs7(Badge3, { variant: "outline", size: "sm", className: "shrink-0 gap-1 font-normal", "data-testid": "database-lock", "data-writes": "never", children: [
      /* @__PURE__ */ jsx7(Icon6, { name: "Lock", className: "size-3" }),
      "Read-only"
    ] }) });
  }
  if (!writes.on) {
    return /* @__PURE__ */ jsxs7(Button6, { size: "sm", variant: "outline", className: "shrink-0", onClick: writes.ask, "data-testid": "database-lock", "data-writes": "off", children: [
      /* @__PURE__ */ jsx7(Icon6, { name: "Lock", className: "size-3.5" }),
      "Unlock writes\u2026"
    ] });
  }
  return /* @__PURE__ */ jsxs7("span", { className: "flex shrink-0 items-center gap-1.5", "data-testid": "database-lock", "data-writes": "on", children: [
    /* @__PURE__ */ jsx7(Tip5, { label: "Edits and statements can change this database until the time runs out", children: /* @__PURE__ */ jsxs7(Badge3, { variant: "outline", size: "sm", className: "gap-1 border-destructive/40 text-destructive-foreground tabular-nums", role: "timer", "aria-label": `Writes are on for ${left((writes.until ?? now) - now)} more`, children: [
      /* @__PURE__ */ jsx7(Icon6, { name: "LockOpen", className: "size-3" }),
      "Writes on \xB7 ",
      left((writes.until ?? now) - now)
    ] }) }),
    /* @__PURE__ */ jsx7(Button6, { size: "sm", variant: "outline", loading: busy, onClick: onLock, children: "Lock" })
  ] });
}
function UnlockDialog({ open, conn, engine, box, worktree, onUnlock, onClose }) {
  const [busy, setBusy] = useState7(false);
  const [fault, setFault] = useState7();
  const go = async () => {
    setBusy(true);
    setFault(void 0);
    try {
      await onUnlock();
      onClose();
    } catch (err) {
      setFault(faultOf(err));
    } finally {
      setBusy(false);
    }
  };
  return /* @__PURE__ */ jsx7(
    AlertDialog2,
    {
      open,
      onOpenChange: (o) => {
        if (!o) {
          setFault(void 0);
          onClose();
        }
      },
      children: /* @__PURE__ */ jsxs7(AlertDialogPopup2, { className: "sm:max-w-lg", children: [
        /* @__PURE__ */ jsxs7(AlertDialogHeader2, { children: [
          /* @__PURE__ */ jsxs7(AlertDialogTitle2, { children: [
            "Unlock writes to ",
            conn.database,
            " on ",
            box,
            "?"
          ] }),
          /* @__PURE__ */ jsxs7(AlertDialogDescription2, { children: [
            "For ",
            MINUTES,
            " minutes, the changes you apply in the grid and the statements you run in the console can change ",
            /* @__PURE__ */ jsx7("code", { children: conn.database }),
            ": the ",
            engine.name,
            " database ",
            worktree,
            "'s services use on ",
            box,
            conn.host && ` (${conn.host}${conn.port ? `:${conn.port}` : ""})`,
            ". That includes deleting rows and dropping tables. It locks again by itself after ",
            MINUTES,
            " minutes, or when ",
            /* @__PURE__ */ jsx7("code", { children: HELPER }),
            " restarts; ",
            /* @__PURE__ */ jsx7("b", { children: "Lock" }),
            " does it at once."
          ] })
        ] }),
        fault && /* @__PURE__ */ jsx7("p", { className: "mx-6 whitespace-pre-wrap rounded-md bg-destructive/8 p-2 text-destructive-foreground text-xs", children: fault.message }),
        /* @__PURE__ */ jsxs7(AlertDialogFooter2, { children: [
          /* @__PURE__ */ jsx7(AlertDialogClose2, { render: /* @__PURE__ */ jsx7(Button6, { variant: "ghost" }), children: "Cancel" }),
          /* @__PURE__ */ jsxs7(Button6, { loading: busy, onClick: () => void go(), children: [
            "Unlock for ",
            MINUTES,
            " minutes"
          ] })
        ] })
      ] })
    }
  );
}

// src/source.ts
var enc = new TextEncoder();
function namePart(s) {
  let out = "";
  for (const ch of s.trim()) {
    const c = ch.codePointAt(0);
    out += ch === "/" || ch === ":" || ch === "\\" || c <= 32 || c === 127 ? "-" : ch;
  }
  out = out.replace(/^-+|-+$/g, "");
  if (enc.encode(out).length <= 60) return out;
  let cut = "";
  let size = 0;
  for (const ch of out) {
    size += enc.encode(ch).length;
    if (size > 60) break;
    cut += ch;
  }
  return cut;
}
function sourceOf(w) {
  const project = namePart(w.location);
  const name = w.main ? "" : namePart(w.worktree);
  if (!project) return name;
  return !name || name === project ? project : `${project}/${name}`;
}

// src/panel.tsx
import { Fragment as Fragment5, jsx as jsx8, jsxs as jsxs8 } from "react/jsx-runtime";
var GONE = /* @__PURE__ */ new Set(["off", "none", "unsupported"]);
function DatabasePanel({ berth, box, location, worktree, main }) {
  const [ref, visible] = useVisible();
  const online = useBoxes().find((b) => b.name === box)?.state === "online";
  const helper = useHelperState(berth, box, visible && online);
  const state = helper.status?.state;
  const running = online && state === "running";
  const [kept, setKept] = useState8(false);
  useEffect5(() => {
    if (running) setKept(true);
    else if (online && state && GONE.has(state)) setKept(false);
  }, [running, online, state]);
  return /* @__PURE__ */ jsxs8("div", { ref, "data-testid": "database", "data-state": online ? state ?? "reading" : "offline", className: "@container flex h-full min-h-0 flex-col bg-background text-sm", children: [
    (running || kept) && /* @__PURE__ */ jsx8("div", { className: running ? "contents" : "hidden", children: /* @__PURE__ */ jsx8(Connections, { berth, box, source: sourceOf({ location, worktree, main }), worktree: main ? location : worktree, at: { location, worktree: main ? "" : worktree }, active: visible && running }) }),
    !running && kept && /* @__PURE__ */ jsx8(Line, { tone: "note", testId: "database-kept", children: online ? /* @__PURE__ */ jsxs8(Fragment5, { children: [
      "What you staged and typed in this tab is kept, and is here again once ",
      /* @__PURE__ */ jsx8("code", { children: HELPER }),
      " is running on ",
      box,
      ". Writes will be locked."
    ] }) : /* @__PURE__ */ jsxs8(Fragment5, { children: [
      "What you staged and typed in this tab is kept, and is here again once ",
      box,
      " is back."
    ] }) }),
    !online ? /* @__PURE__ */ jsx8(Notice, { icon: "CloudOff", title: `${box} is offline`, body: "Database's helper runs on the box, beside the database it reads, so there is nothing to show until the box is back." }) : !running && /* @__PURE__ */ jsx8(HelperBox, { variant: "panel", berth, box, copy: COPY, testId: "database", onAbout: () => berth.openScreen("database") })
  ] });
}
var skews = /* @__PURE__ */ new Map();
function Connections({ berth, box, source, worktree, at, active }) {
  const api = useMemo4(() => database(berth), [berth]);
  const helper = useHelperState(berth, box, false);
  const refresh = helper.refresh;
  const onFault = useCallback4(
    (fault) => {
      if (lostHelper(fault)) refresh();
    },
    [refresh]
  );
  const key = `${box}
${source}`;
  const originKey = `env:${box}/${source}`;
  const [origin, setOrigin] = useState8(() => berth.storage.get(originKey, null));
  const [looking, setLooking] = useState8(false);
  const [looked, setLooked] = useState8();
  const lookedFor = useRef5(void 0);
  const again = useRef5(false);
  const known = useRef5(origin);
  known.current = origin;
  const handKey = `hand:${box}/${source}`;
  const [byHand, setByHand] = useState8(() => berth.storage.get(handKey, []));
  const hand = useRef5(byHand);
  hand.current = byHand;
  const [connecting, setConnecting] = useState8(false);
  const read = async () => {
    const asked = again.current;
    again.current = false;
    const list2 = await api.connections(box, source);
    const first = lookedFor.current !== key;
    if (!(first && list2.length === 0) && !(asked && lookAgain(known.current, list2, hand.current))) return list2;
    lookedFor.current = key;
    let result = { kind: "old" };
    if (api.canLook) {
      setLooking(true);
      try {
        const look = await api.look(box, at.location, at.worktree);
        result = { kind: "looked", look };
        const found = originOf(look) ?? null;
        berth.storage.set(originKey, found);
        setOrigin(found);
      } catch (err) {
        const fault = faultOf(err);
        onFault(fault);
        result = cannotLook(fault) ? { kind: "old" } : { kind: "failed", message: fault.message };
      } finally {
        setLooking(false);
      }
    }
    setLooked({ key, result });
    return result.kind === "looked" ? api.connections(box, source) : list2;
  };
  const conns = useRead(read, key, { active, every: 1e4 });
  const reload = conns.reload;
  const relook = useCallback4(() => {
    again.current = true;
    reload();
  }, [reload]);
  useEffect5(() => {
    if (conns.fault) onFault(conns.fault);
  }, [conns.fault, onFault]);
  const pickKey = `connection:${box}/${source}`;
  const [picked, setPicked] = useState8(() => berth.storage.get(pickKey, ""));
  const pick = (id) => {
    setPicked(id);
    berth.storage.set(pickKey, id);
  };
  const where = { box, location: at.location, worktree: at.worktree };
  const connect = /* @__PURE__ */ jsx8(
    ConnectDialog,
    {
      open: connecting,
      api,
      at: where,
      name: worktree,
      onConnected: (id) => {
        berth.storage.set(handKey, [id]);
        setByHand([id]);
        pick(id);
        reload();
      },
      onClose: () => setConnecting(false)
    }
  );
  if (!conns.data) {
    if (conns.fault && isOldBox(conns.fault)) {
      return /* @__PURE__ */ jsx8(
        Notice,
        {
          icon: "CircleFadingArrowUp",
          title: `${box} needs updating for Database`,
          body: /* @__PURE__ */ jsxs8(Fragment5, { children: [
            /* @__PURE__ */ jsx8("code", { children: HELPER }),
            " runs on ",
            box,
            " and answers only the box's own daemon, which proves itself with a token. ",
            box,
            "'s ",
            /* @__PURE__ */ jsx8("code", { children: "berthd" }),
            " is from before it sent one, so every request is turned away. Update the box in Settings \u2192 Boxes; nothing needs installing again."
          ] }),
          actions: /* @__PURE__ */ jsxs8(Fragment5, { children: [
            /* @__PURE__ */ jsx8(Button7, { size: "sm", onClick: () => berth.openSettings("boxes"), children: "Open Settings \u2192 Boxes" }),
            /* @__PURE__ */ jsx8(Button7, { size: "sm", variant: "ghost", onClick: conns.reload, children: "Check again" })
          ] })
        }
      );
    }
    if (conns.fault && isOldHelper(conns.fault)) {
      const s = helper.status;
      return /* @__PURE__ */ jsx8(
        Notice,
        {
          icon: "CircleFadingArrowUp",
          title: `Database on ${box} needs updating`,
          body: s?.update ? /* @__PURE__ */ jsxs8(Fragment5, { children: [
            box,
            " runs ",
            /* @__PURE__ */ jsx8("code", { children: HELPER }),
            " ",
            s.version,
            ", from before it could do this. This plugin carries ",
            s.latest,
            ": open Database, and choose ",
            /* @__PURE__ */ jsxs8("b", { children: [
              "Update to ",
              s.latest,
              "\u2026"
            ] }),
            " on ",
            box,
            "'s row."
          ] }) : /* @__PURE__ */ jsxs8(Fragment5, { children: [
            box,
            " runs a ",
            /* @__PURE__ */ jsx8("code", { children: HELPER }),
            " from before it could do this, and this copy of the plugin carries nothing newer. Update the plugin in Settings \u2192 Plugins, then choose ",
            /* @__PURE__ */ jsx8("b", { children: "Update" }),
            " on ",
            box,
            "'s row in Database."
          ] }),
          actions: /* @__PURE__ */ jsxs8(Fragment5, { children: [
            /* @__PURE__ */ jsx8(Button7, { size: "sm", onClick: () => berth.openScreen("database"), children: "Open Database" }),
            !s?.update && /* @__PURE__ */ jsx8(Button7, { size: "sm", variant: "outline", onClick: () => berth.openSettings("plugins"), children: "Open Settings \u2192 Plugins" }),
            /* @__PURE__ */ jsx8(Button7, { size: "sm", variant: "ghost", onClick: () => (refresh(), conns.reload()), children: "Check again" })
          ] })
        }
      );
    }
    if (looking && !conns.fault) {
      return /* @__PURE__ */ jsxs8(Fragment5, { children: [
        /* @__PURE__ */ jsxs8(Line, { tone: "note", testId: "database-looking", children: [
          "Looking in ",
          worktree,
          "'s ",
          /* @__PURE__ */ jsx8("code", { children: ".env" }),
          " files on ",
          box,
          " for its database\u2026"
        ] }),
        /* @__PURE__ */ jsx8(Waiting, {})
      ] });
    }
    return /* @__PURE__ */ jsx8(Waiting, { fault: conns.fault, onRetry: conns.reload, title: `Couldn't ask ${box} for ${worktree}'s database` });
  }
  if (conns.data.length === 0) {
    const result = looked?.key === key ? looked.result : void 0;
    return /* @__PURE__ */ jsxs8(Fragment5, { children: [
      connect,
      /* @__PURE__ */ jsx8(
        Notice,
        {
          icon: "table-2",
          title: `No database for ${worktree} yet`,
          body: result?.kind === "looked" || result?.kind === "failed" ? /* @__PURE__ */ jsxs8(Fragment5, { children: [
            /* @__PURE__ */ jsx8("span", { className: "block text-foreground", "data-testid": "database-looked", children: result.kind === "looked" ? said(result.look, box).join(" ") : `Couldn't look in ${worktree}'s .env files on ${box}: ${result.message}` }),
            /* @__PURE__ */ jsxs8("span", { className: "mt-2 block", children: [
              "Database shows the Postgres a worktree's ",
              /* @__PURE__ */ jsx8("code", { children: "DATABASE_URL" }),
              " or ",
              /* @__PURE__ */ jsx8("code", { children: "POSTGRES_URL" }),
              " points at when it is on ",
              box,
              " itself. It reads that from the worktree's ",
              /* @__PURE__ */ jsx8("code", { children: ".env" }),
              " files when this tab opens. A service of this worktree that Shipyard starts with one shows here too, once it has started; one that was already running when Database was turned on needs a restart. A database on another host, or one whose address is a secret reference, is not shown. If nothing says which database ",
              worktree,
              " has, connect it yourself with its address."
            ] })
          ] }) : /* @__PURE__ */ jsxs8(Fragment5, { children: [
            "A service of this worktree that Shipyard starts with a ",
            /* @__PURE__ */ jsx8("code", { children: "DATABASE_URL" }),
            " or ",
            /* @__PURE__ */ jsx8("code", { children: "POSTGRES_URL" }),
            " pointing at a Postgres on ",
            box,
            " itself shows here once it has started. Database learns of a database when a service starts, so one that was already running when Database was turned on needs a restart. A database on another host, or one whose address is a secret reference, is not shown."
          ] }),
          actions: /* @__PURE__ */ jsxs8(Fragment5, { children: [
            /* @__PURE__ */ jsx8(Button7, { size: "sm", variant: "outline", loading: looking, onClick: relook, children: "Look again" }),
            api.canLook && result?.kind !== "old" && /* @__PURE__ */ jsx8(Button7, { size: "sm", variant: "outline", onClick: () => setConnecting(true), children: "Connect a database\u2026" }),
            /* @__PURE__ */ jsx8(Button7, { size: "sm", variant: "ghost", onClick: () => berth.openScreen("database"), children: "Which databases show" })
          ] })
        }
      )
    ] });
  }
  const conn = conns.data.find((c) => c.id === picked) ?? conns.data[0];
  return /* @__PURE__ */ jsxs8(Fragment5, { children: [
    connect,
    /* @__PURE__ */ jsx8(
      Connected,
      {
        scope: { berth, box, source, worktree, active },
        api,
        at: where,
        conn,
        all: conns.data,
        origin,
        byHand,
        relook,
        onConnect: api.canLook && conns.data.length > 1 && !conns.data.some((c) => byHand.includes(c.id)) ? () => setConnecting(true) : void 0,
        onForgotten: () => {
          berth.storage.set(handKey, []);
          setByHand([]);
          relook();
        },
        stale: conns.fault,
        onPick: pick,
        onFault,
        reread: conns.reload
      },
      conn.id
    )
  ] });
}
function Connected({
  scope,
  api,
  at,
  conn,
  all,
  origin,
  byHand,
  relook,
  onConnect,
  onForgotten,
  stale,
  onPick,
  onFault,
  reread
}) {
  const { berth, box, source, worktree, active } = scope;
  const file = fromFile(origin, conn);
  const mine = byHand.includes(conn.id);
  const [forgetting, setForgetting] = useState8(false);
  const engine = useMemo4(() => engineOf(conn.engine), [conn.engine]);
  const caps = conn.capabilities ?? NO_CAPABILITIES;
  const [mode, setMode] = useState8("browse");
  const [put, setPut] = useState8();
  const [answered, setAnswered] = useState8();
  useEffect5(() => setAnswered(void 0), [conn.writes_until]);
  const read = useMemo4(() => conn.writes_until === void 0 ? void 0 : untilHere(conn.writes_until, skews.get(box) ?? 0, Date.now()), [conn.writes_until, box]);
  const until = answered ? answered.until : read;
  const now = useNow(until !== void 0);
  const on = caps.writes && until !== void 0 && until > now;
  const [asking, setAsking] = useState8(false);
  const [locking, setLocking] = useState8(false);
  const [ranOut, setRanOut] = useState8(false);
  const was = useRef5(on);
  useEffect5(() => {
    if (was.current && !on && until !== void 0 && until <= Date.now()) setRanOut(true);
    was.current = on;
  }, [on, until]);
  const unlock = async () => {
    const r = await api.writes(box, conn.id, true);
    const arrived = Date.now();
    if (r.writes_until !== void 0) skews.set(box, skewOf(r.writes_until, arrived));
    setRanOut(false);
    setAnswered({ until: r.writes_until === void 0 ? void 0 : arrived + WRITES_FOR_MS });
    reread();
  };
  const lock = async () => {
    setLocking(true);
    try {
      await api.writes(box, conn.id, false);
      setAnswered({});
      reread();
    } catch (err) {
      berth.notify(`Couldn't lock writes to ${conn.database}`, String(err?.message ?? err));
    } finally {
      setLocking(false);
    }
  };
  const writes = {
    can: caps.writes,
    on,
    until,
    ask: () => setAsking(true),
    refused: () => {
      setAnswered({});
      reread();
    }
  };
  const schema = useRead(() => api.schema(box, conn.id), `${box}
${conn.id}`, { active });
  useEffect5(() => {
    if (!schema.fault) return;
    onFault(schema.fault);
    if (schema.fault.code === "not_found") reread();
  }, [schema.fault, onFault, reread]);
  const ctx = { berth, api, box, source, worktree, conn, caps, engine, writes, active, onFault };
  const where = `${conn.host}${conn.port ? `:${conn.port}` : ""}`;
  return /* @__PURE__ */ jsxs8(Fragment5, { children: [
    /* @__PURE__ */ jsxs8("div", { className: "flex min-h-11 shrink-0 flex-wrap items-center gap-2 border-b px-3 py-1.5", "data-testid": "database-bar", "data-engine": conn.engine, children: [
      all.length > 1 ? /* @__PURE__ */ jsxs8(Menu3, { children: [
        /* @__PURE__ */ jsxs8(MenuTrigger3, { render: /* @__PURE__ */ jsx8(Button7, { size: "sm", variant: "outline", className: "max-w-64", "aria-label": `Database: ${conn.database}, ${engine.name}. Pick another`, "data-testid": "database-connection" }), children: [
          /* @__PURE__ */ jsx8(Icon7, { name: engine.icon, className: "size-3.5" }),
          /* @__PURE__ */ jsx8("span", { className: "truncate", children: conn.database }),
          /* @__PURE__ */ jsx8(Icon7, { name: "ChevronsUpDown", className: "size-3 text-muted-foreground" })
        ] }),
        /* @__PURE__ */ jsxs8(MenuPopup3, { align: "start", className: "min-w-64", children: [
          all.map((c) => /* @__PURE__ */ jsxs8(MenuItem3, { onClick: () => onPick(c.id), children: [
            /* @__PURE__ */ jsx8(Icon7, { name: engineOf(c.engine).icon, className: "size-3.5 text-muted-foreground" }),
            /* @__PURE__ */ jsx8("span", { className: "min-w-0 flex-1 truncate", children: c.database }),
            /* @__PURE__ */ jsxs8("span", { className: "ml-3 shrink-0 text-muted-foreground text-xs", children: [
              engineOf(c.engine).name,
              byHand.includes(c.id) ? " \xB7 added by hand" : c.label && ` \xB7 ${c.label}`,
              !byHand.includes(c.id) && fromFile(origin, c) && ` in ${fromFile(origin, c)}`
            ] })
          ] }, c.id)),
          onConnect && /* @__PURE__ */ jsxs8(Fragment5, { children: [
            /* @__PURE__ */ jsx8(MenuSeparator2, {}),
            /* @__PURE__ */ jsxs8(MenuItem3, { onClick: onConnect, children: [
              /* @__PURE__ */ jsx8(Icon7, { name: "Plus", className: "size-3.5 text-muted-foreground" }),
              "Connect a database\u2026"
            ] })
          ] })
        ] })
      ] }) : /* @__PURE__ */ jsxs8("span", { className: "flex min-w-0 items-center gap-1.5 font-medium", "data-testid": "database-connection", children: [
        /* @__PURE__ */ jsx8(Icon7, { name: engine.icon, className: "size-3.5 shrink-0 text-muted-foreground" }),
        /* @__PURE__ */ jsx8("span", { className: "truncate", children: conn.database })
      ] }),
      /* @__PURE__ */ jsx8(Tip6, { label: `${engine.name}${where ? ` at ${where}` : ""}${conn.user ? `, as ${conn.user}` : ""}${mine ? ", added by hand" : conn.label ? `, from ${conn.label}` : ""}`, children: /* @__PURE__ */ jsx8(Badge4, { variant: "outline", size: "sm", className: "hidden shrink-0 font-normal @lg:inline-flex", children: engine.name }) }),
      mine && // Where Database learned of it: someone pasted its address. Only
      // such a connection can be forgotten: the others would come back.
      /* @__PURE__ */ jsxs8(Fragment5, { children: [
        /* @__PURE__ */ jsx8(Tip6, { label: `You connected this one by hand. Its address is kept on ${box} until you forget it, ${worktree} is removed, or Database's data on ${box} is removed.`, children: /* @__PURE__ */ jsx8(Badge4, { variant: "outline", size: "sm", className: "shrink-0 font-normal text-muted-foreground", "data-testid": "database-by-hand", children: "added by hand" }) }),
        /* @__PURE__ */ jsx8(Button7, { size: "xs", variant: "ghost", className: "shrink-0", "aria-label": "Forget this connection\u2026", onClick: () => setForgetting(true), children: "Forget\u2026" })
      ] }),
      file && !mine && // Where Database learned of it: not a service Shipyard started.
      /* @__PURE__ */ jsx8(Tip6, { label: `${conn.label}, read from ${worktree}'s ${file} on ${box}. Reading the tables again looks in the file again.`, children: /* @__PURE__ */ jsx8(Badge4, { variant: "outline", size: "sm", className: "shrink-0 font-mono font-normal text-muted-foreground", "data-testid": "database-from-env", "aria-label": `From ${conn.label} in ${worktree}'s ${file}`, children: file }) }),
      /* @__PURE__ */ jsx8(
        PickOne2,
        {
          label: "Browse the tables, or run SQL",
          value: mode,
          onChange: (v) => setMode(v),
          options: [
            { value: "browse", label: "Tables" },
            { value: "console", label: "Console" }
          ]
        }
      ),
      /* @__PURE__ */ jsx8("span", { className: "ml-auto", children: /* @__PURE__ */ jsx8(Lock, { writes, now, busy: locking, onLock: () => void lock() }) })
    ] }),
    ranOut && !on && /* @__PURE__ */ jsx8(Line, { tone: "note", testId: "database-ran-out", actions: /* @__PURE__ */ jsx8(Button7, { size: "xs", variant: "ghost", onClick: () => setRanOut(false), children: "Dismiss" }), children: "Writes locked themselves again: the fifteen minutes ran out. What you staged is still staged." }),
    stale && /* @__PURE__ */ jsxs8(Line, { tone: "bad", children: [
      "Couldn't ask ",
      box,
      " again: ",
      stale.message
    ] }),
    !schema.data || !schema.fresh ? schema.fault && schema.fault.code !== "db_unavailable" ? /* @__PURE__ */ jsx8(Waiting, { fault: schema.fault, onRetry: schema.reload, title: `Couldn't read ${conn.database}'s tables` }) : schema.fault ? (
      // The helper has the connection, and the database isn't answering it.
      /* @__PURE__ */ jsx8(
        Notice,
        {
          icon: "Unplug",
          title: `Couldn't reach ${conn.database}`,
          body: /* @__PURE__ */ jsxs8(Fragment5, { children: [
            /* @__PURE__ */ jsx8(FaultText, { fault: schema.fault }),
            /* @__PURE__ */ jsxs8("span", { className: "mt-2 block", children: [
              engine.name,
              where && ` at ${where}`,
              " isn't answering ",
              box,
              ". If its server or container stopped, start it and try again; if the worktree's services moved to another database, restart them and it shows here."
            ] })
          ] }),
          actions: /* @__PURE__ */ jsx8(Button7, { size: "sm", variant: "outline", onClick: schema.reload, children: "Try again" })
        }
      )
    ) : /* @__PURE__ */ jsx8(Waiting, {}) : /* @__PURE__ */ jsxs8(Fragment5, { children: [
      /* @__PURE__ */ jsx8("div", { className: mode === "browse" ? "flex min-h-0 flex-1 flex-col" : "hidden", children: /* @__PURE__ */ jsx8(
        Browser,
        {
          ctx,
          schemas: schema.data,
          onReload: () => {
            relook();
            schema.reload();
          },
          toConsole: (sql) => {
            setPut((p) => ({ sql, n: (p?.n ?? 0) + 1 }));
            setMode("console");
          }
        }
      ) }),
      /* @__PURE__ */ jsx8("div", { className: mode === "console" ? "flex min-h-0 flex-1 flex-col" : "hidden", children: /* @__PURE__ */ jsx8(Console, { ctx, schemas: schema.data, put }) })
    ] }),
    /* @__PURE__ */ jsx8(UnlockDialog, { open: asking, conn, engine, box, worktree, onUnlock: unlock, onClose: () => setAsking(false) }),
    /* @__PURE__ */ jsx8(ForgetDialog, { open: forgetting, api, at, name: worktree, conn, onForgotten, onClose: () => setForgetting(false) })
  ] });
}

// src/screen.tsx
import { useBoxes as useBoxes2 } from "@berth/plugin";
import { Button as Button8, Empty as Empty2, EmptyDescription as EmptyDescription2, EmptyHeader as EmptyHeader2, EmptyTitle as EmptyTitle2, Frame, FrameDescription, FrameHeader, FramePanel, FrameTitle, HelperBox as HelperBox2, Icon as Icon8, ViewHeader } from "@berth/plugin/ui";
import { jsx as jsx9, jsxs as jsxs9 } from "react/jsx-runtime";
function DatabaseScreen({ berth }) {
  const boxes = useBoxes2();
  const [ref, visible] = useVisible();
  return /* @__PURE__ */ jsxs9("div", { ref, className: "space-y-5", "data-testid": "database-screen", children: [
    /* @__PURE__ */ jsx9(
      ViewHeader,
      {
        title: "Database",
        description: "A worktree's own development database: its tables, its rows and a SQL console, read-only until you unlock it.",
        actions: /* @__PURE__ */ jsxs9(Button8, { size: "sm", variant: "outline", onClick: () => berth.openUrl(GUIDE), children: [
          "Guide ",
          /* @__PURE__ */ jsx9(Icon8, { name: "ArrowUpRight", className: "size-3.5" })
        ] })
      }
    ),
    /* @__PURE__ */ jsxs9(Frame, { variant: "card", children: [
      /* @__PURE__ */ jsxs9(FrameHeader, { children: [
        /* @__PURE__ */ jsx9(FrameTitle, { children: "Boxes" }),
        /* @__PURE__ */ jsxs9(FrameDescription, { children: [
          "Database runs as a program on a box, ",
          /* @__PURE__ */ jsx9("code", { children: HELPER }),
          ", beside the databases it reads. It is off until you turn it on for a box, and it keeps each database's address on that box."
        ] })
      ] }),
      /* @__PURE__ */ jsx9(FramePanel, { className: "p-0", children: boxes.length === 0 ? /* @__PURE__ */ jsx9(Empty2, { className: "py-10", children: /* @__PURE__ */ jsxs9(EmptyHeader2, { children: [
        /* @__PURE__ */ jsx9(Icon8, { name: "Server", className: "mx-auto mb-2 size-5 text-muted-foreground" }),
        /* @__PURE__ */ jsx9(EmptyTitle2, { children: "No boxes yet" }),
        /* @__PURE__ */ jsx9(EmptyDescription2, { children: "Add a box, and Database can be turned on for it here." })
      ] }) }) : /* @__PURE__ */ jsx9("ul", { className: "divide-y", children: boxes.map((b) => /* @__PURE__ */ jsx9(HelperBox2, { berth, box: b.name, online: b.state === "online", active: visible, copy: COPY, testId: "database" }, b.name)) }) })
    ] }),
    /* @__PURE__ */ jsxs9(Frame, { variant: "card", children: [
      /* @__PURE__ */ jsxs9(FrameHeader, { children: [
        /* @__PURE__ */ jsx9(FrameTitle, { children: "Which databases show" }),
        /* @__PURE__ */ jsx9(FrameDescription, { children: "A worktree's Database tab shows the worktree's own development database. It learns of it from a service, from the worktree's files, or from you." })
      ] }),
      /* @__PURE__ */ jsx9(FramePanel, { children: /* @__PURE__ */ jsxs9("ol", { className: "space-y-3 text-sm", children: [
        /* @__PURE__ */ jsxs9(Step, { n: 1, title: "From a service Shipyard starts in the worktree", children: [
          "With Database on for a box, Shipyard tells it about each service it starts there: the ones under ",
          /* @__PURE__ */ jsx9("code", { children: "services" }),
          " in the repository's ",
          /* @__PURE__ */ jsx9("code", { children: ".berth/config.json" }),
          ". A service that was already running needs a restart to be noticed."
        ] }),
        /* @__PURE__ */ jsxs9(Step, { n: 2, title: "Or from the worktree's .env files", children: [
          "When you open a worktree's Database tab and no service has said, the box reads ",
          /* @__PURE__ */ jsx9("code", { children: ".env" }),
          ", ",
          /* @__PURE__ */ jsx9("code", { children: ".env.development" }),
          ", ",
          /* @__PURE__ */ jsx9("code", { children: ".env.local" }),
          " and ",
          /* @__PURE__ */ jsx9("code", { children: ".env.development.local" }),
          " in the worktree's own folder, a later file winning, and takes the ",
          /* @__PURE__ */ jsx9("code", { children: "DATABASE_URL" }),
          " or ",
          /* @__PURE__ */ jsx9("code", { children: "POSTGRES_URL" }),
          " it finds. So a server you start by hand in a terminal works too. Reading the tables again looks in the files again."
        ] }),
        /* @__PURE__ */ jsxs9(Step, { n: 3, title: "Its DATABASE_URL or POSTGRES_URL points at a Postgres on the box", children: [
          "On the box itself: ",
          /* @__PURE__ */ jsx9("code", { children: "localhost" }),
          ", ",
          /* @__PURE__ */ jsx9("code", { children: "127.0.0.1" }),
          " or a Unix socket. That is the worktree's own development database, and the only kind shown. Nothing in a service's environment is changed, and no file is written."
        ] }),
        /* @__PURE__ */ jsxs9(Step, { n: 4, title: "Or connect it yourself", children: [
          "When neither says which database a worktree has, its tab offers ",
          /* @__PURE__ */ jsx9("b", { children: "Connect a database\u2026" }),
          ": paste the address, and it is kept on the box with the others until you forget it. The same rule holds: a Postgres on the box itself."
        ] }),
        /* @__PURE__ */ jsxs9(Step, { n: 5, title: "What doesn't show", children: [
          "A database on another host (a shared staging database, a hosted one) is never opened from here. Neither is one whose address is a reference to a secret and not the address itself, or a ",
          /* @__PURE__ */ jsx9("code", { children: ".env" }),
          " that is a link to a file outside the worktree. Other engines than Postgres don't show yet."
        ] })
      ] }) })
    ] })
  ] });
}
function Step({ n, title, children }) {
  return /* @__PURE__ */ jsxs9("li", { className: "flex gap-3", children: [
    /* @__PURE__ */ jsx9("span", { className: "mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-muted font-medium text-muted-foreground text-xs tabular-nums", children: n }),
    /* @__PURE__ */ jsxs9("div", { className: "min-w-0", children: [
      /* @__PURE__ */ jsx9("div", { className: "font-medium", children: title }),
      /* @__PURE__ */ jsx9("p", { className: "mt-0.5 text-pretty text-muted-foreground [&_code]:text-foreground", children })
    ] })
  ] });
}

// src/index.tsx
import { jsx as jsx10, jsxs as jsxs10 } from "react/jsx-runtime";
var index_default = definePlugin((berth) => {
  if (!berth.helper) {
    berth.addScreen({ id: "database", title: "Database", Component: TooOld });
    berth.addSidebarItem({ id: "database", title: "Database", icon: "table-2", screen: "database" });
    return;
  }
  berth.addWorktreePanel({ id: "database", title: "Database", icon: "table-2", Component: DatabasePanel });
  berth.addScreen({ id: "database", title: "Database", description: "A worktree's own development database: tables, rows and a SQL console, read-only until you unlock it.", Component: DatabaseScreen });
  berth.addSidebarItem({ id: "database", title: "Database", icon: "table-2", screen: "database" });
  berth.addCommand({ id: "open", title: "Show Database", group: "Database", run: () => berth.openScreen("database") });
  berth.addCommand({ id: "panel", title: "Show this worktree's database", group: "Database", run: () => berth.openPanel("database") });
});
function TooOld(_) {
  return /* @__PURE__ */ jsx10(Empty3, { className: "py-16", children: /* @__PURE__ */ jsxs10(EmptyHeader3, { children: [
    /* @__PURE__ */ jsx10(Icon9, { name: "table-2", className: "mx-auto mb-2 size-5 text-muted-foreground" }),
    /* @__PURE__ */ jsx10(EmptyTitle3, { children: "Database needs a newer Shipyard" }),
    /* @__PURE__ */ jsx10(EmptyDescription3, { children: "This version can't install a plugin's program on a box. Update Shipyard, then reload plugins in Settings \u2192 Plugins." })
  ] }) });
}
export {
  index_default as default
};
