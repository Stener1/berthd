// node --experimental-strip-types --test src/lib/linear-issues.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import type { Project, Session } from "@berth/plugin";

import { DEFAULT_TEMPLATE, identifierIn, priorityRank, renderPrompt, runsByIdentifier, worktreeOf } from "../../../plugins/issues/src/linear.ts";

test("an issue's worktree is named after it and takes Linear's branch", () => {
  assert.deepEqual(worktreeOf({ identifier: "WEB-12", title: "Retrying a failed payment charges the card twice!", branch: "robin/web-12-retrying-a-failed-payment" }), {
    name: "web-12-retrying-a-failed-payment-charges-th",
    branch: "robin/web-12-retrying-a-failed-payment",
  });
  // Without a suggestion the branch is the worktree's name, and a title of
  // nothing but symbols leaves the identifier alone.
  assert.deepEqual(worktreeOf({ identifier: "PLA-7", title: "Fix it" }), { name: "pla-7-fix-it", branch: "pla-7-fix-it" });
  assert.deepEqual(worktreeOf({ identifier: "PLA-7", title: "???", branch: " " }), { name: "pla-7", branch: "pla-7" });
});

test("an identifier is found in a branch or worktree name only as its own word", () => {
  const teams = ["WEB", "PLA"];
  assert.equal(identifierIn("robin/web-12-retrying", teams), "WEB-12");
  assert.equal(identifierIn("WEB-12", teams), "WEB-12");
  assert.equal(identifierIn("fix_pla-7", teams), "PLA-7");
  assert.equal(identifierIn("web-123-other", teams), "WEB-123");
  assert.equal(identifierIn("cobweb-12", teams), undefined);
  assert.equal(identifierIn("issue-12-from-github", teams), undefined);
  assert.equal(identifierIn("web-12x", teams), undefined);
  assert.equal(identifierIn("web-12", []), undefined);
  assert.equal(identifierIn(undefined, teams), undefined);
});

test("worktrees made for an issue are found in every project, the one that needs you first", () => {
  const wt = (name: string, branch: string, main = false) => ({ name, branch, path: `/srv/shop/${name}`, main });
  const projects = [
    {
      id: "acme/shop",
      name: "shop",
      members: [
        { box: "devl", online: true, location: { name: "shop", worktrees: [wt("shop", "main", true), wt("web-12-retry", "robin/web-12-retry"), wt("checkout", "feat/checkout")] } },
        { box: "mini", online: true, location: { name: "shop", worktrees: [wt("second-try", "web-12-second-try")] } },
      ],
    },
  ] as unknown as Project[];
  const session = (name: string, location: string, agent_state: string) => ({ name, location, agent: "claude", agent_state, created: "2026-10-01T00:00:00Z" }) as unknown as Session;
  const runs = runsByIdentifier(projects, { devl: [session("a", "shop/web-12-retry", "running")], mini: [session("b", "shop/second-try", "waiting")] }, ["WEB"]);
  assert.deepEqual([...runs.keys()], ["WEB-12"]);
  assert.deepEqual(
    runs.get("WEB-12")!.map((r) => `${r.box}:${r.worktree}:${r.session?.agent_state}`),
    ["mini:second-try:waiting", "devl:web-12-retry:running"],
  );
});

test("the first prompt is the template with the issue in it", () => {
  const issue = { identifier: "WEB-12", title: "Charges twice", url: "https://linear.app/acme/issue/WEB-12", team: { id: "t", key: "WEB", name: "Web" } };
  assert.equal(
    renderPrompt(DEFAULT_TEMPLATE, { ...issue, description: "<!-- template -->\nRetry charges twice.\n" }),
    "Work on WEB-12: Charges twice\n\nhttps://linear.app/acme/issue/WEB-12\n\nRetry charges twice.\n\nOpen a PR when done, with WEB-12 in its title so Linear links it to the issue.",
  );
  // No description leaves no hole, and an unknown variable stays as written.
  assert.equal(renderPrompt("{{id}} for {{team}}\n\n{{body}}\n\n{{nope}}", issue), "WEB-12 for Web\n\n{{nope}}");
  const long = renderPrompt("{{body}}", { ...issue, description: "x".repeat(9000) });
  assert.ok(long.length < 8200 && long.endsWith("read the rest at https://linear.app/acme/issue/WEB-12.)"));
});

test("urgent sorts first and no priority last", () => {
  assert.deepEqual([0, 3, 1, 4, 2].sort((a, b) => priorityRank(a) - priorityRank(b)), [1, 2, 3, 4, 0]);
});
