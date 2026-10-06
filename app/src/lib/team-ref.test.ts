// node --experimental-strip-types --test src/lib/team-ref.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { isLink, teamRef } from "./team-ref.ts";

test("an org, however it is typed", () => {
  for (const s of ["calcom", "@calcom", " github.com/calcom ", "https://github.com/calcom", "https://github.com/calcom/.berth", "berth://team?org=calcom"]) assert.equal(teamRef(s), "calcom", s);
});

test("a link to a repo, a ref or a folder", () => {
  const full = "github.com/sean-brydon/berth-kit-calcom/tree/team-setup/team";
  assert.equal(teamRef(`https://${full}`), full);
  assert.equal(teamRef(full), full);
  assert.equal(teamRef("sean-brydon/berth-kit-calcom/tree/team-setup/team"), full);
  assert.equal(teamRef(`berth://team?src=${encodeURIComponent(`https://${full}`)}`), full);
  assert.equal(teamRef("github.com/sean-brydon/berth-kit-calcom@team-setup"), "github.com/sean-brydon/berth-kit-calcom@team-setup");
  assert.equal(teamRef("https://github.com/acme/infra/"), "github.com/acme/infra");
  assert.ok(isLink(teamRef("acme/infra")!));
});

test("what is neither", () => {
  for (const s of ["", "-bad", "a/b/blob/main/x", "a/b@", "a/b@x/tree/y/z", "a/b/tree", "https://gitlab.com/a/b"]) assert.equal(teamRef(s), undefined, s);
});
