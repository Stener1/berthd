import { expect, mockOnly, test } from "./fixtures";

// A narrow window with the sidebar open: the Worktrees table and its
// filters fit, nothing runs off the right edge, and the show-only chips
// are one Show menu.

test("Worktrees fits a narrow window: no sideways overflow, chips fold into Show", async ({ app }) => {
  mockOnly();
  const page = app.page;
  await page.setViewportSize({ width: 1100, height: 720 });
  await app.open();
  await page.getByTestId("nav-worktrees").click();
  const rows = page.locator("[data-row]");
  await expect(rows.first()).toBeVisible();

  // Nothing is wider than the view: not the page, not a row, not the filters.
  const fits = () =>
    page.evaluate(() => {
      const main = document.querySelector("main")!.getBoundingClientRect();
      const row = document.querySelector("[data-row]")!;
      const bar = document.querySelector("[aria-label='Search worktrees']")!.closest(".\\@container\\/toolbar")!;
      const shown = [...bar.children].filter((c) => c.getBoundingClientRect().width > 0);
      return (
        document.documentElement.scrollWidth <= window.innerWidth &&
        row.scrollWidth <= row.clientWidth + 1 &&
        row.getBoundingClientRect().right <= main.right + 1 &&
        bar.scrollWidth <= bar.clientWidth + 1 &&
        // One row of filters.
        new Set(shown.map((c) => Math.round(c.getBoundingClientRect().top + c.getBoundingClientRect().height / 2))).size === 1
      );
    });
  await expect.poll(fits).toBe(true);

  // The port steps aside for the last commit.
  await expect(page.getByText("Port", { exact: true })).toBeHidden();
  // The chips are one menu, and it filters as they do.
  await expect(page.getByRole("group", { name: "Show only" })).toBeHidden();
  const all = await rows.count();
  await page.getByRole("button", { name: "Show only" }).click();
  await page.getByRole("menuitemcheckbox", { name: "Behind base" }).click();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("button", { name: "Show only: 1 on" })).toBeVisible();
  await expect.poll(() => rows.count()).toBeLessThan(all);
  // Clear filters is an icon here, and the filters still fit on one row.
  await expect.poll(fits).toBe(true);
  await page.getByRole("button", { name: "Clear filters" }).click();
  await expect(rows).toHaveCount(all);

  // Wide again, the chips come back.
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(page.getByRole("group", { name: "Show only" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Show only" })).toBeHidden();
});
