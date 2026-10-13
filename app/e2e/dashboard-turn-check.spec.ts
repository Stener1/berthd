import { expect, mockOnly, test } from "./fixtures";

// Turn check marks a finished turn whose work still runs without the agent
// (a build, a deploy, a watcher) as unfinished, and one that waits on
// someone else's reply or a later time with nothing running as parked: the
// board shows the first working and the second idle, so Done holds only
// work that is done.
test("an agent waiting on its own work sits in Working, a parked one in Idle, neither in Done", async ({ app }) => {
  mockOnly("the unfinished and parked agents are mock fixtures");
  await app.open({ prefs: { labs: false, labsChosen: true, version: 3 } });
  await app.page.getByTestId("nav-home").click();
  const working = app.page.getByRole("region", { name: "Working" });
  const idle = app.page.getByRole("region", { name: "Idle" });
  const done = app.page.getByRole("region", { name: "Done" });
  const card = working.locator("article", { hasText: "Add tests for the export job" });
  await expect(card).toBeVisible();
  await expect(card.getByText("Waiting on its work")).toBeVisible();
  await expect(done.locator("article", { hasText: "Add tests for the export job" })).toHaveCount(0);
  // Parked until a reply: idle, not working and not done.
  const parked = idle.locator("article", { hasText: "Rerun the evals once the vendor replies" });
  await expect(parked).toBeVisible();
  await expect(parked.getByText("Parked: waiting on a reply or a later time")).toBeVisible();
  await expect(working.locator("article", { hasText: "Rerun the evals once the vendor replies" })).toHaveCount(0);
  await expect(done.locator("article", { hasText: "Rerun the evals once the vendor replies" })).toHaveCount(0);
  // A finished one that is done stays in Done, without either marker.
  const finished = done.locator("article", { hasText: "Speed up product search" });
  await expect(finished).toBeVisible();
  await expect(finished.getByText("Waiting on its work")).toHaveCount(0);
  await expect(finished.getByText("Parked:")).toHaveCount(0);
  await app.page.screenshot({ path: test.info().outputPath("dashboard.png") });
});
