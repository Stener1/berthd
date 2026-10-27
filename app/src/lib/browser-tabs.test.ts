// node --experimental-strip-types --test src/lib/browser-tabs.test.ts (pnpm test)
import assert from "node:assert/strict";
import { test } from "node:test";

import { type AgentBrowserTab, chooseTab, mostRecentTab, noteTabs, showTabStrip, tabActive, tabMovedNote, tabQuery } from "./browser-tabs.ts";

const T0 = Date.parse("2026-10-13T10:00:00Z");
const tab = (name: string, secondsAfter: number, extra: Partial<AgentBrowserTab> = {}): AgentBrowserTab => ({
  name,
  last_used: new Date(T0 + secondsAfter * 1000).toISOString(),
  watchers: 0,
  viewport: { width: 1920, height: 1080, scale: 1 },
  size: "1920×1080",
  ...extra,
});

const tabs = [tab("", 10), tab("signup-a", 30), tab("signup-b", 20), tab("onboarding", 5)];

test("the view opens on the most recently used tab", () => {
  assert.deepEqual(chooseTab(tabs, undefined), { name: "signup-a" });
  assert.equal(mostRecentTab([tab("x", 1), tab("y", 1)])?.name, "x", "a tie goes to the earlier tab in the box's order");
});

test("the watched tab stays while it exists, whatever the others do", () => {
  assert.deepEqual(chooseTab(tabs, "onboarding"), { name: "onboarding" });
  const busier = tabs.map((t) => (t.name === "signup-b" ? { ...t, last_used: new Date(T0 + 99_000).toISOString(), busy: true } : t));
  assert.deepEqual(chooseTab(busier, "onboarding"), { name: "onboarding" });
  assert.deepEqual(chooseTab(tabs, ""), { name: "" });
});

test("when the watched tab closes, the view moves to the most recent of the rest and says so", () => {
  const rest = tabs.filter((t) => t.name !== "signup-a");
  assert.deepEqual(chooseTab(rest, "signup-a"), { name: "signup-b", movedFrom: "signup-a" });
  assert.equal(tabMovedNote("signup-a", "signup-b"), "signup-a closed. Watching signup-b.");
  // Main drops out of the list while unused, which isn't closing.
  assert.equal(tabMovedNote("", "signup-b"), "Watching signup-b, the tab in use.");
});

test("no list keeps what is watched, and an old box watches main", () => {
  assert.deepEqual(chooseTab(undefined, "signup-a"), { name: "signup-a" });
  assert.equal(chooseTab(undefined, undefined), undefined);
  assert.deepEqual(chooseTab([], undefined), undefined);
});

test("the strip shows only when there is a choice", () => {
  assert.equal(showTabStrip(undefined), false);
  assert.equal(showTabStrip([tab("", 0)]), false);
  assert.equal(showTabStrip([tab("signup-a", 0)]), true);
  assert.equal(showTabStrip(tabs), true);
});

test("a tab's query is only there for a named tab, encoded", () => {
  assert.equal(tabQuery(""), "");
  assert.equal(tabQuery(undefined), "");
  assert.equal(tabQuery("sign up/a"), "?tab=sign%20up%2Fa");
});

test("activity: busy, recently used by its clock, or seen to change when the clocks differ", () => {
  const now = T0 + 31_000;
  assert.equal(tabActive(tabs[1], {}, now), true, "used a second ago");
  assert.equal(tabActive(tabs[2], {}, now), false, "used eleven seconds ago");
  assert.equal(tabActive({ ...tabs[2], busy: true }, {}, now), true);
  // A box whose clock is a minute behind: its times look old, but a change
  // between polls still shows.
  let seen = noteTabs({}, tabs, now);
  assert.equal(seen["signup-b"].changedAt, 0, "the first poll has nothing to compare with");
  const moved = tabs.map((t) => (t.name === "signup-b" ? { ...t, last_used: new Date(T0 - 60_000).toISOString() } : t));
  seen = noteTabs(seen, moved, now + 2000);
  assert.equal(tabActive(moved[2], seen, now + 2500), true);
  assert.equal(tabActive(moved[2], seen, now + 2000 + 3000), false, "and it fades after a few seconds");
  // A clock ahead of this one doesn't make a tab look busy forever.
  assert.equal(tabActive(tab("ahead", 120), {}, now), false);
});

test("a box that says how long a tab has been idle is believed over the clocks", () => {
  // Its clock is an hour behind this one: last_used alone would say idle.
  const behind = (name: string, idle: number) => tab(name, -3600, { idle_ms: idle });
  const list = [behind("", 60_000), behind("signup-a", 500), behind("signup-b", 9000)];
  assert.equal(tabActive(list[1], {}, Date.now()), true);
  assert.equal(tabActive(list[2], {}, Date.now()), false);
  assert.equal(mostRecentTab(list)?.name, "signup-a");
  assert.deepEqual(chooseTab(list, undefined), { name: "signup-a" });
});
