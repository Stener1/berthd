import { expect, mockOnly, test } from "./fixtures";

// When an agent opens its browser (berthd browser open) in the worktree you
// look at, its live view opens beside the agent, so the chat and the page
// show side by side. window.__berthMockBrowser.opened() plays the event the
// box sends as the checkout-fix worktree's browser starts.

type MockBrowser = { __berthMockBrowser: { opened(): void } };

test("an agent opening its browser shows its live view beside the agent, and the keyboard stays with the agent", async ({ app }) => {
  mockOnly("plays an agent opening its browser");
  await app.open();
  const page = app.page;
  await app.openWorktree("devl/checkout-fix");
  const agent = app.panes.and(page.locator("[data-pane-kind=terminal]"));
  await expect(app.panes).toHaveCount(1);
  await expect(agent).toHaveAttribute("data-pane-focused", "");

  await page.evaluate(() => (window as unknown as MockBrowser).__berthMockBrowser.opened());
  const browser = app.panes.and(page.locator("[data-pane-kind=browser]"));
  await expect(app.panes).toHaveCount(2);
  await expect(browser.getByTestId("agent-frame")).toBeVisible({ timeout: 15_000 });
  await expect(browser.getByText("Agent's view · live").first()).toBeVisible();
  // Beside the agent, on its right, and the agent keeps the focus.
  await expect.poll(async () => (await browser.boundingBox())!.x - (await agent.boundingBox())!.x).toBeGreaterThan(0);
  await expect(agent).toHaveAttribute("data-pane-focused", "");

  // Back to your own view; the next time the agent opens its browser, the
  // same pane shows it again rather than a second one opening.
  await browser.getByRole("button", { name: "Your view", exact: true }).click();
  await expect(browser.getByRole("textbox", { name: "Address" })).toBeVisible();
  await page.evaluate(() => (window as unknown as MockBrowser).__berthMockBrowser.opened());
  await expect(browser.getByTestId("agent-frame")).toBeVisible({ timeout: 15_000 });
  await expect(app.panes).toHaveCount(2);
});
