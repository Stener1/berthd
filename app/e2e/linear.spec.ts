import { expect, mockOnly, test } from "./fixtures";

// Linear in Shipyard: a personal API key added in Settings → Integrations
// (the laptop keeps it), and the Issues screen's Linear side: what is
// assigned to you or a team's open issues, each one's thread, and an agent
// on any of them in the project you choose.

const issues = async (page: import("@playwright/test").Page) => {
  // The command is the Issues plugin's, there once the plugin has loaded;
  // until then the same words only offer a new task, so look again.
  const input = page.getByRole("combobox", { name: "Search sessions, worktrees and commands" });
  const command = page.getByRole("option", { name: /^Show issues/ });
  await expect(async () => {
    if (!(await input.isVisible())) await page.keyboard.press("Meta+k");
    await input.fill("Show issues");
    if (!(await command.isVisible())) await page.keyboard.press("Escape");
    await expect(command).toBeVisible({ timeout: 500 });
  }).toPass();
  await command.click();
  await expect(page.getByRole("button", { name: "Linear", exact: true })).toBeVisible();
};

test("a Linear key is added and removed in Settings, and a refused one says so", async ({ app }) => {
  mockOnly();
  await app.open({ params: { linear: "off" } });
  const section = await app.openSettings("integrations");
  const key = section.getByTestId("linear-key");
  await expect(key).toBeEnabled();
  const connect = section.getByRole("button", { name: "Connect" });
  await expect(connect).toBeDisabled();

  await key.fill("lin_api_bad");
  await connect.click();
  await expect(section).toContainText("Linear didn't accept the API key");
  await expect(section.getByTestId("linear-connected")).toHaveCount(0);

  await key.fill("lin_api_good");
  await key.press("Enter");
  await expect(section.getByTestId("linear-connected")).toHaveText("Connected as Robin Vance");
  await expect(section).toContainText("Acme · robin@acme.test");
  // The key is gone from the page once it is kept.
  await expect(section.getByTestId("linear-key")).toHaveCount(0);

  await section.getByRole("button", { name: "Disconnect" }).click();
  await expect(section.getByTestId("linear-key")).toBeVisible();
  expect(app.agentCalls).toEqual([]);
});

test("without a key the Linear side says how to connect, and takes you there", async ({ app }) => {
  mockOnly();
  await app.open({ params: { linear: "off" } });
  await issues(app.page);
  await app.page.getByRole("button", { name: "Linear", exact: true }).click();
  await expect(app.page.getByText("Connect Linear")).toBeVisible();
  await app.page.getByRole("button", { name: "Open Settings" }).click();
  await expect(app.page.getByTestId("settings-integrations")).toBeVisible();
});

test("Linear issues list, filter, open and start an agent on Linear's branch", async ({ app }) => {
  mockOnly();
  await app.open();
  const { page } = app;
  await issues(page);
  await page.getByRole("button", { name: "Linear", exact: true }).click();

  // Assigned to me, the most recently updated first, with its thread.
  const mine = page.getByRole("list", { name: "Issues assigned to you" });
  await expect(mine.getByRole("listitem")).toHaveCount(3);
  await expect(mine.getByRole("listitem").first()).toContainText("WEB-412");
  await expect(page.getByRole("heading", { name: "Retrying a failed payment charges the card twice" })).toBeVisible();
  await expect(page.getByText("The key is made in the click handler")).toBeVisible();
  await expect(page.getByText("acme/shop#18231 Reuse the idempotency key across retries")).toBeVisible();

  // A team's issues, narrowed by a label and by who has them.
  await page.getByRole("button", { name: "Which issues" }).click();
  await page.getByRole("menuitem", { name: /Web/ }).click();
  const web = page.getByRole("list", { name: "Web's open issues" });
  await expect(web.getByRole("listitem")).toHaveCount(5);
  await page.getByRole("button", { name: "Unassigned", exact: true }).click();
  await expect(web.getByRole("listitem")).toHaveCount(2);
  await page.getByRole("button", { name: "All", exact: true }).click();
  await page.getByRole("button", { name: "Accessibility", exact: true }).click();
  await expect(web.getByRole("listitem")).toHaveCount(1);
  await expect(web).toContainText("WEB-398");
  await page.getByRole("button", { name: "Accessibility", exact: true }).click();

  // S starts an agent: the worktree is named for the issue, on the branch
  // Linear suggests, with the issue as the first prompt.
  await page.getByPlaceholder("Search issues").fill("save a cart");
  await expect(web.getByRole("listitem")).toHaveCount(1);
  await page.getByPlaceholder("Search issues").blur();
  await page.keyboard.press("s");
  // A Linear issue names no repository, so the first start asks which
  // project, once; the choice is kept.
  await page.getByRole("menuitem", { name: /shop/ }).click();
  const sheet = page.getByRole("dialog");
  await expect(sheet).toContainText("Start an agent on WEB-405");
  await expect(sheet.locator("input.font-mono, .font-mono input").first()).toHaveValue("web-405-let-people-save-a-cart-for-later");
  await expect(sheet).toContainText("robin/web-405-let-people-save-a-cart-for-later");
  await expect(sheet.getByRole("textbox").last()).toHaveValue(/^Work on WEB-405: Let people save a cart for later\n\nhttps:\/\/linear\.app\/acme\/issue\/WEB-405/);
  await expect(sheet.getByRole("textbox").last()).toHaveValue(/with WEB-405 in its title so Linear links it to the issue\.$/);
  await sheet.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("button", { name: "Project agents start in" })).toContainText("shop");
  expect(app.agentCalls).toEqual([]);
});

test("a comment goes to Linear only after it is confirmed", async ({ app }) => {
  mockOnly();
  await app.open();
  const { page } = app;
  await issues(page);
  await page.getByRole("button", { name: "Linear", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Retrying a failed payment charges the card twice" })).toBeVisible();
  await page.getByPlaceholder("Leave a comment (Markdown)").fill("The fix is in review.");
  await page.getByRole("button", { name: "Comment", exact: true }).click();
  const confirm = page.getByRole("alertdialog");
  await expect(confirm).toContainText("Comment on WEB-412?");
  await expect(confirm).toContainText("posted to Linear as Robin Vance");
  await confirm.getByRole("button", { name: "Post comment" }).click();
  await expect(confirm).toHaveCount(0);
  await expect(page.locator("article").getByText("The fix is in review.")).toBeVisible();
  await expect(page.getByPlaceholder("Leave a comment (Markdown)")).toHaveValue("");
});
