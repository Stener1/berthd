// node --experimental-strip-types --test src/lib/login-url.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { loginUrl, loginUserLabel, nextFrom, plausibleEmail, safeNext } from "./login-url.ts";

const origin = "http://checkout.shop.devl.localhost:1377";

test("the link names the user by email and keeps the page to land on", () => {
  assert.equal(loginUrl(origin, "pro@acme.test", "/event-types?x=1"), `${origin}/__berth/login?as=pro%40acme.test&next=%2Fevent-types%3Fx%3D1`);
  assert.equal(loginUrl(`${origin}/`, "a+b@acme.test"), `${origin}/__berth/login?as=a%2Bb%40acme.test&next=%2F`);
});

test("a next that would leave the worktree's host becomes /", () => {
  for (const bad of ["//evil.test", "https://evil.test", "/\\evil.test", "\\\\evil.test", "/%2F%2Fevil.test", "%2F%2Fevil.test", "/%5Cevil.test", "javascript:alert(1)", "evil", "/a\nb", ""]) {
    assert.equal(safeNext(bad), "/", bad);
  }
  assert.equal(safeNext("/settings/billing?tab=2#plan"), "/settings/billing?tab=2#plan");
});

test("logging in keeps the page you were on, only on the same host", () => {
  assert.equal(nextFrom(`${origin}/bookings?status=upcoming`, origin), "/bookings?status=upcoming");
  assert.equal(nextFrom("http://3000.devl.localhost:1377/bookings", origin), "/");
  assert.equal(nextFrom(`${origin}/__berth/login?as=x%40acme.test&next=%2Fteams`, origin), "/teams");
  assert.equal(nextFrom(undefined, origin), "/");
});

test("users show by label, else by email; emails are checked before asking", () => {
  assert.equal(loginUserLabel({ email: "pro@acme.test", label: "Pro user" }), "Pro user");
  assert.equal(loginUserLabel({ email: "free@acme.test" }), "free@acme.test");
  assert.ok(plausibleEmail("qa+1@acme.test"));
  for (const bad of ["a@b", "a b@acme.test", "a@acme.test; rm -rf /", "$(id)@acme.test", "a@acme.test\n"]) assert.ok(!plausibleEmail(bad), bad);
});
