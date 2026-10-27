// node --experimental-strip-types --test src/lib/md-links.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { isMarkdown, resolveImage, resolveLink, slug } from "./md-links.ts";

test("a web address opens in the browser", () => {
  assert.deepEqual(resolveLink("docs/a.md", "https://acme.dev/x"), { kind: "web", url: "https://acme.dev/x" });
  assert.deepEqual(resolveLink("docs/a.md", "HTTP://acme.dev"), { kind: "web", url: "HTTP://acme.dev" });
});

test("a path is the file's folder's, and / is the repository's top", () => {
  assert.deepEqual(resolveLink("docs/a.md", "b.md"), { kind: "file", path: "docs/b.md" });
  assert.deepEqual(resolveLink("docs/a.md", "./b.md"), { kind: "file", path: "docs/b.md" });
  assert.deepEqual(resolveLink("docs/guides/a.md", "../b.md#setup"), { kind: "file", path: "docs/b.md", anchor: "setup" });
  assert.deepEqual(resolveLink("docs/a.md", "/README.md"), { kind: "file", path: "README.md" });
  assert.deepEqual(resolveLink("README.md", "src/index.ts?plain=1"), { kind: "file", path: "src/index.ts" });
  assert.deepEqual(resolveLink("README.md", "docs/my%20notes.md"), { kind: "file", path: "docs/my notes.md" });
});

test("a link out of the worktree, or to another scheme, goes nowhere", () => {
  assert.equal(resolveLink("README.md", "../secret.md"), undefined);
  assert.equal(resolveLink("README.md", "mailto:a@acme.dev"), undefined);
  assert.equal(resolveLink("README.md", "javascript:alert(1)"), undefined);
  assert.equal(resolveLink("README.md", "//acme.dev/x"), undefined);
  assert.equal(resolveLink("README.md", ""), undefined);
  assert.equal(resolveLink("README.md", "#"), undefined);
});

test("#a-heading scrolls the page", () => {
  assert.deepEqual(resolveLink("README.md", "#set-up--run"), { kind: "anchor", anchor: "set-up--run" });
});

test("a picture is the worktree's unless it is from the web", () => {
  assert.equal(resolveImage("docs/a.md", "img/flow.png"), "docs/img/flow.png");
  assert.equal(resolveImage("docs/a.md", "../apps/web/public/logo.png?v=2"), "apps/web/public/logo.png");
  assert.equal(resolveImage("docs/a.md", "/logo.png"), "logo.png");
  assert.equal(resolveImage("docs/a.md", "https://acme.dev/badge.svg"), undefined);
  assert.equal(resolveImage("docs/a.md", "data:image/png;base64,AAAA"), undefined);
  assert.equal(resolveImage("README.md", "../../etc/x.png"), undefined);
});

test("slug makes GitHub's heading anchors", () => {
  assert.equal(slug("Set up & run"), "set-up--run");
  assert.equal(slug("  Webhooks  "), "webhooks");
  assert.equal(slug("What's `claimEvent`?"), "whats-claimevent");
  assert.equal(slug("Größe 2"), "größe-2");
});

test("Preview is for Markdown, not MDX", () => {
  assert.ok(isMarkdown("README.md"));
  assert.ok(isMarkdown("docs/x.MARKDOWN"));
  assert.ok(!isMarkdown("docs/x.mdx"));
  assert.ok(!isMarkdown("src/md.ts"));
});
