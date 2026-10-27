import { expect, mockOnly, test } from "./fixtures";

// The Files panel (components/files/tree-dock.tsx) on the mock's shop
// repository (lib/mock-files.ts): checkout-fix has a Claude turn that
// changed four files, and its Claude waits for an answer until a test puts
// it to work through (window as unknown as W).__berthMockFiles.

const WEBHOOK = "apps/web/lib/payments/webhook.ts";

interface MockFiles {
  agentWritesLive(path?: string): void;
  agentWorks(on?: boolean): void;
  contentOf(path: string): string | undefined;
}
// Its own name for the hook: e2e/files.spec.ts declares the window's.
type W = { __berthMockFiles: MockFiles };

type Page = import("@playwright/test").Page;
const panel = (page: Page) => page.getByTestId("files-panel");
const tree = (page: Page) => panel(page).getByTestId("file-tree");
const row = (page: Page, path: string) => tree(page).locator(`[role=treeitem][data-path="${path}"]`);

async function openFolders(page: Page, ...paths: string[]) {
  for (const p of paths) {
    const r = row(page, p);
    if ((await r.getAttribute("aria-expanded")) !== "true") await r.click();
    await expect(r).toHaveAttribute("aria-expanded", "true");
  }
}

test.beforeEach(() => mockOnly());

test("⌘⇧E shows and hides the Files panel, and it stays open across a restart", async ({ app }) => {
  await app.open();
  await app.openWorktree("devl/checkout-fix");
  await expect(panel(app.page)).toHaveCount(0);
  // Closed, the strip's button says the turn changed files.
  await expect(app.page.getByTestId("files-panel-dot")).toBeVisible();

  await app.page.keyboard.press("Meta+Shift+E");
  await expect(panel(app.page)).toBeVisible();
  await expect(panel(app.page)).not.toHaveAttribute("data-float");
  await expect(app.page.getByTestId("files-panel-button")).toHaveAttribute("aria-pressed", "true");
  expect(((await app.stored("berth.prefs")) as { filesPanel?: boolean }).filesPanel).toBe(true);
  // Docked beside the tabs: the panes end where it starts.
  const pane = await app.page.locator("[data-pane-area]").boundingBox();
  const side = await panel(app.page).boundingBox();
  expect(Math.round(pane!.x + pane!.width)).toBeLessThanOrEqual(Math.round(side!.x) + 1);

  await app.page.reload();
  await expect(app.page.getByTestId("nav-home")).toBeVisible();
  await app.openWorktree("devl/checkout-fix");
  await expect(panel(app.page)).toBeVisible();

  await app.page.getByTestId("files-panel-button").click();
  await expect(panel(app.page)).toHaveCount(0);
  expect(((await app.stored("berth.prefs")) as { filesPanel?: boolean }).filesPanel).toBe(false);
  await app.page.reload();
  await expect(app.page.getByTestId("nav-home")).toBeVisible();
  await app.openWorktree("devl/checkout-fix");
  await expect(app.page.getByTestId("files-panel-button")).toBeVisible();
  await expect(panel(app.page)).toHaveCount(0);
});

test("All lists a folder at a time; Changed folds to the turn's files", async ({ app }) => {
  await app.open({ prefs: { filesPanel: true } });
  await app.openWorktree("devl/checkout-fix");
  const t = tree(app.page);
  // Its agent waits for an answer, not working: All.
  await expect(t).toHaveAttribute("data-filter", "all");
  await expect(row(app.page, "apps")).toBeVisible();
  await expect(row(app.page, "README.md")).toBeVisible();
  // Folders first, then files; a dot on a folder with changes inside.
  const top = await t.locator("[role=treeitem][aria-level='1']").evaluateAll((els) => els.map((e) => e.getAttribute("data-kind")));
  expect(top.indexOf("file")).toBeGreaterThan(top.lastIndexOf("dir"));
  await expect(row(app.page, "apps").getByTestId("tree-dot")).toBeVisible();
  await expect(row(app.page, "docs").getByTestId("tree-dot")).toHaveCount(0);
  await openFolders(app.page, "apps", "apps/web", "apps/web/lib", "apps/web/lib/payments");
  await expect(row(app.page, "apps/web/lib/payments/idempotency.ts").getByTestId("tree-badge")).toHaveText("A");
  await expect(row(app.page, WEBHOOK).getByTestId("tree-badge")).toHaveText("M");
  await expect(row(app.page, "apps/web/lib/payments/client.ts").getByTestId("tree-badge")).toHaveCount(0);

  await panel(app.page).getByTestId("tree-filter-changed").click();
  await expect(t).toHaveAttribute("data-filter", "changed");
  // Single folders compacted into one row, with counts and letters.
  await expect(row(app.page, "apps/web/lib")).toContainText("apps/web/lib");
  await expect(t.locator("[role=treeitem][data-kind=file]")).toHaveCount(4);
  await expect(row(app.page, "apps/web/lib/db/migrations")).toContainText("db/migrations");
  await expect(row(app.page, WEBHOOK)).toContainText("+12");
  await expect(row(app.page, WEBHOOK)).toContainText("−2");
  await expect(row(app.page, "README.md")).toHaveCount(0);
  await expect(panel(app.page).getByTestId("tree-changed-footer")).toContainText("4 files this turn");
  // A closed folder hides its files.
  await row(app.page, "apps/web/lib/payments").click();
  await expect(row(app.page, WEBHOOK)).toHaveCount(0);

  // The pick is the worktree's, and remembered.
  expect(await app.stored("berth.files.filter")).toEqual({ "devl:/home/me/work/shop-checkout-fix": "changed" });
});

test("Changed is the default while the agent works, with a live mark on what it writes", async ({ app }) => {
  await app.open({ prefs: { filesPanel: true }, params: { view: "conversation" } });
  await app.openWorktree("devl/checkout-fix");
  await expect(tree(app.page)).toHaveAttribute("data-filter", "all");
  await expect(app.page.getByTestId("tree-live")).toHaveCount(0);

  await app.page.evaluate(() => (window as unknown as W).__berthMockFiles.agentWritesLive());
  await expect(tree(app.page)).toHaveAttribute("data-filter", "changed");
  const totals = row(app.page, "apps/web/lib/checkout/totals.ts");
  await expect(totals.getByTestId("tree-live")).toBeVisible();
  await expect(totals.getByTestId("tree-badge")).toHaveText("M");
  await expect(row(app.page, "apps/web/lib/checkout").getByTestId("tree-dot")).toHaveAttribute("data-live", "true");
  // Beside the chat: its column ends before the panel.
  await expect(app.chat).toBeVisible();
  const chat = await app.chat.boundingBox();
  const side = await panel(app.page).boundingBox();
  expect(chat!.x + chat!.width).toBeLessThanOrEqual(side!.x + 1);

  // The agent stops: no live mark, however recent the write, and All again.
  await app.page.evaluate(() => (window as unknown as W).__berthMockFiles.agentWorks(false));
  await expect(app.page.getByTestId("tree-live")).toHaveCount(0);
  await expect(tree(app.page)).toHaveAttribute("data-filter", "all");

  // Once picked, the pick holds whatever the agent does.
  await panel(app.page).getByTestId("tree-filter-all").click();
  await app.page.evaluate(() => (window as unknown as W).__berthMockFiles.agentWorks(true));
  await expect(app.page.locator("[data-testid=worktree-row][data-worktree='devl/checkout-fix']")).toBeVisible();
  await expect(tree(app.page)).toHaveAttribute("data-filter", "all");
});

test("a file picked in the tree opens in a File tab, and the tree follows the tab in front", async ({ app }) => {
  await app.open({ prefs: { filesPanel: true } });
  await app.openWorktree("devl/checkout-fix");
  await openFolders(app.page, "apps", "apps/web", "apps/web/lib", "apps/web/lib/payments");
  await row(app.page, WEBHOOK).click();
  const pane = app.page.locator(`[data-testid=file-pane][data-path="${WEBHOOK}"]`);
  await expect(pane.locator(".cm-content")).toBeVisible();
  await expect(row(app.page, WEBHOOK)).toHaveAttribute("aria-current", "page");
  // The keyboard: ↓ to the next file, ↵ opens it.
  await tree(app.page).locator("[role=tree]").focus();
  await app.page.keyboard.press("ArrowUp");
  await app.page.keyboard.press("Enter");
  const test = app.page.locator('[data-testid=file-pane][data-path="apps/web/lib/payments/webhook.test.ts"]');
  await expect(test.locator(".cm-content")).toBeVisible();
  await expect(row(app.page, "apps/web/lib/payments/webhook.test.ts")).toHaveAttribute("aria-current", "page");
});

test("New file makes an empty file in the folder you are on and opens it", async ({ app }) => {
  await app.open({ prefs: { filesPanel: true } });
  await app.openWorktree("devl/checkout-fix");
  await openFolders(app.page, "apps", "apps/web", "apps/web/lib");
  await row(app.page, "apps/web/lib").click(); // closes it, and is the folder you are on
  await panel(app.page).getByTestId("tree-new-file-button").click();
  const form = panel(app.page).getByTestId("tree-new-file");
  await expect(form).toContainText("in apps/web/lib/");
  await app.page.keyboard.type("refunds.ts");
  await app.page.keyboard.press("Enter");
  const pane = app.page.locator('[data-testid=file-pane][data-path="apps/web/lib/refunds.ts"]');
  await expect(pane.locator(".cm-content")).toBeVisible();
  await expect(form).toHaveCount(0);
  await expect(row(app.page, "apps/web/lib/refunds.ts")).toBeVisible();
  expect(await app.page.evaluate(() => (window as unknown as W).__berthMockFiles.contentOf("apps/web/lib/refunds.ts"))).toBe("");

  // A name that is taken is refused, and nothing is replaced.
  await panel(app.page).getByTestId("tree-new-file-button").click();
  await app.page.keyboard.type("refunds.ts");
  await app.page.keyboard.press("Enter");
  await expect(form.getByRole("alert")).toContainText("already exists");
  await app.page.keyboard.press("Escape");
  await expect(form).toHaveCount(0);
});

test("below 1100px the panel floats over the tabs, and goes on esc or a pick", async ({ app }) => {
  await app.page.setViewportSize({ width: 1000, height: 800 });
  await app.open({ prefs: { filesPanel: true } });
  await app.openWorktree("devl/checkout-fix");
  // Docked open is a pref for wide windows: a narrow one starts without it.
  await expect(panel(app.page)).toHaveCount(0);
  await app.page.keyboard.press("Meta+Shift+E");
  await expect(panel(app.page)).toHaveAttribute("data-float", "true");
  await expect(app.page.getByTestId("tree-scrim")).toBeVisible();
  // Floating, it takes nothing from the panes.
  const area = await app.page.locator("[data-pane-area]").boundingBox();
  const main = await app.page.locator("main").boundingBox();
  expect(Math.round(area!.width)).toBe(Math.round(main!.width));

  await app.page.keyboard.press("Escape");
  await expect(panel(app.page)).toHaveCount(0);

  await app.page.getByTestId("files-panel-button").click();
  await expect(panel(app.page)).toBeVisible();
  await row(app.page, "README.md").click();
  await expect(app.page.locator('[data-testid=file-pane][data-path="README.md"] [data-testid=file-markdown]')).toBeVisible();
  await expect(panel(app.page)).toHaveCount(0);
  // The docked pref is left as it was.
  expect(((await app.stored("berth.prefs")) as { filesPanel?: boolean }).filesPanel).toBe(true);

  // Wider, it docks again.
  await app.page.setViewportSize({ width: 1300, height: 800 });
  await expect(panel(app.page)).toBeVisible();
  await expect(panel(app.page)).not.toHaveAttribute("data-float");
});

test("a big repository lists every folder, past the 20,000 ⌘P searches", async ({ app }) => {
  await app.open({ prefs: { filesPanel: true }, params: { big: "25000" } });
  await app.openWorktree("devl/checkout-fix");
  // services/ sorts after the cap's end; the panel lists it all the same.
  await openFolders(app.page, "services");
  await expect(tree(app.page).locator('[role=treeitem][data-path^="services/"]').first()).toBeVisible();
  await expect(panel(app.page).getByTestId("tree-truncated")).toHaveCount(0);
  // ⌘P says it searches the first 20,000.
  await app.page.keyboard.press("Meta+p");
  await app.page.keyboard.type("webhook");
  await expect(app.page.getByTestId("file-picker-truncated")).toBeVisible();
});
