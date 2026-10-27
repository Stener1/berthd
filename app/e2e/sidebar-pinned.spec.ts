import { expect, mockOnly, test } from "./fixtures";

// Pinned worktrees sit above the projects, in the order they were pinned
// (lib/pinned-worktrees.ts). In the fixtures checkout-fix on devl handed off
// order-export, which handed off https-linear-app-acme.

test("pinned from its menu, a worktree moves to the top and stays there after a reload", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  const pinned = page.getByTestId("pinned-worktrees");
  const pin = async (w: string, label: string) => {
    await app.worktree(w).click({ button: "right" });
    await page.getByRole("menuitem", { name: label }).click();
  };
  await expect(app.worktree("devl/checkout-fix")).toBeVisible();
  await expect(pinned).toBeHidden();

  // Pinned, it shows once, at the top with its project's name; the one it
  // handed off stays under the project.
  await pin("devl/checkout-fix", "Pin to top");
  await expect(pinned.locator('[data-worktree="devl/checkout-fix"]')).toBeVisible();
  await expect(app.worktree("devl/checkout-fix")).toHaveCount(1);
  await expect(pinned.getByTestId("worktree-row-project")).toHaveText("shop");
  await expect(app.worktree("devl/order-export")).toBeVisible();
  await expect(pinned.locator('[data-worktree="devl/order-export"]')).toHaveCount(0);

  // A second pin goes under the first, and both survive a reload.
  await pin("devl/order-export", "Pin to top");
  await expect(pinned.getByTestId("worktree-row")).toHaveCount(2);
  await page.reload();
  const rows = pinned.getByTestId("worktree-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toHaveAttribute("data-worktree", "devl/checkout-fix");
  await expect(rows.nth(1)).toHaveAttribute("data-worktree", "devl/order-export");

  // Unpinned, each goes back under its project.
  await pin("devl/checkout-fix", "Unpin");
  await pin("devl/order-export", "Unpin");
  await expect(pinned).toBeHidden();
  await expect(app.worktree("devl/checkout-fix")).toBeVisible();
  await expect(app.worktree("devl/order-export")).toBeVisible();
});

test("a pinned worktree's pin goes once the box says the worktree went", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  const row = app.worktree("devl/search-perf");
  await row.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Pin to top" }).click();
  await expect(page.getByTestId("pinned-worktrees").locator('[data-worktree="devl/search-perf"]')).toBeVisible();

  await row.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Remove worktree" }).click();
  await expect(row).toHaveCount(0);
  await expect(page.getByTestId("pinned-worktrees")).toBeHidden();
  expect(await page.evaluate(() => localStorage.getItem("berth.pinnedWorktrees"))).toBe("[]");
});
