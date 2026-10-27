import type { HelperState, HelperStatus } from "@berth/plugin";

import { ApiError } from "@/lib/api";
import { helperEnvAsked, helperRelayed } from "@/lib/helper-path";
import type { HelperRemoved, PluginHelpers } from "@/lib/plugin-helpers";

// Mock mode's plugin helpers, behaving like the laptop agent's
// /v1/plugins/{id}/helper and a box's helpers/{plugin}/api/ closely enough
// to build on. Two plugins carry one: Leadline (berth-leadline) and
// Database (berth-database). Each has a state of its own on each box: both
// run on devl and are off on gpu, and turning one on takes a moment
// (installing, starting, running), as a download does.
//
// What a running helper answers is src/lib/fixtures/leadline.json: the real
// berth-leadline's answers to made-up "acme" telemetry for one worktree,
// shop / checkout-fix on devl (scripts/capture-leadline-fixtures.mjs wrote
// it). shop's main checkout on devl, which has no agent, answers the same
// under its own name; every other worktree has sent nothing yet. Times are
// moved so the newest is a few seconds old whenever it is read.
//
// A box answers "off" for a helper it does not have, whether it never had
// it or it was turned off, and so do these; "none" is a plugin that names no
// helper. Turning one off answers with the services still pointed at it.
//
// ?helper=unsupported is a fleet whose boxes run a berthd from before
// helpers, ?helper=nobuild one whose boxes the plugin has no build for,
// ?helper=failed a helper that installs and then will not start,
// ?helper=mismatch a download that is not what the manifest's checksum
// says (the agent refuses the install: nothing changes on the box),
// ?helper=update has devl on an older build than the plugin carries, and
// ?helper=held has the plugin changed since it was allowed, so it is off
// for review while its helper, which did not change, is left running. They
// are about every helper alike, except held, which is Leadline's.
//
// What Database's helper answers is lib/mock-database.ts, an "acme" shop
// kept in memory. ?db=old has devl on a berth-database from before its db/
// routes: it answers 404 for them, as a real one would, until it is updated.
//
// A box also looks in a worktree's .env files for a helper when asked
// (helpers/{plugin}/attach-env, berth.helper.attachEnv). What devl finds for
// Database is mock-database.ts's to say (?db=env and the others there);
// ?db=env-old is a devl whose berthd is from before the route.
//
// window.__berthMockHelper.restart("database", "devl", 1500) is a running
// helper that ended and that the box starts again after that long, as berthd
// does (a crash, or berthd's own update): meanwhile the box says it is
// starting and answers "helper_off" for it, and what it kept only in memory
// (Database's writes switch) is gone when it is back.

type Delay = <T>(v: T) => Promise<T>;

const params = typeof window === "undefined" ? undefined : new URLSearchParams(window.location.search);
const scenario = params?.get("helper") ?? null;
const dbScenario = params?.get("db") ?? null;
// A build from before the one a plugin carries.
const OLDER = "0.0.9";
const PLATFORM = "linux-amd64";

// The plugins that carry a helper: the program each one's manifest names,
// the version of it the plugin carries, and the boxes it runs on from the
// start.
const HELPERS: Record<string, { program: string; latest: string; on: string[] }> = {
  leadline: { program: "berth-leadline", latest: "0.1.1", on: ["devl"] },
  database: { program: "berth-database", latest: "0.1.0", on: ["devl"] },
};
const programOf = (plugin: string) => HELPERS[plugin]?.program ?? `${plugin}'s helper`;
const latestOf = (plugin: string) => HELPERS[plugin]?.latest ?? "0.1.0";

interface Helper {
  state: HelperState;
  version?: string;
  error?: string;
  // What it recorded was removed, so it holds nothing.
  purged?: boolean;
  timers: number[];
}

const helpers = new Map<string, Helper>();

function helperOf(plugin: string, box: string): Helper {
  const key = `${plugin}\n${box}`;
  let h = helpers.get(key);
  if (!h) {
    const mine = HELPERS[plugin];
    const on = !!mine?.on.includes(box);
    const cant = scenario === "unsupported" || scenario === "nobuild";
    const older = scenario === "update" || (plugin === "database" && dbScenario === "old");
    h = { state: cant ? "unsupported" : on ? "running" : mine ? "off" : "none", version: on && !cant ? (older ? OLDER : latestOf(plugin)) : undefined, timers: [] };
    helpers.set(key, h);
  }
  return h;
}

function statusOf(plugin: string, box: string): HelperStatus {
  const h = helperOf(plugin, box);
  const update = h.state === "running" && h.version !== latestOf(plugin);
  // As the agent says each: a berthd from before helpers never told it the
  // box's platform; one the plugin has no build for did.
  if (scenario === "unsupported") return { plugin, box, state: h.state, platform: "", latest: latestOf(plugin), error: "this box runs an older berthd, which cannot run helpers; update it" };
  if (scenario === "nobuild") return { plugin, box, state: h.state, platform: PLATFORM, latest: latestOf(plugin), error: `${programOf(plugin)} has no build for ${PLATFORM}` };
  return { plugin, box, state: h.state, platform: PLATFORM, version: h.version, latest: latestOf(plugin), ...(update ? { update: true as const } : {}), error: h.error };
}

// forgetData is a helper's data removed from a box: it holds nothing it
// recorded or kept, the databases connected by hand among it.
function forgetData(plugin: string, box: string, h: Helper) {
  h.purged = true;
  if (plugin === "database") void databaseMock?.then((m) => m.databasePurged(box));
}

// helperLaptopCall answers the laptop agent's helper routes, or undefined
// for any other path. online says whether a box can be reached.
export function helperLaptopCall(method: string, path: string, online: (box: string) => boolean, delay: Delay): Promise<unknown> | undefined {
  const m = /^\/v1\/plugins\/([^/]+)\/helper\?(.*)$/.exec(path);
  if (!m) return undefined;
  const plugin = decodeURIComponent(m[1]);
  const query = new URLSearchParams(m[2]);
  const box = query.get("box") ?? "";
  if (!online(box)) return delay(null).then(() => Promise.reject(new ApiError(`${box} is offline`, 503)));
  const h = helperOf(plugin, box);
  if (method === "GET") return delay(statusOf(plugin, box));
  if (h.state === "unsupported" && method === "POST") {
    const why = scenario === "nobuild" ? `${programOf(plugin)} has no build for ${PLATFORM}, which ${box} runs` : `${box} cannot run ${plugin}'s helper: this box runs an older berthd, which cannot run helpers; update it`;
    return delay(null).then(() => Promise.reject(new ApiError(why, 409, "helper_unsupported")));
  }
  if (method === "POST" && scenario === "mismatch") {
    return delay(null).then(() => Promise.reject(new ApiError(`the helper at https://downloads.acme.example/${programOf(plugin)}-linux-amd64 does not match the checksum in the plugin's manifest; nothing was installed`, 502, "helper_mismatch")));
  }
  h.timers.forEach((t) => window.clearTimeout(t));
  h.timers = [];
  if (method === "POST") {
    h.state = "installing";
    h.error = undefined;
    const then = (ms: number, next: () => void) => h.timers.push(window.setTimeout(next, ms));
    if (scenario === "failed") {
      // The box has the program, and it will not stay up: berthd's words.
      then(900, () => {
        h.state = "failed";
        h.version = latestOf(plugin);
        h.error = "it ended (exit status 1) before it was ready; berthd tries again in 4s";
      });
    } else {
      then(900, () => {
        h.state = "starting";
        h.version = latestOf(plugin);
      });
      then(1500, () => {
        h.state = "running";
      });
    }
    return delay(statusOf(plugin, box));
  }
  if (method === "DELETE") {
    const purge = query.get("purge") === "1";
    // devl's dev server was started while Leadline's helper was on, with
    // its telemetry and database pointed at it, and runs on. Database's
    // helper changes nothing in a service's environment, so it leaves none.
    const attached = h.state === "running" && plugin === "leadline" && box === "devl" ? [{ location: "shop", worktree: "checkout-fix", service: "web" }] : undefined;
    if (h.state !== "unsupported") h.state = "off";
    h.version = undefined;
    h.error = undefined;
    if (purge) forgetData(plugin, box, h);
    return delay({ ...statusOf(plugin, box), ...(attached ? { attached } : {}) });
  }
  return undefined;
}

// A plugin's helper does not outlive it (internal/agent/helpercleanup.go):
// turning the plugin off, or uninstalling it, stops the helper on every box
// that has it. A box that is offline is done when it next connects.

interface MockBox {
  name: string;
  online: boolean;
}

// carries says whether a plugin brings a helper at all, in this mock.
const carries = (plugin: string, boxes: MockBox[]) => boxes.some((b) => helperOf(plugin, b.name).state !== "none");

// removeHelpers is what the agent does to a plugin's helper when the plugin
// is turned off (or, with purge, uninstalled), and what it answers of each
// box. Undefined for a plugin with no helper.
export function removeHelpers(plugin: string, boxes: MockBox[], purge: boolean): HelperRemoved[] | undefined {
  if (!carries(plugin, boxes)) return undefined;
  return boxes.map((b): HelperRemoved => {
    const h = helperOf(plugin, b.name);
    if (!b.online) return { box: b.name, state: "pending" };
    const was = h.state !== "off" && h.state !== "unsupported";
    // devl's dev server was started while the helper was on, and runs on.
    const attached = was && plugin === "leadline" && b.name === "devl" ? [{ location: "shop", worktree: "checkout-fix", service: "web" }] : undefined;
    h.timers.forEach((t) => window.clearTimeout(t));
    h.timers = [];
    if (h.state !== "unsupported") h.state = "off";
    h.version = undefined;
    h.error = undefined;
    if (purge) forgetData(plugin, b.name, h);
    return { box: b.name, state: was ? "removed" : "none", ...(purge ? { purged: true } : {}), ...(attached ? { attached } : {}) };
  });
}

// pluginHelpersCall answers GET /v1/plugins/{id}/helpers: where a plugin's
// helper is, for Settings to say before the plugin is turned off. plugin is
// the plugin as the mock has it: one that changed while it was on has its
// helper held until it is reviewed.
export function pluginHelpersCall(method: string, path: string, boxes: MockBox[], plugin: (id: string) => { enabled: boolean; changed?: boolean } | undefined, delay: Delay): Promise<PluginHelpers> | undefined {
  const m = /^\/v1\/plugins\/([^/]+)\/helpers$/.exec(path);
  if (!m || method !== "GET") return undefined;
  const id = decodeURIComponent(m[1]);
  const p = plugin(id);
  const has = carries(id, boxes);
  return delay({
    plugin: id,
    ...(has && HELPERS[id] ? { name: HELPERS[id].program } : {}),
    ...(has && p && !p.enabled && p.changed ? { held: true } : {}),
    boxes: has ? boxes.map((b) => (b.online ? { box: b.name, state: helperOf(id, b.name).state, version: helperOf(id, b.name).version } : { box: b.name, state: "offline" })) : [],
  });
}

type Row = Record<string, unknown>;

interface Fixture {
  captured_ms: number;
  source: string;
  health: Row;
  storage: Row;
  sources: { sources: Row[]; services: Row[]; proxies: Row[]; [k: string]: unknown };
  queries: Row[];
  control: Row[];
  query_details: { stats: Row; [k: string]: unknown }[];
  traces: Row[];
  trace_details: Row[];
  logs: Row[];
  endpoints: { rows: Row[]; [k: string]: unknown };
  endpoint_details: { stats: Row; [k: string]: unknown }[];
}

// The project's main checkout on devl, which has no agent in it: the same
// telemetry under its own name (the project's alone), as traces of its own.
const MAIN = "shop";

let fixtures: Promise<Fixture[]> | undefined;
// Read as text: 180 KB of JSON parses faster than it compiles.
const load = () =>
  (fixtures ??= import("@/lib/fixtures/leadline.json?raw").then((m) => {
    const first = JSON.parse(m.default) as Fixture;
    const main = JSON.parse(m.default.replaceAll(`"${first.source}`, `"${MAIN}`).replace(/[0-9a-f]{32}/g, (id) => [...id].reverse().join(""))) as Fixture;
    main.sources.sources = main.sources.sources.map(({ worktree: _, ...s }) => ({ ...s, branch: "main" }));
    return [first, main];
  }));

// The fields that hold a moment (unix ms), as against a length of time.
const MOMENTS = new Set(["start_ms", "ts_ms", "last_ms", "last_seen_ms", "last_activity_ms", "now_ms", "since_ms", "oldest_ms"]);

// moved is v with every moment in it by milliseconds later.
function moved<T>(v: T, by: number, key = ""): T {
  if (Array.isArray(v)) return v.map((x) => (key === "t" && typeof x === "number" ? x + by : moved(x, by))) as T;
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, typeof x === "number" && MOMENTS.has(k) && x > 0 ? x + by : moved(x, by, k)])) as T;
  return v;
}

// A helper's own refusal, as the box passes it through and the app's HTTP
// client reads it: the words are inside "error" (lib/helper.ts lifts them out).
function refusal(status: number, code: string, message: string): ApiError {
  const err = new ApiError(String({}), status);
  err.detail = { error: { code, message } };
  return err;
}

const SEVERITY: Record<string, number> = { trace: 1, debug: 5, info: 9, warn: 13, error: 17, fatal: 21 };
const has = (text: unknown, q: string) => !q || String(text ?? "").toLowerCase().includes(q.toLowerCase());
const num = (q: URLSearchParams, k: string, fallback: number) => {
  const n = Number(q.get(k));
  return q.get(k) && Number.isFinite(n) ? n : fallback;
};

// A page of a list ordered by time: the rows after the cursor's.
function page(rows: Row[], q: URLSearchParams, limit: number): Row[] {
  const before = q.get("before");
  const from = before ? rows.findIndex((r) => r.cursor === before) + 1 : 0;
  if (before && from === 0) throw refusal(400, "bad_request", "before is not a cursor of this list.");
  return rows.slice(from, from + limit);
}

const EMPTY_DETAIL = { stats: null, series: { bucket_ms: 0, t: [] }, histogram: { per_decade: 10, bins: [] }, endpoints: [], traces: [], n_plus_one: [], queries: [], slowest: [], failing: [] };

function answer(all: Fixture[], route: string, q: URLSearchParams, empty: boolean): unknown {
  const source = q.get("source") ?? "";
  // Two worktrees have sent anything: the fixture's, and its main checkout.
  const f = all.find((x) => x.source === source) ?? all[0];
  const mine = !empty && (!source || source === f.source);
  const since = num(q, "since_ms", 0);
  const text = q.get("q") ?? "";
  switch (route) {
    case "health":
      return f.health;
    case "storage":
      return mine ? f.storage : { ...f.storage, size_bytes: 0, file_bytes: 4096, oldest_ms: 0 };
    case "sources": {
      const heard = !mine ? [] : source ? [f] : [...all].sort((a, b) => a.source.localeCompare(b.source));
      return { ...f.sources, total: heard.length, sources: heard.flatMap((x) => x.sources.sources), services: heard.flatMap((x) => x.sources.services), proxies: [] };
    }
    case "queries": {
      const rows = !mine ? [] : (q.get("control") === "1" ? f.control : f.queries).filter((r) => has(r.fingerprint, text) || has(r.example, text));
      const offset = num(q, "offset", 0);
      return rows.slice(offset, offset + num(q, "limit", 200));
    }
    case "queries/detail":
      return (mine && f.query_details.find((d) => d.stats.fingerprint === q.get("fingerprint") && d.stats.db_system === (q.get("db_system") ?? ""))) || EMPTY_DETAIL;
    case "traces": {
      const min = num(q, "min_duration_ms", 0);
      const rows = !mine ? [] : f.traces.filter((r) => has(r.name, text) && (q.get("errors") !== "1" || Number(r.errors) > 0) && Number(r.duration_ms) >= min && Number(r.start_ms) >= since);
      return page(rows, q, num(q, "limit", 200));
    }
    case "logs": {
      const min = num(q, "min_severity", 0);
      const rows = !mine ? [] : f.logs.filter((r) => has(r.body, text) && (SEVERITY[String(r.severity)] ?? 9) >= min && Number(r.ts_ms) >= since);
      return page(rows, q, num(q, "limit", 500));
    }
    case "endpoints": {
      const keys: Record<string, string> = { time: "total_ms", name: "name", service: "service", requests: "requests", avg: "avg_ms", p50: "p50_ms", p95: "p95_ms", max: "max_ms", queries: "queries", db: "db_ms" };
      const sort = q.get("sort") ?? "time";
      const value = (r: Row) => (sort === "errors" ? Number(r.errors) / Math.max(1, Number(r.requests)) : (r[keys[sort] ?? "total_ms"] as number | string));
      const dir = q.get("dir") === "asc" ? 1 : -1;
      const rows = !mine ? [] : f.endpoints.rows.filter((r) => has(r.name, text)).sort((a, b) => (value(a) < value(b) ? -dir : value(a) > value(b) ? dir : 0));
      const offset = num(q, "offset", 0);
      return { ...f.endpoints, total: rows.length, rows: rows.slice(offset, offset + num(q, "limit", 200)) };
    }
    case "endpoints/detail":
      return (mine && f.endpoint_details.find((d) => d.stats.name === q.get("name") && d.stats.service === (q.get("endpoint_service") ?? ""))) || EMPTY_DETAIL;
    default: {
      const trace = /^traces\/([0-9a-f]{32})$/.exec(route);
      const found = trace && !empty && all.flatMap((x) => x.trace_details).find((d) => d.trace_id === trace[1]);
      if (found) return found;
      throw refusal(404, "not_found", trace ? "No such trace: it may have aged out." : `No such route: /api/${route}.`);
    }
  }
}

let databaseMock: Promise<typeof import("@/lib/mock-database")> | undefined;

if (typeof window !== "undefined") {
  (window as unknown as { __berthMockHelper: unknown }).__berthMockHelper = {
    restart(plugin: string, box: string, ms = 1500) {
      const h = helperOf(plugin, box);
      if (h.state !== "running") return;
      h.timers.forEach((t) => window.clearTimeout(t));
      h.timers = [];
      h.state = "starting";
      // berthd's own words for it.
      h.error = "it ended (signal: killed)";
      if (plugin === "database") void (databaseMock ??= import("@/lib/mock-database")).then((m) => m.databaseRestarted());
      h.timers.push(
        window.setTimeout(() => {
          h.state = "running";
          h.error = undefined;
        }, ms),
      );
    },
  };
}

// helperBoxCall answers a box's helpers/{plugin}/api/{path}, or undefined
// for any other path. {path} is the path on the helper, and its API is
// under /api/ there.
export function helperBoxCall(box: string, method: string, path: string, delay: Delay, body?: unknown): Promise<unknown> | undefined {
  // The box's own look in a worktree's .env files for a helper
  // (helpers/{plugin}/attach-env), beside the relay.
  const asked = helperEnvAsked(path);
  if (asked) {
    // A berthd from before the route answers a plain 404, which the laptop
    // agent reads as a box that is too old (?db=env-old).
    if (method !== "POST" || dbScenario === "env-old") return delay(null).then(() => Promise.reject(new ApiError(`${box} runs an older berthd that doesn't have this yet`, 404, "box_outdated")));
    const h = helperOf(asked, box);
    if (h.state !== "running") return delay(null).then(() => Promise.reject(new ApiError(`the ${asked} helper is not running on this box`, 409, "helper_off")));
    const where = (body ?? {}) as { location?: string; worktree?: string; env?: Record<string, unknown> | null };
    const location = where.location ?? "";
    const worktree = where.worktree ?? "";
    // With "env" the box reads no file: the helper is told what was given.
    const given = where.env && typeof where.env === "object" ? where.env : undefined;
    // Only Database's helper is told anything this way, in this mock.
    if (asked !== "database") return delay({ plugin: asked, location, worktree: worktree || location, command: given ? ".manual" : ".env", files: [], skipped: [], variables: [], answer: {} });
    if (given) {
      return (databaseMock ??= import("@/lib/mock-database")).then((m) => {
        try {
          return delay(m.databaseConnect(box, location, worktree, given));
        } catch (err) {
          return delay(null).then(() => Promise.reject(err));
        }
      });
    }
    return (databaseMock ??= import("@/lib/mock-database")).then((m) => new Promise((done) => window.setTimeout(done, m.ENV_LOOK_MS)).then(() => delay(m.databaseEnvLook(location, worktree, box !== "devl" || !!h.purged))));
  }
  const to = helperRelayed(path);
  if (!to) return undefined;
  const { plugin } = to;
  const h = helperOf(plugin, box);
  if (h.state !== "running") return delay(null).then(() => Promise.reject(new ApiError(`${plugin}'s helper isn't running on ${box}`, 409, "helper_off")));
  // Outside /api/ the helper answers as a plain web server does, and the
  // laptop agent takes a plain 404 from a box for a berthd too old to know
  // the route: so it reads to the app, as it does with real ones.
  if (!to.path.startsWith("/api/")) return delay(null).then(() => Promise.reject(new ApiError(`${box} runs an older berthd that doesn't have this yet`, 404, "box_outdated")));
  const route = to.path.slice("/api/".length).replace(/\/+$/, "");
  const noRoute = () => delay(null).then(() => Promise.reject(refusal(404, "not_found", `No such route: ${method} /api/${route}.`)));
  // Only devl's has heard from anything, until what it kept is removed.
  const empty = box !== "devl" || !!h.purged;
  if (plugin === "database") {
    // A berth-database from before db/ has no such routes.
    // In berth-database's own words.
    if (!route.startsWith("db/") || h.version !== latestOf(plugin)) return delay(null).then(() => Promise.reject(refusal(404, "not_found", "There is no such route.")));
    return (databaseMock ??= import("@/lib/mock-database")).then((m) => {
      try {
        return delay(m.databaseCall(method, route, new URLSearchParams(to.query), body, empty, box));
      } catch (err) {
        return delay(null).then(() => Promise.reject(err));
      }
    });
  }
  if (method !== "GET" || plugin !== "leadline") return noRoute();
  return load().then((all) => {
    const by = Date.now() - all[0].captured_ms;
    // Asked for in moved time; the fixture is filtered in its own.
    const q = new URLSearchParams(to.query);
    if (q.get("since_ms")) q.set("since_ms", String(Number(q.get("since_ms")) - by));
    return delay(moved(answer(all, route, q, empty), by));
  });
}
