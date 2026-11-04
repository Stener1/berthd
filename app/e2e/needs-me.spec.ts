import type { Page } from "@playwright/test";

import { expect, mockOnly, test } from "./fixtures";

// Needs me on the mock's ?scene=needs-me, a fleet with one of everything it
// sorts: one piece of work fills the screen, in a fixed order, and everything
// leaves. window.__berthMockWork stands in for what a box or the clock would do.

type MockWork = {
  __berthMockWork: {
    arrive(name: string, title: string): void;
    pass(id: string): void;
    clear(): void;
    refuse(): void;
    moveOn(session: string): void;
    record(id: string): { status: string; not_until?: string; waiting_on?: string; check_back?: string } | undefined;
    sent: Record<string, string>;
  };
};
const work = (page: Page) => page.evaluateHandle(() => (window as unknown as MockWork).__berthMockWork);

const ORDER = [
  "Choose the checkout rollout", // pinned decision
  "Pick the pricing copy", // decisions, longest unseen first
  "Approve the database migration",
  "Ship the spring release",
  "Add the staging API key", // pinned task, by its repo's pin
  "Add the DNS record for acme.example", // task
  "Retry the failed webhooks", // result
  "Spike the search index", // close-out
  "Group Fix the cart badge with Test the cart badge?", // suggested group
];

test.beforeEach(() => mockOnly("runs on the mock's scene"));

async function open(app: import("./fixtures").App) {
  await app.open({ params: { scene: "needs-me", still: "1", labs: "0", sidebar: "list" } });
  await app.page.getByTestId("nav-needs-me").click();
  await expect(title(app.page)).toHaveText(ORDER[0]);
}

const title = (page: Page) => page.getByTestId("needs-me-title");
const left = (page: Page) => page.getByTestId("needs-me-left");
const rows = (page: Page) => page.getByTestId("needs-me-row");
const button = (page: Page, name: string) => page.getByTestId("needs-me-card").getByRole("button", { name, exact: true });

test("the queue comes in order, and the count on the menu is the list's length", async ({ app, page }) => {
  await open(app);
  await expect(left(page)).toHaveText("9 left, about 12 min");
  await expect(page.getByTestId("nav-needs-me").locator("..").getByTestId("nav-badge")).toHaveText("9");
  await expect(rows(page)).toHaveCount(ORDER.length);
  await expect(rows(page).locator(".text-row")).toHaveText(ORDER);
});

test("queued and parked sessions stay out", async ({ app, page }) => {
  await open(app);
  await expect(page.getByText("Wait for the queued prompt")).toHaveCount(0);
  await expect(page.getByText("Check again in the morning")).toHaveCount(0);
});

test("j walks through everything in order, and k comes back", async ({ app, page }) => {
  await open(app);
  for (const t of ORDER) {
    await expect(title(page)).toHaveText(t);
    await page.keyboard.press("j");
  }
  // Past the last, the round starts again rather than stopping.
  await expect(title(page)).toHaveText(ORDER[0]);
  await page.keyboard.press("k");
  await expect(title(page)).toHaveText(ORDER[ORDER.length - 1]);
});

test("a pinned question arriving mid-card becomes next and doesn't move the current card", async ({ app, page }) => {
  await open(app);
  await page.keyboard.press("j");
  await expect(title(page)).toHaveText("Pick the pricing copy");
  await page.getByRole("textbox", { name: "Reply" }).fill("half-typed answer");

  await (await work(page)).evaluate((w) => w.arrive("hotfix-claude", "Decide on the hotfix"));

  await expect(left(page)).toHaveText("10 left, about 13 min", { timeout: 15_000 });
  await expect(rows(page).nth(1).locator(".text-row")).toHaveText("Decide on the hotfix");
  // The card in front stays, with what was typed in it.
  await expect(title(page)).toHaveText("Pick the pricing copy");
  await expect(page.getByRole("textbox", { name: "Reply" })).toHaveValue("half-typed answer");
  await expect(rows(page).first()).toHaveAttribute("aria-current", "true");

  await page.getByRole("textbox", { name: "Reply" }).fill("");
  await page.getByRole("heading", { name: "Pick the pricing copy" }).click();
  await page.keyboard.press("j");
  await expect(title(page)).toHaveText("Decide on the hotfix");
});

test("a form is answered with the number keys, and the next card comes up", async ({ app, page }) => {
  await open(app);
  const form = page.getByTestId("question-form");
  await expect(form).toContainText("How should acme roll out the new checkout (ACME-12)?");
  await page.keyboard.press("2");
  await expect(form.getByRole("radio", { name: /All at once/ })).toBeChecked();
  await page.keyboard.press("1");
  await expect(form.getByRole("radio", { name: /Flag first/ })).toBeChecked();
  await page.keyboard.press("Enter");

  await expect(title(page)).toHaveText("Pick the pricing copy");
  await expect(left(page)).toHaveText("8 left, about 10 min");
  const sent = await (await work(page)).evaluate((w) => w.sent["devl/rollout-claude"]);
  expect(sent).toBe("Flag first");
});

test("the issue sheet has the keyboard: the card's shortcuts don't reach through it", async ({ app, page }) => {
  await open(app);
  await page.getByTestId("issue-chip").first().click();
  await expect(page.getByTestId("issue-sheet")).toBeVisible();
  // Close, and a number key that would pick an option.
  await page.keyboard.press("d");
  await page.keyboard.press("1");
  await expect(page.getByTestId("issue-sheet")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("issue-sheet")).toBeHidden();
  await expect(title(page)).toHaveText(ORDER[0]);
  await expect(page.getByTestId("question-form").getByRole("radio", { name: /Flag first/ })).not.toBeChecked();
  expect((await (await work(page)).evaluate((w) => w.record("w_rollout")))?.status).toBe("open");
});

test("a change the box refuses says so, and the card stays", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Retry the failed webhooks" }).click();
  await (await work(page)).evaluate((w) => w.refuse());
  await button(page, "Read").click();
  await expect(page.getByText("Couldn't mark it seen")).toBeVisible();
  await expect(title(page)).toHaveText("Retry the failed webhooks");
});

test("a permission that has moved on isn't answered", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Approve the database migration" }).click();
  await expect(page.getByTestId("needs-me-choices").getByRole("button")).toHaveCount(3);
  await (await work(page)).evaluate((w) => w.moveOn("migrate-claude"));
  await page.getByTestId("needs-me-choices").getByRole("button").first().click();
  await expect(page.getByText(/moved on/).first()).toBeVisible();
  expect(await (await work(page)).evaluate((w) => w.sent["devl/migrate-claude"])).toBeUndefined();
});

test("a question with no form shows its last message and a reply box, and agent text stays text", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Pick the pricing copy" }).click();
  await expect(page.getByTestId("needs-me-why")).toHaveText("Claude Code asked in its last message");
  await expect(page.getByTestId("needs-me-text")).toContainText('Should the pricing page say "from $9"');
  await page.getByRole("textbox", { name: "Reply" }).fill("The second one.");
  await page.getByRole("button", { name: "Reply", exact: true }).click();
  await expect(title(page)).not.toHaveText("Pick the pricing copy");
  expect(await (await work(page)).evaluate((w) => w.sent["devl/pricing-claude"])).toBe("The second one.");

  await rows(page).filter({ hasText: "Retry the failed webhooks" }).click();
  // Markup in what an agent says is only ever drawn as its letters.
  await expect(page.getByTestId("needs-me-text")).toContainText("<script>alert(1)</script>");
  await expect(page.getByTestId("needs-me-text").locator("script")).toHaveCount(0);
});

test("a permission's answers go by their key", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Approve the database migration" }).click();
  await expect(page.getByTestId("needs-me-text").first()).toContainText("Wants to use Bash");
  await expect(page.getByTestId("needs-me-choices").getByRole("button")).toHaveCount(3);
  await page.keyboard.press("1");
  await expect(title(page)).not.toHaveText("Approve the database migration");
  expect(await (await work(page)).evaluate((w) => w.sent["devl/migrate-claude"])).toBe("1");
});

test("Not now leaves and comes back when due", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Retry the failed webhooks" }).click();
  await button(page, "Not now").click();
  await page.getByTestId("needs-me-not-now").getByRole("button", { name: "Tomorrow" }).click();

  await expect(rows(page).filter({ hasText: "Retry the failed webhooks" })).toHaveCount(0);
  await expect(left(page)).toHaveText("8 left, about 11 min");
  const later = page.getByTestId("needs-me-later-row").filter({ hasText: "Retry the failed webhooks" });
  await expect(later).toContainText("Not now, back ");
  expect((await (await work(page)).evaluate((w) => w.record("w_webhooks")))?.status).toBe("parked");

  await (await work(page)).evaluate((w) => w.pass("w_webhooks"));
  await expect(rows(page).filter({ hasText: "Retry the failed webhooks" })).toHaveCount(1, { timeout: 15_000 });
  await expect(left(page)).toHaveText("9 left, about 12 min");
  await expect(later).toHaveCount(0);
});

test("Waiting on says who it waits for, and comes back on the date to check", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Add the DNS record" }).click();
  await button(page, "Waiting on").click();
  const dialog = page.getByTestId("needs-me-waiting-on");
  await expect(dialog.getByRole("button", { name: "Wait", exact: true })).toBeDisabled();
  await dialog.getByRole("textbox", { name: "Who or what" }).fill("Theo at acme");
  await dialog.getByRole("button", { name: "Wait", exact: true }).click();

  await expect(rows(page).filter({ hasText: "Add the DNS record" })).toHaveCount(0);
  const later = page.getByTestId("needs-me-later-row").filter({ hasText: "Add the DNS record" });
  await expect(later).toContainText("Waiting on Theo at acme, check back ");
  await expect(page.getByTestId("needs-me-later")).not.toContainText("waiting_on");

  await (await work(page)).evaluate((w) => w.pass("w_dns"));
  await expect(rows(page).filter({ hasText: "Add the DNS record" })).toHaveCount(1, { timeout: 15_000 });
  await rows(page).filter({ hasText: "Add the DNS record" }).click();
  await expect(page.getByTestId("needs-me-why")).toHaveText("Waiting on Theo at acme: time to check back");
});

test("Bring back returns something put off before its date", async ({ app, page }) => {
  await open(app);
  const legal = page.getByTestId("needs-me-later-row").filter({ hasText: "Hear back from legal" });
  await expect(legal).toContainText("Waiting on Legal at acme, check back ");
  await legal.getByRole("button", { name: "Bring back" }).click();
  await expect(legal).toHaveCount(0);
  expect((await (await work(page)).evaluate((w) => w.record("w_legal")))?.status).toBe("open");
});

test("Close ends the session and keeps the record", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Retry the failed webhooks" }).click();
  await button(page, "Close").click();
  await expect(rows(page).filter({ hasText: "Retry the failed webhooks" })).toHaveCount(0);
  await expect(left(page)).toHaveText("8 left, about 11 min");
  const record = await (await work(page)).evaluate((w) => w.record("w_webhooks"));
  expect(record?.status).toBe("closed");
});

test("Close asks first when a session is still working", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Ship the spring release" }).click();
  await button(page, "Close").click();
  await expect(page.getByRole("alertdialog").or(page.getByRole("dialog"))).toContainText("Coordinate the spring release, Run the release tests are still working");
});

test("Read takes a result off, and a question it can't settle goes to the back of its pass", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Retry the failed webhooks" }).click();
  await button(page, "Read").click();
  await expect(rows(page).filter({ hasText: "Retry the failed webhooks" })).toHaveCount(0);
  await expect(left(page)).toHaveText("8 left, about 11 min");

  await rows(page).filter({ hasText: "Pick the pricing copy" }).click();
  await button(page, "Read").click();
  await expect(title(page)).not.toHaveText("Pick the pricing copy");
  // It still needs an answer, so it stays in the count.
  await expect(left(page)).toHaveText("8 left, about 11 min");
  await expect(rows(page).filter({ hasText: "Pick the pricing copy" })).toHaveCount(1);
});

test("Pin moves a piece of work up to the pinned decisions", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Approve the database migration" }).click();
  await button(page, "Pin").click();
  await page.getByRole("menuitem", { name: "Pin this work" }).click();
  await expect(page.getByTestId("needs-me-pinned")).toBeVisible();
  await expect(rows(page).locator(".text-row").nth(1)).toHaveText("Choose the checkout rollout");
});

test("the close-out deck offers Close, Park and Still needed", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Spike the search index" }).click();
  await expect(page.getByTestId("needs-me-card")).toHaveAttribute("data-kind", "close-out");
  await expect(page.getByTestId("needs-me-why")).toHaveText(/^Nobody has looked at this for \d+ days$/);
  await expect(button(page, "Read")).toHaveCount(0);
  await button(page, "Still needed").click();
  await expect(rows(page).filter({ hasText: "Spike the search index" })).toHaveCount(0);
  expect((await (await work(page)).evaluate((w) => w.record("w_spike")))?.status).toBe("open");
});

test("Park on the deck stays out until it's brought back", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Spike the search index" }).click();
  await button(page, "Park").click();
  await expect(rows(page).filter({ hasText: "Spike the search index" })).toHaveCount(0);
  await expect(page.getByTestId("needs-me-later-row").filter({ hasText: "Spike the search index" })).toContainText("Parked until you bring it back");
});

test("a group draws as a tree with the orchestrator on top, and a worker's question is answered directly", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Ship the spring release" }).click();
  const tree = page.getByTestId("needs-me-session");
  await expect(tree).toHaveCount(3);
  await expect(tree.nth(0)).toHaveAttribute("data-session", "release-lead-claude");
  await expect(tree.nth(0)).toHaveAttribute("data-depth", "0");
  await expect(tree.nth(1)).toHaveAttribute("data-depth", "1");
  await expect(tree.nth(2)).toHaveAttribute("data-depth", "1");
  // The worker's question is in front, in its own words.
  await expect(page.locator('[data-testid=needs-me-session][data-focus="true"]')).toHaveAttribute("data-session", "release-docs-claude");
  await expect(page.getByTestId("needs-me-text")).toContainText("Which version should the release notes name");
  await page.getByRole("textbox", { name: "Reply" }).fill("3.0");
  await page.getByRole("button", { name: "Reply", exact: true }).click();
  await expect(rows(page).filter({ hasText: "Ship the spring release" })).toHaveCount(0);
  expect(await (await work(page)).evaluate((w) => w.sent["devl/release-docs-claude"])).toBe("3.0");
});

test("Ungroup takes a session out, and Group with puts it back", async ({ app, page }) => {
  await open(app);
  await rows(page).filter({ hasText: "Ship the spring release" }).click();
  await page.getByRole("button", { name: "Ungroup Run the release tests" }).click();
  await expect(page.getByTestId("needs-me-session")).toHaveCount(2);
  await page.getByRole("button", { name: "Group with" }).click();
  await page.getByRole("menuitem", { name: "release-tests-claude" }).click();
  await expect(page.getByTestId("needs-me-session")).toHaveCount(3);
});

test("a suggested group is a card with Accept and Reject", async ({ app, page }) => {
  await open(app);
  const suggested = rows(page).filter({ hasText: "Group Fix the cart badge with Test the cart badge?" });
  await suggested.click();
  await expect(page.getByTestId("needs-me-card")).toHaveAttribute("data-kind", "group");
  await expect(page.getByTestId("needs-me-card").getByRole("table")).toContainText("Test the cart badge");
  await button(page, "Reject").click();
  await expect(suggested).toHaveCount(0);
});

test("an empty queue says so, calmly, with what's put off still there", async ({ app, page }) => {
  await open(app);
  await (await work(page)).evaluate((w) => w.clear());
  await expect(page.getByTestId("needs-me-empty")).toContainText("Nothing needs you", { timeout: 15_000 });
  await expect(left(page)).toHaveText("Nothing left");
  await expect(page.getByTestId("nav-needs-me").locator("..").getByTestId("nav-badge")).toHaveCount(0);
  await expect(page.getByTestId("needs-me-later-row").filter({ hasText: "Hear back from legal" })).toHaveCount(1);
});
