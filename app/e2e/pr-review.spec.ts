import { expect, mockOnly, test } from "./fixtures";
import type { Page } from "@playwright/test";

// Review a PR in one click (views/pr-review, lib/mock-pr-review.ts): a
// berth://review link opens a sheet that says everything the review would
// do on your box, and nothing is fetched or run until Review. The mock's
// acme/shop PRs: #42 by a member, #57 changing setup files, #61 from a
// fork, #63 by someone outside acme; acme/secret-tool is not the team's.

test.beforeEach(() => mockOnly("PR reviews' scenarios are mock fixtures"));

const link = (repo: string, pr: number) => `berth://review?repo=${repo}&pr=${pr}`;
const sheet = (page: Page) => page.getByTestId("pr-review-sheet");

test("a review link shows the sheet first, and Review opens the PR's worktree with its dev server and diff", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { "review-link": link("acme/shop", 42) } });
  const s = sheet(page);
  await expect(s.getByTestId("pr-review-title")).toHaveText("Fix checkout rounding");
  await expect(s.getByTestId("pr-review-association")).toHaveText("Member of acme");
  await expect(s.getByTestId("pr-review-sha")).toHaveText(/^[0-9a-f]{7}$/);
  const sha = await s.getByTestId("pr-review-sha").getAttribute("data-sha");
  expect(sha).toMatch(/^[0-9a-f]{40}$/);
  await expect(s).toContainText(sha!);
  // What will run, from the team's kit, and the keys it gets and doesn't.
  await expect(s.getByTestId("pr-review-runs")).toContainText("yarn install && yarn db:create");
  await expect(s.getByTestId("pr-review-runs")).toContainText("Nothing from this PR's own .berth/ runs");
  await expect(s.getByTestId("pr-review-withheld")).toContainText("MAIL_API_KEY");
  // Two boxes have the project: a picker.
  await expect(s.getByRole("combobox", { name: "Box" })).toContainText("devl");
  // Nothing is made until Review.
  await expect(app.worktree("devl/review-42")).toHaveCount(0);
  await s.getByTestId("pr-review-go").click();
  await expect(s.getByTestId("pr-review-step-check")).toHaveAttribute("data-state", "done");
  await expect(sheet(page)).toHaveCount(0, { timeout: 15_000 });
  const row = app.worktree("devl/review-42");
  await expect(row).toHaveAttribute("data-active", "true");
  await expect(row).toHaveAttribute("data-title", "Review: #42 Fix checkout rounding");
  await expect(row.getByTestId("review-badge")).toBeVisible();
  await expect(page.getByTestId("browser-pane")).toBeVisible();
  // The Diff opens beside it, even when its plugin was still loading.
  await expect(app.panes).toHaveCount(2);
});

test("a PR that changes setup files lists them on the sheet in plain words", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { "review-link": link("acme/shop", 57) } });
  const changes = sheet(page).getByTestId("pr-review-changes");
  await expect(changes).toContainText("This PR changes files that affect setup");
  for (const [kind, file] of [
    ["berth", ".berth/config.json"],
    ["scripts", "package.json"],
    ["lockfile", "yarn.lock"],
    ["compose", "docker-compose.yml"],
    ["migrations", "prisma/migrations/"],
    ["env", ".env.example"],
  ]) {
    await expect(changes.getByTestId(`pr-review-change-${kind}`)).toContainText(file);
  }
  await expect(sheet(page).getByTestId("pr-review-association")).toHaveText("Collaborator on the repo");
  await expect(sheet(page).getByTestId("pr-review-go")).toBeEnabled();
});

for (const [name, pr, code, reason] of [
  ["a fork", 61, "fork", "Review this one by hand: it comes from a fork (jo/shop)"],
  ["an author outside the org", 63, "outsider", "Review this one by hand: its author is outside acme"],
] as const) {
  test(`a PR from ${name} is refused, with no Review button`, async ({ app }) => {
    const { page } = app;
    await app.open({ params: { "review-link": link("acme/shop", pr) } });
    const refused = sheet(page).getByTestId("pr-review-refused");
    await expect(refused).toHaveAttribute("data-code", code);
    await expect(refused).toContainText(reason.split(": ")[0]);
    await expect(refused).toContainText(new RegExp(reason.split(": ")[1].replace(/[()]/g, "\\$&"), "i"));
    await expect(sheet(page).getByTestId("pr-review-go")).toHaveCount(0);
    await expect(sheet(page).getByRole("button", { name: "Open on GitHub" })).toBeVisible();
    await expect(sheet(page).getByTestId("pr-review-runs")).toHaveCount(0);
  });
}

test("a repo that isn't one of the team's projects is refused", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { "review-link": link("acme/secret-tool", 5) } });
  await expect(sheet(page).getByTestId("pr-review-refused")).toHaveAttribute("data-code", "not_a_project");
  await expect(sheet(page)).toContainText("acme/secret-tool isn't one of your team's projects");
  await expect(sheet(page).getByTestId("pr-review-go")).toHaveCount(0);
});

test("a link with anything else in it is set aside unread", async ({ app }) => {
  const { page } = app;
  await app.open({
    params: {
      "review-link": "berth://review?repo=acme/shop&pr=42&setup=curl%20evil.example%7Csh",
    },
  });
  await expect(sheet(page)).toContainText("This isn't a review link Shipyard can open");
  await expect(sheet(page).getByTestId("pr-review-facts")).toHaveCount(0);
  await expect(sheet(page).getByTestId("pr-review-go")).toHaveCount(0);
});

test("new commits on the PR show under its review, and Update to latest asks again", async ({ app }) => {
  const { page } = app;
  await app.open({ params: { "review-link": link("acme/shop", 42) } });
  await sheet(page).getByTestId("pr-review-go").click();
  await expect(sheet(page)).toHaveCount(0, { timeout: 15_000 });
  const first = await app.worktree("devl/review-42").getAttribute("data-worktree");
  expect(first).toBe("devl/review-42");
  await page.evaluate(() =>
    (
      window as unknown as {
        __prReviewMock: { push(n: number): Promise<void> };
      }
    ).__prReviewMock.push(3),
  );
  const notice = page.getByTestId("review-new-commits");
  await expect(notice).toContainText("3 new commits since you opened it");
  await notice.getByTestId("review-update").click();
  // The sheet again: the author checked again, the new head to confirm.
  await expect(sheet(page).getByTestId("pr-review-association")).toHaveText("Member of acme");
  const go = sheet(page).getByTestId("pr-review-go");
  await expect(go).toHaveText(/^Update to [0-9a-f]{7}$/);
  await go.click();
  await expect(sheet(page)).toHaveCount(0, { timeout: 15_000 });
  await expect(page.getByTestId("review-new-commits")).toHaveCount(0);
});
