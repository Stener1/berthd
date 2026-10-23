import { type App, expect, mockOnly, test } from "./fixtures";

// The board's live status cards (Settings › Agents › Agent Dashboard):
// what the agent does now, its model and task progress, the steps it took
// this turn with their times, and the controls its state calls for
// (views/dashboard/status-card.tsx).

const open = async (app: App) => {
  await app.open({ prefs: { labs: false, labsChosen: true, version: 3, dashboardCards: "status" } });
  await app.page.getByTestId("nav-home").click();
};
const card = (app: App, title: string) => app.page.locator("article[role=button]", { hasText: title });

test("Live status cards is a switch in Settings › Agents, off until turned on, and the board follows it", async ({ app }) => {
  mockOnly("the judge agent's turn is a mock fixture");
  await app.open({ prefs: { labs: false, labsChosen: true, version: 3 } });
  await app.page.getByTestId("nav-home").click();
  // The classic card: no task progress line.
  await expect(card(app, "Tune the judge prompt")).toBeVisible();
  await expect(card(app, "Tune the judge prompt")).not.toContainText("Step 4 of 6");
  const agents = await app.openSettings("agents");
  const live = agents.getByRole("switch", { name: "Live status cards" });
  await expect(live).not.toBeChecked();
  await live.click();
  await expect(live).toBeChecked();
  expect(((await app.stored("berth.prefs")) as { dashboardCards?: string }).dashboardCards).toBe("status");
  await app.leaveSettings();
  await app.page.getByTestId("nav-home").click();
  await expect(card(app, "Tune the judge prompt")).toContainText("Step 4 of 6");
});

test("a working agent's card says its step now, its model and task progress, and the steps it took", async ({ app }) => {
  mockOnly("the judge agent's turn is a mock fixture");
  await open(app);
  const c = card(app, "Tune the judge prompt");
  await expect(c).toContainText("Running pnpm eval --suite judge");
  await expect(c).toContainText("Sonnet 4.5");
  await expect(c).toContainText("Step 4 of 6");
  const steps = c.getByRole("list", { name: "Steps" }).getByRole("listitem");
  await expect(steps).toHaveCount(3);
  // Finished steps in the past tense with how long they took.
  await expect(steps.filter({ hasText: "Ran pnpm test payments" })).toContainText(/\d+s$/);
  // The step under way is its own line under the title, timed.
  const now = c.getByTestId("card-now");
  await expect(now).toHaveAttribute("data-status", "active");
  await expect(now).toContainText(/Running pnpm eval --suite judge\s*\d+s/);
  await expect(c.getByRole("button", { name: "Pause" })).toBeVisible();
  await expect(c.getByRole("button", { name: /^Stop / })).toBeVisible();
  await expect(c.getByTestId("card-timer")).toBeVisible();
});

test("an approval puts Approve and Deny where the card's controls were, and Approve answers", async ({ app }) => {
  mockOnly("the approval is a mock fixture");
  await open(app);
  const c = card(app, "Fix checkout webhook retries");
  await expect(c).toContainText("Needs your approval to run pnpm prisma migrate dev --name idempotency_key");
  await expect(c.locator("[data-status=waiting]")).toBeVisible();
  const approve = c.getByRole("button", { name: "Approve", exact: true });
  await expect(approve).toBeVisible();
  await expect(c.getByRole("button", { name: "Deny", exact: true })).toBeVisible();
  // The footer is as tall as a working card's: nothing moves when it swaps.
  const working = await card(app, "Tune the judge prompt").locator("footer").boundingBox();
  const asking = await c.locator("footer").boundingBox();
  expect(Math.round(asking?.height ?? 0)).toBe(Math.round(working?.height ?? -1));
  await approve.click();
  await expect(app.page.getByText("Approved", { exact: true }).first()).toBeVisible();
});

test("a form of questions is answered in the agent's own view, not by one number", async ({ app }) => {
  mockOnly("the questions are a mock fixture");
  await open(app);
  const c = card(app, "Plan the checkout release");
  await expect(c.getByTestId("card-now")).toContainText("Three questions about the release window");
  await expect(c.getByRole("button", { name: /^1 / })).toHaveCount(0);
  await c.getByRole("button", { name: "Answer", exact: true }).click();
  await expect(app.page.getByRole("heading", { name: "Agent Dashboard" })).toHaveCount(0);
});

test("a finished card says how long its turn took, with no clock and only Open run", async ({ app }) => {
  mockOnly("the finished turn is a mock fixture");
  await open(app);
  const c = card(app, "Export orders as CSV from the admin");
  await expect(c).toContainText(/Done in \d+s/);
  await expect(c).toContainText("3 of 3 steps");
  await expect(c.getByTestId("card-timer")).toHaveCount(0);
  await expect(c.getByRole("button", { name: "Pause" })).toHaveCount(0);
  await expect(c.getByRole("button", { name: /^Stop / })).toHaveCount(0);
  await c.getByRole("button", { name: /Open run/ }).click();
  await expect(app.page.getByRole("heading", { name: "Agent Dashboard" })).toHaveCount(0);
});
