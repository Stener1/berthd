import type { Page } from "@playwright/test";

import { expect, mockOnly, test } from "./fixtures";

// Team suggestions (lib/team-suggest.ts, internal/agent/teamsuggest.go):
// the laptop noticed that acme, which devl's shop comes from, publishes a
// team setup the person hasn't accepted (?teamsuggest=1). The app says so
// quietly: a mark on the project's row and a line in its menu, a line in
// Project settings, ⌘K, and a card on Home until Not now. Review… opens
// Team setup on devl, using shop as it is.

test.beforeEach(() => mockOnly("team suggestions are mock fixtures"));

const PARAMS = { teamsuggest: "1" };
const mark = (page: Page) => page.getByTestId("team-suggest-mark");
const card = (page: Page) => page.getByTestId("team-suggest-card");

async function shopMenu(page: Page) {
  await mark(page).locator("xpath=ancestor::button[1]").click({ button: "right" });
  await expect(page.getByRole("menu")).toBeVisible();
}

async function expectTeamPageUsingShop(page: Page) {
  await expect(page.getByRole("heading", { name: "Acme team setup" })).toBeVisible();
  await expect(page.getByTestId("team-suggested")).toContainText("Suggested because your projects come from acme");
  // On shop's own box, which already has shop: used as it is.
  await expect(page.getByTestId("team-checklist").locator("visible=true").getByRole("combobox")).toContainText("devl");
  const found = page.getByTestId("found-shop");
  await expect(found).toContainText("Found ~/work/shop, a clone of acme/shop");
  await expect(found).toContainText("the Shipyard project shop");
  await expect(page.getByTestId("use-shop")).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByTestId("found-review-shop")).toContainText("It is the Shipyard project shop already");
}

test("a project whose org has a team setup gets a quiet mark, and its menu's Review… opens it using the project", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await expect(mark(page)).toHaveCount(1);
  await expect(mark(page)).toHaveAttribute("aria-label", "Acme has a team setup");
  await shopMenu(page);
  await expect(page.getByRole("menuitem", { name: /Don't suggest again for acme/ })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: /Copy team setup link/ })).toBeVisible();
  await page.getByRole("menuitem", { name: /Acme has a team setup.*Review…/ }).click();
  await expectTeamPageUsingShop(page);
});

test("Copy team setup link copies berth://team?org=acme to send a teammate", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await shopMenu(page);
  await page.getByRole("menuitem", { name: /Copy team setup link/ }).click();
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe("berth://team?org=acme");
});

test("Project settings says what acme/.berth sets the project up with, and Not for me can be undone", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await shopMenu(page);
  // shop is on devl and gpu: its settings are per box.
  await page.getByRole("menuitem", { name: /^Project settings/ }).click();
  await page.getByRole("menuitem", { name: "devl", exact: true }).locator("visible=true").last().click();
  const line = page.getByTestId("team-suggest-line");
  await expect(line).toContainText("acme/.berth sets this project up: per-worktree databases, services, Log in as…, review links");
  await line.getByRole("button", { name: "Not for me" }).click();
  // Every hint for acme goes.
  await expect(line).toHaveCount(0);
  await expect(mark(page)).toHaveCount(0);
  await page.getByRole("button", { name: "Undo" }).click();
  await expect(page.getByTestId("team-suggest-line")).toBeVisible();
  await expect(mark(page)).toHaveCount(1);
  await page.getByTestId("team-suggest-line").getByRole("button", { name: "Review…" }).click();
  await expectTeamPageUsingShop(page);
});

test("Home shows the suggestion once: Not now puts the card away for good and keeps the sidebar's mark", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await expect(card(page)).toContainText("Acme has a team setup");
  await expect(card(page)).toContainText("For shop on devl, in acme/.berth. Nothing runs until you review it.");
  await card(page).getByRole("button", { name: "Not now" }).click();
  await expect(card(page)).toHaveCount(0);
  await expect(mark(page)).toHaveCount(1);
  expect(await app.stored("berth.teamSuggest")).toEqual({ seen: ["acme"] });
  await page.reload();
  await expect(mark(page)).toHaveCount(1);
  await expect(card(page)).toHaveCount(0);
});

test("Home's Review… opens the team setup on the project's box", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await card(page).getByRole("button", { name: "Review…" }).click();
  await expectTeamPageUsingShop(page);
});

test("Don't suggest again for acme hides every hint for the org, ⌘K's too", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await expect(card(page)).toBeVisible();
  const search = page.getByPlaceholder("Jump to a session, worktree, or command…");
  await page.getByRole("button", { name: /^Search/ }).click();
  await search.fill("team setup for");
  await expect(page.getByRole("option", { name: /Team setup for acme/ })).toBeVisible();
  await page.keyboard.press("Escape");

  await shopMenu(page);
  await page.getByRole("menuitem", { name: /Don't suggest again for acme/ }).click();
  await expect(mark(page)).toHaveCount(0);
  await expect(card(page)).toHaveCount(0);
  await page.getByRole("button", { name: /^Search/ }).click();
  await search.fill("team setup");
  await expect(page.getByRole("option", { name: /Team setup…/ })).toBeVisible();
  await expect(page.getByRole("option", { name: /Team setup for acme/ })).toHaveCount(0);
});

test("⌘K's Team setup for acme opens it on the project's box", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await page.getByRole("button", { name: /^Search/ }).click();
  await page.getByPlaceholder("Jump to a session, worktree, or command…").fill("team setup for acme");
  await page.getByRole("option", { name: /Team setup for acme/ }).click();
  await expectTeamPageUsingShop(page);
});

test("without a suggestion, nothing about team setups shows on projects", async ({ app }) => {
  const { page } = app;
  await app.open();
  await expect(page.getByTestId("home-grid")).toBeVisible();
  await expect(mark(page)).toHaveCount(0);
  await expect(card(page)).toHaveCount(0);
});

// Just the kit (views/team/team-kit-sheet.tsx): the project's kit from the
// team setup, without its box steps, sudo, clone or 1Password.
const sheet = (page: Page) => page.getByTestId("team-kit-sheet");

test("just the kit: the sheet lists what the kit needs, keys and init stay off, and only the kit is applied", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await shopMenu(page);
  await page.getByRole("menuitem", { name: /Use acme's kit for this project…/ }).click();
  await expect(page.getByRole("heading", { name: "Use Acme's kit for shop" })).toBeVisible();
  await expect(page.getByTestId("team-kit-requires")).toContainText("pg_dump missing: the full team setup installs it");
  await expect(page.getByTestId("team-kit-requires")).toContainText("node found");
  await expect(sheet(page)).toContainText("Your checkout stays as it is.");
  await expect(page.getByTestId("team-kit-keys")).not.toBeChecked();
  await expect(page.getByTestId("team-kit-init")).not.toBeChecked();
  await expect(sheet(page)).toContainText("Used by");
  await page.getByTestId("team-kit-apply").click();
  await expect(page.getByText("shop follows Acme's kit")).toBeVisible();
  // Only the kit: no keys, no init, and no team setup run (no steps, no
  // sudo terminal).
  await expect.poll(() => page.evaluate(() => (window as unknown as { __teamKits?: unknown[] }).__teamKits)).toEqual([{ box: "devl", location: "shop", keys: false, init: false }]);
  expect(await page.evaluate(() => Object.keys((window as unknown as { __teamMock: { runs: object } }).__teamMock.runs).length)).toBe(0);
  // The project follows the kit now: the menu no longer offers it, and
  // Project settings says so; the full setup is still a Review away.
  await shopMenu(page);
  await expect(page.getByRole("menuitem", { name: /Acme has a team setup/ })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: /kit for this project/ })).toHaveCount(0);
  await page.getByRole("menuitem", { name: /^Project settings/ }).click();
  await page.getByRole("menuitem", { name: "devl", exact: true }).locator("visible=true").last().click();
  await expect(page.getByTestId("team-suggest-line")).toContainText("This project follows its kit");
  await expect(page.getByTestId("team-suggest-kit")).toHaveCount(0);
});

test("the shared keys and the first-time setup are each a choice", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await shopMenu(page);
  await page.getByRole("menuitem", { name: /Use acme's kit for this project…/ }).click();
  await page.getByTestId("team-kit-keys").click();
  await page.getByTestId("team-kit-init").click();
  await page.getByTestId("team-kit-apply").click();
  await expect.poll(() => page.evaluate(() => (window as unknown as { __teamKits?: unknown[] }).__teamKits)).toEqual([{ box: "devl", location: "shop", keys: true, init: true }]);
});

test("the Team setup page offers everything, or just the kit for a project you have", async ({ app }) => {
  const { page } = app;
  await app.open({ params: PARAMS });
  await card(page).getByRole("button", { name: "Review…" }).click();
  const choices = page.getByTestId("team-kit-choices");
  await expect(choices).toContainText("You have shop on devl already");
  await expect(page.getByTestId("kit-choice-shop").getByRole("button", { name: "Set up everything" })).toHaveAttribute("aria-pressed", "true");
  await page.getByTestId("kit-choice-shop").getByRole("button", { name: "Just the kit for shop…" }).click();
  await expect(page.getByRole("heading", { name: "Use Acme's kit for shop" })).toBeVisible();
});
