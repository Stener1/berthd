import { expect, mockOnly, test } from "./fixtures";

const WHY = "Its turn ended while its work runs on: a delegated task, a build, a deploy or a watcher. It picks up by itself.";

// A finished turn turn check read as working ("the build runs, I'll check
// it when it reports back") still counts as working, but the sidebar and
// its tab draw it with an hourglass that says why, not the spinner of a
// turn still going; a turn still going keeps the spinner.
test("an agent waiting on its own work wears an hourglass in the sidebar and its tab, a live turn the spinner", async ({ app }) => {
  mockOnly("the unfinished agent is a mock fixture");
  await app.open({ prefs: { labs: false, labsChosen: true, version: 3 } });
  const { page } = app;

  // order-export: its turn ended, its work runs on.
  const waiting = app.worktree("devl/order-export");
  await expect(waiting.getByRole("img", { name: "Waiting on its work" })).toBeVisible();
  await expect(waiting.getByRole("img", { name: "Working" })).toHaveCount(0);
  await waiting.getByTestId("on-its-work").hover();
  await expect(page.locator("[data-slot=tooltip-popup]").filter({ hasText: WHY })).toBeVisible();
  // qa-deck: a turn still going.
  const live = app.worktree("devl/qa-deck");
  await expect(live.getByRole("img", { name: "Working" })).toBeVisible();
  await expect(live.getByTestId("on-its-work")).toHaveCount(0);
  await page.mouse.move(800, 600);
  await page.screenshot({ path: test.info().outputPath("sidebar.png") });

  await app.openWorktree("devl/order-export");
  const tab = page.getByRole("tab", { name: /Add tests for the export job/ });
  await expect(tab.getByRole("img", { name: "Waiting on its work" })).toBeVisible();
  await expect(tab.getByRole("img", { name: "Working" })).toHaveCount(0);
  await tab.hover();
  await expect(page.locator("[data-slot=tooltip-popup]").filter({ hasText: WHY })).toBeVisible();
  await page.screenshot({ path: test.info().outputPath("tab-on-its-work.png") });

  await app.openWorktree("devl/qa-deck");
  const liveTab = page.getByRole("tab", { name: /Build the QA deck for the release/ });
  await expect(liveTab.getByRole("img", { name: "Working" })).toBeVisible();
  await expect(liveTab.getByTestId("on-its-work")).toHaveCount(0);
  await page.screenshot({ path: test.info().outputPath("tab-live.png") });
});
