import type { Locator, Page } from "@playwright/test";

import { type App, expect, mockOnly, test } from "./fixtures";

// Leadline (plugins/leadline): what a worktree's code did in development,
// as four panels on the worktree, read from a helper on its box. In mock
// mode the plugin is the listing's own bundle, installed and allowed, and
// the helper answers with what the real berth-leadline said about made-up
// telemetry for shop / checkout-fix on devl (src/lib/mock-helper.ts). It
// runs on devl, is off on gpu, and no other worktree has sent anything.

const panel = (page: Page, id: string) => page.locator(`[data-testid=leadline][data-leadline=${id}]:visible`);
const rows = (p: Locator) => p.getByTestId("leadline-list").getByTestId("leadline-row");

// openPanel opens one of Leadline's panels as a tab of the worktree in
// front. The "+" menu lists it once the plugin has loaded.
async function openPanel(app: App, title: string): Promise<Locator> {
  const option = app.page.getByRole("option", { name: new RegExp(`^${title}`) });
  await expect(async () => {
    if (!(await option.isVisible())) await app.page.getByRole("button", { name: "New tab" }).click();
    await expect(option).toBeVisible({ timeout: 1000 });
  }).toPass();
  await option.click();
  const p = panel(app.page, title.toLowerCase());
  await expect(p).toBeVisible();
  return p;
}

// openLeadline opens the plugin's screen from the palette: its sidebar item
// starts under More.
async function openLeadline(page: Page): Promise<Locator> {
  const input = page.getByRole("combobox", { name: "Search sessions, worktrees and commands" });
  const command = page.getByRole("option", { name: /^Show Leadline/ });
  await expect(async () => {
    if (!(await input.isVisible())) await page.keyboard.press("Meta+k");
    await input.fill("Show Leadline");
    if (!(await command.isVisible())) await page.keyboard.press("Escape");
    await expect(command).toBeVisible({ timeout: 500 });
  }).toPass();
  await command.click();
  const screen = page.getByTestId("leadline-screen");
  await expect(screen).toBeVisible();
  return screen;
}

const box = (screen: Locator, name: string) => screen.locator(`[data-testid=leadline-box][data-box="${name}"]`);

test("Queries lists a worktree's statements by shape, flags the N+1, and sorts and searches", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Queries");
  await expect(rows(p)).toHaveCount(9);
  await expect(p.getByTestId("leadline-count")).toHaveText("9 query shapes");
  // Costliest first: the slow search, then the statement one request repeats.
  await expect(rows(p).first()).toContainText("ILIKE ? OR description ILIKE ?");
  await expect(rows(p).first()).toContainText("Slow");
  const repeated = rows(p).filter({ hasText: "FROM order_items WHERE order_id = ?" });
  await expect(repeated).toContainText("N+1 ×18");
  await expect(rows(p).filter({ hasText: "UPDATE orders SET status" })).toContainText("3 failed");
  // A shape has a ? where a value was, never the value or its placeholder.
  await expect(p.getByTestId("leadline-list")).not.toContainText("$1");

  await p.getByRole("button", { name: "Calls", exact: true }).click();
  await expect(rows(p).first()).toContainText("FROM order_items");
  await expect(p.getByRole("columnheader", { name: "Calls" })).toHaveAttribute("aria-sort", "descending");
  await p.getByRole("button", { name: "Calls", exact: true }).click();
  await expect(rows(p).first()).toContainText("FROM customers");

  await p.getByRole("searchbox", { name: "Filter by SQL text" }).fill("inventory");
  await expect(rows(p)).toHaveCount(1);
  await expect(rows(p)).toContainText("UPDATE inventory SET reserved");
  await p.getByRole("searchbox", { name: "Filter by SQL text" }).fill("no such table");
  await expect(p.getByTestId("leadline-notice")).toContainText("No queries match");
  await p.getByRole("searchbox", { name: "Filter by SQL text" }).fill("");

  // BEGIN and COMMIT are kept out of the queries, and listed on request.
  await p.getByRole("button", { name: "Control", exact: true }).click();
  await expect(rows(p)).toHaveCount(2);
  await expect(p.getByTestId("leadline-list")).toContainText("COMMIT");
});

test("a query opens to its numbers, the requests that run it and the N+1's evidence, a click from each trace", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Queries");
  await rows(p).filter({ hasText: "FROM order_items" }).click();
  const detail = p.getByTestId("leadline-detail");
  await expect(detail).toContainText("SELECT id, product_id, quantity FROM order_items WHERE order_id = ?");
  await expect(detail.getByText("Most in one request")).toBeVisible();
  await expect(detail).toContainText("×18");
  await expect(detail).toContainText("in 6 traces");
  const evidence = detail.locator("section", { hasText: "Requests that show the N+1" }).getByTestId("leadline-traces");
  await expect(evidence.getByRole("button")).toHaveCount(6);
  await expect(evidence.getByRole("button").first()).toContainText("18 calls");
  await expect(detail.locator("section", { hasText: "Requests that run it" })).toContainText("GET /api/orders");

  // The request that ran it most, opened as its trace.
  await evidence.getByRole("button").first().click();
  const traces = panel(app.page, "traces");
  await expect(traces.getByTestId("leadline-detail")).toBeVisible();
  await expect(traces.getByTestId("leadline-span")).toHaveCount(20);
});

test("Traces lists requests newest first, and one opens as a waterfall with its SQL and its logs", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Traces");
  await expect(rows(p)).toHaveCount(32);
  await expect(p.getByTestId("leadline-count")).toHaveText("32 traces");
  await p.getByRole("button", { name: "Failed", exact: true }).click();
  await expect(rows(p)).toHaveCount(3);
  await expect(rows(p).first()).toContainText("POST /api/webhooks/payments");
  await p.getByRole("button", { name: "Failed", exact: true }).click();
  await p.getByRole("searchbox", { name: "Filter by name" }).fill("checkout");
  await expect(rows(p)).toHaveCount(4);

  await rows(p).first().click();
  const detail = p.getByTestId("leadline-detail");
  // The request, BEGIN, two statements, the payment call and COMMIT.
  await expect(detail.getByTestId("leadline-span")).toHaveCount(6);
  await expect(detail.getByTestId("leadline-span").first()).toContainText("POST /api/checkout");
  await expect(detail.getByTestId("leadline-span").filter({ hasText: "BEGIN" })).toContainText("control");
  const insert = detail.getByTestId("leadline-span").filter({ hasText: "INSERT INTO orders" });
  await insert.click();
  const info = detail.getByTestId("leadline-span-info");
  await expect(info).toContainText("INSERT INTO orders (customer_id, total_cents, status) VALUES (?, ?, ?) RETURNING id");
  await expect(info).toContainText("counted as a query");
  // The statement as it was sent stays out of what is shown.
  await expect(info).not.toContainText("$1");
  await expect(info).not.toContainText("db.query.text");
  await expect(detail.getByTestId("leadline-trace-logs")).toContainText("order placed");
});

test("Logs filters by level and by text, and a line opens its trace", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Logs");
  await expect(rows(p)).toHaveCount(25);
  await p.getByRole("group", { name: "Lowest level" }).getByRole("button", { name: "Error" }).click();
  await expect(rows(p)).toHaveCount(3);
  await expect(rows(p).first()).toContainText("webhook failed: order already settled by an earlier delivery");
  await rows(p).first().click();
  await expect(p.getByTestId("leadline-log-info")).toContainText("attempt");

  await p.getByRole("group", { name: "Lowest level" }).getByRole("button", { name: "All" }).click();
  await p.getByRole("searchbox", { name: "Filter by message" }).fill("slow search");
  await expect(rows(p)).toHaveCount(1);
  await p.getByRole("button", { name: "Trace" }).click();
  const traces = panel(app.page, "traces");
  await expect(traces.getByTestId("leadline-detail").getByTestId("leadline-span").first()).toContainText("GET /api/search");
  await expect(traces.getByTestId("leadline-trace-logs")).toContainText("slow search took");
});

test("Endpoints lists each route's requests, latency and queries, sorts, and opens one", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Endpoints");
  await expect(rows(p)).toHaveCount(6);
  await expect(p.getByTestId("leadline-count")).toHaveText("6 endpoints");
  // By total time first: the slow search.
  await expect(rows(p).first()).toContainText("GET /api/search");
  await expect(rows(p).filter({ hasText: "job send-receipt" })).toContainText("worker");
  await expect(rows(p).filter({ hasText: "webhooks" })).toContainText("3 (60%)");
  await p.getByRole("button", { name: "Queries", exact: true }).click();
  await expect(rows(p).first()).toContainText("GET /api/orders");
  await p.getByRole("button", { name: "Endpoint", exact: true }).click();
  await expect(rows(p).first()).toContainText("GET /api/orders");
  await expect(rows(p).last()).toContainText("job send-receipt");

  await rows(p).filter({ hasText: "webhooks" }).click();
  const detail = p.getByTestId("leadline-detail");
  await expect(detail.getByText("Queries per request")).toBeVisible();
  await expect(detail).toContainText("60%");
  await expect(detail.locator("section", { hasText: "Failing requests" }).getByRole("button")).toHaveCount(3);
  await expect(detail.locator("section", { hasText: "Queries a request runs" })).toContainText("UPDATE orders SET status = ?, settled_at = now() WHERE id = ? AND status = ?");
  // One of its queries, opened in Queries.
  await detail.locator("section", { hasText: "Queries a request runs" }).getByRole("button").first().click();
  await expect(panel(app.page, "queries").getByTestId("leadline-detail")).toContainText("UPDATE orders SET status");
});

test("the time window is one for every panel, and is kept", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/checkout-fix");
  const queries = await openPanel(app, "Queries");
  const pressed = (p: Locator, label: string) => p.getByRole("group", { name: "Time window" }).getByRole("button", { name: label, exact: true });
  await expect(pressed(queries, "15m")).toHaveAttribute("aria-pressed", "true");
  await pressed(queries, "1h").click();
  await expect(pressed(queries, "1h")).toHaveAttribute("aria-pressed", "true");
  await expect(rows(queries)).toHaveCount(9);
  const traces = await openPanel(app, "Traces");
  await expect(pressed(traces, "1h")).toHaveAttribute("aria-pressed", "true");
  await expect.poll(() => app.stored("berth.plugin.leadline.window")).toBe("1h");
});

test("Send to the agent shows the brief it wrote, takes edits, and sends exactly that", async ({ app }) => {
  mockOnly("sends a prompt");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Queries");
  await rows(p).filter({ hasText: "FROM order_items" }).getByRole("button", { name: "Send this N+1 to the agent" }).click();
  const sheet = app.page.getByTestId("leadline-send");
  const text = sheet.getByRole("textbox");
  await expect(text).toHaveValue(/^Fix the N\+1 on order_items in GET \/api\/orders\n/);
  // What is wrong with its numbers, the shape, the requests that show it, and what done looks like.
  await expect(text).toHaveValue(/runs the same postgresql query up to 18 times in a single request \(13\.7 times per request on average, over 6 requests\)/);
  await expect(text).toHaveValue(/SELECT id, product_id, quantity FROM order_items WHERE order_id = \?/);
  await expect(text).toHaveValue(/The requests that show it, by trace id/);
  await expect(text).toHaveValue(/## Done when\n\n- `GET \/api\/orders` runs this query once per request/);
  await expect(text).not.toHaveValue(/\$1/);
  await expect(sheet).toContainText("Fix checkout webhook retries");

  await text.fill("Batch the order_items lookups in GET /api/orders.");
  await sheet.getByRole("button", { name: "Send", exact: true }).click();
  await expect(sheet).toHaveCount(0);
  // The agent is waiting on a question, so the box holds it: its chat shows it queued.
  await app.page.locator("[data-tab]", { hasText: "Fix checkout webhook" }).first().click();
  await expect(app.chat.getByTestId("queued-reply")).toContainText("Batch the order_items lookups in GET /api/orders.");
});

test("a failing endpoint's brief says how its newest failing request failed", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Endpoints");
  await rows(p).filter({ hasText: "webhooks" }).getByRole("button", { name: "Send this failing endpoint to the agent" }).click();
  const text = app.page.getByTestId("leadline-send").getByRole("textbox");
  await expect(text).toHaveValue(/^Fix the failing requests to POST \/api\/webhooks\/payments\n/);
  await expect(text).toHaveValue(/failed in 3 of its 5 requests \(60%\)/);
  await expect(text).toHaveValue(/could not serialize access due to concurrent update/);
  await expect(text).toHaveValue(/curl -s "http:\/\/127\.0\.0\.1:4325\/api\/endpoints\?source=shop%2Fcheckout-fix/);
});

test("with no agent in the worktree, Send to the agent starts one on the brief", async ({ app }) => {
  mockOnly("starts a session");
  // shop's main checkout on devl has a shell and no agent, and its telemetry
  // is filed under the project's name alone.
  await app.open();
  await app.openWorktree("devl/shop");
  const p = await openPanel(app, "Queries");
  await rows(p).filter({ hasText: "ILIKE" }).click();
  await p.getByRole("button", { name: "Send to the agent…" }).click();
  const sheet = app.page.getByTestId("leadline-send");
  await expect(sheet.getByRole("textbox")).toHaveValue(/^Speed up the products query in GET \/api\/search\n/);
  await expect(sheet.getByRole("textbox")).toHaveValue(/its telemetry is filed under `shop`\)/);
  await expect(sheet).toContainText("No agent runs in shop.");
  await sheet.getByRole("button", { name: /^Start / }).click();
  await expect(sheet).toHaveCount(0);
  // The new agent's tab is in the worktree, named for the brief.
  await expect(app.page.locator("[data-tab]", { hasText: "Speed up the products query" })).toBeVisible();
});

test("where Leadline is off, a panel says so, and turns it on after a confirm that names the program", async ({ app }) => {
  mockOnly("installs a helper");
  await app.open();
  await app.openWorktree("gpu/ci-flake");
  const p = await openPanel(app, "Queries");
  await expect(p).toHaveAttribute("data-state", "off");
  await expect(p.getByTestId("leadline-notice")).toContainText("Leadline is off on gpu");
  await p.getByRole("button", { name: "Turn on…" }).click();
  const confirm = app.page.getByRole("alertdialog");
  await expect(confirm).toContainText("Turn on Leadline on gpu?");
  await expect(confirm).toContainText("berth-leadline");
  await expect(confirm).toContainText("Nothing it records leaves gpu");
  // Nothing happens until it is confirmed.
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(p).toHaveAttribute("data-state", "off");

  await p.getByRole("button", { name: "Turn on…" }).click();
  await app.page.getByRole("alertdialog").getByRole("button", { name: "Install and turn on" }).click();
  await expect(p.getByTestId("leadline-notice")).toContainText(/(Installing|Starting) berth-leadline on gpu/);
  await expect(p).toHaveAttribute("data-state", "running");
  // It runs, and this worktree's app has sent it nothing.
  await expect(p.getByTestId("leadline-notice")).toContainText("Nothing from ci-flake yet");
});

test("a worktree that has sent nothing says what an app needs", async ({ app }) => {
  mockOnly();
  await app.open();
  await app.openWorktree("devl/qa-deck");
  const p = await openPanel(app, "Traces");
  const notice = p.getByTestId("leadline-notice");
  await expect(notice).toContainText("Nothing from qa-deck yet");
  await expect(notice).toContainText("OpenTelemetry SDK");
  await notice.getByRole("button", { name: "What an app needs" }).click();
  await expect(app.page.getByTestId("leadline-screen")).toContainText("When an app sends nothing");
});

test("the Leadline screen says where it runs, and turns it on, off and removes what it recorded", async ({ app }) => {
  mockOnly("installs and removes a helper");
  await app.open();
  const screen = await openLeadline(app.page);
  await expect(box(screen, "devl")).toHaveAttribute("data-state", "running");
  await expect(box(screen, "devl")).toContainText("berth-leadline 0.1.1");
  await expect(box(screen, "devl").getByTestId("leadline-heard")).toContainText("2 worktrees have sent something");
  await expect(box(screen, "devl").getByTestId("leadline-heard")).toContainText("shop/checkout-fix");
  await expect(box(screen, "devl").getByTestId("leadline-heard")).toContainText("for 3 days");
  await expect(box(screen, "gpu")).toHaveAttribute("data-state", "off");
  await expect(box(screen, "old-vps")).toHaveAttribute("data-state", "offline");
  await expect(box(screen, "old-vps").getByRole("button")).toHaveCount(0);

  await box(screen, "gpu").getByRole("button", { name: "Turn on…" }).click();
  const confirm = app.page.getByRole("alertdialog");
  await expect(confirm).toContainText("installs berth-leadline, a program this plugin carries, on gpu");
  await confirm.getByRole("button", { name: "Install and turn on" }).click();
  await expect(box(screen, "gpu")).toHaveAttribute("data-state", "running");
  await expect(box(screen, "gpu").getByTestId("leadline-heard")).toContainText("No worktree has sent anything yet");
  await box(screen, "gpu").getByRole("button", { name: "Turn off" }).click();
  await expect(box(screen, "gpu")).toHaveAttribute("data-state", "off");
  await expect(box(screen, "gpu")).toContainText("what it recorded before, if anything, is kept on the box");
  // Nothing was started there while it was on, so nothing needs a restart.
  await expect(box(screen, "gpu").getByTestId("leadline-restart")).toHaveCount(0);

  await box(screen, "devl").getByRole("button", { name: "Remove data…" }).click();
  const remove = app.page.getByRole("alertdialog");
  await expect(remove).toContainText("deletes everything it recorded there");
  await remove.getByRole("button", { name: "Remove data" }).click();
  // A box says "off" for a helper it does not have, whatever it had before.
  await expect(box(screen, "devl")).toHaveAttribute("data-state", "off");
  await expect(box(screen, "devl").getByRole("button", { name: "Turn on…" })).toBeVisible();
});

test("turning Leadline off names the services that still point at it", async ({ app }) => {
  mockOnly("removes a helper");
  await app.open();
  const screen = await openLeadline(app.page);
  await box(screen, "devl").getByRole("button", { name: "Turn off" }).click();
  await expect(box(screen, "devl")).toHaveAttribute("data-state", "off");
  // The box answered with the dev server it started while the helper was on.
  await expect(box(screen, "devl").getByTestId("leadline-restart")).toHaveText("Restart web in shop/checkout-fix: it was started while Leadline was on, and its telemetry and database connection still go to it.");
  await expect(app.page.getByText("Restart web in shop/checkout-fix: it still points at Leadline.")).toBeVisible();

  // Removing its data afterwards changes nothing for that service: the box
  // has no more to say of it, and the row still does.
  await box(screen, "devl").getByRole("button", { name: "Remove data…" }).click();
  await app.page.getByRole("alertdialog").getByRole("button", { name: "Remove data" }).click();
  await expect(app.page.getByRole("alertdialog")).toHaveCount(0);
  await expect(box(screen, "devl").getByTestId("leadline-restart")).toContainText("Restart web in shop/checkout-fix");
});

test("a box whose berthd is too old says Leadline can't run there", async ({ app }) => {
  mockOnly();
  await app.open({ params: { helper: "unsupported" } });
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Logs");
  await expect(p).toHaveAttribute("data-state", "unsupported");
  await expect(p.getByTestId("leadline-notice")).toContainText("devl can't run Leadline yet");
  await expect(p.getByTestId("leadline-notice")).toContainText("This box runs an older berthd, which cannot run helpers; update it. Update the box in Settings → Boxes");
  await expect(p.getByRole("button", { name: "Open Settings → Boxes" })).toBeVisible();
  await expect(p.getByRole("button", { name: "Turn on…" })).toHaveCount(0);
});

test("a box the plugin has no build for says so, and doesn't send you to update the box", async ({ app }) => {
  mockOnly();
  await app.open({ params: { helper: "nobuild" } });
  await app.openWorktree("devl/checkout-fix");
  const p = await openPanel(app, "Logs");
  await expect(p).toHaveAttribute("data-state", "unsupported");
  await expect(p.getByTestId("leadline-notice")).toContainText("berth-leadline has no build for linux-amd64 yet, so it can't be installed on devl.");
  await expect(p.getByRole("button", { name: "Open Settings → Boxes" })).toHaveCount(0);
  const screen = await openLeadline(app.page);
  await expect(box(screen, "devl")).toContainText("berth-leadline has no build for linux-amd64 yet");
  await expect(box(screen, "devl")).not.toContainText("Update the box");
});

test("a helper that won't start says why, and can be tried again", async ({ app }) => {
  mockOnly("installs a helper");
  await app.open({ params: { helper: "failed" } });
  await app.openWorktree("gpu/ci-flake");
  const p = await openPanel(app, "Endpoints");
  await p.getByRole("button", { name: "Turn on…" }).click();
  await app.page.getByRole("alertdialog").getByRole("button", { name: "Install and turn on" }).click();
  await expect(p).toHaveAttribute("data-state", "failed");
  await expect(p.getByTestId("leadline-notice")).toContainText("it ended (exit status 1) before it was ready");
  await expect(p.getByRole("button", { name: "Try again" })).toBeVisible();
});

test("an install the agent refuses says why in the confirm, and leaves the box as it was", async ({ app }) => {
  mockOnly("installs a helper");
  await app.open({ params: { helper: "mismatch" } });
  await app.openWorktree("gpu/ci-flake");
  const p = await openPanel(app, "Endpoints");
  await p.getByRole("button", { name: "Turn on…" }).click();
  const confirm = app.page.getByRole("alertdialog");
  await confirm.getByRole("button", { name: "Install and turn on" }).click();
  // The agent's own sentence: the download was checked and thrown away.
  await expect(confirm).toContainText("does not match the checksum in the plugin's manifest; nothing was installed");
  await expect(p).toHaveAttribute("data-state", "off");
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(p.getByTestId("leadline-notice")).toContainText("Leadline is off on gpu");
});

test("an older helper than the plugin carries offers an update, after a confirm", async ({ app }) => {
  mockOnly("installs a helper");
  await app.open({ params: { helper: "update" } });
  const screen = await openLeadline(app.page);
  await expect(box(screen, "devl")).toContainText("berth-leadline 0.0.9");
  await box(screen, "devl").getByRole("button", { name: "Update to 0.1.1…" }).click();
  const confirm = app.page.getByRole("alertdialog");
  await expect(confirm).toContainText("Update Leadline on devl?");
  await confirm.getByRole("button", { name: "Update", exact: true }).click();
  await expect(box(screen, "devl")).toContainText("berth-leadline 0.1.1");
  await expect(box(screen, "devl").getByRole("button", { name: /^Update to/ })).toHaveCount(0);
});

test("allowing a plugin that carries a program says it can install and run it on boxes, by name", async ({ app }) => {
  mockOnly("changes what is allowed");
  await app.open();
  const section = await app.openSettings("plugins");
  const row = section.locator('[data-testid=plugin-row][data-plugin="leadline"]');
  await expect(row.getByTestId("plugin-helper")).toHaveText("It can also install and run a program, berth-leadline, on the boxes you turn it on for.");
  await expect(row).toContainText("Panel: Queries");
  await expect(section.locator('[data-testid=market-plugin][data-plugin="leadline"]')).toContainText("Installed");

  // Off, then on again: turning it on asks, and the dialog says the same.
  await row.getByRole("switch", { name: "Turn off Leadline" }).click();
  await app.page.getByRole("alertdialog", { name: "Turn off Leadline?" }).getByRole("button", { name: "Turn off", exact: true }).click();
  await row.getByRole("switch", { name: "Turn on Leadline" }).click();
  // By its own name: the note that it was turned off is a dialog too.
  const consent = app.page.getByRole("dialog", { name: "Allow Leadline?" });
  await expect(consent.getByTestId("consent-helper")).toHaveText("Install and run a program, berth-leadline, on the boxes you turn it on for");
  await expect(consent).toContainText("berth-leadline 0.1.1");
  // It was turned off, not changed: nothing of it was left running.
  await expect(consent.getByTestId("consent-helper-held")).toHaveCount(0);
  await consent.getByRole("button", { name: "Allow and turn on" }).click();
  await expect(consent).toHaveCount(0);
  await expect(row.getByRole("switch", { name: "Turn off Leadline" })).toBeVisible();
});

// A plugin's program doesn't outlive it. In mock mode berth-leadline runs
// on devl, is off on gpu, and old-vps is offline (src/lib/mock-helper.ts).
const leadlineRow = (section: Locator) => section.locator('[data-testid=plugin-row][data-plugin="leadline"]');

test("turning off a plugin that carries a program says which boxes it stops on, and stops it there", async ({ app }) => {
  mockOnly("removes a helper");
  await app.open();
  const section = await app.openSettings("plugins");
  const row = leadlineRow(section);
  await row.getByRole("switch", { name: "Turn off Leadline" }).click();

  const confirm = app.page.getByRole("alertdialog", { name: "Turn off Leadline?" });
  // The boxes are named once the agent has said where it runs.
  await expect(confirm).toContainText("Its program, berth-leadline, is stopped and removed on devl. What it recorded there is kept.");
  await expect(confirm).toContainText("old-vps is offline: if it runs there, it's stopped when old-vps next connects.");
  await expect(confirm).toContainText("Turning Leadline on again doesn't start it: you turn it on for each box again.");
  // Cancelled, nothing changed.
  await confirm.getByRole("button", { name: "Cancel" }).click();
  await expect(confirm).toHaveCount(0);
  await expect(row.getByRole("switch", { name: "Turn off Leadline" })).toHaveAttribute("aria-checked", "true");

  await row.getByRole("switch", { name: "Turn off Leadline" }).click();
  await confirm.getByRole("button", { name: "Turn off", exact: true }).click();
  await expect(confirm).toHaveCount(0);
  await expect(row.getByRole("switch", { name: "Turn on Leadline" })).toHaveAttribute("aria-checked", "false");
  // What was done: where it stopped, where that waits, what to restart.
  await expect(app.page.getByText("Turned off Leadline")).toBeVisible();
  await expect(app.page.getByText("Stopped berth-leadline on devl. old-vps can't be reached: it's stopped there when old-vps next connects. Restart web in shop/checkout-fix on devl: it still points at berth-leadline.")).toBeVisible();

  // Turned on again, its program is not: devl has it off until asked.
  await row.getByRole("switch", { name: "Turn on Leadline" }).click();
  await app.page.getByRole("dialog", { name: "Allow Leadline?" }).getByRole("button", { name: "Allow and turn on" }).click();
  await expect(row.getByRole("switch", { name: "Turn off Leadline" })).toBeVisible();
  const screen = await openLeadline(app.page);
  await expect(box(screen, "devl")).toHaveAttribute("data-state", "off");
  await expect(box(screen, "devl").getByRole("button", { name: "Turn on…" })).toBeVisible();
});

test("removing a plugin that carries a program says its program and what it recorded go from the boxes", async ({ app }) => {
  mockOnly("removes a helper and its data");
  await app.open();
  const section = await app.openSettings("plugins");
  const row = leadlineRow(section);
  await row.getByRole("button", { name: "Remove…" }).click();

  const confirm = app.page.getByRole("alertdialog", { name: "Remove Leadline?" });
  await expect(confirm).toContainText("~/.berth/plugins/leadline");
  await expect(confirm).toContainText("Its program, berth-leadline, is stopped and removed on devl. What it recorded on your boxes is deleted too.");
  await expect(confirm).toContainText("old-vps is offline: if it runs there, it's stopped when old-vps next connects.");
  await confirm.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(row).toHaveCount(0);
  await expect(app.page.getByText("Removed Leadline")).toBeVisible();
  await expect(app.page.getByText(/^Stopped berth-leadline on devl\./)).toBeVisible();
  await expect(section.locator('[data-testid=market-plugin][data-plugin="leadline"]').getByRole("button", { name: "Install", exact: true })).toBeVisible();
});

test("a plugin with no program is turned off without asking", async ({ app }) => {
  mockOnly("changes what is allowed");
  await app.open();
  const section = await app.openSettings("plugins");
  const row = section.locator('[data-testid=plugin-row][data-plugin="acme-deploys"]');
  await row.getByRole("switch", { name: "Turn on Deploy board" }).click();
  await app.page.getByRole("dialog", { name: "Allow Deploy board?" }).getByRole("button", { name: "Allow and turn on" }).click();
  await row.getByRole("switch", { name: "Turn off Deploy board" }).click();
  await expect(row.getByRole("switch", { name: "Turn on Deploy board" })).toHaveAttribute("aria-checked", "false");
  await expect(app.page.getByRole("alertdialog")).toHaveCount(0);
});

test("a plugin that changed says its program is still running while it waits for review, and can stop it", async ({ app }) => {
  mockOnly("removes a helper");
  await app.open({ params: { helper: "held" } });
  // Shipyard asks about the changed plugin by itself, and says what was
  // left running meanwhile.
  const consent = app.page.getByRole("dialog", { name: "Leadline has changed" });
  await expect(consent.getByTestId("consent-helper-held")).toHaveText(
    "berth-leadline is still running on devl while this waits for your review. Allowing this version keeps it running. To stop it instead, choose Stop it beside Leadline in Settings → Plugins.",
  );
  await consent.getByRole("button", { name: "Not now" }).click();
  await expect(consent).toHaveCount(0);

  const section = await app.openSettings("plugins");
  const row = leadlineRow(section);
  await expect(row.getByRole("switch", { name: "Turn on Leadline" })).toHaveAttribute("aria-checked", "false");
  const held = row.getByTestId("plugin-helper-held");
  await expect(held).toContainText("berth-leadline is still running on devl while this waits for your review.");
  await held.getByRole("button", { name: "Stop it" }).click();
  const confirm = app.page.getByRole("alertdialog", { name: "Stop berth-leadline?" });
  await expect(confirm).toContainText("Its program, berth-leadline, is stopped and removed on devl. What it recorded there is kept.");
  await confirm.getByRole("button", { name: "Stop it", exact: true }).click();
  await expect(confirm).toHaveCount(0);
  await expect(held).toHaveCount(0);
  await expect(app.page.getByText(/^Stopped berth-leadline on devl\./)).toBeVisible();
});
