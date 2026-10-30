import { expect, mockOnly, test } from "./fixtures";

// Review lists its work by project when there is more than one, J and K
// walk the list across the groups, and the agent's last message reads as
// prose rather than the terminal's lines. Discard waits in the ⋯ menu, its
// D still works.

const list = (page: import("@playwright/test").Page) => page.getByRole("list", { name: "Work to review" });

test("work from several projects is grouped under each project, and J and K cross the groups", async ({ app }) => {
  mockOnly("the crowd's review fixtures");
  await app.open({ params: { crowd: "1" } });
  const page = app.page;
  await page.getByTestId("nav-review").click();
  for (const name of ["berth on build", "shop on devl", "infra on build"]) await expect(list(page).getByRole("list", { name })).toBeVisible();

  const shop = list(page).getByRole("list", { name: "shop on devl" });
  await shop.getByRole("button").last().click();
  await page.keyboard.press("j");
  await expect(list(page).getByRole("list", { name: "infra on build" }).getByRole("button").first()).toHaveAttribute("aria-current", "true");
  await expect(page.getByRole("heading", { level: 2 })).toHaveText("Cut DNS over to the new load balancer");
  await page.keyboard.press("k");
  await expect(shop.getByRole("button").last()).toHaveAttribute("aria-current", "true");
});

test("one project's work isn't grouped: each row says where it is, project first", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  await page.getByTestId("nav-review").click();
  await expect(list(page).getByRole("list")).toHaveCount(0);
  await expect(list(page).getByRole("button").filter({ hasText: "Speed up product search" })).toContainText("shop / search-perf · devl");
});

test("the agent's last message is prose, and Discard is in the ⋯ menu with its D", async ({ app }) => {
  mockOnly();
  await app.open();
  const page = app.page;
  await page.getByTestId("nav-review").click();
  await list(page).getByRole("button").filter({ hasText: "Fix checkout webhook retries" }).click();
  const message = page.getByTestId("review-last-message");
  // The terminal broke this sentence after "now count as"; it is one paragraph.
  await expect(message.locator("p").first()).toHaveText("Retries are capped at 3 with exponential backoff, and 429s now count as retryable. I added tests for the cap and the backoff timing.");
  await expect(message.locator("ol li")).toHaveText(["Yes, cap it at 30s", "No, leave it"]);

  await expect(page.getByRole("button", { name: /Discard/ })).toHaveCount(0);
  await page.getByRole("button", { name: "More actions" }).click();
  await page.getByRole("menuitem", { name: /Discard changes/ }).click();
  await expect(page.getByRole("dialog", { name: "Discard these changes?" })).toBeVisible();
  await page.getByRole("button", { name: "Keep them" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.keyboard.press("d");
  await expect(page.getByRole("dialog", { name: "Discard these changes?" })).toBeVisible();
});
