// A finished turn turn check read as working (its work runs on: a delegated
// task it waits on, a build, a deploy) wears an hourglass with why in its
// tip, in the sidebar and on its tab; a turn still going keeps the spinner.
// features/turn-check-indicator.md.

export const params = { sidebar: "list" };

const WHY = "Its turn ended while its work runs on: a delegated task, a build, a deploy or a watcher. It picks up by itself.";

export async function run({ page, shot, step, expect }) {
  const row = (w) => page.locator(`[data-testid=worktree-row][data-worktree="${w}"]`);
  const tip = page.locator("[data-slot=tooltip-popup]").filter({ hasText: WHY });

  step("the sidebar: order-export (turn ended, work runs on) shows the hourglass, qa-deck (a live turn) the spinner");
  await expect(row("devl/order-export").getByRole("img", { name: "Waiting on its work" })).toBeVisible();
  await expect(row("devl/order-export").getByRole("img", { name: "Working" })).toHaveCount(0);
  await expect(row("devl/qa-deck").getByRole("img", { name: "Working" })).toBeVisible();
  await expect(row("devl/qa-deck").getByTestId("on-its-work")).toHaveCount(0);

  step("hover the hourglass: its tip says why");
  await row("devl/order-export").getByTestId("on-its-work").hover();
  await expect(tip).toBeVisible();
  await expect(tip).toHaveCSS("opacity", "1");
  await shot("1-sidebar-hourglass-tip");

  step("open order-export: the tab of 'Add tests for the export job' shows the hourglass, and its tip says why");
  await row("devl/order-export").click({ position: { x: 32, y: 12 } });
  const tab = page.getByRole("tab", { name: /Add tests for the export job/ });
  await expect(tab.getByRole("img", { name: "Waiting on its work" })).toBeVisible();
  await expect(tab.getByRole("img", { name: "Working" })).toHaveCount(0);
  await tab.hover();
  await expect(tip).toBeVisible();
  await expect(tip).toHaveCSS("opacity", "1");
  await shot("2-tab-hourglass-tip");

  step("open qa-deck: its live turn's tab keeps the spinner");
  await page.mouse.move(900, 500);
  await row("devl/qa-deck").click({ position: { x: 32, y: 12 } });
  const live = page.getByRole("tab", { name: /Build the QA deck for the release/ });
  await expect(live.getByRole("img", { name: "Working" })).toBeVisible();
  await expect(live.getByTestId("on-its-work")).toHaveCount(0);
  await shot("3-tab-live-spinner");
}
