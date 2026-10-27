import type { Page } from "@playwright/test";

import { expect, mockOnly, test } from "./fixtures";

// A Markdown File tab (files/markdown-view.tsx) on the mock shop's README
// (lib/mock-files.ts): it opens as a page, shows its source or both, and
// follows its links.

test.beforeEach(async ({ app }) => {
  mockOnly("the mock shop's README");
  await app.open();
  await app.openWorktree("devl/checkout-fix");
});

async function openReadme(page: Page) {
  await page.keyboard.press("Meta+p");
  await expect(page.getByTestId("file-picker")).toBeVisible();
  await page.keyboard.type("readme");
  await expect(page.getByTestId("file-picker").getByRole("option").first()).toHaveAttribute("data-path", "README.md");
  await page.keyboard.press("Enter");
  return page.locator('[data-testid=file-pane][data-path="README.md"]');
}

test("a Markdown file opens as a page, with the worktree's pictures", async ({ app }) => {
  const pane = await openReadme(app.page);
  const page = pane.getByTestId("file-markdown");
  await expect(page.getByRole("heading", { level: 1, name: "Shop" })).toBeVisible();
  await expect(page.getByRole("table")).toContainText("packages/money");
  await expect(page.getByRole("checkbox")).toHaveCount(3);
  await expect(page.locator("pre code")).toContainText("pnpm db:migrate");
  // The logo comes from the box, as a blob: URL.
  await expect(page.getByRole("img", { name: "The shop's logo" })).toHaveAttribute("src", /^blob:/, { timeout: 15_000 });
  await expect(pane.locator(".cm-content")).toHaveCount(0);
  await expect(pane.getByRole("button", { name: "Preview", exact: true })).toHaveAttribute("aria-pressed", "true");
});

test("Source shows the editor, and side by side the page follows your typing", async ({ app }) => {
  const pane = await openReadme(app.page);
  await expect(pane.getByTestId("file-markdown")).toBeVisible();

  await pane.getByRole("button", { name: "Source", exact: true }).click();
  await expect(pane.locator(".cm-content")).toBeVisible();
  await expect(pane.getByTestId("file-markdown")).toHaveCount(0);
  await expect.poll(() => app.stored("berth.files.mdView")).toBe("source");

  await pane.getByRole("button", { name: "Source and preview side by side" }).click();
  const split = pane.getByTestId("file-split");
  await expect(split.locator(".cm-content")).toBeVisible();
  await expect(split.getByTestId("file-markdown")).toBeVisible();

  await split.locator(".cm-content").click();
  await app.page.keyboard.press("ControlOrMeta+End");
  await app.page.keyboard.type("\n## Typed just now\n");
  await expect(split.getByTestId("file-markdown").getByRole("heading", { level: 2, name: "Typed just now" })).toBeVisible();
});

test("a link opens the file it names, and a #heading link scrolls to it", async ({ app }) => {
  const pane = await openReadme(app.page);
  const page = pane.getByTestId("file-markdown");

  await page.getByRole("link", { name: "the top" }).scrollIntoViewIfNeeded();
  await page.getByRole("link", { name: "the top" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Shop" })).toBeInViewport();

  await page.getByRole("link", { name: "payments", exact: true }).click();
  const doc = app.page.locator('[data-testid=file-pane][data-path="docs/payments.md"]');
  await expect(doc.getByTestId("file-markdown").getByRole("heading", { level: 1, name: "Payments" })).toBeVisible();
  await expect(app.page.locator("[data-tab-strip] [data-tab][aria-selected=true]")).toContainText("payments.md");
});
