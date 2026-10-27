// Mock mode's worktree files (?mock): the box's file routes
// (internal/box/worktreefiles.go, touched.go) over a made-up shop
// repository. Every path and line here is synthetic. The shop's
// checkout-fix worktree has a Claude turn that changed four files; e2e and
// screenshots make the agent write again through window.__berthMockFiles.
import { ApiError } from "@/lib/api";
import { rank } from "@/lib/file-match";

const SHOP_FILES: string[] = [
  "README.md",
  "package.json",
  "pnpm-workspace.yaml",
  "tsconfig.base.json",
  ".github/workflows/ci.yml",
  "apps/web/package.json",
  "apps/web/next.config.mjs",
  "apps/web/tsconfig.json",
  "apps/web/app/layout.tsx",
  "apps/web/app/page.tsx",
  "apps/web/app/checkout/page.tsx",
  "apps/web/app/checkout/success/page.tsx",
  "apps/web/app/api/webhooks/payments/route.ts",
  "apps/web/app/api/orders/route.ts",
  "apps/web/app/api/orders/[id]/route.ts",
  "apps/web/components/cart/cart-drawer.tsx",
  "apps/web/components/cart/cart-line.tsx",
  "apps/web/components/checkout/checkout-form.tsx",
  "apps/web/components/checkout/order-summary.tsx",
  "apps/web/components/checkout/payment-element.tsx",
  "apps/web/components/ui/button.tsx",
  "apps/web/components/ui/input.tsx",
  "apps/web/lib/checkout/createOrder.ts",
  "apps/web/lib/checkout/createOrder.test.ts",
  "apps/web/lib/checkout/totals.ts",
  "apps/web/lib/checkout/totals.test.ts",
  "apps/web/lib/payments/webhook.ts",
  "apps/web/lib/payments/webhook.test.ts",
  "apps/web/lib/payments/idempotency.ts",
  "apps/web/lib/payments/client.ts",
  "apps/web/lib/payments/signature.ts",
  "apps/web/lib/db/index.ts",
  "apps/web/lib/db/schema.ts",
  "apps/web/lib/db/migrations/0007_orders.sql",
  "apps/web/lib/db/migrations/0008_webhook_events.sql",
  "apps/web/lib/email/order-confirmation.tsx",
  "apps/web/lib/log.ts",
  "apps/web/public/logo.png",
  "screenshots/checkout-result.png",
  "apps/web/public/fonts/shop-sans.woff2",
  "apps/web/styles/globals.css",
  "data/exports/orders-2026-09.csv",
  "packages/money/src/index.ts",
  "packages/money/src/format.ts",
  "packages/money/package.json",
  "packages/config/eslint.js",
  "scripts/seed.ts",
  "docs/payments.md",
  "docs/checkout-flow.md",
];

const OTHER_FILES = ["README.md", "package.json", "src/index.ts", "src/lib/util.ts", "src/lib/util.test.ts", ".github/workflows/ci.yml"];

const WEBHOOK_BEFORE = `import { db } from "@/lib/db";
import { log } from "@/lib/log";
import { verifySignature } from "./signature";
import type { PaymentEvent } from "./client";

// handleWebhook takes one event from the payment provider and records
// what it says about an order. The provider retries until it gets a 2xx.
export async function handleWebhook(req: Request): Promise<Response> {
  const body = await req.text();
  const signature = req.headers.get("x-signature") ?? "";
  if (!verifySignature(body, signature)) {
    return new Response("bad signature", { status: 400 });
  }

  const event = JSON.parse(body) as PaymentEvent;
  log.info("webhook", { type: event.type, id: event.id });

  switch (event.type) {
    case "payment.succeeded":
      await db.orders.create({
        cartId: event.data.cartId,
        amount: event.data.amount,
        currency: event.data.currency,
      });
      break;
    case "payment.failed":
      await db.carts.markFailed(event.data.cartId);
      break;
    default:
      log.warn("webhook: unhandled", { type: event.type, livemode: event.livemode, account: event.account ?? "platform", received: new Date().toISOString() });
  }

  return new Response("ok");
}

export function isTestEvent(event: PaymentEvent): boolean {
  return event.livemode === false;
}
`;

const WEBHOOK_AFTER = `import { db } from "@/lib/db";
import { log } from "@/lib/log";
import { claimEvent } from "./idempotency";
import { verifySignature } from "./signature";
import type { PaymentEvent } from "./client";

// handleWebhook takes one event from the payment provider and records
// what it says about an order. The provider retries until it gets a 2xx,
// so the same event can arrive more than once: each is claimed first.
export async function handleWebhook(req: Request): Promise<Response> {
  const body = await req.text();
  const signature = req.headers.get("x-signature") ?? "";
  if (!verifySignature(body, signature)) {
    return new Response("bad signature", { status: 400 });
  }

  const event = JSON.parse(body) as PaymentEvent;
  log.info("webhook", { type: event.type, id: event.id });

  // A retry of an event we already handled: say ok, do nothing.
  const fresh = await claimEvent(event.id);
  if (!fresh) {
    log.info("webhook: duplicate", { id: event.id });
    return new Response("ok");
  }

  switch (event.type) {
    case "payment.succeeded":
      await db.orders.upsert({
        cartId: event.data.cartId,
        amount: event.data.amount,
        currency: event.data.currency,
        idempotencyKey: event.id,
      });
      break;
    case "payment.failed":
      await db.carts.markFailed(event.data.cartId);
      break;
    default:
      log.warn("webhook: unhandled", { type: event.type, livemode: event.livemode, account: event.account ?? "platform", received: new Date().toISOString() });
  }

  return new Response("ok");
}

export function isTestEvent(event: PaymentEvent): boolean {
  return event.livemode === false;
}
`;

// What the agent writes next, for the conflict: it counts duplicates.
export const WEBHOOK_AGAIN = WEBHOOK_AFTER.replace('import { log } from "@/lib/log";\n', 'import { log } from "@/lib/log";\nimport { metrics } from "@/lib/metrics";\n').replace(
  '    log.info("webhook: duplicate", { id: event.id });\n',
  '    log.info("webhook: duplicate", { id: event.id, type: event.type });\n    metrics.increment("webhook.duplicate");\n',
);

const IDEMPOTENCY = `import { db } from "@/lib/db";

// claimEvent records that an event is being handled. It returns false when
// the event was claimed before, by this request or an earlier one.
export async function claimEvent(id: string): Promise<boolean> {
  const inserted = await db.webhookEvents.insertIgnore({ id, at: new Date() });
  return inserted > 0;
}
`;

const WEBHOOK_TEST = `import { describe, expect, it } from "vitest";
import { handleWebhook } from "./webhook";
import { signed } from "./test-helpers";

describe("handleWebhook", () => {
  it("creates one order when the same event arrives twice", async () => {
    const req = () => signed({ id: "evt_1", type: "payment.succeeded" });
    await handleWebhook(req());
    await handleWebhook(req());
    expect(await countOrders("cart_1")).toBe(1);
  });
});
`;

const MIGRATION = `create table webhook_events (
  id text primary key,
  at timestamptz not null default now()
);
`;

const CREATE_ORDER = `import { db } from "@/lib/db";
import { totals } from "./totals";

export interface NewOrder {
  cartId: string;
  email: string;
}

// createOrder turns a paid cart into an order. It is called once the
// payment provider says the payment went through.
export async function createOrder(input: NewOrder) {
  const cart = await db.carts.get(input.cartId);
  if (!cart) throw new Error(\`no cart \${input.cartId}\`);
  const { subtotal, tax, total } = totals(cart.lines);
  return db.orders.create({
    cartId: cart.id,
    email: input.email,
    subtotal,
    tax,
    total,
  });
}
`;

const CHECKOUT_FORM = `"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PaymentElement } from "./payment-element";

export function CheckoutForm({ cartId }: { cartId: string }) {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <form
      className="grid gap-4"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        await fetch("/api/orders", { method: "POST", body: JSON.stringify({ cartId, email }) });
        setBusy(false);
      }}
    >
      <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" />
      <PaymentElement />
      <Button disabled={busy}>Pay now</Button>
    </form>
  );
}
`;

const PAYMENTS_DOC = `# Payments

The shop takes payments through the provider's hosted element. A paid
cart becomes an order when the provider's webhook says so.

## Webhooks

- Every event is signed; \`verifySignature\` checks it before anything else.
- The provider retries an event until it gets a 2xx, so handlers must be idempotent.
- \`claimEvent\` records each event id once; a retry is answered \`ok\` and ignored.
`;

const SHOP_README = `# Shop

The storefront and checkout for the shop: a Next.js app over a Postgres
database, paid through the provider's hosted element.

![The shop's logo](apps/web/public/logo.png)

## Set up & run

\`\`\`sh
pnpm install
pnpm db:migrate
pnpm dev
\`\`\`

The app is on port 3000. Payments run against the provider's test mode.

## Where things are

| Folder | What's in it |
| --- | --- |
| \`apps/web\` | The storefront, checkout and webhook routes |
| \`packages/money\` | Amounts in minor units, and formatting them |
| \`docs\` | How [payments](docs/payments.md) and the checkout flow work |

## Before a release

- [x] Webhooks are idempotent (see [Payments](docs/payments.md#webhooks))
- [ ] Refunds reach the order page
- [ ] Back to [the top](#shop)

> Read [Set up & run](#set-up--run) first.
`;

// What the agent did this turn: each file before (none: it made it) and
// after, and how many minutes ago.
interface Touched {
  path: string;
  before?: string;
  after: string;
  ago: number;
  // When the agent wrote it, exactly (a live write in mock mode).
  atMs?: number;
}

const TOUCHED: Touched[] = [
  { path: "apps/web/lib/payments/webhook.ts", before: WEBHOOK_BEFORE, after: WEBHOOK_AFTER, ago: 1 },
  { path: "apps/web/lib/payments/idempotency.ts", after: IDEMPOTENCY, ago: 3 },
  { path: "apps/web/lib/payments/webhook.test.ts", after: WEBHOOK_TEST, ago: 2 },
  { path: "apps/web/lib/db/migrations/0008_webhook_events.sql", after: MIGRATION, ago: 4 },
];

// Files you had open before in checkout-fix, newest first (lib/files.ts
// seeds them in mock mode).
export const MOCK_RECENT: Record<string, string[]> = {
  "devl:/home/me/work/shop-checkout-fix": ["apps/web/lib/checkout/createOrder.ts", "apps/web/components/checkout/checkout-form.tsx", "docs/payments.md", "apps/web/app/api/webhooks/payments/route.ts"],
};

const isShop = (loc: string) => loc === "shop";

// ---- A big repository (?big=N) ----
// The Files panel and ⌘P are checked on a big repository too: the shop's
// files plus N made-up ones in a monorepo's shape, every name synthetic.
// ⌘P searches at most maxListedFiles (20,000, as internal/box/commands.go)
// and says truncated past it; the panel lists a folder at a time, whole.
const MAX_LISTED = 20_000;
const WORDS = ["billing", "catalog", "search", "identity", "inventory", "pricing", "shipping", "reviews", "analytics", "notify", "media", "tax", "loyalty", "gift", "returns", "audit", "support", "ledger", "promo", "fulfil"];
const PARTS = ["api", "model", "store", "hooks", "utils", "view", "client", "schema", "queue", "worker", "cache", "events"];
const KINDS = [".ts", ".tsx", ".test.ts", ".md", ".json", ".sql", ".css"];
function bigFiles(n: number): string[] {
  const out: string[] = [];
  for (let i = 0; out.length < n; i++) {
    const pkg = WORDS[i % WORDS.length] + (i >= WORDS.length * 40 ? `-${Math.floor(i / (WORDS.length * 40))}` : "");
    const area = PARTS[Math.floor(i / WORDS.length) % PARTS.length];
    const sub = Math.floor(i / (WORDS.length * PARTS.length)) % 4;
    const kind = KINDS[i % KINDS.length];
    const top = i % 9 === 0 ? "services" : "packages";
    out.push(`${top}/${pkg}/src/${area}${sub ? `/v${sub}` : ""}/${PARTS[(i * 7) % PARTS.length]}-${i}${kind}`);
  }
  return out.sort();
}
const bigN = typeof window !== "undefined" ? Number(new URLSearchParams(location.search).get("big")) || 0 : 0;
let bigList: string[] | undefined;
function shopFiles(): string[] {
  if (!bigN) return SHOP_FILES;
  return (bigList ??= [...new Set([...SHOP_FILES, ...bigFiles(bigN)])].sort());
}
// What ⌘P searches: the files in git's order, cut at the cap.
function capped(loc: string): { files: string[]; truncated: boolean } {
  const all = listOf(loc);
  return { files: all.slice(0, MAX_LISTED), truncated: all.length > MAX_LISTED };
}
const hasTurn = (loc: string, wt: string) => loc === "shop" && wt === "checkout-fix";

// A file's text as it starts: the agent's version for what it touched, a
// few made-up lines for anything else.
function initial(loc: string, wt: string, path: string): string {
  const t = hasTurn(loc, wt) ? TOUCHED.find((x) => x.path === path) : undefined;
  if (t) return t.after;
  if (path === "apps/web/lib/payments/webhook.ts") return WEBHOOK_BEFORE;
  if (path.endsWith("createOrder.ts")) return CREATE_ORDER;
  if (path.endsWith("checkout-form.tsx")) return CHECKOUT_FORM;
  if (path === "docs/payments.md") return PAYMENTS_DOC;
  if (path === "README.md" && isShop(loc)) return SHOP_README;
  if (path.endsWith(".md")) return `# ${path.split("/").pop()!.replace(/\.md$/, "")}\n\nNotes for the ${isShop(loc) ? "shop's checkout" : "project"}.\n`;
  if (path.endsWith(".json")) return `{\n  "name": "${path.split("/").slice(-2, -1)[0] ?? loc}",\n  "private": true\n}\n`;
  if (path.endsWith(".yml") || path.endsWith(".yaml")) return `name: ci\non: [push]\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - run: pnpm test\n`;
  if (path.endsWith(".css")) return `:root {\n  --brand: #1f6f5c;\n}\n\nbody {\n  font-family: system-ui, sans-serif;\n}\n`;
  if (path.endsWith(".sql")) return `create table orders (\n  id text primary key,\n  cart_id text not null,\n  total integer not null\n);\n`;
  return `// ${path.split("/").pop()}\nexport {};\n`;
}

// ---- The mock disk ----

interface Entry {
  content?: string;
  mtime: number;
  // A picture, another binary file, or one too large to open, by size.
  kind?: "image" | "binary" | "large";
  size?: number;
}

const disk = new Map<string, Entry>();
const started = Date.now();
const keyOf = (box: string, loc: string, wt: string, path: string) => `${box}\0${loc}\0${wt}\0${path}`;
const listOf = (loc: string) => (isShop(loc) ? shopFiles() : OTHER_FILES);

function entry(box: string, loc: string, wt: string, path: string): Entry | undefined {
  const k = keyOf(box, loc, wt, path);
  if (disk.has(k)) return disk.get(k);
  if (!listOf(loc).includes(path) && !(hasTurn(loc, wt) && TOUCHED.some((t) => t.path === path))) return undefined;
  const t = hasTurn(loc, wt) ? TOUCHED.find((x) => x.path === path) : undefined;
  const mtime = t ? (t.atMs ?? started - t.ago * 60_000) : started - 86_400_000;
  let e: Entry;
  if (path.endsWith(".png")) e = { kind: "image", size: 18_432, mtime };
  else if (path.endsWith(".woff2")) e = { kind: "binary", size: 48_200, mtime };
  else if (path.endsWith(".csv")) e = { kind: "large", size: 14_884_102, mtime };
  else e = { content: initial(loc, wt, path), mtime };
  disk.set(k, e);
  return e;
}

// The etag names the contents, as the box's does.
function etagOf(e: Entry): string {
  if (e.content === undefined) return `stat-${e.size}-${e.mtime}`;
  let h = 0x811c9dc5;
  for (let i = 0; i < e.content.length; i++) h = Math.imul(h ^ e.content.charCodeAt(i), 0x01000193);
  return `sha256-${(h >>> 0).toString(16).padStart(8, "0")}${e.content.length.toString(16)}`;
}

const sizeWords = (n: number) => (n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.round(n / 1024)} KB`);

function fileJson(path: string, e: Entry, withContent: boolean) {
  const name = path.split("/").pop();
  const out: Record<string, unknown> = { path, etag: etagOf(e), mtime: e.mtime, size: e.size ?? e.content?.length ?? 0 };
  if (e.kind === "image") Object.assign(out, { binary: true, image: "image/png" });
  else if (e.kind === "binary") Object.assign(out, { binary: true, reason: `${name} isn't text, so the editor can't show it. Open it in your editor.` });
  else if (e.kind === "large") Object.assign(out, { too_large: true, reason: `${name} is ${sizeWords(e.size ?? 0)}, more than the 2.0 MB Shipyard opens. Open it in your editor.` });
  else if (withContent) out.content = e.content;
  return out;
}

async function touchedOf(box: string, loc: string, wt: string) {
  if (!hasTurn(loc, wt)) return [];
  const { changes } = await import("@/lib/file-marks");
  return TOUCHED.map((t) => {
    const e = entry(box, loc, wt, t.path);
    const now = e?.content ?? "";
    const c = changes(t.before, now);
    const at = t.atMs ?? started - t.ago * 60_000;
    // Live, as the box says it: written in the last 20s (the box also asks
    // that the session works; the app checks that itself).
    return { path: t.path, added: c.added, removed: c.removed, created: t.before == null || undefined, at, live: Date.now() - at < 20_000 || undefined, session: "checkout-fix-claude", agent: "claude", base: "turn", before: t.before ?? null };
  }).sort((a, b) => b.at - a.at || (a.path < b.path ? -1 : 1));
}

const wait = <T,>(v: T, ms = 60) => new Promise<T>((r) => setTimeout(() => r(structuredClone(v)), ms));

const changed = (msg: string, detail: Record<string, unknown>) => {
  const e = new ApiError(msg, 412, "file_changed");
  e.detail = { error: msg, code: "file_changed", ...detail };
  return Promise.reject(e);
};

// mockFilesCall answers the file routes, or undefined for anything else.
export function mockFilesCall(box: string, method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<unknown> | undefined {
  const m = /^locations\/([^/]+)\/worktrees\/([^/]+)\/(files|file|touched)(?:\?(.*))?$/.exec(path);
  if (!m) return undefined;
  const [, l, w, route, query = ""] = m;
  const loc = decodeURIComponent(l);
  const wt = decodeURIComponent(w);
  const q = new URLSearchParams(query);
  const mine = `${box}\0${loc}\0${wt}\0`;
  const made = () => [...disk.entries()].filter(([k, e]) => k.startsWith(mine) && e.mtime >= 0).map(([k]) => k.split("\0")[3]);
  const gone = () => new Set([...disk.entries()].filter(([k, e]) => k.startsWith(mine) && e.mtime < 0).map(([k]) => k.split("\0")[3]));
  if (route === "files" && method === "GET" && q.has("dir")) {
    // One folder's children, whole, folders first (worktreefolder.go).
    const raw = q.get("dir") ?? "";
    const dir = raw.replace(/^\.$/, "").replace(/\/+$/, "");
    if (dir.startsWith("/")) return Promise.reject(new ApiError(`give the folder relative to the worktree, not ${dir}`, 400, "bad_request"));
    if (dir.split("/").some((p) => p === ".." || p.toLowerCase() === ".git")) return Promise.reject(new ApiError("that path is outside the worktree", 403, "refused"));
    const pre = dir ? `${dir}/` : "";
    const out = gone();
    const dirs = new Set<string>();
    const files = new Set<string>();
    for (const p of new Set([...listOf(loc), ...made()])) {
      if (!p.startsWith(pre) || out.has(p)) continue;
      const rest = p.slice(pre.length);
      const slash = rest.indexOf("/");
      if (slash < 0) files.add(rest);
      else dirs.add(rest.slice(0, slash));
    }
    if (dir && !dirs.size && !files.size) return Promise.reject(new ApiError(`${dir} isn't in this worktree`, 404, "not_found"));
    const sort = (a: string, b: string) => a.localeCompare(b, undefined, { sensitivity: "base", numeric: true });
    const entries = [...[...dirs].sort(sort).map((name) => ({ name, dir: true, children: true })), ...[...files].sort(sort).map((name) => ({ name }))];
    return wait({ dir, entries }, bigN ? 120 : 50);
  }
  if (route === "files" && method === "GET") {
    const limit = Math.min(Number(q.get("limit")) || 50, 200);
    const { files, truncated } = capped(loc);
    const out = gone();
    const all = files.filter((p) => !out.has(p));
    // Past the cap, what the agent touched is searched all the same.
    const extra = [...made(), ...(truncated && hasTurn(loc, wt) ? TOUCHED.map((t) => t.path) : [])].filter((p) => !all.includes(p));
    return wait({ files: rank(q.get("q") ?? "", [...all, ...new Set(extra)]).slice(0, limit).map((x) => x.path), truncated: truncated || undefined });
  }
  if (route === "touched" && method === "GET") return touchedOf(box, loc, wt).then((files) => wait({ files: files.map(({ before: _, ...f }) => f) }));
  const file = q.get("path") ?? "";
  if (!file || file.startsWith("/") || file.split("/").some((p) => p === ".." || p.toLowerCase() === ".git")) return Promise.reject(new ApiError("that path is outside the worktree", 403, "refused"));
  if (route === "file" && method === "GET") {
    const e = entry(box, loc, wt, file);
    if (!e || e.mtime < 0) return Promise.reject(new ApiError(`${file} isn't in this worktree (any more)`, 404, "not_found"));
    const out = fileJson(file, e, q.get("stat") !== "1");
    if (q.get("turn") !== "1") return wait(out, 40);
    return touchedOf(box, loc, wt).then((t) => wait({ ...out, turn: t.find((x) => x.path === file) }, 40));
  }
  if (route === "file" && method === "PUT") {
    const content = (body as { content?: string } | undefined)?.content;
    if (typeof content !== "string") return Promise.reject(new ApiError("the request has no content", 400, "bad_request"));
    const ifMatch = headers?.["If-Match"];
    const ifNone = headers?.["If-None-Match"];
    if (!ifMatch && ifNone !== "*") return Promise.reject(new ApiError("say which version this replaces", 428, "bad_request"));
    const e = entry(box, loc, wt, file);
    const exists = !!e && e.mtime >= 0;
    if (ifNone === "*" && exists) return changed(`${file} already exists`, fileJson(file, e!, true));
    if (ifMatch && !exists) return changed(`${file} was deleted since you opened it`, { path: file, deleted: true });
    if (ifMatch && ifMatch.replace(/"/g, "") !== etagOf(e!)) return changed(`${file} changed since you opened it`, fileJson(file, e!, true));
    const next: Entry = { content, mtime: Date.now() };
    disk.set(keyOf(box, loc, wt, file), next);
    return wait({ path: file, etag: etagOf(next), mtime: next.mtime, size: content.length }, 80);
  }
  return undefined;
}

// mockFileBlob is a picture's bytes (?raw=1): a made-up logo.
export function mockFileBlob(path: string): Blob | undefined {
  if (!/\/file\?.*raw=1/.test(path)) return undefined;
  if (path.includes("checkout-result.png")) {
    const shot = `<svg xmlns="http://www.w3.org/2000/svg" width="960" height="560" viewBox="0 0 960 560"><rect width="960" height="560" fill="#f3f0e8"/><rect x="58" y="45" width="844" height="470" rx="16" fill="#fff" stroke="#ddd8cb"/><rect x="58" y="45" width="844" height="60" rx="16" fill="#183d35"/><text x="90" y="83" fill="#fff" font-family="Arial,sans-serif" font-weight="700" font-size="22">acme shop</text><text x="825" y="82" fill="#d8e9df" font-family="Arial,sans-serif" font-size="15">Bag · 2</text><text x="104" y="164" fill="#223a32" font-family="Arial,sans-serif" font-weight="700" font-size="30">Checkout complete</text><text x="104" y="198" fill="#65716c" font-family="Arial,sans-serif" font-size="16">Your order is confirmed. A receipt is on its way.</text><rect x="104" y="236" width="500" height="198" rx="12" fill="#f2f7f3" stroke="#d4e7d8"/><circle cx="145" cy="278" r="18" fill="#27835d"/><path d="m136 278 7 7 13-15" fill="none" stroke="white" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/><text x="178" y="285" fill="#20543d" font-family="Arial,sans-serif" font-size="19" font-weight="700">Payment received</text><text x="130" y="341" fill="#65716c" font-family="Arial,sans-serif" font-size="16">Order AC-2048</text><text x="130" y="381" fill="#65716c" font-family="Arial,sans-serif" font-size="16">Delivery estimate</text><text x="454" y="381" fill="#20543d" font-family="Arial,sans-serif" font-size="16" font-weight="700">Friday</text><rect x="677" y="236" width="162" height="198" rx="12" fill="#f8f4e9"/><rect x="705" y="262" width="106" height="94" rx="10" fill="#e2bb83"/><path d="M714 285h88l-12 58h-64z" fill="#f9e6c5"/><text x="698" y="388" fill="#514d42" font-family="Arial,sans-serif" font-size="15">Linen tote</text><text x="698" y="412" fill="#514d42" font-family="Arial,sans-serif" font-size="15">£32.00</text></svg>`;
    return new Blob([shot], { type: "image/svg+xml" });
  }
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512"><rect width="512" height="512" rx="112" fill="#1f6f5c"/><path d="M160 196h192l-16 176a24 24 0 0 1-24 22H200a24 24 0 0 1-24-22z" fill="#f4efe6"/><path d="M208 196v-20a48 48 0 0 1 96 0v20" fill="none" stroke="#f4efe6" stroke-width="22" stroke-linecap="round"/></svg>`;
  return new Blob([svg], { type: "image/svg+xml" });
}

// The agent writing a file, for e2e and screenshots: a new version on the
// mock disk, which open File tabs notice as they would on a box.
function agentWrites(path: string, content: string | null, where = { box: "devl", location: "shop", worktree: "checkout-fix" }) {
  entry(where.box, where.location, where.worktree, path);
  const k = keyOf(where.box, where.location, where.worktree, path);
  if (content === null) disk.set(k, { mtime: -1 });
  else disk.set(k, { content, mtime: Date.now() });
}

// agentWorks puts checkout-fix's Claude to work, or back to waiting.
function agentWorks(on = true) {
  void import("@/lib/mock").then((m) => m.mockDemo.setAgent("devl", "checkout-fix-claude", on ? "running" : "waiting"));
}

if (typeof window !== "undefined" && new URLSearchParams(location.search).has("mock")) {
  (window as unknown as { __berthMockFiles: unknown }).__berthMockFiles = {
    agentWrites,
    // The agent's next edit to webhook.ts: a metrics import and a counter.
    agentEditsWebhook: () => agentWrites("apps/web/lib/payments/webhook.ts", WEBHOOK_AGAIN),
    // The agent writing a file right now (the Files panel's live mark): a
    // new version, touched this very moment, with the agent working.
    agentWritesLive: (path = "apps/web/lib/checkout/totals.ts") => {
      const where = { box: "devl", location: "shop", worktree: "checkout-fix" };
      const before = entry(where.box, where.location, where.worktree, path)?.content;
      const now = Date.now();
      const after = (before ?? "") + "\n// Rounds each line before summing, so totals match the receipt.\nexport const roundLine = (cents: number) => Math.round(cents);\n";
      const had = TOUCHED.find((t) => t.path === path);
      if (had) Object.assign(had, { after, atMs: now });
      else TOUCHED.push({ path, before, after, ago: 0, atMs: now });
      agentWrites(path, after, where);
      agentWorks(true);
    },
    // checkout-fix's agent starts (or stops) working.
    agentWorks,
    // What the mock disk holds now, for checking a save.
    contentOf: (path: string, where = { box: "devl", location: "shop", worktree: "checkout-fix" }) => entry(where.box, where.location, where.worktree, path)?.content,
  };
}
