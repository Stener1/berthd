// node --experimental-strip-types --test src/lib/header-picture.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { DEFAULT_HEADER_PICTURE, HARBOUR_MUTE, headerArt, IMAGE_DIM, IMAGE_MUTE, normalizeHeaderPicture, PLATE_MUTE, PLATES } from "./header-picture.ts";

const image = { id: "img-1", name: "boat.png", w: 800, h: 450 };

test("prefs from before there was a choice get the harbour", () => {
  for (const saved of [undefined, null, {}, "harbour", 3, []]) assert.deepEqual(normalizeHeaderPicture(saved), { ...DEFAULT_HEADER_PICTURE, image: undefined });
  assert.deepEqual(DEFAULT_HEADER_PICTURE, { source: "builtin", builtin: "harbour", position: "center" });
});

test("a saved choice keeps what is valid", () => {
  assert.deepEqual(normalizeHeaderPicture({ source: "builtin", builtin: "meadow" }), { source: "builtin", builtin: "meadow", image: undefined, position: "center" });
  assert.deepEqual(normalizeHeaderPicture({ source: "none", builtin: "meadow" }).source, "none");
  assert.deepEqual(normalizeHeaderPicture({ source: "image", image, position: "top" }), { source: "image", builtin: "harbour", image, position: "top" });
  // Other fields, as a later Shipyard might save, are dropped.
  assert.equal("extra" in normalizeHeaderPicture({ source: "none", extra: 1 }), false);
});

test("what isn't valid falls back to the harbour", () => {
  // A plate Shipyard no longer ships.
  assert.equal(normalizeHeaderPicture({ source: "builtin", builtin: "volcano" }).builtin, "harbour");
  assert.equal(normalizeHeaderPicture({ source: "sky" }).source, "builtin");
  // A picture of your own without the picture.
  assert.equal(normalizeHeaderPicture({ source: "image" }).source, "builtin");
  assert.equal(normalizeHeaderPicture({ source: "image", image: { name: "x" } }).source, "builtin");
  assert.equal(normalizeHeaderPicture({ source: "builtin", position: "left" }).position, "center");
});

test("every plate is a choice", () => {
  assert.equal(PLATES.length, 6);
  for (const p of PLATES) assert.equal(normalizeHeaderPicture({ source: "builtin", builtin: p.id }).builtin, p.id);
});

test("the harbour follows its light", () => {
  for (const light of ["dawn", "day", "dusk", "night"] as const) {
    assert.deepEqual(headerArt(DEFAULT_HEADER_PICTURE, light, light === "night"), { kind: "harbour", light, mute: HARBOUR_MUTE[light], dim: 0 });
  }
});

test("a plate shows its night in the night, its day otherwise", () => {
  const meadow = normalizeHeaderPicture({ source: "builtin", builtin: "meadow" });
  assert.deepEqual(headerArt(meadow, "night", true), { kind: "plate", id: "meadow", night: true, mute: PLATE_MUTE.night, dim: 0 });
  for (const light of ["dawn", "day", "dusk"] as const) assert.deepEqual(headerArt(meadow, light, false), { kind: "plate", id: "meadow", night: false, mute: PLATE_MUTE.day, dim: 0 });
});

test("a picture of your own is dimmed and muted in a dark theme", () => {
  const own = normalizeHeaderPicture({ source: "image", image, position: "bottom" });
  assert.deepEqual(headerArt(own, "day", false), { kind: "image", id: "img-1", mute: IMAGE_MUTE.light, dim: 0, position: 1 });
  assert.deepEqual(headerArt(own, "night", true), { kind: "image", id: "img-1", mute: IMAGE_MUTE.dark, dim: IMAGE_DIM, position: 1 });
  assert.equal(headerArt({ ...own, position: "top" }, "day", false)?.position, 0);
  assert.equal(headerArt({ ...own, position: "center" }, "day", false)?.position, 0.5);
  assert.ok(IMAGE_MUTE.dark > IMAGE_MUTE.light && IMAGE_DIM > 0);
});

test("None draws nothing", () => {
  assert.equal(headerArt({ ...DEFAULT_HEADER_PICTURE, source: "none" }, "day", false), undefined);
  assert.equal(headerArt({ ...DEFAULT_HEADER_PICTURE, source: "none" }, "night", true), undefined);
  // Nor does a picture that is gone, unnormalized.
  assert.equal(headerArt({ ...DEFAULT_HEADER_PICTURE, source: "image" }, "day", false), undefined);
});
