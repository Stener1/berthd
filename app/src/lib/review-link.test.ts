// node --experimental-strip-types --test src/lib/review-link.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { parseReviewLink, parseReviewRef, reviewLink } from "./review-link.ts";

test("a review link is a repository and a PR number", () => {
  assert.deepEqual(parseReviewLink("berth://review?repo=acme/shop&pr=42"), {
    repo: "acme/shop",
    pr: 42,
  });
  assert.deepEqual(parseReviewLink(" berth://review?pr=7&repo=acme/shop.app "), { repo: "acme/shop.app", pr: 7 });
  assert.deepEqual(parseReviewLink("berth://review?repo=acme%2Fshop&pr=42"), {
    repo: "acme/shop",
    pr: 42,
  });
  // The head commit is a hint, kept lowercase.
  assert.deepEqual(parseReviewLink("berth://review?repo=acme/shop&pr=42&sha=9F3C1A2"), { repo: "acme/shop", pr: 42, sha: "9f3c1a2" });
});

test("anything else in a link makes it no link", () => {
  for (const s of [
    "",
    "berth://review",
    "berth://review?",
    "berth://team?org=acme",
    "https://github.com/acme/shop/pull/42",
    "berth://review?repo=acme/shop",
    "berth://review?pr=42",
    "berth://review?repo=acme/shop&pr=0",
    "berth://review?repo=acme/shop&pr=01",
    "berth://review?repo=acme/shop&pr=-1",
    "berth://review?repo=acme/shop&pr=+4",
    "berth://review?repo=acme/shop&pr=1e3",
    "berth://review?repo=acme/shop&pr=4.2",
    "berth://review?repo=acme/shop&pr=99999999999",
    "berth://review?repo=acme/shop&pr=42&pr=43",
    "berth://review?repo=acme/shop&repo=evil/x&pr=42",
    "berth://review?repo=acme/shop&pr=42&cmd=rm",
    "berth://review?repo=acme/shop&pr=42&setup=curl%20x%7Csh",
    "berth://review?repo=acme/shop&pr=42&env=TOKEN",
    "berth://review?repo=acme/shop&pr=42&",
    "berth://review?repo=acme/shop&pr=42#frag",
    "berth://review?repo=acme/shop;rm%20-rf%20~&pr=42",
    "berth://review?repo=acme/shop;rm -rf&pr=42",
    "berth://review?repo=../x&pr=42",
    "berth://review?repo=acme/..&pr=42",
    "berth://review?repo=acme/shop/extra&pr=42",
    "berth://review?repo=acme&pr=42",
    "berth://review?repo=acme/sh%20op&pr=42",
    "berth://review?repo=acme/$(id)&pr=42",
    "berth://review?repo=acme/shop%0a&pr=42",
    "berth://review?repo=acme/shop&pr=42&sha=xyz",
    "berth://review?repo=acme/shop&pr=42&sha=abc",
    "berth://review?repo=acme/shop&pr=42&sha=%E0%A4%A",
  ]) {
    assert.equal(parseReviewLink(s), undefined, s);
  }
});

test("OWNER/NAME#N, as the CLI takes it, or a link", () => {
  assert.deepEqual(parseReviewRef("acme/shop#42"), {
    repo: "acme/shop",
    pr: 42,
  });
  assert.deepEqual(parseReviewRef("berth://review?repo=acme/shop&pr=42"), {
    repo: "acme/shop",
    pr: 42,
  });
  for (const s of ["acme/shop", "acme/shop#", "acme/shop#0", "acme/shop#42x", "acme#42", "acme/shop #42", "acme/sh;op#42", "../x#1"]) assert.equal(parseReviewRef(s), undefined, s);
});

test("the link someone shares reads back as itself", () => {
  const link = reviewLink("acme/shop", 42);
  assert.equal(link, "berth://review?repo=acme/shop&pr=42");
  assert.deepEqual(parseReviewLink(link), { repo: "acme/shop", pr: 42 });
});
