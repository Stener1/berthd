import { expect, mockOnly, test } from "./fixtures";

// Project settings lists a project's flows, a kit's among them. A kit flow
// can use a run step kind (pr, loop, gate, …) the flow editor doesn't edit:
// it shows with a plain icon, and the page doesn't throw (the fixture fails
// the test if it does).

test.beforeEach(() => mockOnly("devl's shop kit is a mock fixture"));

test("a kit flow with a run step shows in Project settings", async ({ app }) => {
  const { page } = app;
  await app.open();
  const shop = page.locator("button[aria-expanded][draggable]").filter({ has: page.getByText("shop", { exact: true }) });
  await shop.first().click({ button: "right" });
  await page.getByRole("menuitem", { name: /^Project settings/ }).click();
  await page.getByRole("menuitem", { name: "devl", exact: true }).locator("visible=true").last().click();
  const flows = page.locator("section#flows");
  await expect(flows.getByText("Open a PR when the checks pass")).toBeVisible();
  await expect(flows.getByText("Lint what changed after every Claude turn")).toBeVisible();
});
