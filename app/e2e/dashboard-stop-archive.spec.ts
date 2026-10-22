import { expect, type App, mockOnly, test } from "./fixtures";

// The board's bulk Stop can archive the stopped agents' worktrees too, where
// it's safe. In the fixtures shop on devl has an archive script, so an
// archive runs it first; checkout-fix has uncommitted work, so git keeps it;
// order-export has three agents, so stopping one leaves the others there.

const prefs = { labs: false, labsChosen: true, version: 3 };

// A card on the board, picked while selecting (or opened otherwise).
const pick = (app: App, title: string) => app.page.locator("article[role=button]", { hasText: title }).getByText(title).click();

async function stopPicked(app: App, titles: string[]) {
  const { page } = app;
  await page.getByTestId("nav-home").click();
  await page.getByRole("button", { name: "Select", exact: true }).click();
  for (const t of titles) await pick(app, t);
  await page.getByRole("button", { name: `Stop ${titles.length}…` }).click();
  return page.getByRole("alertdialog");
}

const archiveBox = (app: App) => app.page.getByRole("checkbox", { name: /Archive their worktrees where it's safe/ });
const row = (app: App, place: string) => app.page.getByRole("group", { name: "Agents to stop" }).getByRole("listitem").filter({ hasText: place });

test("ticked, Stop archives a clean worktree and keeps a dirty one, saying why", async ({ app }) => {
  mockOnly("stops agents and archives worktrees");
  await app.open({ prefs });
  const dialog = await stopPicked(app, ["Speed up product search", "Fix checkout webhook retries"]);
  await expect(archiveBox(app)).not.toBeChecked();
  await archiveBox(app).click();
  await expect(dialog).toContainText("then its worktree is archived where it's safe");
  await dialog.getByRole("button", { name: "Stop 2 agents" }).click();

  // Kept work holds the dialog open, with each row's outcome.
  await expect(dialog.getByRole("heading")).toHaveText("Stopped 2 agents · archiving 1 worktree");
  await expect(row(app, "search-perf")).toContainText("Stopped · Archiving: its archive script runs, then it goes");
  await expect(row(app, "checkout-fix")).toContainText("Stopped · Kept: uncommitted changes");
  await dialog.getByRole("button", { name: "Done" }).click();

  // The clean one goes once its script has run; the dirty one stays.
  await expect(app.worktree("devl/search-perf")).toHaveCount(0, { timeout: 10_000 });
  await expect(app.worktree("devl/checkout-fix")).toBeVisible();
});

test("a worktree where another agent still runs is kept", async ({ app }) => {
  mockOnly("stops agents and archives worktrees");
  await app.open({ prefs });
  const dialog = await stopPicked(app, ["Export orders as CSV from the admin"]);
  await archiveBox(app).click();
  await dialog.getByRole("button", { name: "Stop 1 agent" }).click();
  await expect(dialog.getByRole("heading")).toHaveText("Stopped 1 agent");
  await expect(row(app, "order-export")).toContainText("Kept: another agent is still running there");
  await dialog.getByRole("button", { name: "Done" }).click();
  await expect(app.worktree("devl/order-export")).toBeVisible();
});

test("unticked, Stop only stops: the worktree stays", async ({ app }) => {
  mockOnly("stops agents");
  await app.open({ prefs });
  const dialog = await stopPicked(app, ["Speed up product search"]);
  await expect(dialog).toContainText("its worktree and files are kept");
  await dialog.getByRole("button", { name: "Stop 1 agent" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(app.page.locator("[data-slot=toast-viewport] > *", { hasText: "Stopped 1 agent" })).toContainText("Their worktrees are kept.");
  await expect(app.worktree("devl/search-perf")).toBeVisible();
});

test("the archive choice is remembered, and not offered for the main checkout alone", async ({ app }) => {
  mockOnly("stops agents");
  await app.open({ prefs });
  let dialog = await stopPicked(app, ["Speed up product search"]);
  await archiveBox(app).click();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  await expect.poll(() => app.stored("berth.dashboard.stopArchives")).toBe(true);

  await app.page.getByRole("button", { name: "Stop 1…" }).click();
  dialog = app.page.getByRole("alertdialog");
  await expect(archiveBox(app)).toBeChecked();
  await dialog.getByRole("button", { name: "Cancel" }).click();

  // Only gpu's main checkout: there is nothing to archive.
  await pick(app, "Speed up product search");
  await pick(app, "Plan the checkout release");
  await app.page.getByRole("button", { name: "Stop 1…" }).click();
  await expect(app.page.getByRole("alertdialog")).toContainText("its worktree and files are kept");
  await expect(archiveBox(app)).toHaveCount(0);
});
