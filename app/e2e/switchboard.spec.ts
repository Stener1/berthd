import type { Page } from "@playwright/test";
import { expect, mockOnly, test } from "./fixtures";

// The Switchboard (⌘⇧K) is keyboard-first: type to filter, ↑↓ and ↵, ⌘S to
// star, ⌘E for a pickup note, Tab for the next area, ⇧F10 (or right-click,
// or the row's ⋯) for the rest. Its rows' buttons show on hover.

const title = "Fix checkout webhook retries";

// Home's composer takes the keyboard once it is ready, over any dialog open
// by then (the palette's too): open the Switchboard after that.
async function homeReady(page: Page) {
  await expect(page.getByRole("textbox", { name: "What should your agents work on?" })).toBeFocused();
}

test("Switchboard keeps focused work, pickup notes and areas across a reload, all from the keyboard", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  await homeReady(page);
  await page.getByRole("button", { name: /Open Switchboard/ }).click();
  const board = page.getByRole("dialog", { name: "Switchboard" });
  const search = board.getByRole("combobox", { name: "Search work" });
  await expect(search).toBeFocused();

  // Typing filters; the first match is highlighted.
  await search.fill("webhook");
  const row = board.getByRole("option", { name: new RegExp(`^${title}`) });
  await expect(row).toHaveAttribute("aria-selected", "true");

  // ⌘S stars it into Focus.
  await page.keyboard.press("Meta+s");
  const focus = board.getByRole("group", { name: "Focus" });
  await expect(focus.getByRole("option", { name: new RegExp(`^${title}`) })).toBeVisible();

  // ⌘E leaves a pickup note; ↵ saves it and the keyboard is back in search.
  await page.keyboard.press("Meta+e");
  const note = board.getByRole("textbox", { name: `Pickup note for ${title}` });
  await expect(note).toBeFocused();
  await note.fill("Decide whether to run the migration");
  await page.keyboard.press("Enter");
  await expect(note).toBeHidden();
  await expect(search).toBeFocused();
  await expect(focus).toContainText("Decide whether to run the migration");

  // A new area, then the row moved into it from its menu (⇧F10).
  await search.fill("");
  await board.getByRole("button", { name: "New area" }).click();
  await board.getByRole("textbox", { name: "Area name" }).fill("Launch");
  await page.keyboard.press("Enter");
  await expect(board.getByRole("tab", { name: "Launch" })).toBeVisible();
  await expect(search).toBeFocused();
  await search.fill("webhook");
  await expect(row).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Shift+F10");
  await page.getByRole("menuitem", { name: "Move to area" }).click();
  await page.getByRole("menuitemradio", { name: "Launch" }).click();
  await expect(page.getByRole("menu")).toHaveCount(0);
  await expect(search).toBeFocused();

  // Tab steps through the areas: All, Work, Personal, Launch.
  await search.fill("");
  await page.keyboard.press("Tab");
  await expect(board.getByRole("tab", { name: "Work" })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Tab");
  await expect(board.getByRole("tab", { name: "Launch" })).toHaveAttribute("aria-selected", "true");
  await expect(board.getByRole("option")).toHaveCount(1);
  await expect(row).toBeVisible();

  await page.reload();
  await homeReady(page);
  await page.keyboard.press("Meta+Shift+K");
  const reopened = page.getByRole("dialog", { name: "Switchboard" });
  const again = reopened.getByRole("group", { name: "Focus" }).getByRole("option", { name: new RegExp(`^${title}`) });
  await expect(again).toContainText("Decide whether to run the migration");
  // Focus is first, so ↵ opens it.
  await expect(again).toHaveAttribute("aria-selected", "true");
  await expect(reopened.getByRole("combobox", { name: "Search work" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(reopened).toBeHidden();
  await expect(page.getByTestId("status-worktree")).toContainText("checkout-fix");
});

test("Switchboard moves its highlight with the arrow keys and shows a row's buttons on hover", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  await homeReady(page);
  await page.keyboard.press("Meta+Shift+K");
  const board = page.getByRole("dialog", { name: "Switchboard" });
  const options = board.getByRole("option");
  await expect(options.first()).toHaveAttribute("aria-selected", "true");

  // ↓ moves down, ↑ from the top wraps to the bottom.
  await page.keyboard.press("ArrowDown");
  await expect(options.nth(1)).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("ArrowUp");
  await expect(options.last()).toHaveAttribute("aria-selected", "true");

  // Hovering a row shows its buttons: star it from there.
  const row = board.locator("[data-row]").filter({ hasText: title });
  await row.hover();
  await row.getByRole("button", { name: `Add ${title} to Focus` }).click();
  await expect(board.getByRole("group", { name: "Focus" })).toContainText(title);

  // Its ⋯ menu: Esc closes just the menu. From it, a note; Esc leaves the
  // note, then the Switchboard.
  await row.hover();
  await row.getByRole("button", { name: `More for ${title}` }).click();
  await expect(page.getByRole("menu")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("menu")).toHaveCount(0);
  await expect(board).toBeVisible();
  await row.hover();
  await row.getByRole("button", { name: `More for ${title}` }).click();
  await page.getByRole("menuitem", { name: "Add pickup note" }).click();
  const note = board.getByRole("textbox", { name: `Pickup note for ${title}` });
  await expect(note).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(note).toBeHidden();
  await expect(board).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(board).toBeHidden();
});

test("Switchboard says so when nothing matches", async ({ app }) => {
  mockOnly();
  await app.open();
  await homeReady(app.page);
  await app.page.keyboard.press("Meta+Shift+K");
  const board = app.page.getByRole("dialog", { name: "Switchboard" });
  await board.getByRole("combobox", { name: "Search work" }).fill("zzqx");
  await expect(board).toContainText("Nothing matches “zzqx”");
  await expect(board.getByRole("option")).toHaveCount(0);
});
