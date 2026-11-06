// needs-me: walk the whole queue on the mock's ?scene=needs-me, answering and
// clearing as a person does. Each step asserts the literal title and the "N left"
// count, the menu's badge matches the list, a pinned question arriving mid-card
// becomes next without moving the card in front, and Not now and Waiting on leave
// and come back. See ../../features/needs-me.md.
export const params = { scene: "needs-me", still: "1", labs: "0", sidebar: "list" };

export async function run({ page, shot, step, expect }) {
  const title = page.getByTestId("needs-me-title");
  const left = page.getByTestId("needs-me-left");
  const badge = page.getByTestId("nav-needs-me").locator("..").getByTestId("nav-badge");
  const rows = page.getByTestId("needs-me-row");
  const card = page.getByTestId("needs-me-card");
  const button = (name) => card.getByRole("button", { name, exact: true });
  const at = async (t, n) => {
    await expect(title).toHaveText(t);
    await expect(left).toHaveText(new RegExp(`^${n} left, about \\d+ min$`));
    await expect(rows).toHaveCount(n);
    await expect(badge).toHaveText(String(n));
  };
  const reply = async (words) => {
    await page.getByRole("textbox", { name: "Reply" }).fill(words);
    await page.getByRole("button", { name: "Reply", exact: true }).click();
  };

  step("open Needs me from the menu");
  await page.getByTestId("nav-needs-me").click();
  await at("Choose the checkout rollout", 9);
  await expect(page.getByTestId("needs-me-pinned")).toBeVisible();
  await shot("1-pinned-decision-form");

  step("a pinned question arrives mid-card: it becomes next and the card in front stays");
  await page.evaluate(() => window.__berthMockWork.arrive("hotfix-claude", "Decide on the hotfix"));
  await expect(rows).toHaveCount(10, { timeout: 15_000 });
  await at("Choose the checkout rollout", 10);
  await expect(rows.nth(1).locator(".text-row")).toHaveText("Decide on the hotfix");
  await shot("2-pinned-arrives-mid-card");

  step("answer the form with its number key: 1, then Enter");
  await page.keyboard.press("1");
  await expect(page.getByTestId("question-form").getByRole("radio", { name: /Flag first/ })).toBeChecked();
  await page.keyboard.press("Enter");
  await at("Decide on the hotfix", 9);
  await expect(page.getByTestId("needs-me-text")).toContainText("should the hotfix go out tonight?");
  await shot("3-no-form-reply");

  step("reply to the question that came in with no form");
  await reply("Yes, tonight.");
  await at("Pick the pricing copy", 8);

  step("put one off with Not now");
  await button("Not now").click();
  await shot("4-not-now");
  await page.getByTestId("needs-me-not-now").getByRole("button", { name: "Tomorrow" }).click();
  await at("Approve the database migration", 7);
  const parked = page.getByTestId("needs-me-later-row").filter({ hasText: "Pick the pricing copy" });
  await expect(parked).toContainText("Not now, back ");

  step("answer a permission by its key");
  await expect(page.getByTestId("needs-me-choices").getByRole("button")).toHaveCount(3);
  await shot("5-permission-choices");
  await page.keyboard.press("1");
  await at("Ship the spring release", 6);

  step("a group: the orchestrator on top, the worker's question in front");
  const sessions = page.getByTestId("needs-me-session");
  await expect(sessions).toHaveCount(3);
  await expect(sessions.first()).toHaveAttribute("data-session", "release-lead-claude");
  await expect(page.locator('[data-testid=needs-me-session][data-focus="true"]')).toHaveAttribute("data-session", "release-docs-claude");
  await shot("6-group-tree");
  await reply("3.0");
  await at("Add the staging API key", 5);

  step("a pinned task: say who it's waiting on");
  await expect(card).toHaveAttribute("data-kind", "task");
  await button("Waiting on").click();
  await page.getByTestId("needs-me-waiting-on").getByRole("textbox", { name: "Who or what" }).fill("Theo at acme");
  await shot("7-waiting-on");
  await page.getByTestId("needs-me-waiting-on").getByRole("button", { name: "Wait", exact: true }).click();
  await at("Add the DNS record for acme.example", 4);
  await expect(page.getByTestId("needs-me-later-row").filter({ hasText: "Add the staging API key" })).toContainText("Waiting on Theo at acme, check back ");

  step("a task done");
  await button("Done").click();
  await at("Retry the failed webhooks", 3);

  step("a result: its last message, as text, with Read");
  await expect(card).toHaveAttribute("data-kind", "result");
  await expect(page.getByTestId("needs-me-text")).toContainText("<script>alert(1)</script>");
  await shot("8-result");
  await button("Read").click();
  await at("Spike the search index", 2);

  step("the close-out deck: Close, Park, Still needed");
  await expect(card).toHaveAttribute("data-kind", "close-out");
  await expect(button("Read")).toHaveCount(0);
  await shot("9-close-out");
  await button("Close").click();
  await at("Group Fix the cart badge with Test the cart badge?", 1);

  step("a suggested group, accepted");
  await expect(card).toHaveAttribute("data-kind", "group");
  await shot("10-suggested-group");
  await button("Accept").click();

  step("nothing left: calm, and what's put off is still there");
  await expect(left).toHaveText("Nothing left");
  await expect(page.getByTestId("needs-me-empty")).toContainText("Nothing needs you");
  await expect(badge).toHaveCount(0);
  await expect(page.getByTestId("needs-me-later-row")).toHaveCount(3);
  await shot("11-empty");

  step("what was put off comes back by itself when its date passes");
  await page.evaluate(() => window.__berthMockWork.pass("w_pricing"));
  await expect(title).toHaveText("Pick the pricing copy", { timeout: 15_000 });
  await at("Pick the pricing copy", 1);
  await expect(page.getByTestId("needs-me-why")).toHaveText("You asked to see this again");
  await shot("12-back-when-due");

  step("Close keeps the record");
  const closed = await page.evaluate(() => window.__berthMockWork.record("w_spike"));
  if (closed?.status !== "closed") throw new Error(`Close should keep a closed record, got ${JSON.stringify(closed)}`);
  const sent = await page.evaluate(() => window.__berthMockWork.sent);
  for (const [k, v] of [["devl/rollout-claude", "Flag first"], ["devl/hotfix-claude", "Yes, tonight."], ["devl/migrate-claude", "1"], ["devl/release-docs-claude", "3.0"], ["devl/dns-claude", "Done."]]) {
    if (sent[k] !== v) throw new Error(`${k} should have got ${v}, got ${sent[k]}`);
  }
}
