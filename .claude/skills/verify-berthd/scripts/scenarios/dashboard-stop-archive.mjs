// The Agent Dashboard's bulk Stop, with "Archive their worktrees where it's
// safe" ticked (views/dashboard/stop.tsx): a clean worktree with an archive
// script archives in the background, one without archives at once, a dirty
// one is kept, and one with another agent still in it is kept.
// features/dashboard-stop.md.

const picks = [
  // devl / search-perf: shop has an archive script, so it runs first.
  "Speed up product search",
  // gpu / ci-flake: no archive script, so it goes at once.
  "Move acme billing to the new ledger",
  // devl / checkout-fix: uncommitted work, so git keeps it.
  "Fix checkout webhook retries",
  // devl / order-export: two more agents still run there.
  "Export orders as CSV from the admin",
];

export async function run({ page, shot, step, expect }) {
  const dialog = page.getByRole("alertdialog");
  const archiveBox = dialog.getByRole("checkbox", { name: /Archive their worktrees where it's safe/ });
  const row = (place) => dialog.getByRole("group", { name: "Agents to stop" }).getByRole("listitem").filter({ hasText: place });
  // The Worktrees table lists every box's; the sidebar shows one project.
  const listed = (name) => page.getByRole("checkbox", { name: `Select ${name}`, exact: true });

  step("open the Agent Dashboard, Select, and pick four agents");
  await page.keyboard.press("Meta+j");
  await expect(page.getByRole("heading", { name: "Agent Dashboard" })).toBeVisible();
  await page.getByRole("button", { name: "Select", exact: true }).click();
  for (const t of picks) await page.locator("article[role=button]", { hasText: t }).getByText(t).click();
  await page.getByRole("button", { name: `Stop ${picks.length}…` }).click();
  await expect(archiveBox).not.toBeChecked();
  await expect(dialog).toContainText("its worktree and files are kept");
  await shot("1-stop-dialog-unticked");

  step("tick 'Archive their worktrees where it's safe': the copy says so");
  await archiveBox.click();
  await expect(archiveBox).toBeChecked();
  await expect(dialog).toContainText("then its worktree is archived where it's safe");
  await shot("2-stop-dialog-ticked");

  step("Stop 4 agents: kept work holds the dialog open, each row with its outcome");
  await dialog.getByRole("button", { name: `Stop ${picks.length} agents` }).click();
  await expect(dialog.getByRole("heading")).toHaveText("Stopped 4 agents · archiving 2 worktrees");
  await expect(row("search-perf")).toContainText("Stopped · Archiving: its archive script runs, then it goes");
  await expect(row("ci-flake")).toContainText("Stopped · Archived; its branch is kept");
  await expect(row("checkout-fix")).toContainText("Stopped · Kept: uncommitted changes");
  await expect(row("order-export")).toContainText("Stopped · Kept: another agent is still running there");
  await shot("3-stop-dialog-done");

  step("Done, then the Worktrees table: the archived ones are gone, the kept ones stay");
  await dialog.getByRole("button", { name: "Done" }).click();
  await page.getByTestId("nav-worktrees").click();
  await expect(listed("checkout-fix")).toBeVisible();
  await expect(listed("order-export")).toBeVisible();
  await expect(listed("ci-flake")).toHaveCount(0, { timeout: 10_000 });
  await expect(listed("search-perf")).toHaveCount(0, { timeout: 10_000 });
  await shot("4-worktrees-after");

  step("the choice is remembered: Clean up… opens with the box ticked");
  await page.keyboard.press("Meta+j");
  await page.getByRole("button", { name: "Clean up…" }).click();
  await expect(archiveBox).toBeChecked();
  await expect(dialog).toContainText("Their worktrees are archived where it's safe");
  await shot("5-cleanup-remembers");
}
