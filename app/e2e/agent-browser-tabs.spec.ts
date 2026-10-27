import { type App, expect, mockOnly, test } from "./fixtures";

// A worktree's agent browser has tabs on a box that says "browser.tabs":
// each agent that asks gets a page of its own. The agent's view shows them
// in a strip, watches the one you pick, and never moves on its own while
// agents work in the others. The mock's checkout-fix worktree on devl has
// Main (the cart, used last), signup-a, signup-b and onboarding;
// window.__berthMockBrowser stands in for the agents.

type MockBrowser = { __berthMockBrowser: { navigate(tab: string, path: string): void; openTab(name: string): void; closeTab(name: string): void } };

async function agentView(app: App) {
  await app.open();
  const page = app.page;
  await app.openWorktree("devl/checkout-fix");
  await page.getByRole("button", { name: "New tab" }).click();
  await page.getByRole("option", { name: /New browser tab/ }).click();
  const pane = page.locator("[data-testid=browser-pane]:visible");
  await pane.getByRole("button", { name: /Agent's view/ }).click();
  const strip = pane.getByRole("group", { name: "The agent's browser's tabs" });
  await expect(strip).toBeVisible();
  return { page, pane, strip, frame: pane.getByTestId("agent-frame"), tab: (name: string) => strip.getByRole("button", { name, exact: true }) };
}

test("the agent's view lists the browser's tabs, opens on the one used last, and watches the one you pick", async ({ app }) => {
  mockOnly("casts the demo's agent browser and its tabs");
  const { pane, strip, frame, tab } = await agentView(app);
  await expect(strip.getByRole("button")).toHaveText(["Main", "signup-a", "signup-b", "onboarding"]);
  await expect(tab("Main")).toHaveAttribute("aria-pressed", "true");
  await expect(frame).toHaveAttribute("data-tab", "");
  await expect(pane.getByText(/\/cart$/)).toBeVisible();

  await tab("signup-b").click();
  await expect(tab("signup-b")).toHaveAttribute("aria-pressed", "true");
  await expect(tab("Main")).toHaveAttribute("aria-pressed", "false");
  await expect(frame).toHaveAttribute("data-tab", "signup-b");
  await expect(pane.getByText(/\/signup\?plan=b$/)).toBeVisible();
  // The Console drawer has the watched tab's lines, not main's.
  await pane.getByTestId("devtools-toggle").click();
  const rows = pane.getByTestId("devtools-drawer").getByTestId("console-row");
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText("signup-b: Sign up: plan B is ready");
  await pane.getByTestId("devtools-toggle").click();

  // Each tab has its own size, and the bar says the watched one's.
  await tab("onboarding").click();
  await expect(frame).toHaveAttribute("data-size", "390×844 @3x");
  await expect(pane.getByTestId("agent-size")).toContainText("390×844 @3x");
  await expect(pane.getByText(/\/welcome$/)).toBeVisible();
});

test("agents busy in other tabs never move the view off the one you watch", async ({ app }) => {
  mockOnly("plays agents in the mock's browser tabs");
  const { page, pane, strip, frame, tab } = await agentView(app);
  await tab("signup-a").click();
  await expect(frame).toHaveAttribute("data-tab", "signup-a");

  // Another agent moves signup-b on and opens a tab of its own: the strip
  // shows it, and the view stays where it was.
  await page.evaluate(() => (window as unknown as MockBrowser).__berthMockBrowser.navigate("signup-b", "/signup/verify?plan=b"));
  await expect(tab("signup-b")).toHaveAttribute("data-active", "true");
  await page.evaluate(() => (window as unknown as MockBrowser).__berthMockBrowser.openTab("checkout-c"));
  await expect(tab("checkout-c")).toBeVisible();
  await expect(strip.getByRole("button", { pressed: true })).toHaveText("signup-a");
  await expect(frame).toHaveAttribute("data-tab", "signup-a");
  await expect(pane.getByText(/\/signup\?plan=a$/)).toBeVisible();

  // And it keeps staying: another poll and more activity later, still signup-a.
  await page.evaluate(() => (window as unknown as MockBrowser).__berthMockBrowser.navigate("checkout-c", "/checkout"));
  await expect(tab("checkout-c")).toHaveAttribute("data-active", "true");
  await expect(strip.getByRole("button", { pressed: true })).toHaveText("signup-a");
  await expect(frame).toHaveAttribute("data-tab", "signup-a");
});

test("when the watched tab closes, the view moves to the tab used last and says so", async ({ app }) => {
  mockOnly("plays agents in the mock's browser tabs");
  const { page, pane, frame, tab } = await agentView(app);
  await tab("signup-a").click();
  await expect(frame).toHaveAttribute("data-tab", "signup-a");
  await page.evaluate(() => (window as unknown as MockBrowser).__berthMockBrowser.navigate("signup-b", "/signup/verify?plan=b"));
  await expect(tab("signup-b")).toHaveAttribute("data-active", "true");

  await page.evaluate(() => (window as unknown as MockBrowser).__berthMockBrowser.closeTab("signup-a"));
  await expect(pane.getByRole("status").filter({ hasText: "signup-a closed. Watching signup-b." })).toBeVisible();
  await expect(tab("signup-a")).toHaveCount(0);
  await expect(tab("signup-b")).toHaveAttribute("aria-pressed", "true");
  await expect(frame).toHaveAttribute("data-tab", "signup-b");
  await expect(pane.getByText(/\/signup\/verify\?plan=b$/)).toBeVisible();
});
