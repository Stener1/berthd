import type { Page } from "@playwright/test";

import { type App, expect, mockOnly, test } from "./fixtures";

// The guided install (views/onboarding/guided-install.tsx): adding a box
// over SSH shows the plan with the agents to choose, then runs berth add
// ssh in a terminal here, full screen, beside a checklist the step markers
// keep up to date. The mock (lib/mock-install.ts) plays a fresh Ubuntu box:
// Enter to start, sudo asking for a password, a word in the host picking a
// path ("flaky" fails a step once, "newkey" an unknown host key).

test.beforeEach(() => mockOnly("the guided install's terminal is a mock fixture"));
test.describe.configure({ timeout: 60_000 });

const guided = (page: Page) => page.getByTestId("guided-install");
const step = (page: Page, id: string) => guided(page).getByTestId(`step-${id}`);

async function openPlan(app: App, host: string) {
  const { page } = app;
  await app.open();
  await app.openSettings("boxes");
  await page.getByRole("button", { name: "Add a box" }).first().click();
  await page.getByText("Or let Berth set it up over SSH").click();
  await page.getByLabel("SSH host, like me@my-box").fill(host);
  await page.getByTestId("ssh-set-up").click();
  await expect(guided(page)).toHaveAttribute("data-stage", "plan");
  await expect(page.getByTestId("install-plan")).toBeVisible();
}

async function startRun(page: Page) {
  await page.getByTestId("install-start").click();
  await expect(guided(page)).toHaveAttribute("data-stage", "run");
  await expect(page.getByTestId("install-banner")).toHaveAttribute("data-waiting", "enter");
  // The terminal takes the keyboard once it has drawn itself.
  await page.getByTestId("install-terminal").click();
  await expect.poll(() => page.evaluate(() => !!document.activeElement?.closest("[data-testid=install-terminal]"))).toBe(true);
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("install-banner")).not.toHaveAttribute("data-waiting", "enter");
}

test("the plan, then the terminal: Enter to start, sudo's password, Ready", async ({ app }) => {
  const { page } = app;
  await openPlan(app, "demo@my-box");
  // The plan says what runs, which steps need sudo, and the exact commands.
  const plan = page.getByTestId("install-plan");
  await expect(plan.getByTestId("plan-connect")).toContainText("Connect to demo@my-box");
  await expect(plan.getByTestId("sudo-badge")).toHaveCount(2);
  await plan.getByTestId("plan-tools").getByRole("button").click();
  await expect(page.getByTestId("plan-commands-tools")).toContainText("sudo apt-get install -y -q git");
  await expect(page.getByTestId("plan-commands-tools")).toContainText("~/.local/bin/tmux");
  // Claude Code is ticked the first time.
  await expect(page.getByTestId("agent-claude")).toHaveAttribute("data-checked", "true");
  await expect(plan.getByTestId("plan-agents")).toContainText("Claude Code");

  await startRun(page);
  // sudo asks on the box: the checklist and the banner say so.
  await expect(page.getByTestId("install-banner")).toHaveAttribute("data-waiting", "password", { timeout: 15_000 });
  await expect(step(page, "linger")).toHaveAttribute("data-state", "running");
  await expect(step(page, "linger").getByTestId("step-waiting")).toBeVisible();
  await expect(step(page, "berthd")).toHaveAttribute("data-state", "done");
  await page.keyboard.type("s3cret-pw");
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("install-ready")).toBeVisible({ timeout: 30_000 });
  for (const id of ["connect", "berthd", "linger", "tools", "agents", "integrations", "pair"]) await expect(step(page, id)).toHaveAttribute("data-state", "done");
  // The password went to the box's terminal and nowhere the app keeps.
  expect(await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.body.innerText)).not.toContain("s3cret-pw");
  await page.getByTestId("install-continue").click();
  await expect(guided(page)).toHaveCount(0);
  await expect(page.getByTestId("settings-boxes")).toContainText("my-box");
});

test("the agents chosen are installed, and remembered for the next box", async ({ app }) => {
  const { page } = app;
  await openPlan(app, "demo@my-box");
  await page.getByTestId("agent-codex").click();
  await expect(page.getByTestId("plan-agents")).toContainText("Claude Code and Codex");
  // Gemini needs Node, so Berth says how instead of offering it.
  await expect(page.getByTestId("agent-gemini")).toContainText("npm install -g @google/gemini-cli");
  expect(((await app.stored("berth.prefs")) as { installAgents: string[] }).installAgents).toEqual(["claude", "codex"]);
  await page.getByTestId("agent-claude").click();
  await expect(page.getByTestId("plan-agents")).toContainText("Codex");
  await startRun(page);
  await expect(step(page, "agents")).toContainText("Codex");
  await expect(page.getByTestId("install-banner")).toHaveAttribute("data-waiting", "password", { timeout: 15_000 });
  await page.keyboard.type("pw");
  await page.keyboard.press("Enter");
  await expect(step(page, "agents")).toHaveAttribute("data-state", "done", { timeout: 30_000 });
});

test("a failed step offers its command and Retry from it; the steps before stay done", async ({ app }) => {
  const { page } = app;
  await openPlan(app, "demo@flaky-box");
  await startRun(page);
  await expect(page.getByTestId("install-banner")).toHaveAttribute("data-waiting", "password", { timeout: 15_000 });
  await page.keyboard.type("pw");
  await page.keyboard.press("Enter");
  await expect(step(page, "tools")).toHaveAttribute("data-state", "fail", { timeout: 15_000 });
  await expect(page.getByTestId("install-status")).toHaveText("Stopped");
  // The command as it is typed, never made into a sentence.
  const line = step(page, "tools").getByTestId("command-line");
  await expect(line.locator("code")).toHaveText("sudo apt-get install -y -q git");
  await expect(line.getByRole("button", { name: "Run in terminal" })).toBeVisible();
  await expect(step(page, "linger")).toHaveAttribute("data-state", "done");
  await step(page, "tools").getByTestId("retry-tools").click();
  await expect(step(page, "linger")).toHaveAttribute("data-state", "done");
  await expect(step(page, "tools")).toHaveAttribute("data-state", "running");
  await expect(page.getByTestId("install-ready")).toBeVisible({ timeout: 30_000 });
  await expect(step(page, "tools")).toHaveAttribute("data-state", "done");
});

test("a box this computer hasn't met: trust its key, and it goes on", async ({ app }) => {
  const { page } = app;
  await openPlan(app, "demo@newkey-box");
  await page.getByTestId("install-start").click();
  await expect(step(page, "connect")).toHaveAttribute("data-state", "fail");
  await expect(guided(page)).toContainText("SHA256:Zm9yLWRlbW8tb25seS1ub3QtYS1yZWFsLWtleQ");
  await guided(page).getByRole("button", { name: "Trust and connect" }).click();
  await expect(step(page, "connect")).toHaveAttribute("data-state", "done");
  await expect(page.getByTestId("install-banner")).toHaveAttribute("data-waiting", "enter");
});

test("Add agents from a box's settings runs in the same terminal and checklist", async ({ app }) => {
  const { page } = app;
  await app.open();
  const boxes = await app.openSettings("boxes");
  await boxes.getByRole("button", { name: "devl actions" }).click();
  await page.getByTestId("box-add-agents").click();
  const view = page.getByTestId("add-agents");
  await expect(view.getByTestId("agent-claude")).toContainText("Installed on this box");
  await view.getByTestId("agent-cursor").click();
  await view.getByTestId("add-agents-start").click();
  await expect(view).toHaveAttribute("data-stage", "run");
  await expect(view.getByTestId("step-agent-cursor")).toHaveAttribute("data-state", "done", { timeout: 15_000 });
  await expect(view.getByTestId("step-integrations")).toHaveAttribute("data-state", "done");
  await view.getByTestId("add-agents-done").click();
  await expect(view).toHaveCount(0);
});
