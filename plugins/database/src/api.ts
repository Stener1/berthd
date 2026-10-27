import type { BerthPluginContext } from "@berth/plugin";

import { BY_HAND_VARIABLE, type Told } from "./dotenv.ts";

// The berth-database helper's API, as internal/database/API.md documents it,
// through the box (berth.helper.request): every path is relative to the
// helper's /api/. berthd adds the helper's token itself; the plugin never
// sees or sends one. Only what the plugin reads is typed here.

// A value as the helper sends it: null is SQL NULL, an integer is a number
// up to 2^53 and its digits as a string past that, and what JSON has no kind
// for (a numeric, a timestamp, a uuid, bytea, json, an array) is a string,
// written as the database writes it. A cell that was read can be sent back
// as it came.
export type Cell = null | boolean | number | string;

// What a connection's engine can do. The panel is built from this, not from
// what Postgres happens to do.
export interface Capabilities {
  // The SQL dialect, for the editor's highlighting and completion.
  dialect: string;
  // Tables are grouped in schemas (a level in the tree) or are one flat list.
  schemas: boolean;
  // The filter operators db/{conn}/rows takes.
  operators: string[];
  // The kinds of relation it has: "table", "view", "materialized_view".
  kinds: string[];
  // It takes writes at all (through the lock). False: read-only, always.
  writes: boolean;
  // An edit is all or nothing.
  transactions: boolean;
}

// What a helper that names no capabilities is taken to have: the least.
export const NO_CAPABILITIES: Capabilities = { dialect: "", schemas: true, operators: ["="], kinds: ["table"], writes: false, transactions: false };

export interface Connection {
  // Stable across restarts: "c_" and twelve hex digits.
  id: string;
  source: string;
  // The variable the service has the database in: "DATABASE_URL".
  label: string;
  engine: string;
  database: string;
  host: string;
  port: number;
  user: string;
  // Writes are on until then (unix ms, by the box's clock: see clock.ts);
  // absent while they are locked.
  writes_until?: number;
  capabilities?: Capabilities;
}

export interface Column {
  name: string;
  type: string;
  nullable: boolean;
  default?: string;
  primary_key?: true;
}

export interface Index {
  name: string;
  columns: string[];
  unique: boolean;
}

export interface ForeignKey {
  columns: string[];
  ref_schema: string;
  ref_table: string;
  ref_columns: string[];
}

export interface Table {
  name: string;
  kind: string;
  rows_estimate: number;
  columns: Column[];
  primary_key: string[];
  indexes: Index[];
  foreign_keys: ForeignKey[];
}

export interface Schema {
  name: string;
  tables: Table[];
}

export interface Order {
  column: string;
  desc?: true;
}

export interface Filter {
  column: string;
  op: string;
  value?: Cell;
}

export interface RowsRequest {
  // "" for an engine without schemas: it is then left out of the request.
  schema: string;
  table: string;
  limit?: number;
  offset?: number;
  order_by?: Order[];
  filters?: Filter[];
}

// [row, column] of each cell that was cut at 4,000 characters.
export type Cut = [number, number][];

export interface Rows {
  columns: { name: string; type: string }[];
  rows: Cell[][];
  primary_key: string[];
  // Of the whole table, whatever the filters.
  rows_estimate: number;
  // Rows follow these. The next page starts at offset + rows.length: a page
  // can stop short of its limit, at the helper's size cap.
  more: boolean;
  cut?: Cut;
}

export interface QueryResult {
  columns: { name: string; type: string }[];
  rows: Cell[][];
  // The rows returned; for a statement that returns none, the rows it changed.
  row_count: number;
  command: string;
  duration_ms: number;
  truncated: boolean;
  notices: string[];
  cut?: Cut;
}

export type Change = { op: "update"; key: Record<string, Cell>; set: Record<string, Cell> } | { op: "insert"; set: Record<string, Cell> } | { op: "delete"; key: Record<string, Cell> };

export interface Edited {
  updated: number;
  inserted: number;
  deleted: number;
}

// The rows a page of a table holds, the most one can, and the most a
// statement returns.
export const PAGE = 100;
export const MOST = 500;
export const QUERY_LIMIT = 1000;
// A cell longer than this arrives cut.
export const CUT_AT = 4000;
// The most changes one edit takes.
export const MOST_CHANGES = 1000;

export function database(berth: BerthPluginContext) {
  const on = (conn: string, route: string) => `db/${encodeURIComponent(conn)}/${route}`;
  const post = <T,>(box: string, path: string, body: unknown) => berth.helper.request<T>(box, "POST", path, body);
  return {
    // The databases the helper knows for a worktree. None when its services
    // have not been started since the helper was turned on, or use no
    // database the helper can reach.
    connections: (box: string, source: string) => berth.helper.request<{ connections: Connection[] }>(box, "GET", `db/connections?${new URLSearchParams({ source })}`).then((r) => r.connections ?? []),
    // Whether this Shipyard can have a box look in a worktree's .env files.
    canLook: typeof berth.helper.attachEnv === "function",
    // The box looks in the worktree's .env files and tells the helper of
    // the database it finds there (dotenv.ts). worktree "" is the main
    // checkout. This is the box's own route, not one of the helper's.
    look: (box: string, location: string, worktree: string) => berth.helper.attachEnv<Told>(box, { location, worktree }),
    // Tells the helper of a database the person gave the address of, in
    // place of the one they gave before (dotenv.ts: "by hand"). The address
    // goes to the box in the request and is not kept here.
    connect: (box: string, location: string, worktree: string, address: string) => berth.helper.attachEnv<Told>(box, { location, worktree, env: { [BY_HAND_VARIABLE]: address } }),
    // Takes back the database that was given by hand.
    forget: (box: string, location: string, worktree: string) => berth.helper.attachEnv<Told>(box, { location, worktree, env: {} }),
    schema: (box: string, conn: string) => berth.helper.request<{ schemas: Schema[] }>(box, "GET", on(conn, "schema")).then((r) => r.schemas ?? []),
    rows: (box: string, conn: string, req: RowsRequest) => post<Rows>(box, on(conn, "rows"), { ...req, schema: req.schema || undefined }),
    query: (box: string, conn: string, sql: string, limit = QUERY_LIMIT) => post<QueryResult>(box, on(conn, "query"), { sql, limit }),
    edit: (box: string, conn: string, schema: string, table: string, changes: Change[]) => post<Edited>(box, on(conn, "edit"), { schema: schema || undefined, table, changes }),
    writes: (box: string, conn: string, turnOn: boolean) => post<{ writes_until?: number }>(box, on(conn, "writes"), { on: turnOn }),
  };
}

export type Database = ReturnType<typeof database>;

// What went wrong with a request, in the helper's words. code is what to
// branch on, never the status: "read_only", "row_conflict", "query_failed",
// "timeout", "multiple_statements", "no_primary_key", "too_large",
// "db_unavailable", "not_found", "unauthorized", "bad_request". message is a
// sentence for the person, hint what to try. For a statement the database
// refused there may be its sqlstate, the position it stopped at (a character
// of the statement, counted from 1) and the database's own detail.
export interface Fault {
  message: string;
  code?: string;
  status?: number;
  hint?: string;
  position?: number;
  sqlstate?: string;
  detail?: string;
}

export function faultOf(err: unknown): Fault {
  const e = (err ?? {}) as { message?: unknown; code?: unknown; status?: unknown; detail?: { error?: unknown } };
  const inner = (e.detail?.error && typeof e.detail.error === "object" ? e.detail.error : {}) as { hint?: unknown; position?: unknown; sqlstate?: unknown; detail?: unknown };
  const text = (v: unknown) => (typeof v === "string" && v ? v : undefined);
  return {
    message: typeof e.message === "string" && e.message ? e.message : String(err),
    code: typeof e.code === "string" ? e.code : undefined,
    status: typeof e.status === "number" ? e.status : undefined,
    hint: text(inner.hint),
    position: typeof inner.position === "number" && inner.position >= 1 ? inner.position : undefined,
    sqlstate: text(inner.sqlstate),
    detail: text(inner.detail),
  };
}

// A helper from before db/connections existed says it has no such route.
export const isOldHelper = (f: Fault) => f.code === "not_found";
// Every route answers "unauthorized" when the box's berthd is from before
// it sent a helper its token: the helper is fine, the box needs updating.
export const isOldBox = (f: Fault) => f.code === "unauthorized";
// The helper is not there to answer: the box says it is not running
// ("helper_off", while the box starts it again too), or that it runs and did
// not answer ("helper_unreachable"); or the box could not be asked at all
// (a 503 with no code: not the helper's answer, whose 503s have one).
export const lostHelper = (f: Fault) => f.code === "helper_off" || f.code === "helper_unreachable" || (f.status === 503 && !f.code);
