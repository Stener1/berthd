// node --experimental-strip-types --test src/lib/structured-core.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { launchFor } from "./structured-core.ts";

const all = ["driver.claude", "driver.codex", "driver.acp", "conversations"];

test("an agent runs in chat wherever the box has its driver", () => {
  assert.deepEqual(launchFor("claude", false, all, "bypass"), { driver: "claude", mode: "bypassPermissions" });
  assert.deepEqual(launchFor("codex", false, all, "bypass"), { driver: "codex", mode: "bypassPermissions" });
  assert.deepEqual(launchFor("opencode", true, all, "bypass"), { driver: "acp", mode: "bypassPermissions" });
});

test("Ask starts it asking; an ACP agent in its own first mode", () => {
  assert.deepEqual(launchFor("claude", false, all, "ask"), { driver: "claude", mode: "default" });
  assert.deepEqual(launchFor("codex", false, all, "ask"), { driver: "codex", mode: "default" });
  assert.deepEqual(launchFor("gemini", true, all, "ask"), { driver: "acp", mode: undefined });
});

test("a box without the driver runs the agent in its terminal", () => {
  assert.deepEqual(launchFor("claude", false, [], "bypass"), {});
  assert.deepEqual(launchFor("codex", false, ["driver.claude"], "bypass"), {});
  assert.deepEqual(launchFor("opencode", true, ["driver.claude", "driver.codex"], "bypass"), {});
});

test("an agent with no driver runs in its terminal", () => {
  assert.deepEqual(launchFor("aider", false, all, "bypass"), {});
  assert.deepEqual(launchFor("", false, all, "bypass"), {});
});
