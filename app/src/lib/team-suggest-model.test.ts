// node --experimental-strip-types --test src/lib/team-suggest-model.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { homeCards, projectsLine, putAway, reviewBox, sentence, setsUpLine, suggestionFor, type TeamSuggestion, teamLink, visibleSuggestions } from "./team-suggest-model.ts";

const acme: TeamSuggestion = {
  org: "acme",
  name: "Acme",
  repo: "acme/.berth",
  projects: [
    { box: "devl", location: "shop", repo: "acme/shop", listed: true, sets_up: ["per-worktree databases", "services", "Log in as…", "review links"] },
    { box: "devl", location: "old", repo: "acme/old", listed: false, sets_up: [] },
  ],
};
const northwind: TeamSuggestion = { org: "Northwind", name: "", repo: "Northwind/.berth", projects: [{ box: "gpu", location: "store", repo: "Northwind/store", listed: true, sets_up: [] }] };

test("accepted and turned-down orgs are left out, whatever their case", () => {
  assert.deepEqual(visibleSuggestions([acme, northwind], ["ACME"], []), [northwind]);
  assert.deepEqual(visibleSuggestions([acme, northwind], [], ["northwind"]), [acme]);
  assert.deepEqual(visibleSuggestions(undefined, [], []), []);
  // Nothing left out: the same list, so nothing redraws.
  const list = [acme, northwind];
  assert.equal(visibleSuggestions(list, [], []), list);
});

test("a project finds its org's suggestion", () => {
  assert.equal(suggestionFor([acme, northwind], "devl", "shop")?.suggestion, acme);
  assert.equal(suggestionFor([acme, northwind], "gpu", "store")?.project.repo, "Northwind/store");
  assert.equal(suggestionFor([acme], "gpu", "shop"), undefined);
});

test("Not now puts away the Home card for that org only", () => {
  let prefs = { seen: [] as string[] };
  assert.deepEqual(homeCards([acme, northwind], prefs), [acme, northwind]);
  prefs = putAway(prefs, "Acme");
  assert.deepEqual(homeCards([acme, northwind], prefs), [northwind]);
  assert.equal(putAway(prefs, "acme"), prefs);
  prefs = putAway(prefs, "Northwind");
  assert.deepEqual(homeCards([acme, northwind], prefs), []);
});

test("Project settings' line says what the setup declares for the repo", () => {
  assert.equal(setsUpLine(acme, acme.projects[0]), "acme/.berth sets this project up: per-worktree databases, services, Log in as…, review links");
  assert.equal(setsUpLine(acme, acme.projects[1]), "acme/.berth doesn't list this repo, but sets your box up the way Acme's are");
  assert.equal(setsUpLine(northwind, northwind.projects[0]), "Northwind/.berth lists this project, and sets your box up the way Northwind's are");
});

test("a line ending in Log in as… gets no second full stop", () => {
  assert.equal(sentence("Sets up services and Log in as…"), "Sets up services and Log in as…");
  assert.equal(sentence("Sets up services"), "Sets up services.");
});

test("projects are named with their box", () => {
  assert.equal(projectsLine(acme), "shop and old on devl");
  assert.equal(projectsLine(northwind), "store on gpu");
  const spread = { ...acme, projects: [acme.projects[0], { ...northwind.projects[0], location: "web" }] };
  assert.equal(projectsLine(spread), "shop on devl and web on gpu");
  const many = { ...acme, projects: ["a", "b", "c", "d"].map((l) => ({ ...acme.projects[1], location: l })) };
  assert.equal(projectsLine(many), "a, b and 2 more on devl");
});

test("Review opens on the project's own box, and the link names the org", () => {
  assert.equal(reviewBox(acme), "devl");
  assert.equal(reviewBox(acme, "devl"), "devl");
  assert.equal(reviewBox(acme, "elsewhere"), "devl");
  assert.equal(teamLink("acme"), "berth://team?org=acme");
});
