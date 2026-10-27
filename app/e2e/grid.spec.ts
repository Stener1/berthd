import type { Page } from "@playwright/test";

import { expect, mockOnly, test } from "./fixtures";

// The layout grid (index.css, "The layout grid"): the window's top band is
// one height everywhere, the sidebar's strip, the tab bar and a full-page
// view's header alike, and what sits in it is on its middle, the line the
// traffic lights are placed on. At the default text size and smaller it is
// 40px; past it, it grows with the text.

// band is every top band showing (the sidebar's strip, the tab bar or a
// view's header) and the centres of the controls in them.
const band = (page: Page) =>
  page.evaluate(() => {
    const bands = [
      document.querySelector("[data-testid=sidebar] [data-tauri-drag-region]"),
      document.querySelector("[data-tab-bar]"),
      document.querySelector("main header[data-tauri-drag-region], header[data-tauri-drag-region]"),
    ].filter((el): el is HTMLElement => !!el && el.getBoundingClientRect().height > 0);
    const rects = bands.map((el) => el.getBoundingClientRect());
    const controls = bands.flatMap((el) => [...el.querySelectorAll<HTMLElement>("button, h1")]).filter((el) => el.getBoundingClientRect().height > 0);
    return {
      bottoms: rects.map((r) => Math.round(r.bottom)),
      centres: controls.map((el) => {
        const r = el.getBoundingClientRect();
        return Math.round(r.top + r.height / 2);
      }),
    };
  });

async function expectOnTheBand(page: Page, height: number) {
  await expect
    .poll(async () => {
      const { bottoms, centres } = await band(page);
      return bottoms.length > 0 && bottoms.every((b) => b === height) && centres.every((c) => Math.abs(c - height / 2) <= 1);
    })
    .toBe(true);
}

for (const layout of ["rail", "list", "columns"]) {
  test(`the top band is one height with its controls on its middle, in the ${layout} sidebar`, async ({ app }) => {
    mockOnly();
    await app.open({ prefs: { version: 4, sidebarLayout: layout } });
    const page = app.page;
    // Home, a worktree's tabs, and a full-page view.
    await expectOnTheBand(page, 40);
    // The rail and two columns show one project's worktrees: shop's.
    if (layout !== "list") await page.locator(`[data-testid=${layout === "rail" ? "rail-project" : "column-project"}][data-project=shop]`).click();
    await app.openWorktree("devl/checkout-fix");
    await expectOnTheBand(page, 40);
    await page.getByTestId("nav-review").first().click();
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expectOnTheBand(page, 40);
  });
}

test("the top band stays 40px at a smaller text size and grows past the default", async ({ app }) => {
  mockOnly();
  await app.open({ prefs: { sidebarLayout: "rail" } });
  const page = app.page;
  const size = () => app.stored("berth.prefs").then((p) => (p as { uiFontSize: number }).uiFontSize);
  await page.keyboard.press("Meta+Minus");
  await page.keyboard.press("Meta+Minus");
  await expect.poll(size).toBe(11);
  await expectOnTheBand(page, 40);
  // 2.5rem at 16px × 16/13.
  await page.keyboard.press("Meta+0");
  for (let i = 0; i < 3; i++) await page.keyboard.press("Meta+Equal");
  await expect.poll(size).toBe(16);
  await expectOnTheBand(page, Math.round(2.5 * ((16 * 16) / 13)));
});
