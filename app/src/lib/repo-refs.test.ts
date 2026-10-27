// node --experimental-strip-types --test src/lib/repo-refs.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { findRefs, githubRepo, wholeRef } from "./repo-refs.ts";

const REPO = "acme/shop";
// Each ref's text and where it goes.
const refs = (text: string, repo: string | undefined = REPO) => findRefs(text, repo).map((r) => [text.slice(r.start, r.end), r.url]);
const linked = (text: string) => refs(text).map(([t]) => t);

test("#123 is the chat's repository's issue or pull request", () => {
  assert.deepEqual(refs("Fixed in #123."), [["#123", "https://github.com/acme/shop/issues/123"]]);
  assert.deepEqual(linked("(#1), #42, and #9041: done"), ["#1", "#42", "#9041"]);
  assert.deepEqual(linked("#7 at the start"), ["#7"]);
  assert.deepEqual(linked("Closes #12!"), ["#12"]);
});

test("owner/repo#45 is that repository's", () => {
  assert.deepEqual(refs("See acme/shop#45 and acme-labs/web.app#7."), [
    ["acme/shop#45", "https://github.com/acme/shop/issues/45"],
    ["acme-labs/web.app#7", "https://github.com/acme-labs/web.app/issues/7"],
  ]);
});

test("a commit's hash: 7 to 40 lowercase hex, at least one digit and one a–f", () => {
  assert.deepEqual(refs("Landed in a1b2c3d."), [["a1b2c3d", "https://github.com/acme/shop/commit/a1b2c3d"]]);
  const full = "9f3c1a2b4d5e6f708192a3b4c5d6e7f809a1b2c3";
  assert.deepEqual(refs(`on ${full}`), [[full, `https://github.com/acme/shop/commit/${full}`]]);
  assert.deepEqual(linked("abc1234, 1234abc and 0e1f2a3b"), ["abc1234", "1234abc", "0e1f2a3b"]);
});

test("what isn't a ref stays text", () => {
  for (const text of [
    // Colours, a line in a file, C#, headings' marks, #0 and leading zeros.
    "color: #fff; background: #ff0000aa; #123abc",
    "src/app.ts#L14 and #L14",
    "Written in C# and F#, C#12",
    "## 12 things",
    "##12",
    "#0 and #007",
    // Run into a word, a path, a URL or an entity.
    "page#123 and a/b/c#4 and v1.2#3 and &#123; and x#12-y and #12-col",
    "https://example.com/acme/shop#45",
    "example.com/issues#12",
    "#12.5 and #12/files",
    // Letters only, digits only, too short, too long, uppercase.
    "deadbeef cafe12 1234567 abc123 ABC1234",
    "sha256:9f3c1a2b4d5e6f708192a3b4c5d6e7f809a1b2c3d4e5f60718293a4b5c6d7e8f",
    // A UUID's parts, a hash run into a path, a package's version, a file.
    "550e8400-e29b-41d4-a716-446655440000",
    "migrations/1a2b3c4d_init.sql and a1b2c3d.js and pkg@a1b2c3d and 0x1a2b3c4d",
    "café#12",
  ]) {
    assert.deepEqual(linked(text), [], text);
  }
});

test("without a GitHub repository, nothing links", () => {
  assert.deepEqual(findRefs("#123 acme/shop#45 a1b2c3d", undefined), []);
  assert.deepEqual(findRefs("#123", ""), []);
  assert.deepEqual(findRefs("#123", "not a repo"), []);
  assert.deepEqual(findRefs("#123", "../etc"), []);
});

test("inline code links only when it is a ref and nothing else", () => {
  assert.equal(wholeRef("a1b2c3d", REPO)?.url, "https://github.com/acme/shop/commit/a1b2c3d");
  assert.equal(wholeRef("#123", REPO)?.url, "https://github.com/acme/shop/issues/123");
  assert.equal(wholeRef("acme/billing#9", REPO)?.url, "https://github.com/acme/billing/issues/9");
  for (const code of ["git show a1b2c3d", "#123 and #4", " #123", "#fff", "deadbeef"]) assert.equal(wholeRef(code, REPO), undefined, code);
});

test("a GitHub origin in any form is its owner/name; any other host is none", () => {
  for (const remote of [
    "git@github.com:acme/shop.git",
    "git@github.com:acme/shop",
    "https://github.com/acme/shop.git",
    "https://github.com/acme/shop",
    "https://github.com/acme/shop/",
    "ssh://git@github.com/acme/shop.git",
    "ssh://git@ssh.github.com:443/acme/shop.git",
    "git@github.com-work:acme/shop.git",
    "https://GitHub.com/acme/shop.git",
  ]) {
    assert.equal(githubRepo(remote), "acme/shop", remote);
  }
  for (const remote of [undefined, "", "git@gitlab.com:acme/shop.git", "https://git.acme.dev/acme/shop.git", "https://github.com/acme", "https://github.com/acme/shop/tree/main", "/home/me/shop", "not a url"]) {
    assert.equal(githubRepo(remote), undefined, String(remote));
  }
});
