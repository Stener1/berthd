import { ApiError } from "@/lib/api";

// Mock mode's berth-database helper (plugins/database): what a box's
// helpers/database/api/api/db/… answers, as internal/database/API.md says
// the real one does: the same fields, the same codes, and the real helper's
// own sentences, taken from a run of it against a Postgres 17. It is loaded
// when the panel first asks (mock-helper.ts routes to it).
//
// The data is a made-up "acme" shop, built here from a seed so it is the
// same on every load: customers, products, orders and their items, an
// inventory with a two-column key, an audit log with no key at all, a view
// and a materialized view, foreign keys between them, and a second schema
// with a 41-column table. Every kind of value the helper sends is in it:
// NULLs and empty strings side by side, integers as numbers and, past 2^53
// (page_views' ids), as their digits; numerics as text; timestamps, uuids,
// bytea, jsonb, arrays, one long description and one over 4,000 characters,
// which arrives cut.
//
// shop / checkout-fix on devl has two connections: that Postgres, and
// "events" on a made-up engine ("driftdb") that says it has no schemas,
// three filter operators, a kind of relation nothing has heard of and no
// writes, to show the panel is built from what a connection says it can do.
// shop's main checkout has the Postgres alone. No other worktree has a
// database yet.
//
// Everything is in memory: filters, ordering and paging (a page of the wide
// table stops short, as one does at the helper's size cap), the lock and
// its fifteen minutes, edits (all or nothing, row_conflict for a keyed row
// that is gone, query_failed for a value or a key the table refuses) and a
// console. The real helper hands a statement to Postgres; this one runs a
// small set itself and answers anything else query_failed, saying what it
// does run. That sentence is the only one here the real helper never says.
//
// ?db=none is a fleet where no worktree has a database yet, ?db=refused one
// whose database isn't answering the helper (db_unavailable), ?db=old a
// devl whose berth-database is from before the db/ routes (mock-helper.ts:
// it answers not_found until it is updated), and ?db=unauthorized a devl
// whose berthd is from before it sent a helper its token (every route
// answers unauthorized).
//
// The box also looks in a worktree's .env files when the panel asks
// (databaseEnvLook: helpers/database/attach-env on a real box). Without a
// scenario the files are not there: the worktrees above have their database
// from a service, and the others have none. The ?db=env scenarios are a
// project whose services Shipyard does not start, so the helper knows no
// database until a look: ?db=env has shop / checkout-fix's DATABASE_URL in
// its .env.local, ?db=env-none has the files and no such variable in them,
// ?db=env-refused has a DATABASE_URL that points at another host and a
// POSTGRES_URL that names a secret, ?db=env-slow is ?db=env on a box that
// takes a moment to look, and ?db=env-old is a devl whose berthd cannot
// look at all. window.__berthMockDatabase.dotenv("env") changes what the
// files hold while the page is open, as an editor would, and .looks() is
// how many times a box was asked to look.

type Cell = null | boolean | number | string;

interface Column {
  name: string;
  type: string;
  nullable: boolean;
  default?: string;
  primary_key?: true;
}

interface MockTable {
  schema: string;
  name: string;
  kind: string;
  columns: Column[];
  primary_key: string[];
  indexes: { name: string; columns: string[]; unique: boolean }[];
  foreign_keys: { columns: string[]; ref_schema: string; ref_table: string; ref_columns: string[] }[];
  rows: Cell[][];
}

interface MockConn {
  id: string;
  source: string;
  label: string;
  engine: string;
  database: string;
  host: string;
  port: number;
  user: string;
  capabilities: { dialect: string; schemas: boolean; operators: string[]; kinds: string[]; writes: boolean; transactions: boolean };
  tables: MockTable[];
  writesUntil?: number;
}

const scenario = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("db");

const WRITES_FOR_MS = 15 * 60_000;
const CUT_AT = 4000;

// A helper's own refusal, as the box passes it through: the words are
// inside "error" (lib/helper.ts lifts them out). For a statement the
// database refused there may be its sqlstate, where it stopped and its
// detail.
function refusal(status: number, code: string, message: string, more: { hint?: string; sqlstate?: string; position?: number; detail?: string } = {}): ApiError {
  const err = new ApiError(String({}), status);
  err.detail = { error: { code, message, ...more } };
  return err;
}

// ---- The acme shop ----

function seeded(seed: number) {
  let s = seed;
  return () => {
    s = (s + 0x6d2b79f5) | 0;
    let t = Math.imul(s ^ (s >>> 15), 1 | s);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// The newest row's moment: fixed, so a screenshot is the same every time.
const NOW = Date.UTC(2026, 8, 28, 9, 30, 0);
const DAY = 86_400_000;
const iso = (ms: number) => new Date(ms).toISOString();

const FIRST = ["Robin", "Sam", "Ada", "Noor", "Kai", "Maya", "Jonas", "Ines", "Tariq", "Lena", "Otto", "Priya", "Emre", "Zoe", "Hugo", "Anya", "Felix", "Mira", "Idris", "Clara"];
const LAST = ["Okafor", "Lindqvist", "Marsh", "Castellano", "Brandt", "Nakamura", "Oyelaran", "Petrov", "Haddad", "Whitlock", "Serrano", "Kowalski"];
const NOUNS = ["Kettle", "Lamp", "Notebook", "Backpack", "Mug", "Chair", "Desk mat", "Water bottle", "Headphones", "Keyboard", "Planter", "Blanket", "Candle", "Tote", "Speaker", "Clock"];
const ADJ = ["Harbor", "Slate", "Juniper", "Copper", "Fog", "Tidal", "Ember", "Linen"];
const PARAGRAPH =
  "Made in small batches for Acme by people who use it every day. The body is powder-coated steel with a matte finish that hides fingerprints, the handle is oiled beech, and every seam is welded and ground flush. It ships flat in recycled card with no plastic, and the two screws it needs are in the box with the key that turns them. ";

function shopTables(): MockTable[] {
  const r = seeded(1378);
  const pick = <T,>(list: T[]) => list[Math.floor(r() * list.length)];
  const int = (lo: number, hi: number) => lo + Math.floor(r() * (hi - lo + 1));
  const hex = (n: number) => Array.from({ length: n }, () => Math.floor(r() * 16).toString(16)).join("");
  const uuid = () => `${hex(8)}-${hex(4)}-4${hex(3)}-a${hex(3)}-${hex(12)}`;

  const customers: Cell[][] = [];
  for (let id = 1; id <= 240; id++) {
    const first = FIRST[(id * 7) % FIRST.length];
    const last = LAST[(id * 5) % LAST.length];
    const plan = id % 9 === 0 ? "business" : id % 3 === 0 ? "team" : "free";
    // NULL, the empty string and a note, side by side in the first rows.
    const notes = id === 1 ? null : id === 2 ? "" : id === 3 ? "Prefers email. Never phone before noon." : id % 11 === 0 ? "Wholesale account, net 30." : null;
    customers.push([
      id,
      `${first}.${last}${id > 60 ? id : ""}@${id % 4 === 0 ? "acme.test" : "example.com"}`.toLowerCase(),
      `${first} ${last}`,
      plan,
      id % 5 === 0,
      (int(0, 25000) / 100).toFixed(2),
      notes,
      id % 6 === 0 ? "{vip,wholesale}" : id % 4 === 0 ? "{newsletter}" : "{}",
      id % 7 === 0 ? null : JSON.stringify({ source: pick(["ad", "referral", "organic"]), locale: pick(["en-GB", "en-US", "de-DE", "sv-SE"]) }),
      iso(NOW - (241 - id) * DAY * 1.7 - int(0, DAY)),
    ]);
  }

  const products: Cell[][] = [];
  for (let id = 1; id <= 64; id++) {
    const name = `${ADJ[id % ADJ.length]} ${NOUNS[id % NOUNS.length]}`;
    // One long description, and one over 4,000 characters that arrives cut.
    const description = id === 1 ? `The ${name}. ${PARAGRAPH.repeat(3)}`.trim() : id === 2 ? `The ${name}, in full. ${PARAGRAPH.repeat(17)}`.trim() : id % 8 === 0 ? null : `${name} for the desk, the shelf or the bag.`;
    products.push([
      id,
      `ACME-${String(1000 + id * 13)}`,
      name,
      description,
      int(4, 240) * 50 - 1,
      id % 10 === 0 ? null : (int(80, 4200) / 1000).toFixed(3),
      id % 13 !== 0,
      id % 4 === 0 ? null : `\\x89504e470d0a1a0a${hex(40)}`,
      JSON.stringify({ color: pick(["slate", "sand", "moss", "ink"]), sizes: id % 3 === 0 ? ["s", "m", "l"] : ["one"] }),
      iso(NOW - (70 - id) * DAY * 9),
    ]);
  }

  const STATUS = ["paid", "paid", "shipped", "shipped", "shipped", "pending", "refunded", "cancelled"];
  const orders: Cell[][] = [];
  const items: Cell[][] = [];
  let item = 20_000;
  for (let n = 0; n < 1280; n++) {
    const id = 5001 + n;
    const status = STATUS[int(0, STATUS.length - 1)];
    const placed = NOW - (1280 - n) * 3_600_000 * 2.1;
    let total = 0;
    const lines = int(1, 5);
    for (let l = 0; l < lines; l++) {
      const product = int(1, 64);
      const quantity = int(1, 4);
      const price = products[product - 1][4] as number;
      total += quantity * price;
      items.push([++item, id, product, quantity, price]);
    }
    orders.push([id, int(1, 240), status, total, n % 17 === 0 ? "EUR" : "USD", iso(placed), status === "pending" || status === "cancelled" ? null : iso(placed + int(60_000, 900_000)), status === "shipped" ? uuid() : null, n % 29 === 0 ? "Leave with the neighbour at 14." : n % 31 === 0 ? "" : null]);
  }

  const inventory: Cell[][] = [];
  for (let id = 1; id <= 64; id++) {
    for (const warehouse of ["east", "north", "west"]) {
      const on_hand = int(0, 400);
      inventory.push([id, warehouse, on_hand, Math.min(on_hand, int(0, 40)), id % 5 === 0 ? null : iso(NOW - int(1, 60) * DAY).slice(0, 10)]);
    }
  }

  const ACTIONS = ["order.refund", "order.cancel", "product.update", "customer.merge", "inventory.adjust", "login"];
  const audit: Cell[][] = [];
  for (let n = 0; n < 420; n++) {
    const action = ACTIONS[int(0, ACTIONS.length - 1)];
    audit.push([iso(NOW - n * 3_600_000 * 1.3), n % 6 === 0 ? "system" : `${pick(FIRST).toLowerCase()}@acme.test`, action, action === "login" ? null : `${action.split(".")[0]}:${int(1, 900)}`, action === "login" ? null : JSON.stringify({ reason: pick(["requested", "duplicate", "stock count", "typo"]), by: "admin" })]);
  }

  const totals = customers
    .map((c) => {
      const mine = orders.filter((o) => o[1] === c[0]);
      return [c[0], c[2], mine.length, String(mine.reduce((sum, o) => sum + (o[3] as number), 0)), mine.length ? (mine[mine.length - 1][5] as string) : null] as Cell[];
    })
    .sort((a, b) => Number(b[3]) - Number(a[3]));

  const revenue: Cell[][] = [];
  for (let d = 0; d < 30; d++) {
    const day = iso(NOW - d * DAY).slice(0, 10);
    const mine = orders.filter((o) => (o[5] as string).startsWith(day));
    revenue.push([day, mine.length, `${mine.reduce((sum, o) => sum + (o[3] as number), 0)}.00`]);
  }

  const pk = (table: string, type = "integer"): Column => ({ name: "id", type, nullable: false, primary_key: true, default: `nextval('${table}_id_seq'::regclass)` });
  const col = (name: string, type: string, nullable = false, fallback?: string): Column => ({ name, type, nullable, ...(fallback ? { default: fallback } : {}) });
  const fk = (column: string, table: string, ref = "id") => ({ columns: [column], ref_schema: "public", ref_table: table, ref_columns: [ref] });

  const views = pageViews(seeded(1420));

  return [
    {
      schema: "public",
      name: "audit_log",
      kind: "table",
      columns: [col("at", "timestamp with time zone", false, "now()"), col("actor", "text"), col("action", "text"), col("subject", "text", true), col("detail", "jsonb", true)],
      primary_key: [],
      indexes: [{ name: "audit_log_at_idx", columns: ["at"], unique: false }],
      foreign_keys: [],
      rows: audit,
    },
    {
      schema: "public",
      name: "customers",
      kind: "table",
      columns: [
        pk("customers"),
        col("email", "text"),
        col("name", "text"),
        col("plan", "text", false, "'free'::text"),
        col("marketing_opt_in", "boolean", false, "false"),
        col("credit", "numeric(10,2)", false, "0"),
        col("notes", "text", true),
        col("tags", "text[]", false, "'{}'::text[]"),
        col("metadata", "jsonb", true),
        col("created_at", "timestamp with time zone", false, "now()"),
      ],
      primary_key: ["id"],
      indexes: [
        { name: "customers_pkey", columns: ["id"], unique: true },
        { name: "customers_email_key", columns: ["email"], unique: true },
      ],
      foreign_keys: [],
      rows: customers,
    },
    {
      schema: "public",
      name: "inventory",
      kind: "table",
      columns: [{ ...col("product_id", "integer"), primary_key: true }, { ...col("warehouse", "text"), primary_key: true }, col("on_hand", "integer", false, "0"), col("reserved", "integer", false, "0"), col("restocked_at", "date", true)],
      primary_key: ["product_id", "warehouse"],
      indexes: [{ name: "inventory_pkey", columns: ["product_id", "warehouse"], unique: true }],
      foreign_keys: [fk("product_id", "products")],
      rows: inventory,
    },
    {
      schema: "public",
      name: "order_items",
      kind: "table",
      columns: [pk("order_items", "bigint"), col("order_id", "bigint"), col("product_id", "integer"), col("quantity", "integer", false, "1"), col("unit_price_cents", "integer")],
      primary_key: ["id"],
      indexes: [
        { name: "order_items_pkey", columns: ["id"], unique: true },
        { name: "order_items_order_id_idx", columns: ["order_id"], unique: false },
      ],
      foreign_keys: [fk("order_id", "orders"), fk("product_id", "products")],
      rows: items,
    },
    {
      schema: "public",
      name: "orders",
      kind: "table",
      columns: [
        pk("orders", "bigint"),
        col("customer_id", "integer"),
        col("status", "text", false, "'pending'::text"),
        col("total_cents", "integer"),
        col("currency", "character(3)", false, "'USD'::bpchar"),
        col("placed_at", "timestamp with time zone", false, "now()"),
        col("settled_at", "timestamp with time zone", true),
        col("tracking", "uuid", true),
        col("note", "text", true),
      ],
      primary_key: ["id"],
      indexes: [
        { name: "orders_pkey", columns: ["id"], unique: true },
        { name: "orders_customer_id_placed_at_idx", columns: ["customer_id", "placed_at"], unique: false },
      ],
      foreign_keys: [fk("customer_id", "customers")],
      rows: orders,
    },
    {
      schema: "public",
      name: "products",
      kind: "table",
      columns: [
        pk("products"),
        col("sku", "text"),
        col("name", "text"),
        col("description", "text", true),
        col("price_cents", "integer"),
        col("weight_kg", "numeric(6,3)", true),
        col("active", "boolean", false, "true"),
        col("thumbnail", "bytea", true),
        col("attributes", "jsonb", false, "'{}'::jsonb"),
        col("created_at", "timestamp with time zone", false, "now()"),
      ],
      primary_key: ["id"],
      indexes: [
        { name: "products_pkey", columns: ["id"], unique: true },
        { name: "products_sku_key", columns: ["sku"], unique: true },
      ],
      foreign_keys: [],
      rows: products,
    },
    {
      schema: "public",
      name: "customer_totals",
      kind: "view",
      columns: [col("customer_id", "integer", true), col("name", "text", true), col("orders", "bigint", true), col("total_cents", "numeric", true), col("last_order_at", "timestamp with time zone", true)],
      primary_key: [],
      indexes: [],
      foreign_keys: [],
      rows: totals,
    },
    {
      schema: "public",
      name: "daily_revenue",
      kind: "materialized_view",
      columns: [col("day", "date", true), col("orders", "bigint", true), col("revenue_cents", "numeric", true)],
      primary_key: [],
      indexes: [{ name: "daily_revenue_day_idx", columns: ["day"], unique: true }],
      foreign_keys: [],
      rows: revenue,
    },
    views,
  ];
}

// analytics.page_views: 41 columns and 1,500 rows, for a grid that has to
// stay quick when a table is wide.
function pageViews(r: () => number): MockTable {
  const int = (lo: number, hi: number) => lo + Math.floor(r() * (hi - lo + 1));
  const pick = <T,>(list: T[]) => list[Math.floor(r() * list.length)];
  const hex = (n: number) => Array.from({ length: n }, () => Math.floor(r() * 16).toString(16)).join("");
  const text = (name: string, values: (string | null)[]) => ({ name, type: "text", make: () => pick(values) as Cell });
  const num = (name: string, lo: number, hi: number, type = "integer") => ({ name, type, make: () => int(lo, hi) as Cell });
  const spec: { name: string; type: string; make(n: number): Cell }[] = [
    { name: "id", type: "bigint", make: (n) => String(9007199254740993n + BigInt(n)) },
    { name: "session", type: "uuid", make: () => `${hex(8)}-${hex(4)}-4${hex(3)}-a${hex(3)}-${hex(12)}` },
    { name: "customer_id", type: "integer", make: () => (r() < 0.4 ? null : int(1, 240)) },
    text("path", ["/", "/products", "/products/harbor-kettle", "/cart", "/checkout", "/orders", "/search?q=lamp"]),
    text("referrer", [null, null, "https://search.example/", "https://news.example/acme", ""]),
    text("utm_source", [null, null, "newsletter", "ad"]),
    text("utm_medium", [null, null, "email", "cpc"]),
    text("utm_campaign", [null, null, "autumn-sale", "launch"]),
    text("utm_term", [null]),
    text("utm_content", [null, "hero", "footer"]),
    text("device", ["desktop", "mobile", "tablet"]),
    text("os", ["macOS", "Windows", "iOS", "Android", "Linux"]),
    text("os_version", ["15.6", "11", "18.1", "15", "6.11"]),
    text("browser", ["Safari", "Chrome", "Firefox", "Edge"]),
    text("browser_version", ["18.0", "129.0", "131.0"]),
    num("screen_w", 360, 3840),
    num("screen_h", 640, 2160),
    num("viewport_w", 320, 2560),
    num("viewport_h", 480, 1400),
    text("locale", ["en-GB", "en-US", "de-DE", "sv-SE", "fr-FR"]),
    text("timezone", ["Europe/London", "America/New_York", "Europe/Berlin", "Europe/Stockholm"]),
    text("country", ["GB", "US", "DE", "SE", "FR"]),
    text("region", [null, "England", "New York", "Bavaria"]),
    text("city", [null, "London", "Brooklyn", "Munich", "Malmö"]),
    { name: "lat", type: "numeric(8,5)", make: () => (r() < 0.2 ? null : (int(-9000000, 9000000) / 100000).toFixed(5)) },
    { name: "lon", type: "numeric(8,5)", make: () => (r() < 0.2 ? null : (int(-18000000, 18000000) / 100000).toFixed(5)) },
    { name: "is_bot", type: "boolean", make: () => r() < 0.05 },
    { name: "is_returning", type: "boolean", make: () => r() < 0.4 },
    num("load_ms", 180, 4200),
    num("ttfb_ms", 20, 900),
    num("fcp_ms", 200, 2600),
    num("lcp_ms", 400, 5200),
    { name: "cls", type: "double precision", make: () => Math.round(r() * 300) / 1000 },
    num("inp_ms", 16, 600),
    num("scroll_pct", 0, 100, "smallint"),
    num("clicks", 0, 24, "smallint"),
    num("duration_ms", 400, 600_000),
    { name: "exit", type: "boolean", make: () => r() < 0.3 },
    text("experiment", [null, null, "checkout-copy", "pdp-gallery"]),
    text("variant", [null, null, "a", "b"]),
    { name: "created_at", type: "timestamp with time zone", make: (n) => iso(NOW - (1500 - n) * 41_000) },
  ];
  return {
    schema: "analytics",
    name: "page_views",
    kind: "table",
    columns: spec.map((s, i) => ({ name: s.name, type: s.type, nullable: i > 0 && s.name !== "created_at", ...(i === 0 ? { primary_key: true as const } : {}) })),
    primary_key: ["id"],
    indexes: [
      { name: "page_views_pkey", columns: ["id"], unique: true },
      { name: "page_views_created_at_idx", columns: ["created_at"], unique: false },
    ],
    foreign_keys: [{ columns: ["customer_id"], ref_schema: "public", ref_table: "customers", ref_columns: ["id"] }],
    rows: Array.from({ length: 1500 }, (_, n) => spec.map((s) => s.make(n))),
  };
}

// "events", on an engine nobody has heard of.
function driftTables(): MockTable[] {
  const r = seeded(4320);
  const int = (lo: number, hi: number) => lo + Math.floor(r() * (hi - lo + 1));
  const TOPICS = ["order.placed", "order.shipped", "payment.settled", "payment.failed", "stock.low"];
  const deliveries: Cell[][] = Array.from({ length: 64 }, (_, n) => {
    const ok = n % 7 !== 0;
    return [`dlv_${String(3000 + n)}`, TOPICS[n % TOPICS.length], ok ? 200 : 503, ok, n % 7 === 0 ? int(1, 5) : 0, iso(NOW - n * 97_000), ok ? null : "upstream timed out"];
  });
  const events: Cell[][] = Array.from({ length: 180 }, (_, n) => [88_000 + n, TOPICS[(n * 3) % TOPICS.length], JSON.stringify({ order: 5001 + n, attempt: 1 + (n % 3) }), iso(NOW - n * 31_000)]);
  const c = (name: string, type: string, nullable = false): Column => ({ name, type, nullable });
  return [
    { schema: "", name: "deliveries", kind: "table", columns: [{ ...c("id", "str"), primary_key: true }, c("topic", "str"), c("status", "i32"), c("ok", "bool"), c("retries", "i32"), c("sent", "ts"), c("error", "str", true)], primary_key: ["id"], indexes: [], foreign_keys: [], rows: deliveries },
    { schema: "", name: "webhook_events", kind: "stream", columns: [c("offset", "i64"), c("topic", "str"), c("payload", "doc"), c("at", "ts")], primary_key: [], indexes: [], foreign_keys: [], rows: events },
  ];
}

const POSTGRES = { dialect: "postgresql", schemas: true, operators: ["=", "!=", "<", "<=", ">", ">=", "like", "ilike", "is_null", "not_null"], kinds: ["table", "view", "materialized_view"], writes: true, transactions: true };
const DRIFT = { dialect: "driftql", schemas: false, operators: ["=", "!=", "like"], kinds: ["table", "stream"], writes: false, transactions: false };

let conns: MockConn[] | undefined;

function all(): MockConn[] {
  conns ??= [
    { id: "c_1c28ba20e1b6", source: "shop/checkout-fix", label: "DATABASE_URL", engine: "postgres", database: "shop_checkout_fix", host: "localhost", port: 5432, user: "acme", capabilities: POSTGRES, tables: shopTables() },
    { id: "c_7d90a3b1e44f", source: "shop/checkout-fix", label: "EVENTS_URL", engine: "driftdb", database: "events", host: "localhost", port: 8765, user: "", capabilities: DRIFT, tables: driftTables() },
    { id: "c_a02c77e9b513", source: "shop", label: "DATABASE_URL", engine: "postgres", database: "shop_dev", host: "localhost", port: 5432, user: "acme", capabilities: POSTGRES, tables: shopTables() },
  ];
  return conns;
}

const writing = (c: MockConn) => c.capabilities.writes && c.writesUntil !== undefined && c.writesUntil > Date.now();

// ---- Values ----

const base = (type: string) =>
  type
    .toLowerCase()
    .replace(/\s*\([^)]*\)/g, "")
    .trim();
const INTEGER = new Set(["integer", "bigint", "smallint", "i32", "i64"]);
const FLOAT = new Set(["double precision", "real"]);
const isInteger = (type: string) => INTEGER.has(base(type));
const isNumeric = (type: string) => isInteger(type) || FLOAT.has(base(type)) || base(type) === "numeric";
const isBool = (type: string) => base(type) === "boolean" || base(type) === "bool";
// An integer as the helper sends it: a number while a double holds it
// exactly, its digits as a string past 2^53.
const int = (digits: string): Cell => (Number.isSafeInteger(Number(digits)) ? Number(digits) : digits.replace(/^\+/, ""));

function compare(a: Cell, b: Cell, type: string): number {
  if (isInteger(type)) {
    const x = BigInt(String(a));
    const y = BigInt(String(b));
    return x < y ? -1 : x > y ? 1 : 0;
  }
  if (isNumeric(type)) return Number(a) - Number(b);
  if (typeof a === "boolean" || typeof b === "boolean") return Number(a) - Number(b);
  return String(a) < String(b) ? -1 : String(a) > String(b) ? 1 : 0;
}

const pattern = (like: string, flags: string) => new RegExp(`^${like.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/%/g, "[\\s\\S]*").replace(/_/g, "[\\s\\S]")}$`, flags);

function matches(cell: Cell, op: string, value: Cell, type: string): boolean {
  if (op === "is_null") return cell === null;
  if (op === "not_null") return cell !== null;
  // As in SQL: nothing compares with NULL.
  if (cell === null || value === null || value === undefined) return false;
  const v: Cell = isBool(type) && typeof value === "string" ? /^(t|true|1|yes|on)$/i.test(value) : value;
  switch (op) {
    case "=":
      return compare(cell, v, type) === 0;
    case "!=":
      return compare(cell, v, type) !== 0;
    case "<":
      return compare(cell, v, type) < 0;
    case "<=":
      return compare(cell, v, type) <= 0;
    case ">":
      return compare(cell, v, type) > 0;
    case ">=":
      return compare(cell, v, type) >= 0;
    case "like":
      return pattern(String(v), "").test(String(cell));
    case "ilike":
      return pattern(String(v), "i").test(String(cell));
    default:
      return false;
  }
}

// What the database says of a value a column cannot be, in the helper's
// words (Postgres's own, as a sentence).
const invalid = (type: string, text: string, lead = "") => refusal(400, "query_failed", `${lead}Invalid input syntax for type ${base(type)}: "${text}".`, { sqlstate: "22P02" });

// A value as it is stored for a column, from what was sent: a JSON scalar
// the database casts, or an object or an array for a json column. lead is
// "Change 2: " in an edit.
function cast(value: unknown, column: Column, table: MockTable, lead = ""): Cell {
  if (value === null || value === undefined) {
    if (!column.nullable) throw refusal(400, "query_failed", `${lead}Null value in column "${column.name}" of relation "${table.name}" violates not-null constraint.`, { sqlstate: "23502" });
    return null;
  }
  const type = base(column.type);
  if (type === "jsonb" || type === "json") {
    if (typeof value === "object") return JSON.stringify(value);
    try {
      JSON.parse(String(value));
    } catch {
      throw refusal(400, "query_failed", `${lead}Invalid input syntax for type json.`, { sqlstate: "22P02", detail: "The input string ended unexpectedly, or a token is not valid JSON." });
    }
    return String(value);
  }
  if (isNumeric(type)) {
    const text = String(value).trim();
    if (isInteger(type)) {
      if (!/^[+-]?\d+$/.test(text)) throw invalid(type, text, lead);
      return int(text);
    }
    if (!/^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/i.test(text) && !/^[+-]?(nan|infinity)$/i.test(text)) throw invalid(type, text, lead);
    return FLOAT.has(type) && Number.isFinite(Number(text)) ? Number(text) : text;
  }
  if (isBool(type)) {
    if (typeof value === "boolean") return value;
    if (/^(t|true|1|yes|on)$/i.test(String(value))) return true;
    if (/^(f|false|0|no|off)$/i.test(String(value))) return false;
    throw invalid(type, String(value), lead);
  }
  if (type === "uuid" && !/^[0-9a-f-]{32,36}$/i.test(String(value))) throw invalid(type, String(value), lead);
  return String(value);
}

// What a column takes when a new row leaves it out.
function fallback(column: Column, table: MockTable, lead: string): Cell {
  if (column.default?.startsWith("nextval")) {
    const at = table.columns.indexOf(column);
    return table.rows.reduce((max, row) => Math.max(max, Number(row[at]) || 0), 0) + 1;
  }
  if (column.default === "now()") return new Date().toISOString();
  if (column.default !== undefined) {
    const d = column.default.replace(/::[\w\s[\]]+$/, "");
    if (d === "true" || d === "false") return d === "true";
    if (/^-?\d+$/.test(d)) return isInteger(column.type) ? Number(d) : d;
    return d.replace(/^'|'$/g, "");
  }
  return cast(null, column, table, lead);
}

// Rows as they are sent: a cell longer than 4,000 characters is cut, and
// "cut" says which. It is always there, empty when nothing was.
function sent(rows: Cell[][]): { rows: Cell[][]; cut: [number, number][] } {
  const cut: [number, number][] = [];
  const out = rows.map((row, r) =>
    row.map((cell, c) => {
      if (typeof cell !== "string" || cell.length <= CUT_AT) return cell;
      cut.push([r, c]);
      return cell.slice(0, CUT_AT);
    }),
  );
  return { rows: out, cut };
}

// ---- Routes ----

const quoted = (table: MockTable) => (table.schema ? `"${table.schema}"."${table.name}"` : `"${table.name}"`);

function tableOf(conn: MockConn, schema: string, name: string): MockTable {
  const t = conn.tables.find((x) => x.name === name && (x.schema === schema || (!conn.capabilities.schemas && !schema)));
  if (!t) throw refusal(404, "not_found", `There is no table ${schema ? `"${schema}".` : ""}"${name}" in this database.`);
  return t;
}

const at = (table: MockTable, column: string, what: string): number => {
  const i = table.columns.findIndex((c) => c.name === column);
  if (i < 0) throw refusal(400, "bad_request", `There is no column "${column}" ${what}.`);
  return i;
};

function schemaOf(conn: MockConn) {
  const names = [...new Set(conn.tables.map((t) => t.schema))].sort((a, b) => (a === "public" ? -1 : b === "public" ? 1 : a.localeCompare(b)));
  return {
    schemas: names.map((name) => ({
      name,
      tables: conn.tables
        .filter((t) => t.schema === name)
        .sort((a, b) => a.name.localeCompare(b.name))
        .map(({ rows, schema: _, ...t }) => ({ ...t, rows_estimate: t.kind === "view" ? 0 : rows.length })),
    })),
  };
}

interface RowsBody {
  schema?: string;
  table?: string;
  limit?: number;
  offset?: number;
  order_by?: { column: string; desc?: boolean }[];
  filters?: { column: string; op: string; value?: Cell }[];
}

// The helper stops a page at about 8 MB, short of its limit, and says more
// follow. Here a table as wide as page_views stops at this many rows, so the
// panel is seen to ask for the next page from where this one ended.
const WIDE = 30;
const SHORT_PAGE = 420;

function rowsOf(conn: MockConn, body: RowsBody) {
  const table = tableOf(conn, body.schema ?? "", body.table ?? "");
  const limit = Math.max(1, Math.min(500, body.limit ?? 100));
  const offset = Math.max(0, body.offset ?? 0);
  let rows = table.rows;
  for (const f of body.filters ?? []) {
    if (!conn.capabilities.operators.includes(f.op)) throw refusal(400, "bad_request", `"${f.op}" is not a filter this database has.`, { hint: `It has ${conn.capabilities.operators.join(", ")}.` });
    const i = at(table, f.column, "to filter on");
    const type = table.columns[i].type;
    const takes = f.op !== "is_null" && f.op !== "not_null";
    if (takes && (f.value === null || f.value === undefined)) throw refusal(400, "bad_request", `The filter on "${f.column}" needs a value.`, { hint: "To find rows without one, use is_null." });
    // A value the column cannot be is the database's to refuse.
    if (takes && f.op !== "like" && f.op !== "ilike" && isNumeric(type) && !/^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/i.test(String(f.value).trim())) throw invalid(type, String(f.value));
    rows = rows.filter((row) => matches(row[i], f.op, f.value ?? null, type));
  }
  const order = (body.order_by ?? []).map((o) => ({ i: at(table, o.column, "to sort by"), desc: !!o.desc }));
  if (order.length) {
    rows = [...rows].sort((a, b) => {
      for (const o of order) {
        // NULLs last ascending, first descending, as Postgres orders them.
        const x = a[o.i];
        const y = b[o.i];
        const c = x === null ? (y === null ? 0 : 1) : y === null ? -1 : compare(x, y, table.columns[o.i].type);
        if (c !== 0) return o.desc ? -c : c;
      }
      return 0;
    });
  }
  const most = table.columns.length > WIDE ? Math.min(limit, SHORT_PAGE) : limit;
  const page = rows.slice(offset, offset + most);
  return { columns: table.columns.map((c) => ({ name: c.name, type: c.type })), ...sent(page), primary_key: table.primary_key, rows_estimate: table.kind === "view" ? 0 : table.rows.length, more: offset + page.length < rows.length };
}

type Change = { op: "update"; key: Record<string, Cell>; set: Record<string, unknown> } | { op: "insert"; set: Record<string, unknown> } | { op: "delete"; key: Record<string, Cell> };

const OFF = { message: "Writes are off for this connection.", hint: "Turn writes on; they stay on for 15 minutes." };
const noWrites = (conn: MockConn) => refusal(400, "bad_request", `A ${conn.engine} database cannot be written to from the editor.`);

function edit(conn: MockConn, body: { schema?: string; table?: string; changes?: Change[] }) {
  if (!conn.capabilities.writes) throw noWrites(conn);
  if (!writing(conn)) throw refusal(403, "read_only", OFF.message, { hint: OFF.hint });
  const table = tableOf(conn, body.schema ?? "", body.table ?? "");
  const changes = body.changes ?? [];
  if (changes.length < 1 || changes.length > 1000) throw refusal(400, "bad_request", "An edit is 1 to 1,000 changes.");
  // All or nothing: the changes are made to a copy, which takes the
  // table's place only when every one of them went through.
  const work: MockTable = { ...table, rows: table.rows.map((r) => [...r]) };
  const out = { updated: 0, inserted: 0, deleted: 0 };
  const pk = table.primary_key;
  const find = (key: Record<string, Cell>, n: number) => {
    // A view has no key either: the same refusal.
    if (pk.length === 0 || table.kind !== "table") throw refusal(400, "no_primary_key", `${quoted(table)} has no primary key, so the editor cannot tell one of its rows from another.`, { hint: "Rows can be added to it. To change or remove one, run a statement." });
    if (Object.keys(key ?? {}).length !== pk.length || pk.some((c) => key[c] === null || key[c] === undefined)) throw refusal(400, "bad_request", `Change ${n}: key must be the whole primary key (${pk.join(", ")}), each with a value.`);
    const cols = pk.map((c) => at(table, c, "in the key"));
    const i = work.rows.findIndex((row) => cols.every((c, k) => String(row[c]) === String(key[pk[k]])));
    if (i < 0) throw refusal(409, "row_conflict", `Change ${n}: the row is not there any more.`, { hint: "Nothing was changed. Load the rows again and repeat the edit." });
    return i;
  };
  const unique = (n: number) => {
    if (pk.length === 0) return;
    const cols = pk.map((c) => at(table, c, "in the key"));
    const seen = new Set<string>();
    for (const row of work.rows) {
      const id = JSON.stringify(cols.map((c) => String(row[c])));
      if (seen.has(id)) throw refusal(400, "query_failed", `Change ${n}: Duplicate key value violates unique constraint "${table.name}_pkey".`, { sqlstate: "23505", detail: `Key (${pk.join(", ")})=(${cols.map((c) => row[c]).join(", ")}) already exists.` });
      seen.add(id);
    }
  };
  changes.forEach((change, index) => {
    const n = index + 1;
    const lead = `Change ${n}: `;
    if (change.op === "delete") {
      work.rows.splice(find(change.key, n), 1);
      out.deleted++;
    } else if (change.op === "update") {
      const i = find(change.key, n);
      for (const [name, value] of Object.entries(change.set)) work.rows[i][at(table, name, "to set")] = cast(value, table.columns[at(table, name, "to set")], table, lead);
      out.updated++;
    } else {
      if (table.kind !== "table") throw refusal(400, "query_failed", `${lead}Cannot insert into ${table.kind.replaceAll("_", " ")} "${table.name}".`, { sqlstate: "55000" });
      for (const name of Object.keys(change.set)) at(table, name, "to set");
      work.rows.push(table.columns.map((c) => (c.name in change.set ? cast(change.set[c.name], c, work, lead) : fallback(c, work, lead))));
      out.inserted++;
    }
    unique(n);
  });
  table.rows = work.rows;
  return out;
}

// ---- The console ----

// The real helper hands a statement to Postgres. This mock runs a few
// shapes of statement itself and says so of the rest, in the helper's
// envelope: the code is the helper's, the words are the mock's own.
const UNDERSTOOD = "The mock runs SELECT from one table with WHERE (conditions joined by AND), ORDER BY, LIMIT and OFFSET; count(*); EXPLAIN; and UPDATE … SET … WHERE and DELETE FROM … WHERE.";
const WRITERS = new Set(["INSERT", "UPDATE", "DELETE", "CREATE", "ALTER", "DROP", "TRUNCATE", "GRANT", "REVOKE", "COMMENT", "REINDEX"]);
const STARTS = new Set([...WRITERS, "SELECT", "EXPLAIN", "SHOW", "WITH", "VALUES", "TABLE", "DO", "BEGIN", "COMMIT", "ROLLBACK", "SET", "VACUUM", "COPY"]);

// The database refused the statement: its words as a sentence, its
// sqlstate, and where it stopped (a character of the statement, from 1).
const failed = (message: string, sqlstate: string, at?: number, hint?: string) => refusal(400, "query_failed", message, { sqlstate, ...(at !== undefined ? { position: at + 1 } : {}), ...(hint ? { hint } : {}) });
const notRun = () => refusal(400, "query_failed", "The mock database does not run this statement.", { hint: UNDERSTOOD });
const bare = (name: string) => name.replace(/^"|"$/g, "");

// The table a statement names, wherever in the text its name is.
function named(conn: MockConn, sql: string, name: string): MockTable {
  const [a, b] = name.split(".").map(bare);
  const table = conn.tables.find((t) => (b === undefined ? t.name === a && (t.schema === "public" || t.schema === "") : t.schema === a && t.name === b));
  if (!table) throw failed(`Relation "${name}" does not exist.`, "42P01", sql.indexOf(name));
  return table;
}

// A column a statement names: when it is not there, Postgres suggests the
// one it looks like, and so does this.
function columnOf(table: MockTable, name: string, sql: string, from = 0): number {
  const i = table.columns.findIndex((c) => c.name === name);
  if (i >= 0) return i;
  const letters = (s: string) => [...s].sort().join("");
  const near = table.columns.find((c) => letters(c.name) === letters(name) || (Math.abs(c.name.length - name.length) <= 1 && (c.name.startsWith(name.slice(0, 3)) || c.name.endsWith(name.slice(-3)))));
  throw failed(`Column "${name}" does not exist.`, "42703", sql.indexOf(name, from), near ? `Perhaps you meant to reference the column "${table.name}.${near.name}".` : undefined);
}

interface Condition {
  i: number;
  op: string;
  value: Cell;
}

// WHERE a = 1 AND b LIKE 'x%' AND c IS NULL: nothing cleverer.
function conditions(sql: string, where: string, table: MockTable): Condition[] {
  const out: Condition[] = [];
  const offset = sql.indexOf(where);
  const part = /\s*("?[A-Za-z_]\w*"?)\s*(?:(is\s+not\s+null|is\s+null)|(=|!=|<>|<=|>=|<|>|\bilike\b|\blike\b)\s*('(?:[^']|'')*'|-?\d+(?:\.\d+)?|true|false))\s*(?:\band\b|$)/iy;
  let m: RegExpExecArray | null;
  let last = 0;
  while (last < where.length && (m = part.exec(where))) {
    const i = columnOf(table, bare(m[1]), sql, offset + m.index);
    if (m[2]) out.push({ i, op: /not/i.test(m[2]) ? "not_null" : "is_null", value: null });
    else {
      const raw = m[4];
      const value: Cell = raw.startsWith("'") ? raw.slice(1, -1).replaceAll("''", "'") : /^(true|false)$/i.test(raw) ? raw.toLowerCase() === "true" : Number(raw);
      const op = m[3].toLowerCase();
      out.push({ i, op: op === "<>" ? "!=" : op, value });
    }
    last = part.lastIndex;
  }
  if (last < where.length) {
    const rest = where.slice(last);
    throw failed(`Syntax error at or near "${rest.trim().split(/\s+/)[0]}".`, "42601", offset + last + (rest.length - rest.trimStart().length));
  }
  return out;
}

const holds = (row: Cell[], table: MockTable, where: Condition[]) => where.every((c) => matches(row[c.i], c.op, c.value, table.columns[c.i].type));

function query(conn: MockConn, body: { sql?: string; limit?: number }) {
  const limit = Math.max(1, Math.min(5000, body.limit ?? 1000));
  const plain = (body.sql ?? "").replace(/--[^\n]*/g, "").replace(/\/\*[\s\S]*?\*\//g, "");
  const sql = (body.sql ?? "").trim().replace(/;\s*$/, "");
  if (!plain.replace(/;/g, "").trim()) throw refusal(400, "bad_request", "sql is required.");
  const several = sql
    .replace(/'(?:[^']|'')*'/g, "''")
    .replace(/\$\$[\s\S]*?\$\$/g, "")
    .split(";")
    .filter((s) => s.trim()).length;
  if (several > 1) throw refusal(400, "multiple_statements", `That is ${several} statements; the editor runs one at a time.`, { hint: "Run them one by one." });
  const first = /^[A-Za-z]+/.exec(sql)?.[0] ?? "";
  const word = first.toUpperCase();
  if (!STARTS.has(word)) throw failed(`Syntax error at or near "${first || sql[0]}".`, "42601", 0);
  // Postgres refuses a write in a read-only transaction; the helper passes that on.
  if (WRITERS.has(word) && !writing(conn)) throw refusal(403, "read_only", OFF.message, { hint: OFF.hint, sqlstate: "25006", detail: `cannot execute ${word === "CREATE" || word === "ALTER" || word === "DROP" ? sql.split(/\s+/).slice(0, 2).join(" ").toUpperCase() : word} in a read-only transaction` });
  // rows is every row the statement has; what is sent stops at the limit,
  // and row_count is then only the rows that are here.
  const answer = (command: string, columns: { name: string; type: string }[], rows: Cell[][], changed = 0, notices: string[] = []) => {
    const here = rows.slice(0, limit);
    return { columns, ...sent(here), row_count: columns.length ? here.length : changed, command, duration_ms: Math.round((0.4 + rows.length * 0.004) * 1000) / 1000, truncated: rows.length > limit, notices };
  };

  if (word === "VACUUM") throw failed("VACUUM cannot run inside a transaction block.", "25001");
  if (word === "COPY" && /\b(stdin|stdout)\b/i.test(sql)) throw refusal(400, "bad_request", "COPY to or from the editor is not something this API can carry.");
  if (/^select\s+pg_sleep\(/i.test(sql)) throw refusal(400, "timeout", "The statement ran longer than 10s and was stopped.", { hint: "Narrow it, or add a LIMIT.", sqlstate: "57014" });
  if (word === "DO") {
    const said = /raise\s+notice\s+'((?:[^']|'')*)'/i.exec(sql);
    return answer("DO", [], [], 0, said ? [`NOTICE: ${said[1].replaceAll("''", "'")}`] : []);
  }
  if (/^select\s+1$/i.test(sql)) return answer("SELECT", [{ name: "?column?", type: "integer" }], [[1]]);
  if (/^select\s+now\(\)$/i.test(sql)) return answer("SELECT", [{ name: "now", type: "timestamp with time zone" }], [[new Date().toISOString()]]);
  if (/^select\s+version\(\)$/i.test(sql)) return answer("SELECT", [{ name: "version", type: "text" }], [["PostgreSQL 17.2 on x86_64-pc-linux-gnu, compiled by gcc, 64-bit"]]);
  if (/^select\s+current_database\(\)$/i.test(sql)) return answer("SELECT", [{ name: "current_database", type: "name" }], [[conn.database]]);

  const explain = /^explain\s+(?:\([^)]*\)\s*|analyze\s+)?([\s\S]+)$/i.exec(sql);
  if (explain) {
    const inner = explain[1];
    const from = /\b(?:from|update|into)\s+((?:"?\w+"?\.)?"?\w+"?)/i.exec(inner);
    if (!from) throw notRun();
    const table = named(conn, sql, from[1]);
    const where = /\bwhere\s+([\s\S]+?)(?:\s+order\s+by|\s+limit|$)/i.exec(inner)?.[1];
    const key = where && table.primary_key.length === 1 && new RegExp(`^"?${table.primary_key[0]}"?\\s*=`, "i").test(where.trim());
    const plan = key
      ? [`Index Scan using ${table.name}_pkey on ${table.name}  (cost=0.28..8.29 rows=1 width=${table.columns.length * 12})`, `  Index Cond: (${where!.trim()})`]
      : [`Seq Scan on ${table.name}  (cost=0.00..${(table.rows.length * 0.0125 + 1).toFixed(2)} rows=${where ? Math.max(1, Math.round(table.rows.length / 20)) : table.rows.length} width=${table.columns.length * 12})`, ...(where ? [`  Filter: (${where.trim()})`] : [])];
    return answer(
      "EXPLAIN",
      [{ name: "QUERY PLAN", type: "text" }],
      plan.map((line) => [line]),
    );
  }

  const select = /^select\s+([\s\S]+?)\s+from\s+((?:"?\w+"?\.)?"?\w+"?)(?:\s+where\s+([\s\S]+?))?(?:\s+order\s+by\s+("?\w+"?)(?:\s+(asc|desc))?)?(?:\s+limit\s+(\d+))?(?:\s+offset\s+(\d+))?$/i.exec(sql);
  if (select) {
    const table = named(conn, sql, select[2]);
    const where = select[3] ? conditions(sql, select[3], table) : [];
    let rows = table.rows.filter((row) => holds(row, table, where));
    // A count is a bigint, which is a number while it fits in one.
    if (/^count\(\s*\*\s*\)$/i.test(select[1].trim())) return answer("SELECT", [{ name: "count", type: "bigint" }], [[rows.length]]);
    const wanted =
      select[1].trim() === "*"
        ? table.columns.map((_, i) => i)
        : select[1].split(",").map((raw) => {
            if (!/^"?\w+"?$/.test(raw.trim())) throw notRun();
            return columnOf(table, bare(raw.trim()), sql);
          });
    if (select[4]) {
      const i = columnOf(table, bare(select[4]), sql, sql.toLowerCase().lastIndexOf("order by"));
      const desc = /desc/i.test(select[5] ?? "");
      rows = [...rows].sort((a, b) => {
        const c = a[i] === null ? (b[i] === null ? 0 : 1) : b[i] === null ? -1 : compare(a[i], b[i], table.columns[i].type);
        return desc ? -c : c;
      });
    }
    const offset = select[7] ? Number(select[7]) : 0;
    rows = rows.slice(offset, select[6] ? offset + Number(select[6]) : undefined);
    return answer(
      "SELECT",
      wanted.map((i) => ({ name: table.columns[i].name, type: table.columns[i].type })),
      rows.map((row) => wanted.map((i) => row[i])),
    );
  }
  if (word === "SELECT") {
    // SELECT id FORM customers: Postgres reads FORM as the column's new
    // name, and stops at the word after it.
    const stray = /^(select\s+"?\w+"?\s+\w+\s+)(\w+)/i.exec(sql);
    if (stray && !/\bfrom\b/i.test(sql)) throw failed(`Syntax error at or near "${stray[2]}".`, "42601", stray[1].length);
    throw notRun();
  }

  const remove = /^delete\s+from\s+((?:"?\w+"?\.)?"?\w+"?)\s+where\s+([\s\S]+)$/i.exec(sql);
  if (remove) {
    const table = named(conn, sql, remove[1]);
    const where = conditions(sql, remove[2], table);
    const before = table.rows.length;
    table.rows = table.rows.filter((row) => !holds(row, table, where));
    return answer("DELETE", [], [], before - table.rows.length);
  }
  const update = /^update\s+((?:"?\w+"?\.)?"?\w+"?)\s+set\s+("?\w+"?)\s*=\s*('(?:[^']|'')*'|-?\d+(?:\.\d+)?|true|false|null)\s+where\s+([\s\S]+)$/i.exec(sql);
  if (update) {
    const table = named(conn, sql, update[1]);
    const i = columnOf(table, bare(update[2]), sql);
    const raw = update[3];
    const value = cast(/^null$/i.test(raw) ? null : raw.startsWith("'") ? raw.slice(1, -1).replaceAll("''", "'") : /^(true|false)$/i.test(raw) ? raw.toLowerCase() === "true" : raw, table.columns[i], table);
    const where = conditions(sql, update[4], table);
    let n = 0;
    for (const row of table.rows) {
      if (!holds(row, table, where)) continue;
      row[i] = value;
      n++;
    }
    return answer("UPDATE", [], [], n);
  }
  throw notRun();
}

// databaseRestarted is the helper started again: the databases it knew are
// kept (they are in a file of its own), and writes are off for all of them
// (that is only in its memory).
export function databaseRestarted() {
  for (const c of [...(conns ?? []), ...byHand.values()]) c.writesUntil = undefined;
}

// ---- A worktree's .env files ----

// What the worktrees' .env files hold: nothing (they are not there), or one
// of the ?db=env scenarios.
let dotenv: string | null = scenario?.startsWith("env") ? scenario : null;
// The worktrees whose database the helper has from a look.
const fromEnv = new Set<string>();
let looks = 0;

// How much longer than any other answer a look takes (?db=env-slow).
export const ENV_LOOK_MS = scenario === "env-slow" ? 1500 : 0;

if (typeof window !== "undefined") {
  (window as unknown as { __berthMockDatabase: unknown }).__berthMockDatabase = {
    dotenv(kind: string | null) {
      dotenv = kind;
    },
    looks: () => looks,
  };
}

// The databases that were connected by hand, by box and worktree: what the
// person pasted the address of, and the helper took.
const byHand = new Map<string, MockConn>();
const handKey = (box: string, source: string) => `${box}\n${source}`;

// known is the databases a box's helper has been told of. In the ?db=env
// scenarios no service said anything: it has what a look found. What was
// connected by hand is there whatever else is.
function known(empty: boolean, box: string): MockConn[] {
  const told = empty || scenario === "none" ? [] : dotenv ? all().filter((c) => fromEnv.has(c.source) && c.engine === "postgres") : all();
  const hand = [...byHand].filter(([key]) => key.startsWith(`${box}\n`)).map(([, c]) => c);
  return [...told, ...hand];
}

// databasePurged is a box's helper with its data removed: what it kept of
// the databases connected by hand is gone.
export function databasePurged(box: string) {
  for (const key of [...byHand.keys()]) if (key.startsWith(`${box}\n`)) byHand.delete(key);
}

// A connection's id as the helper makes one: "c_" and twelve hex digits,
// the same for the same database of the same worktree.
function idOf(parts: string[]): string {
  let a = 0x811c9dc5;
  let b = 0x01000193;
  for (const ch of parts.join("\n")) {
    a = Math.imul(a ^ ch.charCodeAt(0), 0x01000193) >>> 0;
    b = Math.imul(b + ch.charCodeAt(0), 0x85ebca6b) >>> 0;
  }
  return `c_${a.toString(16).padStart(8, "0")}${b.toString(16).padStart(8, "0").slice(0, 4)}`;
}

// The database an address names, when it is one the helper opens: a
// Postgres on the box itself. Undefined for anything else, as the helper
// passes it over.
function local(address: string): { host: string; port: number; database: string; user: string } | undefined {
  const m = /^(?:jdbc:)?postgres(?:ql)?(?:\+[a-z0-9]+)?:(\/\/.*)$/i.exec(address.trim());
  if (!m) return undefined;
  let url: URL;
  try {
    url = new URL(`http:${m[1]}`);
  } catch {
    return undefined;
  }
  const socket = url.searchParams.get("host") ?? "";
  const host = url.hostname.replace(/^\[|\]$/g, "");
  const here = host === "localhost" || host.endsWith(".localhost") || /^127\./.test(host) || host === "::1" || host === "0.0.0.0" || socket.startsWith("/");
  if (!here) return undefined;
  const user = decodeURIComponent(url.username);
  return { host: socket.startsWith("/") ? socket : host, port: socket.startsWith("/") ? 0 : Number(url.port) || 5432, database: decodeURIComponent(url.pathname.slice(1)) || user, user };
}

// databaseConnect is the box telling the helper of a database the person
// gave the address of (helpers/database/attach-env with "env"), under
// ".manual": what was given before goes, and what is given now is taken if
// it is a Postgres on the box itself. With nothing given it is only taken
// back. The answer has the variable's name and the connection's id, never
// the address.
export function databaseConnect(box: string, location: string, worktree: string, env: Record<string, unknown>) {
  const main = !worktree || worktree === location;
  const source = main ? location : `${location}/${worktree}`;
  // As berthd refuses them, before the helper hears of anything.
  for (const [name, value] of Object.entries(env)) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) throw new ApiError("env has a name that is not a variable's", 400, "bad_request");
    if (name !== "DATABASE_URL" && name !== "POSTGRES_URL") throw new ApiError(`${name} is not a variable the database helper is told: its plugin's manifest does not name it`, 400, "bad_request");
    if (typeof value !== "string" || !value) throw new ApiError(`${name} must be text of 1 to 32768 bytes`, 400, "bad_request");
    if (/^(op|env):\/\//.test(value)) throw new ApiError(`${name} names a secret; give the value itself`, 400, "bad_request");
  }
  byHand.delete(handKey(box, source));
  const names = Object.keys(env).sort();
  const connections: string[] = [];
  for (const name of names) {
    const at = local(String(env[name]));
    if (!at) continue;
    const id = idOf([source, at.host, String(at.port), at.database, at.user]);
    byHand.set(handKey(box, source), { id, source, label: name, engine: "postgres", database: at.database, host: at.host, port: at.port, user: at.user, capabilities: POSTGRES, tables: shopTables() });
    connections.push(id);
    break;
  }
  return { plugin: "database", location, worktree: worktree || location, command: ".manual", files: [], skipped: [], variables: names.map((name) => ({ name, file: "", sent: true })), answer: { source, connections } };
}

// databaseEnvLook is the box looking in a worktree's .env files and telling
// the helper what it found, as internal/box/helperenv.go answers: the files
// it read, the variables' names, and the helper's own answer. Never a value.
export function databaseEnvLook(location: string, worktree: string, empty: boolean) {
  const main = !worktree || worktree === location;
  const source = main ? location : `${location}/${worktree}`;
  const base = { plugin: "database", location, worktree: worktree || location, command: ".env", files: [] as string[], skipped: [] as { file: string; reason: string }[], variables: [] as { name: string; file: string; sent: boolean; reason?: string }[], answer: { source, connections: [] as string[] } };
  const has = empty ? undefined : all().find((c) => c.source === source && c.engine === "postgres");
  looks++;
  // Told again with what the files hold now: what the last look found goes.
  fromEnv.delete(source);
  if (!has || !dotenv) return base;
  const files = [".env", ".env.local"];
  if (dotenv === "env" || dotenv === "env-slow") {
    fromEnv.add(source);
    return { ...base, files, variables: [{ name: "DATABASE_URL", file: ".env.local", sent: true }], answer: { source, connections: [has.id] } };
  }
  if (dotenv === "env-refused") {
    return {
      ...base,
      files,
      skipped: [{ file: ".env.development", reason: "outside" }],
      variables: [
        // Sent, and not taken: it points at another host.
        { name: "DATABASE_URL", file: ".env.local", sent: true },
        { name: "POSTGRES_URL", file: ".env", sent: false, reason: "secret" },
      ],
    };
  }
  return { ...base, files };
}

// databaseCall answers the helper's /api/db/… routes. empty is a helper
// that knows no worktree's database but the ones connected by hand on that
// box: one just installed, or one whose data was removed.
export function databaseCall(method: string, route: string, q: URLSearchParams, body: unknown, empty: boolean, box = "devl"): unknown {
  // The box's berthd is from before it sent a helper its token: the helper
  // turns every request away.
  if (scenario === "unauthorized") throw refusal(401, "unauthorized", "This route needs the helper's token.", { hint: "The box daemon sends it with what it relays: ask through the box, not the helper." });
  if (method === "GET" && route === "db/connections") {
    const source = q.get("source") ?? "";
    const mine = known(empty, box).filter((c) => !source || c.source === source);
    return { connections: mine.map((c) => ({ id: c.id, source: c.source, label: c.label, engine: c.engine, database: c.database, host: c.host, port: c.port, user: c.user, ...(writing(c) ? { writes_until: c.writesUntil } : {}), capabilities: c.capabilities })) };
  }
  const m = /^db\/([^/]+)\/(schema|rows|query|edit|writes)$/.exec(route);
  const conn = m ? known(empty, box).find((c) => c.id === decodeURIComponent(m[1])) : undefined;
  if (!m) throw refusal(404, "not_found", "There is no such route.", { hint: "internal/database/API.md lists them." });
  if (!conn) throw refusal(404, "not_found", "There is no such connection.", { hint: "GET /api/db/connections lists them; one goes when its worktree is removed." });
  const what = m[2];
  if (what === "writes" && method === "POST") {
    // The switch is the helper's own: it opens no connection to turn.
    if (!conn.capabilities.writes) throw noWrites(conn);
    conn.writesUntil = (body as { on?: boolean } | undefined)?.on ? Date.now() + WRITES_FOR_MS : undefined;
    return conn.writesUntil ? { writes_until: conn.writesUntil } : {};
  }
  // The helper has the address, and nothing answers at it.
  if (scenario === "refused") throw refusal(503, "db_unavailable", "The database could not be opened: connect: connection refused.", { hint: "Check that the worktree's database is running." });
  if (what === "schema" && method === "GET") return schemaOf(conn);
  if (what === "rows" && method === "POST") return rowsOf(conn, (body ?? {}) as RowsBody);
  if (what === "query" && method === "POST") return query(conn, (body ?? {}) as { sql?: string; limit?: number });
  if (what === "edit" && method === "POST") return edit(conn, (body ?? {}) as { schema?: string; table?: string; changes?: Change[] });
  throw refusal(404, "not_found", "There is no such route.", { hint: "internal/database/API.md lists them." });
}
