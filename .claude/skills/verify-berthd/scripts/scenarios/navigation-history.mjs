// Back and forward through the places you've been (lib/nav-history.ts): the
// window strip's arrows, ⌘[ and ⌘], a mouse's back and forward buttons, the
// dashboard keeping its scroll, and archiving the worktree in front taking
// you back to the dashboard instead of Home. features/navigation-history.md.

export async function run({ page, shot, step, expect }) {
  const back = page.getByTestId("history-back");
  const forward = page.getByTestId("history-forward");
  const dashboard = page.getByRole("heading", { name: "Agent Dashboard" });
  const board = page.getByTestId("dashboard-board");
  // The worktree in front names itself in the page's one h1 (App.tsx
  // WorkspaceHeading, or the launcher's title once its tabs are closed).
  const inWorktree = page.getByRole("heading", { level: 1, name: /search-perf/ });
  const card = board.locator("article", { hasText: "Speed up product search" });
  const inQaDeck = page.getByRole("heading", { level: 1, name: /qa-deck/ });
  const scrollTop = () => board.evaluate((el) => el.scrollTop);
  const onSession = async () => {
    await expect(inWorktree).toBeAttached();
    await expect(dashboard).toBeHidden();
  };

  step("a new window is on Home with nowhere to go back or forward to");
  await expect(page.getByRole("group", { name: "History" })).toBeVisible();
  await expect(back).toBeDisabled();
  await expect(forward).toBeDisabled();
  await shot("1-home");

  step("⌘J opens the Agent Dashboard; scroll it to the bottom");
  await page.keyboard.press("Meta+j");
  await expect(dashboard).toBeVisible();
  await expect(back).toBeEnabled();
  await board.hover();
  await page.mouse.wheel(0, 2000);
  // With Labs on the board is only a little taller than the window.
  await expect.poll(scrollTop).toBeGreaterThan(0);
  const scrolled = await scrollTop();
  await shot("2-dashboard-scrolled");

  step("click the 'Speed up product search' card: its worktree, search-perf, comes to the front");
  await card.click();
  await onSession();
  await expect(back).toBeEnabled();
  await expect(forward).toBeDisabled();
  await shot("3-session");

  step("Back returns to the Agent Dashboard, scrolled where it was");
  await back.click();
  await expect(dashboard).toBeVisible();
  await expect.poll(scrollTop).toBe(scrolled);
  await expect(forward).toBeEnabled();
  await shot("4-back-to-dashboard");

  step("Forward returns to the session");
  await forward.click();
  await onSession();
  await expect(forward).toBeDisabled();
  await shot("5-forward-to-session");

  step("⌘[ goes back to the dashboard and ⌘] forward to the session");
  await page.keyboard.press("Meta+BracketLeft");
  await expect(dashboard).toBeVisible();
  await shot("6-cmd-bracket-back");
  await page.keyboard.press("Meta+BracketRight");
  await onSession();
  await shot("7-cmd-bracket-forward");

  step("a mouse's back button (3) and forward button (4) do the same");
  const mouse = (button) => page.evaluate((b) => window.dispatchEvent(new MouseEvent("mouseup", { button: b, bubbles: true, cancelable: true })), button);
  await mouse(3);
  await expect(dashboard).toBeVisible();
  await mouse(4);
  await onSession();

  step("close the session's tab: the worktree's launcher shows");
  await page.keyboard.press("Meta+w");
  const archive = page.getByRole("button", { name: "Archive this worktree…" });
  await expect(archive).toBeVisible();
  await shot("8-launcher");

  step("archive the worktree from the launcher: you land on the Agent Dashboard, not Home");
  await archive.click();
  const confirm = page.getByRole("alertdialog");
  await expect(confirm).toContainText("Archive");
  await confirm.getByRole("button", { name: "Archive", exact: true }).click();
  // shop's archive script runs first (the mock takes 1.5s), then the box
  // says the worktree went.
  await expect(page.getByText("Archived search-perf")).toBeVisible({ timeout: 10_000 });
  await expect(dashboard).toBeVisible();
  await expect(inWorktree).toHaveCount(0);
  await expect(card).toHaveCount(0);
  await shot("9-archived-back-on-dashboard");

  step("the archived worktree is gone from the trail: Forward has nowhere to go");
  await expect(forward).toBeDisabled();

  step("open 'Build the QA deck for the release' (qa-deck), then Remove worktree from its sidebar row: back on the dashboard again");
  await board.locator("article", { hasText: "Build the QA deck for the release" }).click();
  await expect(inQaDeck).toBeAttached();
  const row = page.locator('[data-testid=worktree-row][data-worktree="devl/qa-deck"]');
  await row.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Remove worktree", exact: true }).click();
  await expect(row).toHaveCount(0, { timeout: 10_000 });
  await expect(dashboard).toBeVisible();
  await expect(inQaDeck).toHaveCount(0);
  await shot("10-removed-back-on-dashboard");
}
