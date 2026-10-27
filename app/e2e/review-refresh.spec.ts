import { expect, mockOnly, test } from "./fixtures";

// The Review page reads an item's diff with a command on the box, and a
// command finishing is one of the events that make the inbox read itself
// again. An open item must not turn those two into a loop: read the inbox,
// reload the diff, which reads the inbox, which reloads the diff…

const calls = (page: import("@playwright/test").Page) =>
  page.evaluate(() => {
    const all = (window as unknown as { __berthCalls?: Record<string, number> }).__berthCalls ?? {};
    const sum = (re: RegExp) =>
      Object.entries(all)
        .filter(([k]) => re.test(k))
        .reduce((n, [, v]) => n + v, 0);
    return { exec: sum(/^POST [^ ]+\/exec$/), review: sum(/^GET [^ ]+\/review$/) };
  });

test("an item open in Review doesn't keep the box reading its inbox and its diff", async ({ app }) => {
  mockOnly();
  await app.open();
  const { page } = app;
  await page.getByTestId("nav-review").click();
  await expect(page.getByTestId("nav-review")).toHaveAttribute("data-active", "true");
  // An item is open with its diff drawn.
  await expect(page.locator("[data-diff-line], .diff-line, table tr, pre").first()).toBeVisible();

  // Let what opening the page starts settle: the diff, each item's pull
  // request, and the one read of the inbox each of those commands causes.
  let last = await calls(page);
  await expect(async () => {
    await page.waitForTimeout(2500);
    const now = await calls(page);
    const settled = now.exec === last.exec && now.review === last.review;
    last = now;
    expect(settled, `still asking: ${JSON.stringify(now)}`).toBe(true);
  }).toPass({ timeout: 25_000 });

  // And it stays that way: the inbox waits a second and a half after an
  // event before it reads, so a loop would show well within this.
  const before = await calls(page);
  await page.waitForTimeout(5000);
  expect(await calls(page)).toEqual(before);
  expect(before.exec).toBeGreaterThan(0);
});
