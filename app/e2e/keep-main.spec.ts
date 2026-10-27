import { expect, mockOnly, test } from "./fixtures";

// Project settings' Main branch: keeping main up to date is on until this
// box (or the repository, or the kit) turns it off, and turning it off is
// saved as the box's own keep_main.

test.beforeEach(() => mockOnly("devl's shop config is a mock fixture"));

for (const theme of ["berth-light", "berth-dark"]) {
  test(`keeping main up to date is on, and this box can turn it off (${theme})`, async ({ app }) => {
    const { page } = app;
    await app.open({ theme });
    const shop = page.locator("button[aria-expanded][draggable]").filter({ has: page.getByText("shop", { exact: true }) });
    await shop.first().click({ button: "right" });
    await page.getByRole("menuitem", { name: /^Project settings/ }).click();
    await page.getByRole("menuitem", { name: "devl", exact: true }).locator("visible=true").last().click();

    const main = page.locator("section#main");
    const toggle = main.getByTestId("keep-main-switch");
    await expect(toggle).toBeChecked();
    await main.scrollIntoViewIfNeeded();
    if (process.env.SHOTS) await main.screenshot({ path: `${process.env.SHOTS}/main-on-${theme}.png` });

    await toggle.click();
    await expect(toggle).not.toBeChecked();
    await page.getByRole("button", { name: "Save to devl" }).click();
    // Saved: the box's own setting, badged as this box's.
    await expect(page.getByRole("button", { name: "Discard" })).toHaveCount(0);
    await expect(toggle).not.toBeChecked();
    await expect(main.getByText("devl", { exact: true })).toBeVisible();
    if (process.env.SHOTS) await main.screenshot({ path: `${process.env.SHOTS}/main-off-${theme}.png` });
  });
}
