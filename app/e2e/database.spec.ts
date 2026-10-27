import type { Locator, Page } from "@playwright/test";

import { type App, expect, mockOnly, test } from "./fixtures";

// Database (plugins/database): a worktree's own development database as a
// panel on the worktree, read from a helper on its box (berth-database). In
// mock mode the plugin is the listing's own bundle, installed and allowed,
// and the helper is src/lib/mock-database.ts: a made-up "acme" shop kept in
// memory, answering in the real helper's fields, codes and sentences
// (internal/database/API.md). It runs on devl and is off on gpu. shop / checkout-fix has two
// connections (a Postgres, and "events" on an engine nobody has heard of),
// shop's main checkout has the Postgres alone, and no other worktree has a
// database yet.

const HISTORY = "berth.plugin.database.history:devl/shop/checkout-fix";

// openPanel opens the Database panel as a tab of the worktree in front. The
// "+" menu lists it once the plugin has loaded.
async function openPanel(app: App): Promise<Locator> {
  const option = app.page.getByRole("option", { name: /^Database/ });
  await expect(async () => {
    if (!(await option.isVisible())) await app.page.getByRole("button", { name: "New tab" }).click();
    await expect(option).toBeVisible({ timeout: 1000 });
  }).toPass();
  await option.click();
  const p = app.page.locator("[data-testid=database]:visible");
  await expect(p).toBeVisible();
  return p;
}

// open is the panel on shop / checkout-fix's Postgres, with a table open.
async function open(app: App, table?: string, params?: Record<string, string>): Promise<Locator> {
  await app.open({ params });
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app);
  if (table) await openTable(p, table);
  return p;
}

async function openTable(p: Locator, table: string) {
  await p.getByTestId("database-table").filter({ has: p.page().getByText(table, { exact: true }) }).click();
  await expect(p.getByTestId("database-table-bar").getByRole("heading")).toContainText(table);
  await expect(p.getByTestId("database-row").first()).toBeVisible();
}

// The tables and the console are both kept while one is shown, so what is
// asked for is what is on screen.
const rows = (p: Locator) => p.locator("[data-testid=database-row]:visible");
const notice = (p: Locator) => p.locator("[data-testid=database-notice]:visible");
// A cell by the row's place on screen and its column. Only the rows near
// the view are in the page, so a row far down has to be scrolled to first.
const cell = (p: Locator, row: number, column: string) => rows(p).nth(row).locator(`[data-col="${column}"]`);
const column = (p: Locator, name: string) => p.locator(`[data-testid=database-row]:visible [data-col="${name}"]`);
const range = (p: Locator) => p.getByTestId("database-range");
const lock = (p: Locator) => p.getByTestId("database-lock");
const mode = (p: Locator, name: string) => p.getByRole("group", { name: "Browse the tables, or run SQL" }).getByRole("button", { name, exact: true });

async function unlock(app: App, p: Locator) {
  await p.getByRole("button", { name: "Unlock writes…" }).first().click();
  await app.page.getByRole("alertdialog").getByRole("button", { name: "Unlock for 15 minutes" }).click();
  await expect(lock(p)).toHaveAttribute("data-writes", "on");
}

// edit types a value into a cell and keeps it.
async function edit(page: Page, target: Locator, text: string) {
  await target.dblclick();
  const input = target.getByRole("textbox");
  await expect(input).toBeFocused();
  await input.fill(text);
  await input.press("Enter");
}

// run puts a statement in the console's editor, alone, and runs it.
async function run(app: App, p: Locator, sql: string) {
  const editor = p.getByRole("textbox", { name: "SQL to run" });
  await editor.click();
  await app.page.keyboard.press("ControlOrMeta+a");
  await app.page.keyboard.type(sql);
  await app.page.keyboard.press("ControlOrMeta+Enter");
}

test("Database lists a worktree's tables by schema with how many rows each has, and filters them", async ({ app }) => {
  mockOnly();
  const p = await open(app);
  await expect(p).toHaveAttribute("data-state", "running");
  const tree = p.getByTestId("database-tree");
  await expect(tree.getByTestId("database-table")).toHaveCount(9);
  await expect(tree.getByRole("heading", { name: "public" })).toBeVisible();
  await expect(tree.getByRole("heading", { name: "analytics" })).toBeVisible();
  await expect(tree.getByTestId("database-table").filter({ hasText: "orders" })).toContainText("~1.3k");
  await expect(tree.getByTestId("database-table").filter({ hasText: "customer_totals" })).toContainText("view");
  await expect(tree.getByTestId("database-table").filter({ hasText: "daily_revenue" })).toContainText("mat. view");
  await expect(notice(p)).toContainText("Pick a table");
  // Locked until someone unlocks it.
  await expect(lock(p)).toHaveAttribute("data-writes", "off");

  await tree.getByRole("searchbox", { name: "Filter tables" }).fill("ord");
  await expect(tree.getByTestId("database-table")).toHaveCount(2);
  await tree.getByRole("searchbox", { name: "Filter tables" }).fill("analytics.");
  await expect(tree.getByTestId("database-table")).toHaveText([/page_views/]);
  await tree.getByRole("searchbox", { name: "Filter tables" }).fill("no such table");
  await expect(tree).toContainText("No table matches “no such table”.");
});

test("a table's rows show NULL as NULL and the empty string as empty, and a long or cut value opens beside the grid", async ({ app }) => {
  mockOnly();
  const p = await open(app, "customers");
  await expect(range(p)).toHaveText("Rows 1–100 of about 240");
  await expect(p.getByRole("grid", { name: "Rows of customers" })).toHaveAttribute("aria-rowcount", "101");
  // Each column says its type under its name.
  await expect(p.getByRole("columnheader", { name: /^email/ })).toContainText("text");
  await expect(p.getByRole("columnheader", { name: /^credit/ })).toContainText("numeric(10,2)");
  // The first three customers: no note, an empty note, a note.
  await expect(cell(p, 0, "notes")).toHaveText("NULL");
  await expect(cell(p, 1, "notes")).toHaveText("empty");
  await expect(cell(p, 2, "notes")).toContainText("Prefers email.");
  // A numeric arrives as text and keeps its digits; a boolean is a word.
  await expect(cell(p, 0, "credit")).toHaveText(/^\d+\.\d\d$/);
  await expect(cell(p, 0, "marketing_opt_in")).toHaveText("false");

  await openTable(p, "products");
  // Locked, a cell opens to be read.
  await cell(p, 0, "description").getByRole("button", { name: "Show the whole value of description" }).click();
  const sheet = app.page.getByTestId("database-value");
  await expect(sheet).toContainText("Made in small batches for Acme");
  await expect(sheet).toContainText(/1,0\d\d characters/);
  await expect(sheet.getByRole("textbox")).toHaveCount(0);
  await sheet.getByRole("button", { name: "Close" }).first().click();
  await expect(sheet).toHaveCount(0);
  // The second product's description is over 4,000 characters: it arrives cut, and says so.
  await cell(p, 1, "description").getByRole("button", { name: "Show the whole value of description" }).click();
  await expect(app.page.getByTestId("database-cut")).toContainText("Only the first 4,000 characters were read");
});

test("a heading orders the rows by its column: up, down, then as the table gives them", async ({ app }) => {
  mockOnly();
  const p = await open(app, "orders");
  const head = p.getByRole("columnheader", { name: /^total_cents/ });
  const totals = async () => (await column(p, "total_cents").allTextContents()).slice(0, 8).map(Number);
  await head.getByRole("button", { name: "total_cents", exact: true }).click();
  await expect(head).toHaveAttribute("aria-sort", "ascending");
  await expect.poll(async () => (await totals()).every((n, i, all) => i === 0 || all[i - 1] <= n)).toBe(true);
  const least = (await totals())[0];
  await head.getByRole("button", { name: "total_cents", exact: true }).click();
  await expect(head).toHaveAttribute("aria-sort", "descending");
  await expect.poll(async () => (await totals())[0]).toBeGreaterThan(least);
  await expect.poll(async () => (await totals()).every((n, i, all) => i === 0 || all[i - 1] >= n)).toBe(true);
  await head.getByRole("button", { name: "total_cents", exact: true }).click();
  await expect(head).toHaveAttribute("aria-sort", "none");
  await expect(cell(p, 0, "id")).toHaveText("5001");
});

test("a column's filter narrows the rows, by the operators that mean something for it, and filters add up", async ({ app }) => {
  mockOnly();
  const p = await open(app, "orders");
  await p.getByRole("button", { name: "Filter by status" }).click();
  const filters = p.getByTestId("database-filters");
  // Added and not entered yet, it narrows nothing.
  await expect(range(p)).toHaveText(/^Rows 1–100 of about 1,280$/);
  const value = filters.getByRole("textbox", { name: "Value for status" });
  await expect(value).toBeFocused();
  await value.fill("pending");
  await value.press("Enter");
  await expect(column(p, "status").first()).toHaveText("pending");
  await expect(column(p, "status").filter({ hasNotText: "pending" })).toHaveCount(0);
  // The estimate is the whole table's, so it is not said of a part.
  await expect(range(p)).toHaveText(/^Rows 1–\d+$/);

  // Text can be compared every way the connection offers.
  await filters.getByRole("button", { name: /^How status is compared/ }).click();
  await expect(app.page.getByRole("menuitem")).toHaveText(["=", "≠", "<", "≤", ">", "≥", "like", "like, any case", "is NULL", "is not NULL"]);
  await app.page.getByRole("menuitem", { name: "like", exact: true }).click();
  await value.fill("p%");
  await value.press("Enter");
  await expect(column(p, "status").filter({ hasNotText: /^p/ })).toHaveCount(0);

  // A second filter, on a time: no patterns for it, and NULL takes no value.
  await p.getByTestId("database-table-bar").getByRole("button", { name: "Filter", exact: true }).click();
  await app.page.getByRole("menuitem", { name: "settled_at" }).click();
  const settled = filters.locator('[data-testid=database-filter][data-column="settled_at"]');
  await settled.getByRole("button", { name: /^How settled_at is compared/ }).click();
  await expect(app.page.getByRole("menuitem")).toHaveText(["=", "≠", "<", "≤", ">", "≥", "is NULL", "is not NULL"]);
  await app.page.getByRole("menuitem", { name: "is NULL", exact: true }).click();
  await expect(settled.getByRole("textbox")).toHaveCount(0);
  await expect(column(p, "settled_at").filter({ hasNotText: "NULL" })).toHaveCount(0);
  await expect(column(p, "status").first()).toHaveText(/^p/);
  // Pending and paid orders that were settled: paid ones are, pending never.
  await settled.getByRole("button", { name: /^How settled_at is compared/ }).click();
  await app.page.getByRole("menuitem", { name: "is not NULL", exact: true }).click();
  await expect(column(p, "status").filter({ hasNotText: "paid" })).toHaveCount(0);
  await expect(column(p, "status").first()).toHaveText("paid");

  await filters.getByRole("button", { name: "Clear all" }).click();
  await expect(filters).toHaveCount(0);
  await expect(range(p)).toHaveText(/^Rows 1–100 of about 1,280$/);

  // An empty filter value is the empty string, which is not NULL.
  await p.getByRole("button", { name: "Filter by note" }).click();
  await p.getByRole("textbox", { name: "Value for note" }).press("Enter");
  await expect(column(p, "note").first()).toHaveText("empty");
  await expect(column(p, "note").filter({ hasNotText: "empty" })).toHaveCount(0);
});

test("a table is read a page at a time, of a hundred rows or five hundred", async ({ app }) => {
  mockOnly();
  const p = await open(app, "orders");
  const pages = p.getByTestId("database-pages");
  await expect(range(p)).toHaveText("Rows 1–100 of about 1,280");
  await expect(pages.getByRole("button", { name: "Previous" })).toBeDisabled();
  await pages.getByRole("button", { name: "Next" }).click();
  await expect(range(p)).toHaveText("Rows 101–200 of about 1,280");
  await expect(cell(p, 0, "id")).toHaveText("5101");
  await pages.getByRole("button", { name: "Previous" }).click();
  await expect(range(p)).toHaveText("Rows 1–100 of about 1,280");

  await pages.getByRole("group", { name: "Rows a page" }).getByRole("button", { name: "500" }).click();
  await expect(range(p)).toHaveText("Rows 1–500 of about 1,280");
  await expect(p.getByRole("grid")).toHaveAttribute("aria-rowcount", "501");
  // Only the rows near the view are in the page, however many were read.
  expect(await rows(p).count()).toBeLessThan(80);
  await pages.getByRole("button", { name: "Next" }).click();
  await pages.getByRole("button", { name: "Next" }).click();
  await expect(range(p)).toHaveText("Rows 1,001–1,280 of about 1,280");
  await expect(pages.getByRole("button", { name: "Next" })).toBeDisabled();
});

test("hundreds of rows of forty columns scroll without the whole table being in the page, and a page that stops short is followed from where it ended", async ({ app }) => {
  mockOnly();
  const p = await open(app, "page_views");
  const pages = p.getByTestId("database-pages");
  await pages.getByRole("group", { name: "Rows a page" }).getByRole("button", { name: "500" }).click();
  // Five hundred were asked for: the helper stops a page at its size cap,
  // short of that, and says more follow.
  await expect(range(p)).toHaveText("Rows 1–420 of about 1,500");
  await expect(p.getByRole("grid")).toHaveAttribute("aria-colcount", "42");
  await expect(p.getByRole("grid")).toHaveAttribute("aria-rowcount", "421");
  // Its ids are past what a number holds exactly: they are shown digit for digit.
  await expect(cell(p, 0, "id")).toHaveText("9007199254740993");
  const grid = p.getByTestId("database-grid");
  // To the last row: it is drawn, the first is not, and the header stayed.
  await grid.evaluate((el) => el.scrollTo(0, el.scrollHeight));
  await expect(rows(p).last().locator('[data-col="id"]')).toHaveText("9007199254741412");
  await expect(column(p, "id").filter({ hasText: /^9007199254740993$/ })).toHaveCount(0);
  await expect(p.getByRole("columnheader", { name: /^session/ })).toBeInViewport();
  expect(await rows(p).count()).toBeLessThan(80);

  // The next page starts after the rows that came, not a page-size further on.
  await pages.getByRole("button", { name: "Next" }).click();
  await expect(range(p)).toHaveText("Rows 421–840 of about 1,500");
  await expect(cell(p, 0, "id")).toHaveText("9007199254741413");
  await pages.getByRole("button", { name: "Next" }).click();
  await pages.getByRole("button", { name: "Next" }).click();
  await expect(range(p)).toHaveText("Rows 1,261–1,500 of about 1,500");
  await expect(pages.getByRole("button", { name: "Next" })).toBeDisabled();
  // And back the way it came.
  await pages.getByRole("button", { name: "Previous" }).click();
  await expect(range(p)).toHaveText("Rows 841–1,260 of about 1,500");
});

test("Structure shows a table's columns, its key, its indexes and what it points at", async ({ app }) => {
  mockOnly();
  const p = await open(app, "order_items");
  await p.getByRole("group", { name: "What to show of the table" }).getByRole("button", { name: "Structure" }).click();
  const s = p.getByTestId("database-structure");
  await expect(s.getByRole("row")).toHaveCount(6);
  await expect(s.getByRole("row").nth(1)).toContainText("id");
  await expect(s.getByRole("row").nth(1)).toContainText("bigint");
  await expect(s.getByRole("row").nth(1)).toContainText("nextval('order_items_id_seq'::regclass)");
  await expect(s.locator("section", { hasText: "Primary key" })).toContainText("id");
  await expect(s.locator("section", { hasText: "Indexes" })).toContainText("order_items_order_id_idx");
  await expect(s.locator("section", { hasText: "Indexes" }).getByText("Unique")).toHaveCount(1);
  await expect(s.locator("section", { hasText: "Foreign keys" })).toContainText(/\(order_id\)\s*→\s*orders \(id\)/);
  await expect(s.locator("section", { hasText: "Foreign keys" })).toContainText(/\(product_id\)\s*→\s*products \(id\)/);
});

test("a foreign key's cell opens the row it points at", async ({ app }) => {
  mockOnly();
  const p = await open(app, "orders");
  const customer = (await cell(p, 0, "customer_id").innerText()).trim();
  await cell(p, 0, "customer_id").getByRole("button", { name: "Open the row in customers that customer_id points at" }).click();
  await expect(p.getByTestId("database-table-bar").getByRole("heading")).toHaveText("customers");
  await expect(p.getByTestId("database-filter")).toHaveAttribute("data-column", "id");
  await expect(p.getByRole("textbox", { name: "Value for id" })).toHaveValue(customer);
  await expect(range(p)).toHaveText("Rows 1–1");
  await expect(cell(p, 0, "id")).toHaveText(customer);
});

test("writes are locked until unlocked: the confirm names the database and the box, and Lock ends it at once", async ({ app }) => {
  mockOnly("unlocks writes");
  const p = await open(app, "customers");
  await expect(lock(p)).toHaveAttribute("data-writes", "off");
  await expect(p.getByRole("button", { name: "Add row" })).toHaveCount(0);
  // Locked, a cell can't be typed in: it opens to be read.
  await cell(p, 0, "name").dblclick();
  await expect(app.page.getByTestId("database-value").getByRole("textbox")).toHaveCount(0);
  await app.page.getByTestId("database-value").getByRole("button", { name: "Close" }).first().click();

  await p.getByRole("button", { name: "Unlock writes…" }).click();
  const confirm = app.page.getByRole("alertdialog");
  await expect(confirm).toContainText("Unlock writes to shop_checkout_fix on devl?");
  await expect(confirm).toContainText("For 15 minutes");
  await expect(confirm).toContainText("the PostgreSQL database checkout-fix's services use on devl (localhost:5432)");
  await expect(confirm).toContainText("It locks again by itself after 15 minutes");
  // Nothing happens until it is confirmed.
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(lock(p)).toHaveAttribute("data-writes", "off");

  await unlock(app, p);
  await expect(lock(p)).toContainText(/Writes on · 1[45]:\d\d/);
  await expect(p.getByRole("button", { name: "Add row" })).toBeVisible();
  // The console is under the same lock.
  await mode(p, "Console").click();
  await expect(p.getByTestId("database-result")).toHaveText("Writes are on");
  await mode(p, "Tables").click();

  await lock(p).getByRole("button", { name: "Lock" }).click();
  await expect(lock(p)).toHaveAttribute("data-writes", "off");
  await expect(p.getByRole("button", { name: "Add row" })).toHaveCount(0);
});

test("writes lock themselves again after fifteen minutes, and what was staged stays staged", async ({ app }) => {
  mockOnly("unlocks writes");
  // The page's own clock, so fifteen minutes pass when the test says.
  await app.page.clock.install();
  const p = await open(app, "customers");
  await unlock(app, p);
  await edit(app.page, cell(p, 0, "name"), "Ines N.");
  await expect(p.getByTestId("database-staged")).toContainText("1 staged change");

  await app.page.clock.fastForward("14:00");
  await expect(lock(p)).toHaveAttribute("data-writes", "on");
  await expect(lock(p)).toContainText(/Writes on · 0:\d\d/);
  await app.page.clock.fastForward("01:05");
  await expect(lock(p)).toHaveAttribute("data-writes", "off");
  await expect(p.getByTestId("database-ran-out")).toContainText("Writes locked themselves again: the fifteen minutes ran out.");
  // Nothing was lost, and nothing can be applied until it is unlocked again.
  await expect(p.getByTestId("database-staged")).toContainText("1 staged change");
  await expect(p.getByTestId("database-staged").getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);
  await expect(p.getByTestId("database-staged").getByRole("button", { name: "Unlock to apply…" })).toBeVisible();
});

test("the helper starting again keeps what was staged and typed, and comes back with writes locked", async ({ app }) => {
  mockOnly("unlocks writes");
  const p = await open(app, "customers");
  await unlock(app, p);
  await edit(app.page, cell(p, 0, "name"), "Ines N.");
  await mode(p, "Console").click();
  await p.getByRole("textbox", { name: "SQL to run" }).click();
  await app.page.keyboard.type("SELECT 'typed, not run yet'");
  await mode(p, "Tables").click();
  const bar = p.getByTestId("database-staged");
  await expect(bar).toContainText("1 staged change");

  // The box starts the helper again (it crashed, or berthd was updated),
  // and Apply lands while it is down: the box answers that it isn't running.
  await app.page.evaluate(() => (window as unknown as { __berthMockHelper: { restart(plugin: string, box: string, ms: number): void } }).__berthMockHelper.restart("database", "devl", 4000));
  await bar.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(p).toHaveAttribute("data-state", "starting");
  await expect(notice(p)).toContainText("Starting berth-database on devl…");
  await expect(p.getByTestId("database-kept")).toContainText("What you staged and typed in this tab is kept");
  await expect(bar).toBeHidden();

  // Back: the same table, the change still staged, the statement still in
  // the editor, and writes locked, as the helper has them after a restart.
  await expect(p).toHaveAttribute("data-state", "running", { timeout: 15_000 });
  await expect(p.getByTestId("database-kept")).toHaveCount(0);
  await expect(lock(p)).toHaveAttribute("data-writes", "off");
  await expect(p.getByTestId("database-table-bar").getByRole("heading")).toContainText("customers");
  await expect(bar).toContainText("1 staged change");
  await expect(cell(p, 0, "name")).toHaveText("Ines N.");
  await expect(p.getByTestId("database-refused")).toContainText("Nothing was applied.");
  await expect(bar.getByRole("button", { name: "Unlock to apply…" })).toBeVisible();
  await mode(p, "Console").click();
  await expect(p.getByRole("textbox", { name: "SQL to run" })).toContainText("SELECT 'typed, not run yet'");
});

test("edits are staged and marked, reviewed as old → new, and applied as one", async ({ app }) => {
  mockOnly("edits rows");
  const p = await open(app, "customers");
  await unlock(app, p);

  // A cell, a NULL, a deleted row and a new one.
  await edit(app.page, cell(p, 0, "plan"), "business");
  await expect(cell(p, 0, "plan")).toHaveAttribute("data-changed", "true");
  await cell(p, 2, "notes").dblclick();
  await cell(p, 2, "notes").getByRole("button", { name: "Set NULL" }).click();
  await expect(cell(p, 2, "notes")).toHaveText("NULL");
  // The empty string is a value of its own: NULL to empty is a change.
  await edit(app.page, cell(p, 0, "notes"), "");
  await expect(cell(p, 0, "notes")).toHaveText("empty");
  await expect(cell(p, 0, "notes")).toHaveAttribute("data-changed", "true");
  await p.getByRole("checkbox", { name: "Pick row 5", exact: true }).check();
  await p.getByRole("button", { name: "Delete 1 row" }).click();
  await expect(rows(p).nth(4)).toHaveAttribute("data-state", "deleted");
  await p.getByRole("button", { name: "Add row" }).click();
  await expect(rows(p).first()).toHaveAttribute("data-state", "new");
  await expect(cell(p, 0, "email")).toHaveText("default");
  await edit(app.page, cell(p, 0, "email"), "new.person@example.com");
  await edit(app.page, cell(p, 0, "name"), "New Person");
  // A number column takes a number or NULL, never nothing.
  await cell(p, 0, "credit").dblclick();
  await cell(p, 0, "credit").getByRole("textbox").fill("lots");
  await cell(p, 0, "credit").getByRole("textbox").press("Enter");
  await expect(cell(p, 0, "credit").getByRole("alert")).toHaveText("Type a number.");
  await cell(p, 0, "credit").getByRole("textbox").press("Escape");

  const bar = p.getByTestId("database-staged");
  await expect(bar).toContainText("4 staged changes in customers: 2 edited, 1 added, 1 deleted.");
  await expect(p.getByTestId("database-tree").getByRole("img", { name: "4 staged changes, not applied" })).toBeVisible();

  await bar.getByRole("button", { name: "Review…" }).click();
  const review = app.page.getByTestId("database-review");
  await expect(review).toContainText("4 changes, sent together: all of them apply, or none do.");
  // What an edit does not protect against is said, not implied.
  await expect(review).toContainText("if something else changed it since you read it, your values replace theirs without a word. Only a row that is gone stops the edit.");
  const change = review.getByTestId("database-change");
  await expect(change).toHaveCount(4);
  await expect(change.nth(0)).toContainText("Delete");
  await expect(change.nth(0)).toContainText("id = 5");
  await expect(change.filter({ hasText: "id = 1" })).toContainText(/'free'\s*→\s*'business'/);
  await expect(change.filter({ hasText: "id = 1" })).toContainText(/NULL\s*→\s*''/);
  await expect(change.filter({ hasText: "id = 3" })).toContainText(/'Prefers email\. Never phone before noon\.'\s*→\s*NULL/);
  await expect(change.filter({ hasText: "New row" })).toContainText("'new.person@example.com'");
  // One of them taken back.
  await review.getByRole("button", { name: "Take back the delete of id = 5" }).click();
  await expect(change).toHaveCount(3);

  await review.getByRole("button", { name: "Apply 3 changes" }).click();
  await expect(review).toHaveCount(0);
  await expect(p.getByTestId("database-applied")).toHaveText(/Applied to customers: 2 rows updated, 1 row added\./);
  await expect(bar).toHaveCount(0);
  // Read again, the rows are as they were made.
  await expect(cell(p, 0, "plan")).toHaveText("business");
  await expect(cell(p, 0, "plan")).not.toHaveAttribute("data-changed");
  await expect(cell(p, 0, "notes")).toHaveText("empty");
  await expect(cell(p, 2, "notes")).toHaveText("NULL");
  await expect(cell(p, 4, "id")).toHaveText("5");
  await expect(range(p)).toHaveText("Rows 1–100 of about 241");
});

test("a row that went away since it was read is a conflict: the changes stay staged, it says which row, and offers to reload", async ({ app }) => {
  mockOnly("edits rows");
  const p = await open(app, "customers");
  await unlock(app, p);
  await edit(app.page, cell(p, 6, "name"), "Lena K.");
  await edit(app.page, cell(p, 7, "name"), "Felix B.");
  // Something else deletes one of them meanwhile: here, the console.
  await mode(p, "Console").click();
  await run(app, p, "DELETE FROM customers WHERE id = 7");
  await expect(p.getByTestId("database-result")).toContainText("1 row changed");
  await mode(p, "Tables").click();

  const bar = p.getByTestId("database-staged");
  await bar.getByRole("button", { name: "Apply", exact: true }).click();
  const refused = p.getByTestId("database-refused");
  await expect(refused).toContainText("Nothing was applied.");
  await expect(refused).toContainText("A row you changed is no longer there: id = 7.");
  await expect(refused).toContainText("Your changes are still staged.");
  await expect(bar).toContainText("2 staged changes");
  // All or nothing: the other row was not changed either.
  await expect(rows(p).nth(6)).toHaveAttribute("data-state", "gone");

  await refused.getByRole("button", { name: "Reload the rows" }).click();
  await expect(cell(p, 6, "id")).toHaveText("8");
  await expect(cell(p, 6, "name")).toHaveText("Felix B.");
  await expect(bar).toContainText("2 staged changes");
  await refused.getByRole("button", { name: "Drop that change" }).click();
  await expect(refused).toHaveCount(0);
  await expect(bar).toContainText("1 staged change");
  await bar.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(p.getByTestId("database-applied")).toContainText("1 row updated");
});

test("a view and a table with no key are browse-only and say why; the keyless table still takes new rows", async ({ app }) => {
  mockOnly("edits rows");
  const p = await open(app, "customer_totals");
  await unlock(app, p);
  const note = p.getByTestId("database-browse-only");
  await expect(note).toHaveText("Browse only. customer_totals is a view: its rows come from a query, so they can't be changed here.");
  await expect(p.getByRole("button", { name: "Add row" })).toHaveCount(0);
  await expect(p.getByRole("checkbox")).toHaveCount(0);
  await cell(p, 0, "name").dblclick();
  await expect(app.page.getByTestId("database-value").getByRole("textbox")).toHaveCount(0);
  await app.page.getByTestId("database-value").getByRole("button", { name: "Close" }).first().click();

  await openTable(p, "daily_revenue");
  await expect(note).toContainText("daily_revenue is a materialized view");

  await openTable(p, "audit_log");
  await expect(note).toHaveText("audit_log has no primary key, so one row can't be told from another like it: rows can be added, not edited or deleted.");
  // A row that was read can't be named, so it can't be edited or picked.
  await cell(p, 0, "actor").dblclick();
  await expect(app.page.getByTestId("database-value").getByRole("textbox")).toHaveCount(0);
  await app.page.getByTestId("database-value").getByRole("button", { name: "Close" }).first().click();
  await p.getByRole("button", { name: "Add row" }).click();
  await expect(p.getByRole("checkbox", { name: "Pick the new row" })).toBeVisible();
  await expect(p.getByRole("checkbox", { name: /^Pick row/ })).toHaveCount(0);
  await edit(app.page, cell(p, 0, "actor"), "robin@acme.test");
  await edit(app.page, cell(p, 0, "action"), "order.refund");
  await p.getByTestId("database-staged").getByRole("button", { name: "Apply", exact: true }).click();
  await expect(p.getByTestId("database-applied")).toContainText("1 row added");
  await expect(range(p)).toHaveText("Rows 1–100 of about 421");
});

test("the console runs the statement under the cursor, or the selection, and shows what came back", async ({ app }) => {
  mockOnly();
  const p = await open(app);
  await mode(p, "Console").click();
  await expect(notice(p)).toContainText("Nothing run yet");
  await expect(p.getByTestId("database-result")).toHaveText("Reads only, until writes are unlocked");
  const editor = p.getByRole("textbox", { name: "SQL to run" });
  await editor.click();
  await app.page.keyboard.type("SELECT count(*) FROM orders;");
  await app.page.keyboard.press("Enter");
  await app.page.keyboard.type("SELECT id, name, plan FROM customers WHERE plan = 'business' ORDER BY id LIMIT 3;");
  // The cursor is at the end of the second.
  await app.page.keyboard.press("ControlOrMeta+Enter");
  const result = p.getByTestId("database-result");
  await expect(result).toHaveText(/^SELECT·3 rows·\d/);
  await expect(p.getByRole("grid", { name: "Results" }).getByRole("columnheader")).toHaveText([/Row|#/, /^idinteger/, /^nametext/, /^plantext/]);
  await expect(column(p, "plan")).toHaveText(["business", "business", "business"]);
  await expect(cell(p, 0, "id")).toHaveText("9");

  // The cursor in the first, and the button.
  await app.page.keyboard.press("ControlOrMeta+Home");
  await p.getByRole("button", { name: /^Run/ }).click();
  await expect(result).toHaveText(/^SELECT·1 row·/);
  await expect(cell(p, 0, "count")).toHaveText("1280");

  // The plan, without running it.
  await p.getByRole("button", { name: "Explain" }).click();
  await expect(result).toHaveText(/^EXPLAIN·/);
  await expect(cell(p, 0, "QUERY PLAN")).toContainText("Seq Scan on orders");

  // A table's Query puts its SELECT in the editor.
  await mode(p, "Tables").click();
  await openTable(p, "page_views");
  await p.getByTestId("database-table-bar").getByRole("button", { name: "Query" }).click();
  await expect(editor).toHaveText("SELECT * FROM analytics.page_views LIMIT 100;");
  // It has the keyboard, ready to run.
  await expect(editor).toBeFocused();
  await app.page.keyboard.press("ControlOrMeta+Enter");
  await expect(result).toHaveText(/^SELECT·100 rows·/);
});

test("a SQL error is shown under the editor in Postgres's words, with its place marked", async ({ app }) => {
  mockOnly();
  const p = await open(app);
  await mode(p, "Console").click();
  await run(app, p, "SELECT id, emial FROM customers");
  const error = p.getByTestId("database-error");
  await expect(error).toContainText('Column "emial" does not exist.');
  await expect(error).toContainText("At line 1, column 12, underlined in the editor.");
  // What the database suggests is said too.
  await expect(error).toContainText('Perhaps you meant to reference the column "customers.email".');
  await expect(p.locator(".cm-sql-error")).toHaveText("emial");
  // Typing takes the mark away.
  await app.page.keyboard.type(" ");
  await expect(p.locator(".cm-sql-error")).toHaveCount(0);

  // On the second line of what is in the editor.
  await run(app, p, "SELECT 1;");
  await app.page.keyboard.press("Enter");
  await app.page.keyboard.type("SELECT * FROM ordrs");
  await app.page.keyboard.press("ControlOrMeta+Enter");
  await expect(error).toContainText('Relation "ordrs" does not exist.');
  await expect(error).toContainText("At line 2, column 15");
  await expect(p.locator(".cm-sql-error")).toHaveText("ordrs");
  // A dotted name is marked whole: the database points at where it starts.
  await run(app, p, "SELECT * FROM analytics.page_viewz");
  await expect(error).toContainText('Relation "analytics.page_viewz" does not exist.');
  await expect(p.locator(".cm-sql-error")).toHaveText("analytics.page_viewz");

  // A statement that ran too long was stopped, in the helper's words and with what it suggests.
  await run(app, p, "SELECT pg_sleep(60)");
  await expect(error).toContainText("The statement ran longer than 10s and was stopped.");
  await expect(error).toContainText("Narrow it, or add a LIMIT.");
  await expect(p.locator(".cm-sql-error")).toHaveCount(0);
  // What can't run inside a transaction says so as Postgres does.
  await run(app, p, "VACUUM");
  await expect(error).toContainText("VACUUM cannot run inside a transaction block.");
  await run(app, p, "COPY customers TO STDOUT");
  await expect(error).toContainText("COPY to or from the editor is not something this API can carry.");
  // Two statements selected together are refused before anything runs.
  await run(app, p, "SELECT 1; SELECT now()");
  await app.page.keyboard.press("ControlOrMeta+a");
  await app.page.keyboard.press("ControlOrMeta+Enter");
  await expect(error).toContainText("That is 2 statements; the editor runs one at a time.");
  await expect(error).toContainText("Run them one by one.");
});

test("a statement that writes is refused while writes are locked, and runs once they are unlocked", async ({ app }) => {
  mockOnly("writes rows");
  const p = await open(app);
  await mode(p, "Console").click();
  await run(app, p, "UPDATE customers SET plan = 'team' WHERE id = 1");
  const error = p.getByTestId("database-error");
  await expect(error).toContainText("Not run: this statement writes, and writes to shop_checkout_fix are locked.");
  // Postgres itself refused it: its transaction was read-only.
  await expect(error).toContainText("cannot execute UPDATE in a read-only transaction");
  await error.getByRole("button", { name: "Unlock writes…" }).click();
  await app.page.getByRole("alertdialog").getByRole("button", { name: "Unlock for 15 minutes" }).click();
  await expect(lock(p)).toHaveAttribute("data-writes", "on");
  await p.getByRole("button", { name: /^Run/ }).click();
  await expect(p.getByTestId("database-result")).toHaveText(/^UPDATE·1 row changed·/);
  await expect(notice(p)).toContainText("UPDATE: 1 row changed");
  await expect(error).toHaveCount(0);
  // And it was: the table shows it.
  await mode(p, "Tables").click();
  await openTable(p, "customers");
  await expect(cell(p, 0, "plan")).toHaveText("team");
});

test("results past what the console keeps are cut off, and it says so", async ({ app }) => {
  mockOnly();
  const p = await open(app);
  await mode(p, "Console").click();
  await run(app, p, "SELECT * FROM order_items");
  await expect(p.getByTestId("database-result")).toHaveText(/^SELECT·1,000 rows·/);
  await expect(p.getByTestId("database-truncated")).toContainText("Only the first 1,000 rows are shown: the statement returned more than the console keeps.");
  await expect(p.getByRole("grid", { name: "Results" })).toHaveAttribute("aria-rowcount", "1001");
  expect(await rows(p).count()).toBeLessThan(80);
  // A thousand rows of forty-one columns are the same grid: only what is near the view is drawn.
  await run(app, p, "SELECT * FROM analytics.page_views");
  await expect(p.getByRole("grid", { name: "Results" })).toHaveAttribute("aria-colcount", "42");
  await expect(p.getByTestId("database-truncated")).toContainText("Only the first 1,000 rows");
  expect(await rows(p).count()).toBeLessThan(80);
  // What a statement says on its way is shown too.
  await run(app, p, "DO $$ BEGIN RAISE NOTICE 'two rows; skipped'; END $$");
  await expect(p.getByTestId("database-notices")).toHaveText("NOTICE: two rows; skipped");
  await expect(p.getByTestId("database-truncated")).toHaveCount(0);
});

test("what was run is remembered for the worktree: the statements, and never what they returned", async ({ app }) => {
  mockOnly();
  const p = await open(app);
  await mode(p, "Console").click();
  await expect(p.getByRole("button", { name: "History" })).toBeDisabled();
  await run(app, p, "SELECT email FROM customers WHERE id = 3");
  await expect(cell(p, 0, "email")).toHaveText("sam.castellano@example.com");
  await run(app, p, "SELECT count(*) FROM products");
  await expect(cell(p, 0, "count")).toHaveText("64");

  await expect.poll(() => app.stored(HISTORY)).toEqual([
    { sql: "SELECT count(*) FROM products", at: expect.any(Number) },
    { sql: "SELECT email FROM customers WHERE id = 3", at: expect.any(Number) },
  ]);
  // The address it returned is nowhere in what the app kept.
  expect(await app.page.evaluate(() => JSON.stringify(localStorage))).not.toContain("sam.castellano");

  // It is there after a restart, and a line of it goes back in the editor.
  await app.page.reload();
  await app.openWorktree("devl/checkout-fix");
  const again = app.page.locator("[data-testid=database]:visible");
  await mode(again, "Console").click();
  await again.getByRole("button", { name: "History" }).click();
  const menu = app.page.getByTestId("database-history");
  await expect(menu.getByRole("menuitem")).toHaveText([/^SELECT count\(\*\) FROM products/, /^SELECT email FROM customers WHERE id = 3/, /^Forget what was run in checkout-fix/]);
  await menu.getByRole("menuitem").nth(1).click();
  await expect(again.getByRole("textbox", { name: "SQL to run" })).toHaveText("SELECT email FROM customers WHERE id = 3");

  await again.getByRole("button", { name: "History" }).click();
  await app.page.getByRole("menuitem", { name: "Forget what was run in checkout-fix" }).click();
  await expect.poll(() => app.stored(HISTORY)).toEqual([]);
  await expect(again.getByRole("button", { name: "History" })).toBeDisabled();
});

test("another engine is shown by what its connection says it can do: no schemas, fewer filters, no writes", async ({ app }) => {
  mockOnly();
  const p = await open(app);
  await expect(p.getByTestId("database-bar")).toHaveAttribute("data-engine", "postgres");
  await p.getByTestId("database-connection").click();
  // The worktree's databases, and a way to connect another by its address.
  await expect(app.page.getByRole("menuitem")).toHaveText([/shop_checkout_fix.*PostgreSQL · DATABASE_URL/, /events.*driftdb · EVENTS_URL/, "Connect a database…"]);
  await app.page.getByRole("menuitem", { name: /events/ }).click();
  await expect(p.getByTestId("database-bar")).toHaveAttribute("data-engine", "driftdb");

  // One flat list, a kind of relation nothing has a name for, and no lock to open.
  const tree = p.getByTestId("database-tree");
  await expect(tree.getByTestId("database-table")).toHaveText([/deliveries/, /webhook_events.*stream/]);
  await expect(tree.getByRole("heading")).toHaveCount(0);
  await expect(lock(p)).toHaveAttribute("data-writes", "never");
  await expect(lock(p)).toHaveText("Read-only");
  await expect(p.getByRole("button", { name: "Unlock writes…" })).toHaveCount(0);

  await openTable(p, "deliveries");
  await expect(p.getByTestId("database-browse-only")).toHaveText("Browse only. driftdb takes no writes through Shipyard, so deliveries can be read and not changed.");
  // Its types are its own, shown as they are.
  await expect(p.getByRole("columnheader", { name: /^retries/ })).toContainText("i32");
  await expect(cell(p, 0, "error")).toHaveText("upstream timed out");
  await expect(cell(p, 1, "error")).toHaveText("NULL");
  // Three operators, and no more.
  await p.getByRole("button", { name: "Filter by topic" }).click();
  await p.getByRole("button", { name: /^How topic is compared/ }).click();
  await expect(app.page.getByRole("menuitem")).toHaveText(["=", "≠", "like"]);
  await app.page.keyboard.press("Escape");
  await p.getByRole("textbox", { name: "Value for topic" }).fill("stock.low");
  await p.getByRole("textbox", { name: "Value for topic" }).press("Enter");
  await expect(column(p, "topic").filter({ hasNotText: "stock.low" })).toHaveCount(0);
  await expect(range(p)).toHaveText(/^Rows 1–1\d$/);

  // Its console has no Explain: the plugin knows no way to ask this engine.
  await mode(p, "Console").click();
  await expect(p.getByRole("button", { name: /^Run/ })).toBeVisible();
  await expect(p.getByRole("button", { name: "Explain" })).toHaveCount(0);
  await expect(p.getByTestId("database-result")).toHaveText("Read-only");
  // The connection that was picked is the one it opens on next time.
  await expect.poll(() => app.stored("berth.plugin.database.connection:devl/shop/checkout-fix")).toBe("c_7d90a3b1e44f");
});

test("a worktree with one database shows it without a picker", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/shop");
  const p = await openPanel(app);
  await expect(p.getByTestId("database-connection")).toHaveText("shop_dev");
  await expect(p.getByRole("button", { name: /Pick another/ })).toHaveCount(0);
  await expect(p.getByTestId("database-tree").getByTestId("database-table")).toHaveCount(9);
});

test("where Database is off, the panel says so, and turns it on after a confirm that names the program", async ({ app }) => {
  mockOnly("installs a helper");
  await app.open();
  await app.openWorktree("gpu/ci-flake");
  const p = await openPanel(app);
  await expect(p).toHaveAttribute("data-state", "off");
  const said = notice(p);
  await expect(said).toContainText("Database is off on gpu");
  await p.getByRole("button", { name: "Turn on…" }).click();
  const confirm = app.page.getByRole("alertdialog");
  await expect(confirm).toContainText("Turn on Database on gpu?");
  await expect(confirm).toContainText("This installs berth-database, a program this plugin carries, on gpu");
  await expect(confirm).toContainText("It changes nothing in a service's environment, and only reads until you unlock writes.");
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(p).toHaveAttribute("data-state", "off");

  await p.getByRole("button", { name: "Turn on…" }).click();
  await app.page.getByRole("alertdialog").getByRole("button", { name: "Install and turn on" }).click();
  await expect(said).toContainText(/(Installing|Starting) berth-database on gpu/);
  await expect(p).toHaveAttribute("data-state", "running");
  // It runs, has not seen this worktree's services start, and the worktree
  // has no .env for it to have found a database in.
  await expect(said).toContainText("No database for ci-flake yet");
  await expect(said.getByTestId("database-looked")).toHaveText("Looked for .env, .env.development, .env.local and .env.development.local in this worktree on gpu: there is none.");
  await expect(said).toContainText("A service of this worktree that Shipyard starts with one shows here too, once it has started; one that was already running when Database was turned on needs a restart.");
});

test("a worktree with no database yet says where it looked, which databases show, and why others don't", async ({ app }) => {
  mockOnly();
  const p = await open(app, undefined, { db: "none" });
  const said = notice(p);
  await expect(said).toContainText("No database for checkout-fix yet");
  await expect(said.getByTestId("database-looked")).toHaveText("Looked for .env, .env.development, .env.local and .env.development.local in this worktree on devl: there is none.");
  await expect(said).toContainText("Database shows the Postgres a worktree's DATABASE_URL or POSTGRES_URL points at when it is on devl itself. It reads that from the worktree's .env files when this tab opens.");
  await expect(said).toContainText("A database on another host, or one whose address is a secret reference, is not shown.");
  await said.getByRole("button", { name: "Which databases show" }).click();
  const screen = app.page.getByTestId("database-screen");
  await expect(screen).toContainText("Which databases show");
  await expect(screen).toContainText("Or from the worktree's .env files");
  await expect(screen).toContainText("the box reads .env, .env.development, .env.local and .env.development.local in the worktree's own folder, a later file winning");
});

// The ?db=env scenarios are a project whose dev servers Shipyard does not
// start: the helper has been told of no database, and the worktree's
// DATABASE_URL is in its .env files, which the box reads when the tab opens.
const looks = (app: App) => app.page.evaluate(() => (window as unknown as { __berthMockDatabase: { looks(): number } }).__berthMockDatabase.looks());
const dotenv = (app: App, kind: string) => app.page.evaluate((k) => (window as unknown as { __berthMockDatabase: { dotenv(kind: string): void } }).__berthMockDatabase.dotenv(k), kind);
const ORIGIN = "berth.plugin.database.env:devl/shop/checkout-fix";

test("a worktree whose database is only in its .env is found when the tab opens, and says where it came from", async ({ app }) => {
  mockOnly();
  const p = await open(app, undefined, { db: "env-slow" });
  // While the box looks, the panel says so.
  await expect(p.getByTestId("database-looking")).toHaveText("Looking in checkout-fix's .env files on devl for its database…");
  // Then the database, as any other.
  await expect(p.getByTestId("database-connection")).toHaveText("shop_checkout_fix");
  await expect(p.getByTestId("database-tree").getByTestId("database-table")).toHaveCount(9);
  await expect(p.getByTestId("database-looking")).toHaveCount(0);
  await expect(lock(p)).toHaveAttribute("data-writes", "off");
  // Where Database learned of it: the variable, and the file it was in.
  const from = p.getByTestId("database-from-env");
  await expect(from).toHaveText(".env.local");
  await expect(from).toHaveAccessibleName("From DATABASE_URL in checkout-fix's .env.local");
  await from.hover();
  await expect(app.page.getByText("DATABASE_URL, read from checkout-fix's .env.local on devl. Reading the tables again looks in the file again.")).toBeVisible();
  // It looked once, and remembers what the look gave: ids and file names, never an address.
  await expect.poll(() => looks(app)).toBe(1);
  await expect.poll(() => app.stored(ORIGIN)).toEqual({ ids: ["c_1c28ba20e1b6"], files: { DATABASE_URL: ".env.local" } });
  // A table opens and reads as one of a database a service gave.
  await openTable(p, "customers");
  await expect(range(p)).toHaveText("Rows 1–100 of about 240");
});

test("a database a service gave says nothing of .env, and reading its tables again does not look there", async ({ app }) => {
  mockOnly();
  const p = await open(app);
  await expect(p.getByTestId("database-tree").getByTestId("database-table")).toHaveCount(9);
  await expect(p.getByTestId("database-from-env")).toHaveCount(0);
  await p.getByRole("button", { name: "Read the tables again" }).click();
  await expect(p.getByTestId("database-tree").getByTestId("database-table")).toHaveCount(9);
  expect(await looks(app)).toBe(0);
  expect(await app.stored(ORIGIN)).toBeNull();
});

test("a .env with no database address says which files were read, and looking again picks up a change to them", async ({ app }) => {
  mockOnly();
  const p = await open(app, undefined, { db: "env-none" });
  const said = notice(p);
  await expect(said).toContainText("No database for checkout-fix yet");
  await expect(said.getByTestId("database-looked")).toHaveText("Looked in .env and .env.local: no DATABASE_URL or POSTGRES_URL in them.");
  await expect(said).toContainText("A service of this worktree that Shipyard starts with one shows here too, once it has started");
  await expect.poll(() => looks(app)).toBe(1);

  // The file gets a DATABASE_URL: Look again reads it.
  await dotenv(app, "env");
  await said.getByRole("button", { name: "Look again" }).click();
  await expect(p.getByTestId("database-connection")).toHaveText("shop_checkout_fix");
  await expect(p.getByTestId("database-from-env")).toHaveText(".env.local");
  await expect(p.getByTestId("database-tree").getByTestId("database-table")).toHaveCount(9);
  await expect.poll(() => looks(app)).toBe(2);

  // And loses it again: reading the tables again looks in the files again,
  // because that is where this database came from.
  await dotenv(app, "env-none");
  await p.getByRole("button", { name: "Read the tables again" }).click();
  await expect(notice(p).getByTestId("database-looked")).toHaveText("Looked in .env and .env.local: no DATABASE_URL or POSTGRES_URL in them.");
  await expect(p.getByTestId("database-connection")).toHaveCount(0);
  await expect.poll(() => app.stored(ORIGIN)).toBeNull();
});

test("a .env whose database address can't be shown says which variable and why", async ({ app }) => {
  mockOnly();
  const p = await open(app, undefined, { db: "env-refused" });
  const said = notice(p);
  await expect(said).toContainText("No database for checkout-fix yet");
  const looked = said.getByTestId("database-looked");
  // Another host, a secret's name, and a file that links out of the worktree.
  await expect(looked).toContainText("DATABASE_URL in .env.local doesn't point at a Postgres on devl itself, so it isn't opened.");
  await expect(looked).toContainText("POSTGRES_URL in .env is a reference to a secret, not an address, so it isn't read.");
  await expect(looked).toContainText(".env.development is a link to a file outside the worktree, which isn't followed.");
  await expect(looked).not.toContainText("Looked in");
  await expect(said.getByRole("button", { name: "Look again" })).toBeEnabled();
});

test("a box too old to look in .env says what it always said, and nothing went wrong", async ({ app }) => {
  mockOnly();
  const p = await open(app, undefined, { db: "env-old" });
  const said = notice(p);
  await expect(said).toContainText("No database for checkout-fix yet");
  await expect(said).toContainText("A service of this worktree that Shipyard starts with a DATABASE_URL or POSTGRES_URL pointing at a Postgres on devl itself shows here once it has started. Database learns of a database when a service starts, so one that was already running when Database was turned on needs a restart.");
  await expect(said.getByTestId("database-looked")).toHaveCount(0);
  await expect(said).not.toContainText("older berthd");
  await expect(said).not.toContainText("Couldn't");
  await expect(p).toHaveAttribute("data-state", "running");
  // Look again asks again, and is as quiet.
  await said.getByRole("button", { name: "Look again" }).click();
  await expect(said).toContainText("Database learns of a database when a service starts");
  await expect(said.getByTestId("database-looked")).toHaveCount(0);
  // A box that can't look can't be given an address either.
  await expect(said.getByRole("button", { name: "Connect a database…" })).toHaveCount(0);
});

// A database connected by hand: its address is pasted once, goes to the box,
// and is nowhere in the app after that.
const HAND = "berth.plugin.database.hand:devl/shop/checkout-fix";
const SECRET = "hunter2-by-hand";

// Everything the page holds that someone could read back: what it shows,
// what its fields hold, and what it saved.
const pageHolds = (app: App, text: string) =>
  app.page.evaluate((t) => {
    const fields = [...document.querySelectorAll("input, textarea")].map((el) => (el as HTMLInputElement).value);
    const saved = Object.keys(localStorage).map((k) => localStorage.getItem(k) ?? "");
    return [document.body.innerText, document.body.innerHTML, ...fields, ...saved].some((s) => s.includes(t));
  }, text);

test("with no database found, one is connected by pasting its address, which is masked and never shown again", async ({ app }) => {
  mockOnly("connects a database");
  const p = await open(app, undefined, { db: "none" });
  await notice(p).getByRole("button", { name: "Connect a database…" }).click();
  const dialog = app.page.getByRole("alertdialog");
  await expect(dialog).toContainText("Connect a database to checkout-fix on devl");
  await expect(dialog).toContainText("It has to be a Postgres on devl itself (localhost, 127.0.0.1 or a Unix socket): berth-database opens no other, and a database on another machine is not taken.");
  await expect(dialog).toContainText("The address is kept on devl, in a file only you can read there, until checkout-fix is removed, you forget the connection, or Database's data on devl is removed. It isn't kept on this computer, and isn't shown again");
  // Masked like a password, with a way to see what was pasted.
  const address = dialog.getByLabel("Address", { exact: true });
  await expect(address).toBeFocused();
  await expect(address).toHaveAttribute("type", "password");
  await expect(address).toHaveAttribute("autocomplete", "off");
  await dialog.getByRole("button", { name: "Show the address" }).click();
  await expect(address).toHaveAttribute("type", "text");
  await dialog.getByRole("button", { name: "Hide the address" }).click();
  await expect(address).toHaveAttribute("type", "password");

  // Nothing, and something that is no address, are not sent anywhere.
  await dialog.getByRole("button", { name: "Connect", exact: true }).click();
  const problem = dialog.getByTestId("database-connect-problem");
  await expect(problem).toHaveText("Paste the database's address.");
  await address.fill(`mysql://acme:${SECRET}@localhost:3306/shop_scratch`);
  await address.press("Enter");
  await expect(problem).toContainText("That isn't a Postgres address. One looks like postgres://user:password@localhost:5432/database.");
  await expect(problem).not.toContainText(SECRET);

  await address.fill(`postgres://acme:${SECRET}@localhost:5433/shop_scratch`);
  await address.press("Enter");
  await expect(dialog).toHaveCount(0);
  // It shows as its database, like any other, and says how it got here.
  await expect(p.getByTestId("database-connection")).toHaveText("shop_scratch");
  await expect(p.getByTestId("database-by-hand")).toHaveText("added by hand");
  await expect(p.getByTestId("database-from-env")).toHaveCount(0);
  await expect(p.getByTestId("database-tree").getByTestId("database-table")).toHaveCount(9);
  await p.getByTestId("database-by-hand").hover();
  await expect(app.page.getByText("You connected this one by hand. Its address is kept on devl until you forget it, checkout-fix is removed, or Database's data on devl is removed.")).toBeVisible();
  // The address is nowhere in the page, and what was saved is the connection's id.
  expect(await pageHolds(app, SECRET)).toBe(false);
  expect(await pageHolds(app, "postgres://")).toBe(false);
  const ids = (await app.stored(HAND)) as string[];
  expect(ids).toHaveLength(1);
  expect(ids[0]).toMatch(/^c_[0-9a-f]{12}$/);

  // The lock is the same lock.
  await expect(lock(p)).toHaveAttribute("data-writes", "off");
  await unlock(app, p);
  await openTable(p, "customers");
  await expect(range(p)).toHaveText("Rows 1–100 of about 240");
});

test("a database on another machine is not taken, in the helper's rule, and nothing is kept", async ({ app }) => {
  mockOnly("connects a database");
  const p = await open(app, undefined, { db: "none" });
  await notice(p).getByRole("button", { name: "Connect a database…" }).click();
  const dialog = app.page.getByRole("alertdialog");
  const address = dialog.getByLabel("Address", { exact: true });
  await address.fill(`postgres://acme:${SECRET}@db.acme.example:5432/shop_staging`);
  await dialog.getByRole("button", { name: "Connect", exact: true }).click();
  const problem = dialog.getByTestId("database-connect-problem");
  await expect(problem).toHaveText("berth-database on devl didn't take this address. It opens a Postgres on devl itself and nothing else: one at localhost, 127.0.0.1 or a Unix socket. Nothing was kept.");
  // The dialog stays, with what was pasted, for it to be put right.
  await expect(address).toHaveValue(`postgres://acme:${SECRET}@db.acme.example:5432/shop_staging`);
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(notice(p)).toContainText("No database for checkout-fix yet");
  expect(await pageHolds(app, SECRET)).toBe(false);
  expect((await app.stored(HAND)) ?? []).toEqual([]);
  // Opened again, the field is empty: what was typed went with the dialog.
  await notice(p).getByRole("button", { name: "Connect a database…" }).click();
  await expect(app.page.getByRole("alertdialog").getByLabel("Address", { exact: true })).toHaveValue("");
});

test("a database connected by hand is forgotten after a confirm, and one a service gave does not offer it", async ({ app }) => {
  mockOnly("connects and forgets a database");
  // shop / checkout-fix has two databases its services gave: the picker offers a third.
  const p = await open(app);
  await expect(p.getByTestId("database-tree").getByTestId("database-table")).toHaveCount(9);
  await expect(p.getByRole("button", { name: "Forget this connection…" })).toHaveCount(0);
  await p.getByTestId("database-connection").click();
  await app.page.getByRole("menuitem", { name: "Connect a database…" }).click();
  const dialog = app.page.getByRole("alertdialog");
  await dialog.getByLabel("Address", { exact: true }).fill(`postgres://acme:${SECRET}@127.0.0.1:5433/shop_scratch`);
  await dialog.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  // The one just connected is the one in front, among the three.
  await expect(p.getByTestId("database-connection")).toContainText("shop_scratch");
  await expect(p.getByTestId("database-by-hand")).toBeVisible();
  await p.getByTestId("database-connection").click();
  await expect(app.page.getByRole("menuitem")).toHaveCount(3);
  await expect(app.page.getByRole("menuitem", { name: /shop_scratch/ })).toContainText("added by hand");
  // One by hand for a worktree: no second is offered while it is there.
  await expect(app.page.getByRole("menuitem", { name: "Connect a database…" })).toHaveCount(0);
  // A database a service gave says where it came from, and can't be forgotten: it would come back.
  await app.page.getByRole("menuitem", { name: /shop_checkout_fix/ }).click();
  await expect(p.getByTestId("database-connection")).toContainText("shop_checkout_fix");
  await expect(p.getByTestId("database-by-hand")).toHaveCount(0);
  await expect(p.getByRole("button", { name: "Forget this connection…" })).toHaveCount(0);

  await p.getByTestId("database-connection").click();
  await app.page.getByRole("menuitem", { name: /shop_scratch/ }).click();
  await p.getByRole("button", { name: "Forget this connection…" }).click();
  const confirm = app.page.getByRole("alertdialog");
  await expect(confirm).toContainText("Forget shop_scratch for checkout-fix?");
  await expect(confirm).toContainText("berth-database on devl forgets the address you gave it for shop_scratch (127.0.0.1:5433)");
  await expect(confirm).toContainText("The database itself isn't touched.");
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(p.getByTestId("database-by-hand")).toBeVisible();

  await p.getByRole("button", { name: "Forget this connection…" }).click();
  await app.page.getByRole("alertdialog").getByRole("button", { name: "Forget this connection" }).click();
  await expect(app.page.getByRole("alertdialog")).toHaveCount(0);
  // Back to the two a service gave, and another can be connected again.
  await expect(p.getByTestId("database-by-hand")).toHaveCount(0);
  await expect(p.getByTestId("database-connection")).toContainText("shop_checkout_fix");
  await p.getByTestId("database-connection").click();
  await expect(app.page.getByRole("menuitem")).toHaveCount(3);
  await expect(app.page.getByRole("menuitem", { name: "Connect a database…" })).toBeVisible();
  expect(await app.stored(HAND)).toEqual([]);
  expect(await pageHolds(app, SECRET)).toBe(false);
});

test("a database that isn't answering says so in the helper's words, and can be tried again", async ({ app }) => {
  mockOnly();
  const p = await open(app, undefined, { db: "refused" });
  const said = notice(p);
  await expect(said).toContainText("Couldn't reach shop_checkout_fix");
  await expect(said).toContainText("The database could not be opened: connect: connection refused.");
  await expect(said).toContainText("Check that the worktree's database is running.");
  await expect(said).toContainText("PostgreSQL at localhost:5432 isn't answering devl.");
  await expect(said.getByRole("button", { name: "Try again" })).toBeVisible();
  // The connection itself is known: its name and its lock are there.
  await expect(p.getByTestId("database-connection")).toContainText("shop_checkout_fix");
});

test("a box too old to send the helper its token says to update the box, not that something broke", async ({ app }) => {
  mockOnly();
  const p = await open(app, undefined, { db: "unauthorized" });
  const said = notice(p);
  await expect(said).toContainText("devl needs updating for Database");
  await expect(said).toContainText("devl's berthd is from before it sent one, so every request is turned away. Update the box in Settings → Boxes");
  await expect(said).not.toContainText("This route needs the helper's token");
  await said.getByRole("button", { name: "Open Settings → Boxes" }).click();
  await expect(app.page.getByTestId("settings-boxes")).toBeVisible();
});

test("a helper from before the database routes says it needs updating, and works once it is", async ({ app }) => {
  mockOnly("installs a helper");
  const p = await open(app, undefined, { db: "old" });
  const said = notice(p);
  await expect(said).toContainText("Database on devl needs updating");
  await expect(said).toContainText("devl runs berth-database 0.0.9, from before it could do this.");
  await said.getByRole("button", { name: "Open Database" }).click();
  const row = app.page.locator('[data-testid=database-box][data-box="devl"]');
  await row.getByRole("button", { name: "Update to 0.1.0…" }).click();
  const confirm = app.page.getByRole("alertdialog");
  await expect(confirm).toContainText("Update Database on devl?");
  await confirm.getByRole("button", { name: "Update", exact: true }).click();
  await expect(row).toContainText("berth-database 0.1.0");
  await app.openWorktree("devl/checkout-fix");
  await expect(p.getByTestId("database-tree").getByTestId("database-table")).toHaveCount(9);
});

test("the Database screen says where it runs, and turns it on and off for a box without touching Leadline", async ({ app }) => {
  mockOnly("installs and removes a helper");
  await app.open();
  const input = app.page.getByRole("combobox", { name: "Search sessions, worktrees and commands" });
  const command = app.page.getByRole("option", { name: /^Show Database/ });
  await expect(async () => {
    if (!(await input.isVisible())) await app.page.keyboard.press("Meta+k");
    await input.fill("Show Database");
    if (!(await command.isVisible())) await app.page.keyboard.press("Escape");
    await expect(command).toBeVisible({ timeout: 500 });
  }).toPass();
  await command.click();
  const screen = app.page.getByTestId("database-screen");
  const box = (name: string) => screen.locator(`[data-testid=database-box][data-box="${name}"]`);
  await expect(box("devl")).toHaveAttribute("data-state", "running");
  await expect(box("devl")).toContainText("berth-database 0.1.0");
  await expect(box("gpu")).toHaveAttribute("data-state", "off");
  await expect(box("gpu")).toContainText("No worktree's database is shown while it is off.");
  await expect(box("old-vps")).toHaveAttribute("data-state", "offline");

  await box("gpu").getByRole("button", { name: "Turn on…" }).click();
  await app.page.getByRole("alertdialog").getByRole("button", { name: "Install and turn on" }).click();
  await expect(box("gpu")).toHaveAttribute("data-state", "running");
  // It changes no service's environment, so turning it off leaves nothing to restart.
  await box("devl").getByRole("button", { name: "Turn off" }).click();
  await expect(box("devl")).toHaveAttribute("data-state", "off");
  await expect(box("devl").getByTestId("database-restart")).toHaveCount(0);

  await box("devl").getByRole("button", { name: "Remove data…" }).click();
  const remove = app.page.getByRole("alertdialog");
  await expect(remove).toContainText("Remove what Database kept on devl?");
  await expect(remove).toContainText("the address and password of each worktree's development database. The databases themselves aren't touched.");
  await remove.getByRole("button", { name: "Cancel" }).click();

  // Leadline's helper is another program with a state of its own.
  await app.openWorktree("devl/checkout-fix");
  await app.page.getByRole("button", { name: "New tab" }).click();
  await app.page.getByRole("option", { name: /^Queries/ }).click();
  await expect(app.page.locator("[data-testid=leadline][data-leadline=queries]:visible")).toHaveAttribute("data-state", "running");
});
