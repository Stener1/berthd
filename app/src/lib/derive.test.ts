// node --experimental-strip-types --test src/lib/derive.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { firstPrompt, offBoard, restartCommand } from "./derive.ts";

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

test("a session is off the board when its location, on its box, is hidden", () => {
  const locations = [
    { name: "ops", path: "/home/me/ops", repo: true, scripts: {}, worktrees: [{ name: "ops", path: "/home/me/ops", main: true }, { name: "go-live", path: "/home/me/ops-go-live" }] },
    { name: "shop", path: "/home/me/shop", repo: true, scripts: {}, worktrees: [{ name: "shop", path: "/home/me/shop", main: true }] },
  ];
  const at = (dir: string) => ({ name: "s", dir, created: "", attached: 0, exited: false });
  const hidden = new Set(["devl/ops"]);
  // The main checkout and its worktrees alike.
  assert.equal(offBoard(locations, "devl", at("/home/me/ops"), hidden), true);
  assert.equal(offBoard(locations, "devl", at("/home/me/ops-go-live"), hidden), true);
  assert.equal(offBoard(locations, "devl", at("/home/me/shop"), hidden), false);
  // The same name on another box is another location.
  assert.equal(offBoard(locations, "gpu", at("/home/me/ops"), hidden), false);
  // Somewhere no location knows, or nothing hidden.
  assert.equal(offBoard(locations, "devl", at("/tmp"), hidden), false);
  assert.equal(offBoard(locations, "devl", at("/home/me/ops"), new Set()), false);
});
