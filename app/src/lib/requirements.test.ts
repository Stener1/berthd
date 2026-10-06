import assert from "node:assert/strict";
import { test } from "node:test";

import { BREW_INSTALL, blocksStart, parseRequirements, requirementsCard, requirementsCopy } from "./requirements.ts";

const claude = { id: "claude", name: "Claude Code", command: "claude", found: true, path: "/opt/homebrew/bin/claude" };
const codex = { id: "codex", name: "Codex", command: "codex", found: false, install: "npm install -g @openai/codex" };

test("parseRequirements reads the box's answer and rejects anything else", () => {
  assert.equal(parseRequirements(undefined), undefined);
  assert.equal(parseRequirements("404 page not found"), undefined);
  assert.equal(parseRequirements({ os: "linux" }), undefined);
  const r = parseRequirements({ os: "darwin", tmux: { found: false, install: " brew install tmux ", manager: "brew" }, agents: [claude, { name: "no id" }, codex] });
  assert.ok(r);
  assert.equal(r.os, "darwin");
  assert.deepEqual(r.tmux, { found: false, path: undefined, install: "brew install tmux", manager: "brew", manager_missing: false, help: undefined });
  assert.deepEqual(
    r.agents.map((a) => [a.id, a.found]),
    [
      ["claude", true],
      ["codex", false],
    ],
  );
  // An answer without agents is still one.
  assert.deepEqual(parseRequirements({ os: "linux", tmux: { found: true } })?.agents, []);
});

test("requirementsCard: unknown goes ahead, tmux first, then the agent", () => {
  const ok = parseRequirements({ os: "darwin", tmux: { found: true }, agents: [claude, codex] });
  const noTmux = parseRequirements({ os: "darwin", tmux: { found: false }, agents: [claude] });
  const noAgent = parseRequirements({ os: "linux", tmux: { found: true }, agents: [{ ...claude, found: false }, codex] });
  assert.equal(requirementsCard(undefined), "hidden", "an older box: proceed as before");
  assert.equal(requirementsCard(ok), "hidden");
  assert.equal(requirementsCard(noTmux), "tmux");
  assert.equal(requirementsCard(noTmux, { noAgent: true }), "hidden", "a worktree alone needs no tmux");
  assert.equal(requirementsCard(noAgent), "agent");
  assert.equal(requirementsCard(ok, { agent: "codex" }), "agent", "the agent asked for is missing");
  assert.equal(requirementsCard(ok, { agent: "claude" }), "hidden");
  assert.equal(requirementsCard(ok, { agent: "my-own" }), "hidden", "a repository's own agent is the box's business");
  assert.equal(blocksStart("tmux"), true);
  assert.equal(blocksStart("hidden"), false);
});

test("requirementsCopy: Homebrew on a Mac, brew.sh without it, the package manager on Linux", () => {
  const mac = parseRequirements({ os: "darwin", tmux: { found: false, install: "brew install tmux", manager: "brew" }, agents: [] });
  const c = requirementsCopy("tmux", mac, "this Mac", { local: true });
  assert.ok(c);
  assert.equal(c.command, "brew install tmux");
  assert.equal(c.first, undefined);
  assert.match(c.body, /Homebrew/);
  assert.equal(c.where, "Terminal on this Mac");

  const bare = parseRequirements({ os: "darwin", tmux: { found: false, manager_missing: true, help: "https://brew.sh" }, agents: [] });
  const b = requirementsCopy("tmux", bare, "this Mac", { local: true });
  assert.equal(b?.first, BREW_INSTALL);
  assert.equal(b?.command, "brew install tmux");
  assert.deepEqual(b?.help, { label: "brew.sh", url: "https://brew.sh" });

  const linux = parseRequirements({ os: "linux", tmux: { found: false, install: "sudo dnf install tmux", manager: "dnf" }, agents: [] });
  const l = requirementsCopy("tmux", linux, "devl");
  assert.equal(l?.command, "sudo dnf install tmux");
  assert.match(l?.where ?? "", /devl/);
  assert.match(l?.body ?? "", /password/);

  const agent = parseRequirements({ os: "linux", tmux: { found: true }, agents: [{ ...claude, found: false, install: "npm install -g @anthropic-ai/claude-code" }, codex] });
  const a = requirementsCopy("agent", agent, "devl");
  assert.equal(a?.title, "Install Claude Code on devl");
  assert.equal(a?.command, "npm install -g @anthropic-ai/claude-code");
  assert.equal(requirementsCopy("agent", agent, "devl", { agent: "codex" })?.command, "npm install -g @openai/codex");
  assert.equal(requirementsCopy("hidden", agent, "devl"), undefined);
});

test("requirementsCopy: a team setup's card says its steps run in tmux", () => {
  const linux = parseRequirements({ os: "linux", tmux: { found: false, install: "sudo apt install tmux", manager: "apt" }, agents: [] });
  const c = requirementsCopy("tmux", linux, "devl", { team: true });
  assert.equal(c?.title, "Install tmux on devl");
  assert.match(c?.body ?? "", /^Berth runs the team setup's steps, and later your agents, in tmux on the box\. /);
  assert.equal(c?.command, "sudo apt install tmux");
  assert.match(requirementsCopy("tmux", linux, "devl")?.body ?? "", /^Berth runs agents in tmux/);
});
