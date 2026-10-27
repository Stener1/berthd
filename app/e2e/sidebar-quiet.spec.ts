import { expect, mockOnly, test } from "./fixtures";

// A worktree with nothing going on stays in the sidebar: nothing folds
// quiet worktrees away under "Show N older".

test("a worktree with no agent stays in the sidebar once you move on", async ({ app }) => {
  mockOnly("the worktree is made in the mock fleet");
  await app.open();
  const { page } = app;
  await app.openWorktree("devl/checkout-fix");

  // A worktree alone, from the palette.
  await page.keyboard.press("ControlOrMeta+KeyK");
  await page.getByRole("option", { name: "New worktree (no agent)…" }).click();
  const dialog = page.getByRole("dialog", { name: "New worktree" });
  await dialog.getByRole("textbox").first().fill("quiet-notes");
  await expect(dialog.getByText("Reading it…")).toBeHidden();
  await dialog.getByRole("button", { name: /^Create worktree/ }).click();
  await expect(dialog).toBeHidden();
  await expect(app.worktree("devl/quiet-notes")).toBeVisible();

  // Somewhere else: it has no agent and isn't open, and it still shows.
  await app.openWorktree("devl/checkout-fix");
  await expect(app.worktree("devl/quiet-notes")).toBeVisible();
  await expect(page.getByText(/^Show \d+ older$/)).toHaveCount(0);
});
