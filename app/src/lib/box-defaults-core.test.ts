// node --experimental-strip-types --test src/lib/box-defaults-core.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { agentDefaultsFor, defaultsToPut } from "./box-defaults-core.ts";

const caps = ["driver.claude", "driver.codex", "driver.acp", "agents.defaults"];
const on = { labs: true, structuredClaude: true, approvals: "bypass" as const };
const off = { labs: true, structuredClaude: false, approvals: "bypass" as const };

test("the prefs map to the default structuredLaunch would use", () => {
  assert.deepEqual(agentDefaultsFor(on), { structured: true, mode: "bypassPermissions" });
  assert.deepEqual(agentDefaultsFor({ ...on, approvals: "ask" }), { structured: true, mode: "default" });
  assert.deepEqual(agentDefaultsFor(off), { structured: false });
  // Labs off turns structured agents off, whatever the switch says.
  assert.deepEqual(agentDefaultsFor({ ...on, labs: false }), { structured: false });
});

test("a box that keeps no default is left alone", () => {
  assert.equal(defaultsToPut(on, ["driver.claude"], undefined), undefined);
  assert.equal(defaultsToPut(on, undefined, undefined), undefined);
});

test("a box is sent the default only when it differs", () => {
  assert.deepEqual(defaultsToPut(on, caps, undefined), { structured: true, mode: "bypassPermissions" });
  assert.deepEqual(defaultsToPut(on, caps, { structured: false }), { structured: true, mode: "bypassPermissions" });
  assert.equal(defaultsToPut(on, caps, { structured: true, mode: "bypassPermissions" }), undefined);
  // Approvals changed: the mode goes too.
  assert.deepEqual(defaultsToPut({ ...on, approvals: "ask" }, caps, { structured: true, mode: "bypassPermissions" }), { structured: true, mode: "default" });
  assert.equal(defaultsToPut({ ...on, approvals: "ask" }, caps, { structured: true }), undefined);
  // Off and never set: nothing to send, so a box isn't written to for nothing.
  assert.equal(defaultsToPut(off, caps, undefined), undefined);
  assert.equal(defaultsToPut(off, caps, { structured: false, mode: "bypassPermissions" }), undefined);
  assert.deepEqual(defaultsToPut(off, caps, { structured: true, mode: "default" }), { structured: false });
});
