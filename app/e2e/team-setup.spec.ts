import { expect, mockOnly, test } from "./fixtures";
import type { Page } from "@playwright/test";

// Team setup (views/team, lib/mock-team.ts): an org's <org>/.berth read
// with this computer's gh, one page from the org card to "You're set up".
// The mock's ?team= picks a scenario; &teamhold= holds the run at a point;
// window.__teamMock.advance(name) moves it on.

test.beforeEach(() => mockOnly("Team setup's scenarios are mock fixtures"));

const advance = (page: Page, name: string) => page.evaluate((n) => (window as unknown as { __teamMock: { advance(n: string): void } }).__teamMock.advance(n), name);
const step = (page: Page, id: string) => page.getByTestId(`step-${id}`);
const repo = (page: Page, id: string) => page.getByTestId(`repo-${id}`);
const checklist = (page: Page) => page.getByTestId("team-checklist").locator("visible=true");

async function startRun(page: Page) {
  await checklist(page).getByTestId("team-run").click();
  await expect(step(page, "update")).toHaveAttribute("data-state", "waiting");
}

test("first run: Joining a team? opens the team setup in the whole window", async ({ app }) => {
  const { page } = app;
  await page.goto("/?mock=1&fresh=1");
  await expect(page.getByRole("heading", { name: "Joining a team?" })).toBeVisible();
  await page.getByLabel("GitHub org").fill("github.com/calcom");
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("heading", { name: "Cal.com team setup" })).toBeVisible();
  // Still first run: no sidebar yet, and the box is the one thing to add.
  await expect(page.getByTestId("nav-home")).toHaveCount(0);
  await expect(checklist(page).getByRole("button", { name: "Add your box" })).toBeVisible();
  await expect(checklist(page).getByTestId("team-run")).toBeDisabled();
  await expect(checklist(page)).toContainText("Pick or add the box to set up");
});

test("Add a box offers to set the box up for a team", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom" } });
  await page.getByLabel("Projects options").click();
  await page.getByRole("menuitem", { name: /Add a box/ }).click();
  await page.getByTestId("addbox-team").click();
  await expect(page.getByRole("heading", { name: "Which GitHub org?" })).toBeVisible();
  await page.getByLabel("GitHub org").fill("calcom");
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("heading", { name: "Cal.com team setup" })).toBeVisible();
});

test("a berth://team link opens the org's setup, read like its repo on GitHub", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", "team-link": "calcom" } });
  const pageEl = page.getByTestId("team-page");
  await expect(pageEl).toContainText("Opened from a link");
  await expect(pageEl).toContainText("Published by Cal.com on GitHub");
  await expect(pageEl).toContainText("Verified");
  await expect(pageEl).toContainText("keithwillcode");
  await expect(page.getByTestId("team-commit")).toHaveText("4e1c9a2");
  await expect(checklist(page)).toContainText("You have access to all 3 repos");
  // The new box is picked: the one with no projects yet.
  await expect(checklist(page).getByRole("combobox")).toContainText("sean-dev");
});

test("without the GitHub CLI, the page says how to install it", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "nogh", "team-link": "calcom" } });
  await expect(page.getByTestId("team-github")).toHaveAttribute("data-state", "missing");
  await expect(page.getByRole("heading", { name: "Install GitHub's CLI first" })).toBeVisible();
  await expect(page.getByTestId("team-github")).toContainText("brew install gh");
  // Sign-in comes before the org card.
  await expect(page.getByRole("heading", { name: "Cal.com team setup" })).toHaveCount(0);
});

test("signed out: Connect GitHub runs gh auth login, and the page carries on when it's done", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "signedout", "team-link": "calcom" } });
  await page.getByTestId("connect-github").click();
  await expect(page.getByText("Waiting for gh auth login to finish")).toBeVisible();
  await expect(page.getByTestId("team-github")).toContainText("gh auth login");
  await expect(page.getByRole("heading", { name: "Cal.com team setup" })).toBeVisible({ timeout: 8000 });
});

test("every line opens to its commands, and Read every command shows the .berth files", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", "team-page": "calcom" } });
  await expect(step(page, "docker")).toContainText("sudo");
  await step(page, "docker").getByRole("button").first().click();
  await expect(step(page, "docker")).toContainText("sudo usermod -aG docker $USER");
  await repo(page, "cal").getByRole("button").first().click();
  await expect(repo(page, "cal")).toContainText("git clone https://github.com/calcom/cal.com");
  await expect(step(page, "github")).toContainText("Berth");
  // The keys are 1Password references: Berth signs op in on the box, as a
  // step of its own after GitHub's.
  await expect(step(page, "1password")).toContainText("1Password on the box");
  await expect(step(page, "1password")).toContainText("Berth");
  await step(page, "1password").getByRole("button").first().click();
  await expect(step(page, "1password")).toContainText("berthd secret signin");
  await page.getByTestId("read-every-command").click();
  await expect(page.getByTestId("team-file")).toContainText("set -euo pipefail");
  await expect(page.getByText(/lines call sudo, marked/)).toBeVisible();
  await page.getByRole("tab", { name: "team.json" }).click();
  await expect(page.getByTestId("team-file")).toContainText('"schema": "berth.team/v1"');
});

test("no access: You can't read this setup", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "unreadable", "team-link": "calcom" } });
  await expect(page.getByRole("heading", { name: "You can't read this setup" })).toBeVisible();
  await expect(page.getByTestId("team-unreadable")).toBeVisible();
  await expect(page.getByRole("button", { name: "Check again" })).toBeVisible();
  await expect(page.getByTestId("team-checklist")).toHaveCount(0);
});

test("partial access: the repo you can't read is left out, and the rest set up", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "partial", "team-page": "calcom" } });
  await expect(page.getByTestId("access-partial").locator("visible=true")).toContainText("You can't read calcom/private-api: ask an admin");
  await expect(page.getByTestId("access-partial").locator("visible=true")).toContainText("Set up the other 2");
  await expect(repo(page, "private-api")).toContainText("You can't read it");
  await expect(checklist(page).getByTestId("team-run")).toBeEnabled();
});

test("box and keys: one line, the asked key entered once", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", "team-page": "calcom" } });
  const keys = checklist(page).getByTestId("keys-line");
  await expect(keys).toHaveText("5 from 1Password · 1 to enter");
  await checklist(page).getByLabel(/SENDGRID_API_KEY/).fill("SG.test");
  await expect(keys).toHaveText("5 from 1Password · 1 entered");
  await expect(checklist(page)).toContainText("4 steps ask for your password on sean-dev");
  await expect(checklist(page)).toContainText("It signs in to GitHub itself, and to 1Password, in its own terminal");
});

test("running: sudo waits in the box's terminal, then the box signs in to GitHub with a code", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", teamhold: "github", "team-page": "calcom" } });
  await startRun(page);
  await expect(step(page, "update")).toContainText("Type it in the terminal below; Berth doesn't see or keep it");
  // The sidebar and the status bar show it from the start.
  await expect(page.getByTestId("team-status")).toContainText("Setting up sean-dev for Cal.com · waiting for your password");
  await expect(page.getByTestId("team-sidebar")).toContainText("queued");
  // The password goes to the terminal, never to Berth.
  await step(page, "update").locator("[data-pane-kind], .xterm, canvas, [contenteditable]").first().click().catch(() => {});
  await page.keyboard.type("hunter2");
  await page.keyboard.press("Enter");
  await advance(page, "sudo");
  await expect(step(page, "github")).toHaveAttribute("data-state", "waiting", { timeout: 15_000 });
  await expect(page.getByTestId("device-code")).toContainText("C4L1-7Q2M");
  await expect(page.getByRole("button", { name: /Open github.com\/login\/device/ })).toBeVisible();
  await expect(page.getByTestId("team-runcard").locator("visible=true")).toContainText("Sign the box in to GitHub");
});

test("1Password: op asks in the box's terminal, which the step shows, then the setup carries on", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", teamhold: "1password", "team-page": "calcom" } });
  await startRun(page);
  await advance(page, "sudo");
  await expect(step(page, "1password")).toHaveAttribute("data-state", "waiting", { timeout: 15_000 });
  await expect(step(page, "1password")).toContainText("op on sean-dev is asking you to sign in to 1Password");
  await expect(step(page, "1password")).toContainText("Berth keeps only op's session there");
  await expect(page.getByTestId("team-runcard").locator("visible=true")).toContainText("Sign the box in to 1Password");
  await expect(page.getByTestId("team-status")).toContainText("waiting for 1Password");
  await advance(page, "1password");
  // The box is done (its steps fold into one line), and the repos follow.
  await expect(step(page, "1password")).toHaveCount(0, { timeout: 10_000 });
  await expect(repo(page, "cal")).toHaveAttribute("data-state", "ready", { timeout: 20_000 });
});

test("a box without tmux: the Box check says how to install it, and setup waits for it", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", tmux: "missing", "team-page": "calcom" } });
  const box = page.getByTestId("check-box").locator("visible=true");
  await expect(box).toHaveAttribute("data-state", "warn");
  await expect(box).toContainText("Install tmux on sean-dev");
  await expect(box).toContainText("Berth runs the team setup's steps, and later your agents, in tmux on the box.");
  await expect(box).toContainText("sudo apt install tmux");
  await expect(checklist(page).getByTestId("team-run")).toBeDisabled();
  await expect(checklist(page)).toContainText("Install tmux on sean-dev first");
  // Installed on the box: Check again finds it.
  await box.getByRole("button", { name: "Check again" }).click();
  await box.getByRole("button", { name: "Check again" }).click();
  await expect(box).toHaveAttribute("data-state", "done");
  await expect(checklist(page).getByTestId("team-run")).toBeEnabled();
});

test("a repo that's ready offers Start on it while the others set up", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", teamhold: "repos", "team-page": "calcom" } });
  await startRun(page);
  await advance(page, "sudo");
  await expect(repo(page, "cal")).toHaveAttribute("data-state", "ready", { timeout: 20_000 });
  await expect(repo(page, "private-api")).toHaveAttribute("data-state", "setting-up");
  await expect(page.getByTestId("team-sidebar")).toContainText("new");
  await expect(page.getByTestId("team-status")).toContainText("1 of 3 repos ready");
  await repo(page, "cal").getByRole("button", { name: "Start on cal" }).click();
  // The composer, on that repo and box, for a task there.
  const composer = page.getByRole("dialog");
  await expect(composer).toBeVisible();
  await expect(composer).toContainText("cal");
  await expect(composer).toContainText("sean-dev");
});

test("a failed step shows why, and Retry goes on from it to You're set up", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "fail", "team-page": "calcom" } });
  await startRun(page);
  await advance(page, "sudo");
  await expect(step(page, "postgres")).toHaveAttribute("data-state", "failed", { timeout: 15_000 });
  await expect(step(page, "postgres")).toContainText("port is already allocated");
  await expect(step(page, "docker")).toHaveAttribute("data-state", "done");
  await expect(page.getByTestId("team-status")).toContainText("Cal.com setup stopped at Postgres in Docker");
  await step(page, "postgres").getByRole("button", { name: "Retry from Postgres" }).click();
  await expect(page.getByRole("heading", { name: "You're set up for Cal.com" })).toBeVisible({ timeout: 25_000 });
});

test("You're set up: the team's first task and each repo", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "done", "team-page": "calcom" } });
  const done = page.getByTestId("team-done");
  await expect(done.getByRole("heading", { name: "You're set up for Cal.com" })).toBeVisible();
  await expect(done.getByLabel("First task", { exact: true })).toHaveValue(/good first issue/);
  await expect(done.getByTestId("start-first-task")).toBeEnabled();
  await expect(done).toContainText("calcom/private-api");
  await expect(done).toContainText("Berth never had it, and nothing kept it");
});

test("an update shows on this page as a diff, and runs only when asked", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "update" } });
  const card = page.getByTestId("team-update-card");
  await expect(card).toContainText("Cal.com's setup changed");
  await card.getByRole("button", { name: "Review update" }).click();
  const diff = page.getByTestId("team-diff");
  await expect(diff).toContainText("Node 20 → 22");
  await expect(diff).toContainText("calcom/cal-video");
  await expect(diff.locator("li", { hasText: "Playwright's system libraries" })).toContainText("sudo");
  await expect(page.getByText(/is a new step that asks for your password/)).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await checklist(page).getByRole("button", { name: "Update sean-dev" }).click();
  await expect(page.getByTestId("team-runcard").locator("visible=true")).toBeVisible();
  await expect(card).toHaveCount(0);
});

test("an org without .berth: its repos, the Berth-configured ones picked", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", "team-page": "northwind" } });
  await expect(page.getByTestId("team-none")).toHaveText("Northwind Labs");
  const box = (name: string) => page.getByRole("checkbox", { name: new RegExp(`^${name}\\b`) });
  await expect(box("northwind/storefront")).toBeChecked();
  await expect(box("northwind/api")).toBeChecked();
  await expect(box("northwind/handbook")).not.toBeChecked();
  await expect(checklist(page).getByTestId("team-run")).toHaveText("Set up 2 repos");
  await box("northwind/api").click();
  await expect(checklist(page).getByTestId("team-run")).toHaveText("Set up 1 repo");
});

test("in the background, a toast says when a repo is ready", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", teamhold: "repos", "team-page": "calcom" } });
  await startRun(page);
  await advance(page, "sudo");
  await page.getByTestId("team-runcard").locator("visible=true").getByRole("button", { name: "Run in background" }).click();
  await expect(page.getByText("cal is ready")).toBeVisible({ timeout: 20_000 });
  await expect(page.getByRole("button", { name: "Start on cal" })).toBeVisible();
});

test("the page fits 900px: the plan starts above the fold", async ({ app }) => {
  const { page } = app;
  await page.setViewportSize({ width: 900, height: 720 });
  await app.open({ params: { team: "calcom", "team-page": "calcom" } });
  await expect(page.getByTestId("team-checklist").locator("visible=true")).toHaveAttribute("data-compact", "true");
  const first = await step(page, "packages").boundingBox();
  expect(first && first.y + first.height).toBeLessThan(720 - 26);
});

// A setup can also load from a link to any repo, branch or folder, to try
// one before <org>/.berth exists. The card names where it came from and
// claims nothing for the team it is for.
const LINK = "https://github.com/sean-brydon/berth-kit-calcom/tree/team-setup/team";

async function expectFromLink(page: Page) {
  await expect(page.getByRole("heading", { name: "Cal.com team setup" })).toBeVisible();
  await expect(page.getByTestId("team-source")).toContainText("sean-brydon/berth-kit-calcom · team-setup branch · team/");
  const provenance = page.getByTestId("team-provenance");
  await expect(provenance).toContainText("A team setup for Cal.com, loaded from sean-brydon's repo, not from calcom/.berth");
  await expect(page.getByTestId("team-page")).not.toContainText("Published by");
  await expect(page.getByTestId("team-page").getByText("Verified")).toHaveCount(0);
  await expect(page.getByTestId("team-commit")).toHaveText("b81d0e4");
  await expect(checklist(page).getByTestId("team-run")).toHaveText("Set up for Cal.com");
}

test("a berth://team?src= link opens a setup from a repo's branch, and sets the box up from it", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", "team-link-src": LINK } });
  await expect(page.getByTestId("team-page")).toContainText("Opened from a link");
  await expectFromLink(page);
  await page.getByTestId("read-every-command").click();
  await expect(page.getByRole("tab", { name: "team/team.json" })).toBeVisible();
  await page.keyboard.press("Escape");
  await startRun(page);
  await expect(page.getByTestId("team-sidebar")).toContainText("Cal.com on sean-dev");
  await advance(page, "sudo");
  await expect(repo(page, "cal")).toHaveAttribute("data-state", "ready", { timeout: 20_000 });
});

test("a setup link typed where an org goes opens it too", async ({ app }) => {
  const { page } = app;
  await page.goto("/?mock=1&fresh=1");
  await page.getByLabel("GitHub org").fill(LINK);
  await page.getByRole("button", { name: "Continue" }).click();
  await expectFromLink(page);
});

test("a setup link pasted in the command palette opens it", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom" } });
  await page.getByRole("button", { name: /Search/ }).first().click();
  await page.keyboard.type("sean-brydon/berth-kit-calcom/tree/team-setup/team");
  await page.getByRole("option", { name: /Open team setup from link/ }).click();
  await expectFromLink(page);
});

test("a link that can't be read says so, and never offers the repo picker", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { team: "calcom", "team-link-src": "github.com/someone/private-setup@main" } });
  await expect(page.getByRole("heading", { name: "You can't read this setup" })).toBeVisible();
  await expect(page.getByTestId("team-unreadable")).toContainText("someone/private-setup@main");
  await expect(page.getByTestId("team-none")).toHaveCount(0);
});
