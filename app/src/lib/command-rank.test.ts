// node --experimental-strip-types --test src/lib/command-rank.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { rankCommands } from "./command-rank.ts";
import type { AgentCommand } from "./commands.ts";

// An agent with more commands of its own than the menu's cap, as Claude
// Code has.
const builtins: AgentCommand[] = Array.from({ length: 81 }, (_, i) => ({ name: `/builtin-${String(i).padStart(2, "0")}`, kind: "builtin" }));
const list: AgentCommand[] = [
  ...builtins,
  { name: "/vercel:deploy", kind: "plugin", source: "vercel" },
  { name: "/release", kind: "skill", source: "project" },
  { name: "/tidy", kind: "skill", source: "user" },
  { name: "/ship", kind: "custom", source: "project" },
];

test("a bare slash lists everything, the person's own first", () => {
  const all = rankCommands(list, "");
  assert.equal(all.length, list.length);
  assert.deepEqual(
    all.slice(0, 4).map((c) => c.name),
    ["/ship", "/release", "/tidy", "/builtin-00"],
  );
  assert.equal(all.at(-1)?.name, "/vercel:deploy");
});

test("a query is capped and ranked by how well the name matches", () => {
  assert.equal(rankCommands(list, "builtin").length, 60);
  assert.equal(rankCommands(list, "rel")[0].name, "/release");
  assert.equal(rankCommands(list, "deploy")[0].name, "/vercel:deploy");
  assert.deepEqual(rankCommands(list, "zzzz"), []);
});
