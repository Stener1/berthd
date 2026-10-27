import { expect, mockOnly, test } from "./fixtures";

// A structured chat's /resume (the demo's ?structured: search-perf's Claude
// runs through its stream). Its agent has no screen to show its own picker,
// so the chat opens the app's: Claude's past conversations in the folder,
// newest first, and picking one carries it on in the same pane.

test.beforeEach(async ({ app }) => {
  mockOnly();
  await app.open({ params: { view: "conversation", structured: "1" } });
  await app.openWorktree("devl/search-perf");
});

test("/resume opens the picker and carries the picked conversation on here", async ({ app, page }) => {
  const box = app.composer.getByRole("textbox", { name: "Reply" });
  await box.fill("/resume");
  // The chip says what it does before it is sent.
  await expect(app.composer.getByRole("status")).toContainText("past conversations");
  await box.press("Enter");

  const picker = page.getByRole("dialog");
  await expect(picker.getByPlaceholder("Resume one of Claude's conversations…")).toBeVisible();
  const rows = picker.getByRole("option");
  await expect(rows).toHaveCount(5);
  // The chat's own conversation is marked, and one running in another
  // session can't be picked here.
  await expect(rows.nth(0)).toContainText("this chat");
  await expect(rows.filter({ hasText: "Cache facet counts in Redis" })).toHaveAttribute("aria-disabled", "true");
  await expect(rows.filter({ hasText: "Cache facet counts in Redis" })).toContainText("running in search-perf-claude-2");
  await picker.getByRole("combobox").fill("mobile");
  await expect(rows).toHaveCount(1);
  await rows.first().click();
  await expect(picker).toBeHidden();
  // Nothing was sent to Claude: the reply box is empty, and no prompt shows.
  await expect(box).toHaveValue("");
  await expect(app.chat.getByText("/resume", { exact: true })).toHaveCount(0);
  // The tab is named after the conversation it carries on now.
  await expect(page.getByText("Find why search is slow on mobile").first()).toBeVisible();

  // Asked again, that one is this chat's.
  await box.fill("/resume");
  await box.press("Enter");
  await expect(rows.filter({ hasText: "Find why search is slow on mobile" })).toContainText("this chat");
});

test("/resume with words opens the picker filtered by them", async ({ app, page }) => {
  const box = app.composer.getByRole("textbox", { name: "Reply" });
  await box.fill("/resume skeleton");
  await box.press("Enter");
  const picker = page.getByRole("dialog");
  await expect(picker.getByRole("combobox")).toHaveValue("skeleton");
  await expect(picker.getByRole("option")).toHaveCount(1);
  await expect(picker.getByRole("option")).toContainText("Add a search-results skeleton");
  await page.keyboard.press("Escape");
  await expect(picker).toBeHidden();
});

test("a skill the agent loaded is a step, not a message from the person", async ({ app }) => {
  const fold = app.chat.getByRole("button", { name: /used 1 skill/ }).first();
  await expect(fold).toBeVisible();
  await fold.click();
  await expect(app.chat.getByText("Used the acme-perf-budget skill")).toBeVisible();
});
