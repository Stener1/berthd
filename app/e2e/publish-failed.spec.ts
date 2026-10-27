import { expect, mockOnly, test } from "./fixtures";

// An attempts run whose pick's pull request didn't open (#138): Review's
// Compare says publishing failed, not the agents' work, and offers to retry
// or finish without one. A run on a repository GitHub doesn't host
// finishes with its branch pushed and says so.

test.beforeEach(async ({ app }) => {
  mockOnly("the mock's runs");
  await app.open();
  await app.page.getByTestId("nav-review").click();
});

test("a pull request that didn't open can be retried from Compare, with the pick kept", async ({ app }) => {
  const page = app.page;
  await page.getByRole("button", { name: "search: 2 attempts · publishing failed" }).click();
  const note = page.getByRole("region", { name: "The pull request didn't open" });
  await expect(note).toContainText("The agents' work is done and your pick is kept; only publishing failed: gh pr create failed.");
  await expect(note).toContainText("HTTP 401: Bad credentials");
  // The pick is still the pick, and there is nothing to pick again.
  await expect(page.getByText("picked", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Pick this one" })).toHaveCount(0);
  await note.getByRole("button", { name: "Retry publishing" }).click();
  await expect(page.getByText("Trying the pull request again")).toBeVisible();
  const pr = page.getByRole("region", { name: "Pull request" });
  await expect(pr).toContainText("Pull request: opened");
  await expect(pr.getByRole("button", { name: "Open pull request" })).toBeVisible();
  await expect(note).toHaveCount(0);
});

test("a pull request that didn't open can be skipped: the run finishes without one", async ({ app }) => {
  const page = app.page;
  await page.getByRole("button", { name: "search: 2 attempts · publishing failed" }).click();
  const note = page.getByRole("region", { name: "The pull request didn't open" });
  await note.getByRole("button", { name: "Finish without a pull request" }).click();
  await expect(page.getByText("The pick is kept, and the other attempts are archived.")).toBeVisible();
  await expect(page.getByRole("region", { name: "Pull request" })).toContainText("Finished without a pull request (gh pr create failed).");
  await expect(page.getByText(/succeeded · started/)).toBeVisible();
});

test("an attempts run on a Gitea repository says its branch is pushed and links to the forge", async ({ app }) => {
  const page = app.page;
  await page.getByRole("button", { name: "invoices: 2 attempts" }).click();
  const pr = page.getByRole("region", { name: "Pull request" });
  await expect(pr).toContainText("This repository is on git.acme.dev, not GitHub, so Shipyard didn't open a pull request.");
  await expect(pr.getByRole("button", { name: "Open git.acme.dev" })).toBeVisible();
  await expect(page.getByText(/succeeded · started/)).toBeVisible();
});
