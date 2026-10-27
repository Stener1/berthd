// node --experimental-strip-types --test src/lib/file-refs.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { excerpt, kindIn, lineLabel, lineSpan, parseRef, splitPath } from "./file-refs.ts";

const ROOT = "/home/me/work/shop-checkout-fix";

test("a path alone, and from ./", () => {
  assert.deepEqual(parseRef("apps/web/lib/payments/webhook.ts"), { path: "apps/web/lib/payments/webhook.ts" });
  assert.deepEqual(parseRef("./apps/web/lib/log.ts"), { path: "apps/web/lib/log.ts" });
  assert.deepEqual(parseRef(" docs/payments.md "), { path: "docs/payments.md" });
  assert.deepEqual(parseRef("apps/web/app/api/orders/[id]/route.ts"), { path: "apps/web/app/api/orders/[id]/route.ts" });
  assert.deepEqual(parseRef("app/(shop)/page.tsx"), { path: "app/(shop)/page.tsx" });
});

test("a line, a line and column, and ranges", () => {
  assert.deepEqual(parseRef("src/app.ts:14"), { path: "src/app.ts", line: 14 });
  // The second number is a column: line 14.
  assert.deepEqual(parseRef("src/app.ts:14:30"), { path: "src/app.ts", line: 14 });
  assert.deepEqual(parseRef("src/app.ts:14-30"), { path: "src/app.ts", line: 14, end: 30 });
  assert.deepEqual(parseRef("src/app.ts#L14"), { path: "src/app.ts", line: 14 });
  assert.deepEqual(parseRef("src/app.ts#L14-L30"), { path: "src/app.ts", line: 14, end: 30 });
  assert.deepEqual(parseRef("src/app.ts#L14-30"), { path: "src/app.ts", line: 14, end: 30 });
  assert.deepEqual(parseRef("src/app.ts L14"), { path: "src/app.ts", line: 14 });
  assert.deepEqual(parseRef("src/app.ts (L14-30)"), { path: "src/app.ts", line: 14, end: 30 });
  assert.deepEqual(parseRef("src/app.ts (lines 14-30)"), { path: "src/app.ts", line: 14, end: 30 });
});

test("a range that runs backwards is its first line", () => {
  assert.deepEqual(parseRef("src/app.ts:30-14"), { path: "src/app.ts", line: 30 });
  assert.equal(parseRef("src/app.ts:0"), undefined);
});

test("an absolute path inside the worktree is read from its top", () => {
  assert.deepEqual(parseRef(`${ROOT}/apps/web/lib/log.ts:7`, ROOT), { path: "apps/web/lib/log.ts", line: 7 });
  assert.deepEqual(parseRef(`${ROOT}/README.md`, `${ROOT}/`), { path: "README.md" });
  // Outside it, or with no worktree to read it from, it's not a link.
  assert.equal(parseRef("/etc/hosts", ROOT), undefined);
  assert.equal(parseRef(`${ROOT}-other/README.md`, ROOT), undefined);
  assert.equal(parseRef(`${ROOT}/README.md`), undefined);
  assert.equal(parseRef(ROOT, ROOT), undefined);
});

test("words, calls, commands and URLs are not paths", () => {
  for (const s of ["react", "npm test", "pnpm db:migrate", "foo()", "orders.byIdempotencyKey()", "createOrder(event)", "https://example.com/a.ts", "http://localhost:3000", "localhost:3000", "mailto:a@b.c", "1.2.3", "0.5", "", "   ", "a && b", "`x`", "src/", "~/notes.md", "../outside.ts", "a/../b.ts", ".git/config", "*.ts", "src/**/*.ts", "$HOME/x.ts", "{a,b}.ts", "a\\b.ts", "const x = 1"]) {
    assert.equal(parseRef(s, ROOT), undefined, s);
  }
});

test("a bare name is a file by its dot or its known name", () => {
  assert.deepEqual(parseRef("package.json"), { path: "package.json" });
  assert.deepEqual(parseRef(".env.local"), { path: ".env.local" });
  assert.deepEqual(parseRef("Makefile"), { path: "Makefile" });
  assert.deepEqual(parseRef("Dockerfile:3"), { path: "Dockerfile", line: 3 });
  assert.equal(parseRef("useState"), undefined);
});

test("a folder's listing says what is in it", () => {
  const entries = [{ name: "payments", dir: true }, { name: "log.ts" }];
  assert.equal(kindIn(entries, "log.ts"), "file");
  assert.equal(kindIn(entries, "payments"), "dir");
  assert.equal(kindIn(entries, "nope.ts"), undefined);
  assert.equal(kindIn(null, "log.ts"), undefined);
  assert.deepEqual(splitPath("apps/web/lib/log.ts"), { dir: "apps/web/lib", name: "log.ts" });
  assert.deepEqual(splitPath("README.md"), { dir: "", name: "README.md" });
});

test("the lines a ref points at, inside the file", () => {
  assert.deepEqual(lineSpan({ path: "a", line: 4 }, 10), { from: 4, to: 4 });
  assert.deepEqual(lineSpan({ path: "a", line: 4, end: 40 }, 10), { from: 4, to: 10 });
  assert.equal(lineSpan({ path: "a", line: 11 }, 10), undefined);
  assert.equal(lineSpan({ path: "a" }, 10), undefined);
  assert.equal(lineLabel({ path: "a", line: 4 }), "line 4");
  assert.equal(lineLabel({ path: "a", line: 4, end: 9 }), "lines 4–9");
  assert.equal(lineLabel({ path: "a" }), undefined);
});

test("an excerpt keeps the file's numbers, with a little before", () => {
  const text = Array.from({ length: 40 }, (_, i) => `line ${i + 1}`).join("\n") + "\n";
  const one = excerpt(text, { from: 20, to: 20 });
  assert.equal(one.start, 17);
  assert.equal(one.lines[0], "line 17");
  assert.equal(one.lines.length, 16);
  // Near the top, it starts at line 1; near the end, it stops there.
  assert.equal(excerpt(text, { from: 2, to: 2 }).start, 1);
  const last = excerpt(text, { from: 39, to: 40 });
  assert.equal(last.lines[last.lines.length - 1], "line 40");
  // A long range shows whole, with a little either side.
  const long = excerpt(text, { from: 5, to: 30 });
  assert.equal(long.start, 2);
  assert.equal(long.lines[long.lines.length - 1], "line 33");
  assert.equal(excerpt(text, undefined).start, 1);
});
