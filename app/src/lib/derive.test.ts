// node --experimental-strip-types --test src/lib/derive.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import type { Session } from "@berth/plugin";

import { firstPrompt, restartCommand, sessionState } from "./derive.ts";

test("a preset session's first prompt is read back from its command", () => {
  assert.equal(firstPrompt({ preset: "claude", command: "claude --model haiku 'fix the flaky test'" }), "fix the flaky test");
  assert.equal(firstPrompt({ preset: "claude", command: `claude 'it'\\''s "done"
next line'` }), `it's "done"\nnext line`);
  assert.equal(firstPrompt({ preset: "opencode", command: "opencode --prompt 'add a health check'" }), "add a health check");
  assert.equal(firstPrompt({ preset: "claude", command: "claude --resume 0d1e 'go on'" }), "go on");
  // No prompt, or a command berth didn't write.
  assert.equal(firstPrompt({ preset: "claude", command: "claude --model haiku" }), undefined);
  assert.equal(firstPrompt({ command: "claude 'typed by hand'" }), undefined);
  assert.equal(firstPrompt(undefined), undefined);
  // What restartCommand drops is the same prompt.
  assert.equal(restartCommand("claude --model haiku 'fix it'"), "claude --model haiku");
});

test("a finished turn whose work goes on without the agent shows as working, not done", () => {
  const s = { name: "a", dir: "/w", created: "2026-10-12T10:00:00Z", attached: 0, exited: false, agent: "claude", agent_state: "finished" } as Session;
  assert.equal(sessionState(s), "finished");
  assert.equal(sessionState({ ...s, unfinished: true }), "running");
  // Only a finished turn: a question still needs you.
  assert.equal(sessionState({ ...s, agent_state: "waiting", unfinished: true }), "waiting");
});
