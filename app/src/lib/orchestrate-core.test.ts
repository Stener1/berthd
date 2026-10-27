// node --experimental-strip-types --test src/lib/orchestrate-core.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { handoff } from "./orchestrate-core.ts";

// recorder is a box that notes each call and answers with a session.
function recorder() {
  const calls: { path: string; body: Record<string, unknown> }[] = [];
  const call = (async (_box: string, _method: string, path: string, body?: unknown) => {
    calls.push({ path, body: body as Record<string, unknown> });
    return path === "tasks" ? { session: { name: "next" } } : { name: "next" };
  }) as Parameters<typeof handoff>[0];
  return { calls, call };
}

const agent = { id: "claude", command: "claude" };

test("a hand-off beside the first agent starts the next one structured when asked", async () => {
  const { calls, call } = recorder();
  await handoff(call, { box: "devbox", fromSession: "a", location: "acme/fix", agent, prompt: "carry on", launch: { driver: "claude", mode: "bypassPermissions" } });
  assert.deepEqual(calls[0], { path: "sessions", body: { location: "acme/fix", agent: "claude", prompt: "carry on", driver: "claude", mode: "bypassPermissions" } });
});

test("a hand-off without a driver starts the next agent in its terminal, as before", async () => {
  const { calls, call } = recorder();
  await handoff(call, { box: "devbox", fromSession: "a", location: "acme/fix", agent, prompt: "carry on" });
  assert.deepEqual(calls[0], { path: "sessions", body: { location: "acme/fix", command: "claude 'carry on'" } });
});

test("a hand-off to a new worktree passes the driver with the task", async () => {
  const { calls, call } = recorder();
  await handoff(call, { box: "devbox", fromSession: "a", location: "acme/fix", agent, prompt: "review it", worktree: { name: "review" }, launch: { driver: "claude", mode: "default" } });
  assert.equal(calls[0].path, "tasks");
  assert.equal(calls[0].body.driver, "claude");
  assert.equal(calls[0].body.mode, "default");
  assert.equal(calls[0].body.agent, "claude");
});
