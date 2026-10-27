import { expect, mockOnly, test } from "./fixtures";

// Removing a worktree asks nothing: it shows "Removing…" and goes. One with
// uncommitted work (checkout-fix, in the fixtures) is kept, and the toast
// that says so offers to remove it anyway.

test("Remove worktree removes it without a dialog, and a dirty one only when told to", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;

  const clean = app.worktree("devl/search-perf");
  await clean.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Remove worktree", exact: true }).click();
  await expect(page.getByRole("alertdialog")).toHaveCount(0);
  await expect(clean).toHaveCount(0, { timeout: 10_000 });

  const dirty = app.worktree("devl/checkout-fix");
  await dirty.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Remove worktree", exact: true }).click();
  await expect(page.getByRole("alertdialog")).toHaveCount(0);
  const toast = page.locator("[data-slot=toast-viewport] > *", { hasText: "Kept checkout-fix" });
  await expect(toast).toBeVisible();
  await expect(dirty).toBeVisible();
  // Another toast can be stacked over it; its button still takes the keyboard.
  await toast.getByRole("button", { name: "Remove anyway" }).focus();
  await page.keyboard.press("Enter");
  await expect(dirty).toHaveCount(0, { timeout: 10_000 });
});

test("Delete in the Worktrees table removes the selected ones without a dialog", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  await page.getByTestId("nav-worktrees").click();
  // The checkbox's cell takes the click; the checkbox itself doesn't.
  await page.getByRole("checkbox", { name: "Select qa-deck" }).locator("xpath=..").click();
  await page.getByRole("checkbox", { name: "Select search-perf" }).locator("xpath=..").click();
  await page.getByRole("button", { name: "Delete", exact: true }).click();
  await expect(page.getByRole("alertdialog")).toHaveCount(0);
  await expect(page.getByRole("checkbox", { name: "Select qa-deck" })).toHaveCount(0, { timeout: 10_000 });
  await expect(page.getByRole("checkbox", { name: "Select search-perf" })).toHaveCount(0);
});
