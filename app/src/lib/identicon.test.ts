import assert from "node:assert/strict";
import { test } from "node:test";

import { identicon } from "./identicon.ts";

test("an identicon is the same for the same name, whatever its case", () => {
  assert.deepEqual(identicon("shop"), identicon("shop"));
  assert.deepEqual(identicon(" Shop "), identicon("shop"));
});

test("different names get different identicons", () => {
  const seen = new Set(["shop", "evals", "notes", "acme-api", "acme-web", "docs"].map((n) => JSON.stringify(identicon(n))));
  assert.equal(seen.size, 6);
});

test("an identicon is mirrored down the middle", () => {
  for (const name of ["shop", "evals", "a", ""]) {
    const { cells } = identicon(name);
    assert.equal(cells.length, 25);
    for (let row = 0; row < 5; row++) {
      assert.equal(cells[row * 5], cells[row * 5 + 4]);
      assert.equal(cells[row * 5 + 1], cells[row * 5 + 3]);
    }
  }
});

test("every identicon has enough in it to read as a mark", () => {
  for (let i = 0; i < 500; i++) {
    const { cells, hue } = identicon(`project-${i}`);
    assert.ok(cells.filter(Boolean).length >= 5);
    assert.ok(hue >= 0 && hue < 360);
  }
});
