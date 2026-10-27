// Captures mock mode's Leadline fixtures (src/lib/fixtures/leadline.json)
// from a real berth-leadline: starts the helper on a scratch folder, sends it
// made-up "acme" telemetry for one worktree of the mock fleet (shop /
// checkout-fix on devl) over OTLP/JSON, and writes down what its API then
// answers. Nothing here comes from a real system, and every statement is
// sent with placeholders ($1), so no fixture holds a value.
//
//   go build -o /tmp/berth-leadline ./cmd/berth-leadline
//   node scripts/capture-leadline-fixtures.mjs /tmp/berth-leadline
//
// Run it again when internal/leadline/API.md changes a response.
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const bin = process.argv[2];
if (!bin) {
  console.error("usage: node scripts/capture-leadline-fixtures.mjs <path to berth-leadline>");
  process.exit(1);
}
const out = resolve(import.meta.dirname, "..", "src", "lib", "fixtures", "leadline.json");

const SOURCE = "shop/checkout-fix";
const WORKER = `${SOURCE}:worker`;
const RESOURCE = { "shipyard.project": "shop", "shipyard.worktree": "checkout-fix", "shipyard.branch": "me/checkout-fix", "shipyard.box": "devl" };

// The same seed gives the same telemetry.
let seed = 0x5eed1e;
const rnd = () => {
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const between = (lo, hi) => lo + rnd() * (hi - lo);
const int = (lo, hi) => Math.floor(between(lo, hi + 1));
const hex = (n) => Array.from({ length: n }, () => Math.floor(rnd() * 16).toString(16)).join("");

const KIND = { internal: 1, server: 2, client: 3, consumer: 5 };
const attrs = (o) => Object.entries(o).map(([key, v]) => ({ key, value: typeof v === "number" ? { intValue: String(v) } : { stringValue: String(v) } }));
const nanos = (ms) => String(BigInt(Math.round(ms * 1000)) * 1000n);

const spans = { [SOURCE]: [], [WORKER]: [] };
const logs = { [SOURCE]: [], [WORKER]: [] };

function span(service, trace, { id = hex(16), parent = "", name, kind, start, ms, attributes = {}, error }) {
  spans[service].push({
    traceId: trace,
    spanId: id,
    parentSpanId: parent,
    name,
    kind: KIND[kind],
    startTimeUnixNano: nanos(start),
    endTimeUnixNano: nanos(start + ms),
    attributes: attrs(attributes),
    status: error ? { code: 2, message: error } : {},
  });
  return id;
}

// One statement, as a driver reports it: placeholders, never values.
function pg(service, trace, parent, at, ms, statement, error) {
  span(service, trace, { parent, name: "pg.query", kind: "client", start: at, ms, attributes: { "db.system.name": "postgresql", "db.namespace": "acme", "db.query.text": statement }, error });
  return at + ms + between(0.05, 0.4);
}

const SEVERITY = { debug: 5, info: 9, warn: 13, error: 17 };
function log(service, at, level, body, trace = "", spanId = "", attributes = {}) {
  logs[service].push({ timeUnixNano: nanos(at), severityNumber: SEVERITY[level], severityText: level.toUpperCase(), body: { stringValue: body }, traceId: trace, spanId, attributes: attrs(attributes) });
}

function request(at, method, route, status, user, body) {
  const trace = hex(32);
  const root = hex(16);
  const end = body(trace, root, at + between(0.4, 1.2));
  span(SOURCE, trace, {
    id: root,
    name: `${method} ${route}`,
    kind: "server",
    start: at,
    ms: end + between(0.3, 1.1) - at,
    attributes: { "http.request.method": method, "http.route": route, "http.response.status_code": status, ...(user ? { "user.id": user } : {}) },
    error: status >= 500 ? "Internal Server Error" : undefined,
  });
  return { trace, root };
}

// The healthy case: one indexed query.
function listProducts(at) {
  const { trace, root } = request(at, "GET", "/api/products", 200, undefined, (t, r, cur) => pg(SOURCE, t, r, cur, between(1.8, 6.5), "SELECT id, name, price_cents FROM products WHERE category_id = $1 ORDER BY name LIMIT $2"));
  if (rnd() < 0.4) log(SOURCE, at + 4, "debug", "listed products", trace, root, { category: "lighting" });
}

// An N+1: one query for the orders, then one for each order.
function listOrders(at) {
  const n = int(8, 19);
  const user = `u_${int(1001, 1009)}`;
  const { trace, root } = request(at, "GET", "/api/orders", 200, user, (t, r, cur) => {
    cur = pg(SOURCE, t, r, cur, between(3, 11), "SELECT id, status, total_cents FROM orders WHERE customer_id = $1 ORDER BY created_at DESC LIMIT $2");
    for (let i = 0; i < n; i++) cur = pg(SOURCE, t, r, cur, between(0.9, 4.2), "SELECT id, product_id, quantity FROM order_items WHERE order_id = $1");
    return cur;
  });
  log(SOURCE, at + 30, "info", `listed ${n} orders`, trace, root, { orders: n });
}

// An unindexed ILIKE that is slow, and now and then very slow.
function search(at, verySlow) {
  const ms = verySlow ? between(1150, 1900) : between(170, 410);
  const { trace, root } = request(at, "GET", "/api/search", 200, undefined, (t, r, cur) => pg(SOURCE, t, r, cur, ms, "SELECT id, name FROM products WHERE name ILIKE $1 OR description ILIKE $1 LIMIT $2"));
  if (verySlow) log(SOURCE, at + ms, "warn", `slow search took ${Math.round(ms)}ms`, trace, root, { rows: int(3, 20) });
}

// A write in a transaction.
function checkout(at) {
  const user = `u_${int(1001, 1009)}`;
  const { trace, root } = request(at, "POST", "/api/checkout", 201, user, (t, r, cur) => {
    cur = pg(SOURCE, t, r, cur, between(0.1, 0.4), "BEGIN");
    cur = pg(SOURCE, t, r, cur, between(2, 7), "INSERT INTO orders (customer_id, total_cents, status) VALUES ($1, $2, $3) RETURNING id");
    cur = pg(SOURCE, t, r, cur, between(2, 8), "UPDATE inventory SET reserved = reserved + $1 WHERE product_id = $2");
    span(SOURCE, t, { parent: r, name: "POST payments.acme.test/charge", kind: "client", start: cur, ms: between(60, 180), attributes: { "http.request.method": "POST", "server.address": "payments.acme.test" } });
    cur += 190;
    return pg(SOURCE, t, r, cur, between(0.2, 0.9), "COMMIT");
  });
  log(SOURCE, at + 200, "info", "order placed", trace, root);
}

// The payment provider's webhook, delivered again: the second delivery
// fails, which is what this worktree is there to fix.
function webhook(at, fails) {
  const { trace, root } = request(at, "POST", "/api/webhooks/payments", fails ? 500 : 200, undefined, (t, r, cur) => {
    cur = pg(SOURCE, t, r, cur, between(1.5, 5), "SELECT id, status FROM orders WHERE payment_ref = $1");
    return pg(SOURCE, t, r, cur, between(2, 6), "UPDATE orders SET status = $1, settled_at = now() WHERE id = $2 AND status = $3", fails ? "could not serialize access due to concurrent update" : undefined);
  });
  if (fails) log(SOURCE, at + 12, "error", "webhook failed: order already settled by an earlier delivery", trace, root, { attempt: int(2, 4) });
}

// A background job, in the worktree's second command.
function receiptJob(at) {
  const trace = hex(32);
  const root = hex(16);
  const end = pg(WORKER, trace, root, at + 1, between(1, 4), "SELECT email FROM customers WHERE id = $1");
  span(WORKER, trace, { id: root, name: "job send-receipt", kind: "consumer", start: at, ms: end + between(20, 60) - at, attributes: { "messaging.operation.name": "process" } });
  log(WORKER, at + 30, "info", "receipt sent", trace, root);
}

function generate(now) {
  // What happened, oldest first, over the last twelve minutes.
  const plan = [
    ...Array.from({ length: 8 }, () => listProducts),
    ...Array.from({ length: 6 }, () => listOrders),
    ...Array.from({ length: 5 }, () => (at) => search(at, false)),
    (at) => search(at, true),
    ...Array.from({ length: 4 }, () => checkout),
    ...Array.from({ length: 3 }, () => (at) => webhook(at, true)),
    ...Array.from({ length: 2 }, () => (at) => webhook(at, false)),
    ...Array.from({ length: 3 }, () => receiptJob),
  ];
  for (let i = plan.length - 1; i > 0; i--) {
    const j = int(0, i);
    [plan[i], plan[j]] = [plan[j], plan[i]];
  }
  const from = now - 12 * 60_000;
  const step = (now - 6000 - from) / plan.length;
  plan.forEach((make, i) => make(from + step * (i + rnd() * 0.8)));
  log(SOURCE, from - 20_000, "info", "server listening on 3000");
  log(SOURCE, from - 19_000, "info", "compiled in 412ms");
  log(WORKER, from - 18_000, "info", "worker ready, 2 queues");
}

const resource = (service) => ({ attributes: attrs({ "service.name": service, ...RESOURCE }) });

async function main() {
  const dir = await mkdtemp(join(tmpdir(), "berth-leadline-fixtures-"));
  const helper = spawn(bin, ["serve", "--dir", dir, "--addr", "127.0.0.1:0"], { stdio: ["ignore", "inherit", "inherit"] });
  try {
    let base;
    for (let i = 0; i < 100 && !base; i++) {
      await new Promise((r) => setTimeout(r, 100));
      base = await readFile(join(dir, "helper.json"), "utf8").then((s) => JSON.parse(s).api, () => undefined);
    }
    if (!base) throw new Error("the helper did not write helper.json");

    generate(Date.now());
    const post = (path, body) => fetch(base + path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }).then((r) => (r.ok ? r.text() : Promise.reject(new Error(`${path}: ${r.status}`))));
    await post("/v1/traces", { resourceSpans: Object.entries(spans).map(([service, s]) => ({ resource: resource(service), scopeSpans: [{ spans: s }] })) });
    await post("/v1/logs", { resourceLogs: Object.entries(logs).map(([service, l]) => ({ resource: resource(service), scopeLogs: [{ logRecords: l }] })) });

    const get = async (path, params = {}) => {
      const res = await fetch(`${base}/api/${path}?${new URLSearchParams(params)}`);
      if (!res.ok) throw new Error(`${path}: ${res.status} ${await res.text()}`);
      return res.json();
    };
    const source = { source: SOURCE };
    const queries = await get("queries", source);
    const traces = await get("traces", source);
    const endpoints = await get("endpoints", { ...source, spark: "24" });
    const fixture = {
      // What every time in this file is counted back from.
      captured_ms: Date.now(),
      source: SOURCE,
      health: await get("health"),
      storage: await get("storage"),
      sources: await get("sources"),
      queries,
      control: await get("queries", { ...source, control: "1" }),
      query_details: await Promise.all(queries.map((q) => get("queries/detail", { ...source, db_system: q.db_system, fingerprint: q.fingerprint, buckets: "30" }))),
      traces,
      trace_details: await Promise.all(traces.map((t) => get(`traces/${t.trace_id}`))),
      logs: await get("logs", source),
      endpoints,
      endpoint_details: await Promise.all(endpoints.rows.map((e) => get("endpoints/detail", { ...source, endpoint_service: e.service, name: e.name, buckets: "30" }))),
    };
    // The helper's address was this run's: the mock gives its own.
    fixture.sources.otlp.endpoints = ["http://127.0.0.1:4325"];
    // One line for each response, so a new capture is a diff that can be read.
    const lines = Object.entries(fixture).map(([k, v]) => `${JSON.stringify(k)}: ${Array.isArray(v) && v.length > 1 ? `[\n${v.map((x) => JSON.stringify(x)).join(",\n")}\n]` : JSON.stringify(v)}`);
    await writeFile(out, `{\n${lines.join(",\n")}\n}\n`);
    console.log(`leadline fixtures: ${queries.length} queries, ${traces.length} traces, ${fixture.logs.length} logs, ${endpoints.rows.length} endpoints -> ${out}`);
  } finally {
    helper.kill("SIGTERM");
    await new Promise((r) => helper.once("exit", r));
    await rm(dir, { recursive: true, force: true });
  }
}

await main();
