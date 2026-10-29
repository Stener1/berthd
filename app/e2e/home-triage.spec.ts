import { expect, mockOnly, test } from "./fixtures";

// Home as a triage screen for someone with many projects: what needs you
// is in view without scrolling, every agent row says its project first,
// and the palette and the notification centre don't repeat the heading
// they sit under.

test("what needs you and what is working show without scrolling in a small window", async ({ app }) => {
  await app.page.setViewportSize({ width: 1100, height: 720 });
  await app.open();
  const grid = app.page.getByTestId("home-grid");
  await expect(grid.getByRole("heading", { name: "Needs you" })).toBeInViewport();
  await expect(grid.getByRole("heading", { name: "Working now" })).toBeInViewport();
});

test("every agent row on Home says its project first", async ({ app }) => {
  mockOnly("the mock fixtures' agents");
  await app.open();
  const grid = app.page.getByTestId("home-grid");
  const waiting = grid.locator('[data-widget="needs-you"]').getByText(/^shop \/ checkout-fix/).first();
  await expect(waiting).toBeVisible();
  // Each working row's second line starts with where it works, whatever
  // its screen says it is doing.
  const working = grid.locator('[data-widget="working"] [data-agent-row]');
  await expect(working.first()).toBeVisible();
  for (const row of await working.all()) {
    const lines = await row.locator(":scope > span").allInnerTexts();
    expect(lines[1] ?? "").toMatch(/^[\w.-]+( \/ [^·]+)?( · [\w.-]+)?/);
    expect(lines[1] ?? "").not.toMatch(/^Running/);
  }
});

test("before the first project Home has no widgets, only the way to add one", async ({ app }) => {
  mockOnly("first run is a mock fixture (?firstrun)");
  await app.open({ params: { firstrun: "1" } });
  await expect(app.page.getByText("No projects yet", { exact: true }).first()).toBeVisible();
  await expect(app.page.getByTestId("home-grid")).toHaveCount(0);
  await expect(app.page.getByRole("button", { name: /Open Switchboard/ })).toHaveCount(0);
});

test("the palette's Needs you rows don't say Needs you again", async ({ app }) => {
  mockOnly("the mock fixtures' waiting agents");
  await app.open();
  await app.page.keyboard.press("ControlOrMeta+k");
  const palette = app.page.getByRole("dialog", { name: "Search and commands" });
  const row = palette.getByRole("option", { name: /Fix checkout webhook retries/ });
  await expect(row).toBeVisible();
  await expect(row).not.toContainText("Needs you");
  await expect(row).toContainText("devl");
});

test("the notification centre sits under the top band, and its Needs you titles don't repeat it", async ({ app }) => {
  mockOnly("the mock fixtures' notes");
  await app.open();
  await app.page.getByRole("button", { name: /^Notifications/ }).first().click();
  const centre = app.page.locator("[data-notification-center]");
  await expect(centre).toBeVisible();
  const needs = centre.locator("section").filter({ has: app.page.getByRole("heading", { name: /Needs you/ }) });
  await expect(needs.locator("[data-note]").first()).toBeVisible();
  for (const title of await needs.locator("[data-note] .text-desc").allInnerTexts()) expect(title).not.toMatch(/needs you$/i);
  // Clear of the band its toolbar sits in.
  const band = await app.page.getByTestId("nav-home").evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize) * 2.5);
  const box = await centre.boundingBox();
  expect(box?.y ?? 0).toBeGreaterThanOrEqual(Math.max(40, band));
});
