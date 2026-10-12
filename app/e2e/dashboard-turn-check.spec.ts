import { expect, mockOnly, test } from "./fixtures";

// Turn check marks a finished turn whose work goes on without the agent (a
// build, a deploy, a later time) as unfinished: the board shows it working,
// so Done holds only work that is done.
test("an agent waiting on its own work sits in Working, not Done", async ({ app }) => {
  mockOnly("the unfinished agent is a mock fixture");
  await app.open({ prefs: { labs: false, labsChosen: true, version: 3 } });
  await app.page.getByTestId("nav-home").click();
  const working = app.page.getByRole("region", { name: "Working" });
  const done = app.page.getByRole("region", { name: "Done" });
  const card = working.locator("article", { hasText: "Add tests for the export job" });
  await expect(card).toBeVisible();
  await expect(card.getByText("Waiting on its work")).toBeVisible();
  await expect(done.locator("article", { hasText: "Add tests for the export job" })).toHaveCount(0);
  // A finished one that is done stays in Done, without the marker.
  const finished = done.locator("article", { hasText: "Speed up product search" });
  await expect(finished).toBeVisible();
  await expect(finished.getByText("Waiting on its work")).toHaveCount(0);
  await app.page.screenshot({ path: test.info().outputPath("dashboard.png") });
});
