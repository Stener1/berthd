import { expect, mockOnly, test } from "./fixtures";

// Files an agent names in a reply link to them (components/conversation/
// file-links.tsx): on the mock's shop repository (lib/mock-files.ts), whose
// checkout-fix worktree has the agent's version of webhook.ts.

const WEBHOOK = "apps/web/lib/payments/webhook.ts";
const ROOT = "/home/me/work/shop-checkout-fix";

const REPLY = [
  `The retry check is in \`${WEBHOOK}:20-25\`, and the claim itself in \`./apps/web/lib/payments/idempotency.ts\`.`,
  "",
  `- The log line: \`${ROOT}/${WEBHOOK}:18:3\``,
  "- Run `pnpm test` to check, or read `docs/` and `apps/web/lib/nope.ts`.",
].join("\n");

declare global {
  interface Window {
    __berthDraft: { land(box: string, session: string, text: string): void };
  }
}

test.beforeEach(async ({ app }) => {
  mockOnly("lands a reply in the mock's conversation");
  await app.open({ params: { view: "conversation" } });
  await app.openWorktree("devl/checkout-fix");
  await expect(app.chat).toBeVisible();
  await app.page.evaluate((text) => window.__berthDraft.land("devl", "checkout-fix-claude", text), REPLY);
});

const reply = (app: { chat: import("@playwright/test").Locator }) => app.chat.locator("[data-kind=text] .cv-md").filter({ hasText: "The retry check is in" });

test("paths of the worktree's files link; commands, folders and missing files don't", async ({ app }) => {
  const md = reply(app);
  await expect(md.locator(`code[data-file-ref="${WEBHOOK}"][data-line="20"][data-end="25"]`)).toHaveAttribute("role", "link");
  await expect(md.locator('code[data-file-ref="apps/web/lib/payments/idempotency.ts"]')).toBeVisible();
  // An absolute path inside the worktree, line and column: line 18.
  await expect(md.locator(`code[data-file-ref="${WEBHOOK}"][data-line="18"]`)).toBeVisible();
  // Once the box has answered for every folder, the rest stay code.
  await expect(md.locator("code[data-file-ref]")).toHaveCount(3);
  for (const plain of ["pnpm test", "docs/", "apps/web/lib/nope.ts"]) await expect(md.locator("code", { hasText: plain })).not.toHaveAttribute("data-file-ref");
});

test("hovering a path shows its lines, the ones named picked out", async ({ app }) => {
  const link = reply(app).locator(`code[data-file-ref="${WEBHOOK}"][data-line="20"]`);
  await link.hover();
  const card = app.page.getByTestId("file-card");
  await expect(card).toBeVisible();
  await expect(card).toHaveAttribute("data-path", WEBHOOK);
  await expect(card).toContainText("lines 20–25");
  // Drawn by the diff renderer, numbered as the file is, line 20 selected.
  const lines = card.locator("diffs-container");
  await expect(lines.getByText("A retry of an event we already handled", { exact: false })).toBeVisible();
  await expect(lines.locator('[data-line="20"][data-selected-line]').first()).toBeAttached();
  await expect(lines.locator('[data-line="26"][data-selected-line]')).toHaveCount(0);
  // Leaving it closes the card.
  await app.page.mouse.move(5, 5);
  await expect(card).toBeHidden();
});

test("clicking a path opens the file at its line", async ({ app }) => {
  await reply(app).locator(`code[data-file-ref="${WEBHOOK}"][data-line="20"]`).click();
  const pane = app.page.locator(`[data-testid=file-pane][data-path="${WEBHOOK}"]`);
  await expect(pane.locator(".cm-content")).toBeVisible();
  // The lines named are selected: the cursor's line is the 25th.
  await expect(pane.locator(".cm-lineNumbers .cm-activeLineGutter")).toHaveText("25");
  await expect(pane.locator(".cm-selectionBackground").first()).toBeAttached();
});

test("a path is reached with Tab and opened with Enter", async ({ app }) => {
  // From the path before it, Tab: the next link in the reply.
  await reply(app).locator(`code[data-file-ref="${WEBHOOK}"][data-line="20"]`).focus();
  await app.page.keyboard.press("Tab");
  await expect(reply(app).locator('code[data-file-ref="apps/web/lib/payments/idempotency.ts"]')).toBeFocused();
  await expect(app.page.getByTestId("file-card")).toHaveAttribute("data-path", "apps/web/lib/payments/idempotency.ts");
  await app.page.keyboard.press("Enter");
  await expect(app.page.locator('[data-testid=file-pane][data-path="apps/web/lib/payments/idempotency.ts"] .cm-content')).toBeVisible();
});
