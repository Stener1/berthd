import { expect, mockOnly, test } from "./fixtures";

// Back and forward through the places you've been (lib/nav-history.ts), and
// archiving the worktree in front taking you back to where you were before
// it rather than Home.

test("Back and Forward walk the places you've been, and archiving the worktree in front goes back", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  const back = page.getByTestId("history-back");
  const forward = page.getByTestId("history-forward");
  const dashboard = page.getByRole("heading", { name: "Agent Dashboard" });
  const inWorktree = page.getByRole("heading", { level: 1, name: /search-perf/ });

  await expect(back).toBeDisabled();
  await expect(forward).toBeDisabled();
  await page.keyboard.press("Meta+j");
  await expect(dashboard).toBeVisible();
  await page.getByTestId("dashboard-board").locator("article", { hasText: "Speed up product search" }).click();
  await expect(inWorktree).toBeAttached();

  await back.click();
  await expect(dashboard).toBeVisible();
  await forward.click();
  await expect(inWorktree).toBeAttached();
  await expect(forward).toBeDisabled();
  await page.keyboard.press("Meta+BracketLeft");
  await expect(dashboard).toBeVisible();
  await page.keyboard.press("Meta+BracketRight");
  await expect(inWorktree).toBeAttached();

  await page.keyboard.press("Meta+w");
  await page.getByRole("button", { name: "Archive this worktree…" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Archive", exact: true }).click();
  await expect(page.getByText("Archived search-perf")).toBeVisible();
  await expect(dashboard).toBeVisible();
  await expect(inWorktree).toHaveCount(0);
  await expect(forward).toBeDisabled();
});
