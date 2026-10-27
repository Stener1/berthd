// node --experimental-strip-types --test src/lib/box-defaults-core.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { agentDefaultsFor, defaultsToPut } from "./box-defaults-core.ts";

const caps = ["driver.claude", "driver.codex", "driver.acp", "agents.defaults"];
const bypass = { approvals: "bypass" as const };
const ask = { approvals: "ask" as const };

test("agents always run structured, in the mode Permissions names", () => {
  assert.deepEqual(agentDefaultsFor(bypass), { structured: true, mode: "bypassPermissions" });
  assert.deepEqual(agentDefaultsFor(ask), { structured: true, mode: "default" });
});

test("prefs an older Shipyard saved don't turn it off", () => {
  // The Labs switch and Labs itself, saved off, no longer have a say.
  const old = { ...bypass, labs: false, structuredClaude: false };
  assert.deepEqual(agentDefaultsFor(old), { structured: true, mode: "bypassPermissions" });
  assert.deepEqual(defaultsToPut(old, caps, { structured: false }), { structured: true, mode: "bypassPermissions" });
});

test("a box that keeps no default is left alone", () => {
  assert.equal(defaultsToPut(bypass, ["driver.claude"], undefined), undefined);
  assert.equal(defaultsToPut(bypass, undefined, undefined), undefined);
});

test("a box is sent the default only when it differs", () => {
  assert.deepEqual(defaultsToPut(bypass, caps, undefined), { structured: true, mode: "bypassPermissions" });
  assert.deepEqual(defaultsToPut(bypass, caps, { structured: false }), { structured: true, mode: "bypassPermissions" });
  assert.equal(defaultsToPut(bypass, caps, { structured: true, mode: "bypassPermissions" }), undefined);
  // A box turned to its terminal by an older Shipyard is turned back.
  assert.deepEqual(defaultsToPut(ask, caps, { structured: false, mode: "bypassPermissions" }), { structured: true, mode: "default" });
  // Permissions changed: the mode goes too.
  assert.deepEqual(defaultsToPut(ask, caps, { structured: true, mode: "bypassPermissions" }), { structured: true, mode: "default" });
  assert.equal(defaultsToPut(ask, caps, { structured: true }), undefined);
  // berthd's own default with nothing saved (agentdefaults.go): structured,
  // asking, which Ask leaves alone and Bypass changes.
  assert.equal(defaultsToPut(ask, caps, { structured: true, mode: "default" }), undefined);
  assert.deepEqual(defaultsToPut(bypass, caps, { structured: true, mode: "default" }), { structured: true, mode: "bypassPermissions" });
});
