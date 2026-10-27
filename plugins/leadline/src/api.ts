import type { BerthPluginContext } from "@berth/plugin";

// The berth-leadline helper's API, as internal/leadline/API.md documents it,
// through the box (berth.helper.request). Only what the plugin reads is
// typed here.

// One request ran the same statement this many times: an N+1. The helper's
// own number, on every row with n_plus_one.
export const N_PLUS_ONE = 5;

export interface QueryRow {
  db_system: string;
  // The statement with "?" where a value was: with db_system, it names the query.
  fingerprint: string;
  calls: number;
  total_ms: number;
  avg_ms: number;
  p50_ms: number;
  p95_ms: number;
  max_ms: number;
  errors: number;
  last_ms: number;
  traces: number;
  max_per_trace: number;
  n_plus_one: boolean;
}

export interface Series {
  bucket_ms: number;
  t: number[];
}

export interface QueryEndpoint {
  service: string;
  name: string;
  calls: number;
  requests: number;
  endpoint_requests: number;
  per_request: number;
  max_per_request: number;
  avg_ms: number;
  total_ms: number;
  n_plus_one: boolean;
}

export interface QueryTrace {
  trace_id: string;
  name: string;
  service: string;
  start_ms: number;
  calls: number;
  errors: number;
  total_ms: number;
  slowest_ms: number;
}

export interface QueryDetail {
  // null when the shape did not run in the window.
  stats: (QueryRow & { share: number }) | null;
  series: Series & { calls: number[]; errors: number[]; total_ms: number[]; avg_ms: (number | null)[]; p95_ms: (number | null)[] };
  endpoints: QueryEndpoint[];
  traces: QueryTrace[];
  // The requests that show the N+1: the traces that ran it five times or more.
  n_plus_one: QueryTrace[];
}

export interface TraceRow {
  trace_id: string;
  name: string;
  service: string;
  start_ms: number;
  duration_ms: number;
  spans: number;
  errors: number;
  queries: number;
  db_ms: number;
  user_id?: string;
  cursor: string;
}

export interface Span {
  span_id: string;
  parent_id: string;
  service: string;
  name: string;
  kind: string;
  start_ms: number;
  duration_ms: number;
  status: "ok" | "error" | "unset";
  status_message: string;
  db_system: string;
  // The SQL as it was sent, values and all: shown and sent only as its shape.
  db_statement: string;
  http_method: string;
  http_route: string;
  http_status: number;
  attrs: Record<string, unknown>;
  db_role?: "query" | "control" | "duplicate";
  entry?: boolean;
  db_server?: string;
  depth: number;
}

export interface TreeNode {
  span_id: string;
  children: TreeNode[];
}

export interface LogRow {
  ts_ms: number;
  service: string;
  severity: string;
  body: string;
  trace_id: string;
  span_id: string;
  attrs: Record<string, unknown>;
  cursor?: string;
}

export interface TraceDetail {
  trace_id: string;
  spans: Span[];
  tree: TreeNode[];
  logs: LogRow[];
}

export interface EndpointRow {
  service: string;
  name: string;
  requests: number;
  errors: number;
  total_ms: number;
  avg_ms: number;
  p50_ms: number;
  p95_ms: number;
  max_ms: number;
  // Queries, and database time, per request on average.
  queries: number;
  db_ms: number;
  share: number;
  last_ms: number;
  spark?: number[];
}

export interface Endpoints extends Series {
  total: number;
  rows: EndpointRow[];
}

export interface EndpointQuery {
  db_system: string;
  fingerprint: string;
  calls: number;
  requests: number;
  per_request: number;
  max_per_request: number;
  avg_ms: number;
  max_ms: number;
  total_ms: number;
  n_plus_one: boolean;
}

export interface EndpointRequest {
  trace_id: string;
  start_ms: number;
  duration_ms: number;
  queries: number;
  db_ms: number;
  failed: boolean;
}

export interface EndpointDetail {
  stats: EndpointRow | null;
  series: Series & { requests: number[]; errors: number[]; p50_ms: (number | null)[]; p95_ms: (number | null)[] };
  queries: EndpointQuery[];
  slowest: EndpointRequest[];
  failing: EndpointRequest[];
}

export interface Source {
  name: string;
  branch?: string;
  services: string[];
  spans: number;
  logs: number;
  last_seen_ms: number;
  // How its data arrives: "otlp", "postgres-proxy", both, or neither yet.
  via: string[];
}

export interface Sources {
  // The base URLs that take OTLP on the box; the first is the helper's own.
  otlp: { endpoints: string[] };
  total: number;
  sources: Source[];
}

export interface Storage {
  size_bytes: number;
  max_bytes: number;
  retain_ms: number;
  oldest_ms: number;
}

export type EndpointSort = "time" | "name" | "requests" | "errors" | "avg" | "p95" | "queries" | "db";

type Params = Record<string, string | number | boolean | undefined>;

// path is a route with its parameters; what is left out or empty is not sent.
export function path(route: string, params: Params = {}): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "" || v === false) continue;
    q.set(k, v === true ? "1" : String(v));
  }
  const qs = q.toString();
  return qs ? `${route}?${qs}` : route;
}

// A worktree's window on the helper: every read is for one source on one
// box, from sinceMs on.
export interface Scope {
  box: string;
  source: string;
  sinceMs: number;
}

export function leadline(berth: BerthPluginContext) {
  const get = <T,>(box: string, route: string, params?: Params) => berth.helper.request<T>(box, "GET", path(route, params));
  const of = (s: Scope) => ({ source: s.source, since_ms: Math.round(s.sinceMs) });
  return {
    // Grouped by shape, costliest first. The route has no order of its own
    // to ask for, so the panel sorts what it has.
    queries: (s: Scope, o: { q?: string; control?: boolean; limit: number; offset?: number }) => get<QueryRow[]>(s.box, "queries", { ...of(s), ...o }),
    query: (s: Scope, q: { db_system: string; fingerprint: string }, buckets = 30) => get<QueryDetail>(s.box, "queries/detail", { ...of(s), ...q, buckets }),
    traces: (s: Scope, o: { q?: string; errors?: boolean; limit: number; before?: string }) => get<TraceRow[]>(s.box, "traces", { ...of(s), ...o }),
    trace: (box: string, id: string) => get<TraceDetail>(box, `traces/${encodeURIComponent(id)}`),
    logs: (s: Scope, o: { q?: string; min_severity?: number; limit: number; before?: string }) => get<LogRow[]>(s.box, "logs", { ...of(s), ...o }),
    endpoints: (s: Scope, o: { q?: string; sort: EndpointSort; dir: "asc" | "desc"; limit: number; offset?: number }) => get<Endpoints>(s.box, "endpoints", { ...of(s), ...o, spark: 24 }),
    endpoint: (s: Scope, e: { service: string; name: string }, buckets = 30) => get<EndpointDetail>(s.box, "endpoints/detail", { ...of(s), endpoint_service: e.service, name: e.name, buckets }),
    sources: (box: string, source?: string) => get<Sources>(box, "sources", { source }),
    storage: (box: string) => get<Storage>(box, "storage"),
  };
}

export type Leadline = ReturnType<typeof leadline>;
