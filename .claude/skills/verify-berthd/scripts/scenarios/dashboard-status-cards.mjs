// The Agent Dashboard's cards as live status cards (views/dashboard/
// status-card.tsx, lib/agent-run.ts): a working agent's current step, model,
// task progress and timed steps with Pause and Stop; an approval that swaps
// the footer for Approve and Deny without moving it; a finished turn's
// "Done in …" with no clock. Turned on in Settings first; they are off by
// default. features/dashboard-cards.md.

const card = (page, title) => page.locator("article[role=button]", { hasText: title });

export async function run({ page, shot, step, expect }) {
  step("turn on Live status cards in Settings › Agents › Agent Dashboard");
  await page.getByTestId("nav-settings").click();
  await page.getByTestId("settings-nav-agents").click();
  const live = page.getByTestId("settings-agents").getByRole("switch", { name: "Live status cards" });
  await expect(live).not.toBeChecked();
  await live.click();
  await expect(live).toBeChecked();
  await shot("0-setting");

  step("open the Agent Dashboard");
  await page.keyboard.press("Meta+j");
  await expect(page.getByRole("heading", { name: "Agent Dashboard" })).toBeVisible();

  step("a working agent: its step now, model, Step 4 of 6, the steps it took, Pause and Stop");
  const working = card(page, "Tune the judge prompt");
  await expect(working).toContainText("Running pnpm eval --suite judge");
  await expect(working).toContainText("Sonnet 4.5");
  await expect(working).toContainText("Step 4 of 6");
  const steps = working.getByRole("list", { name: "Steps" }).locator("li[data-status=done]");
  await expect(steps).toHaveCount(3);
  await expect(working.getByTestId("card-now")).toHaveAttribute("data-status", "active");
  await expect(steps.filter({ hasText: "Ran pnpm test payments" })).toHaveCount(1);
  // A finished step says how long it took.
  await expect(steps.filter({ hasText: "Ran pnpm test payments" })).toContainText(/\d+(\.\d)?s/);
  await expect(working.getByRole("button", { name: "Pause" })).toBeVisible();
  await expect(working.getByRole("button", { name: /^Stop / })).toBeVisible();
  await expect(working.getByRole("button", { name: /Open run/ })).toBeVisible();
  await expect(working.getByTestId("card-timer")).toBeVisible();
  await working.scrollIntoViewIfNeeded();
  await shot("1-board");

  step("an approval: the headline asks, the log row is tinted, Approve and Deny take the footer's place");
  const asking = card(page, "Fix checkout webhook retries");
  await expect(asking.getByTestId("card-now")).toContainText("Claude Code wants to run a command");
  await expect(asking.getByTestId("card-now")).toContainText("pnpm prisma migrate dev --name idempotency_key");
  const approve = asking.getByRole("button", { name: /^Approve/ });
  await expect(approve).toBeVisible();
  await expect(asking.getByRole("button", { name: /^Deny/ })).toBeVisible();
  const footer = asking.locator("footer");
  const before = await footer.boundingBox();
  const workingFooter = await working.locator("footer").boundingBox();
  if (Math.round(before.height) !== Math.round(workingFooter.height)) throw new Error(`approval footer is ${before.height}px, a working card's ${workingFooter.height}px`);
  await shot("2-approval");

  step("a finished turn: a check, Done in …, its steps, no clock, only Open run");
  const done = card(page, "Export orders as CSV from the admin");
  await expect(done).toContainText(/Done in \d+s/);
  await expect(done.getByTestId("card-timer")).toHaveCount(0);
  await expect(done.getByRole("button", { name: "Pause" })).toHaveCount(0);
  await expect(done.getByRole("button", { name: /^Stop / })).toHaveCount(0);
  await expect(done.getByRole("button", { name: /Open run/ })).toBeVisible();
  await done.scrollIntoViewIfNeeded();
  await shot("3-done");

  step("Approve answers the agent");
  await asking.scrollIntoViewIfNeeded();
  await approve.click();
  await expect(page.getByText("Approved", { exact: true }).first()).toBeVisible();
  await shot("4-approved");

  step("Open run opens the agent");
  await done.getByRole("button", { name: /Open run/ }).click();
  await expect(page.getByRole("heading", { name: "Agent Dashboard" })).toHaveCount(0);
  await shot("5-opened");
}
