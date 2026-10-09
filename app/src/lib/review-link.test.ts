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

test("a link can ask to open logged in as a dev user, at a page", () => {
  assert.deepEqual(parseReviewLink("berth://review?repo=acme/web&pr=7&as=pro@acme.test&path=/event-types"), { repo: "acme/web", pr: 7, as: "pro@acme.test", path: "/event-types" });
  assert.deepEqual(parseReviewLink("berth://review?repo=acme/web&pr=7&as=pro%40acme.test&path=%2Forders%3Ftab%3D2"), { repo: "acme/web", pr: 7, as: "pro@acme.test", path: "/orders?tab=2" });
  assert.deepEqual(parseReviewLink("berth://review?repo=acme/web&pr=7&path=/"), { repo: "acme/web", pr: 7, path: "/" });
  assert.deepEqual(parseReviewLink("berth://review?repo=acme/web&pr=7&as=team.lead%2Bqa@acme.test"), { repo: "acme/web", pr: 7, as: "team.lead+qa@acme.test" });
  // The link reviewLink makes reads back the same; the default has neither.
  for (const open of [{ as: "team.lead+qa@acme.test", path: "/orders?tab=2" }, { as: "pro@acme.test" }, { path: "/event-types" }]) {
    const back = parseReviewLink(reviewLink("acme/web", 7, open));
    assert.equal(back?.as, open.as);
    assert.equal(back?.path, open.path);
  }
  assert.equal(reviewLink("acme/web", 7, { as: "team.lead+qa@acme.test", path: "/orders?tab=2" }), "berth://review?repo=acme/web&pr=7&as=team.lead%2Bqa@acme.test&path=/orders%3Ftab%3D2");
  assert.equal(reviewLink("acme/web", 7), "berth://review?repo=acme/web&pr=7");
});

test("as and path refuse anything else, and the whole link with them", () => {
  const base = "berth://review?repo=acme/web&pr=7&";
  for (const q of [
    "as=a@b.c;rm",
    "as=a@b.c%3Brm%20-rf",
    "as=a@b.c%0Aecho",
    "as=a@b.c%0D%0Aecho",
    "as=-x@acme.test",
    "as=pro",
    "as=pro@acme",
    "as=pro@acme.test&as=admin@acme.test",
    "as=",
    `as=${"a".repeat(250)}@acme.test`,
    "as=pro@acme.test%00",
    "as=%60id%60@acme.test",
    "path=//evil.example",
    "path=%2F%2Fevil.example",
    "path=/%2F%2Fevil.example",
    "path=/%252F%252Fevil.example",
    "path=https://evil.example",
    "path=https%3A%2F%2Fevil.example",
    "path=event-types",
    "path=/%5Cevil.example",
    "path=/a%0Ab",
    "path=/a%09b",
    "path=/a%20b",
    "path=/x&path=/y",
    "path=",
    `path=/${"a".repeat(512)}`,
    "path=/x#frag",
    "as=pro@acme.test#x",
    "login=pro@acme.test",
  ]) {
    assert.equal(parseReviewLink(base + q), undefined, q);
  }
});
