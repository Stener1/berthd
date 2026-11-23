// node --experimental-strip-types --test src/lib/reflow.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { reflowScreen } from "./reflow.ts";

test("a paragraph the terminal wrapped is one paragraph again", () => {
  assert.equal(
    reflowScreen(["Retries are capped at 3 with exponential backoff, and 429s now count as", "retryable. I added tests for the cap and the backoff timing."]),
    "Retries are capped at 3 with exponential backoff, and 429s now count as retryable. I added tests for the cap and the backoff timing.",
  );
});

test("an empty line still ends a paragraph", () => {
  assert.equal(reflowScreen(["First one,", "wrapped.", "", "Second."]), "First one, wrapped.\n\nSecond.");
});

test("list items and options keep their own lines, and their wrapped tails join them", () => {
  assert.equal(
    reflowScreen(["Changed:", "- ProductGrid.tsx: wrapped in memo, keys by", "  product id", "• utils/memo.ts: a cache", "", "❯ 1. Yes, cap it at 30s", "  2) No, leave it"]),
    "Changed:\n\n- ProductGrid.tsx: wrapped in memo, keys by product id\n- utils/memo.ts: a cache\n\n1. Yes, cap it at 30s\n2. No, leave it",
  );
});

test("a line ending in a colon starts a new paragraph after it", () => {
  assert.equal(reflowScreen(["Summary:", "it works."]), "Summary:\n\nit works.");
});

test("a table drawn with box lines stays as drawn", () => {
  assert.equal(reflowScreen(["Timings:", "│ before │ 180ms │", "│ after  │ 45ms  │", "Done."]), "Timings:\n\n```text\n│ before │ 180ms │\n│ after  │ 45ms  │\n```\n\nDone.");
});

test("a tilde in a code span is left alone", () => {
  assert.equal(reflowScreen(["run `ls ~/x` and ~2s"]), "run `ls ~/x` and \\~2s");
});

test("a tilde is a tilde, not strikethrough, and code spans stay", () => {
  assert.equal(reflowScreen(["went from ~180ms to ~45ms; `pnpm test` passes"]), "went from \\~180ms to \\~45ms; `pnpm test` passes");
});
