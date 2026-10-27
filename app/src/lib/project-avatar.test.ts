// node --experimental-strip-types --test src/lib/project-avatar.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { avatarUrl, githubOwner } from "./project-avatar.ts";

test("a GitHub repository's owner comes from its slug, or its remote", () => {
  assert.equal(githubOwner("acme/shop", "git@github.com:acme/shop.git"), "acme");
  assert.equal(githubOwner("acme/shop", "https://github.com/acme/shop.git"), "acme");
  assert.equal(githubOwner(undefined, "https://github.com/acme-labs/shop"), "acme-labs");
  assert.equal(githubOwner(undefined, "ssh://git@github.com/acme/shop.git"), "acme");
});

test("no remote, or another forge, has no owner", () => {
  assert.equal(githubOwner("acme/shop", undefined), undefined);
  assert.equal(githubOwner(undefined, undefined), undefined);
  assert.equal(githubOwner("acme/shop", "git@gitlab.com:acme/shop.git"), undefined);
  assert.equal(githubOwner("acme/shop", "https://notgithub.com/acme/shop"), undefined);
  // Not a login: never put into a URL.
  assert.equal(githubOwner("../x/shop", "https://github.com/a b/shop"), undefined);
});

test("the avatar URL names the owner and a size", () => {
  assert.equal(avatarUrl("acme"), "https://avatars.githubusercontent.com/acme?s=96");
  assert.equal(avatarUrl("acme", 64), "https://avatars.githubusercontent.com/acme?s=64");
});
