// dashboard-stop-live: the board's bulk Stop and the sidebar's Archive… on
// a real box (dev-box.sh's devq), as features/dashboard-stop.md and
// features/worktree-archive.md describe. It makes its own throwaway
// worktrees of acme-shop, each with a Claude Code in it, one of them with
// an uncommitted file; stops the three with archiving ticked, and reads
// the result back from git on the box: two worktrees gone with their
// branches kept, the dirty one still there. Then it archives a fourth from
// the sidebar. Nothing it didn't make is touched; dev-box.sh down removes
// the lot.
import { execFileSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { join } from "node:path";

export const live = true;
export const params = { sidebar: "list" };
// The box refuses to archive the dirty worktree up front, 400 in git's
// words, and the browser logs it. run() checks it is that request alone.
export const allowErrors = [/status of 400/];

const API = "/v1/boxes/devq/api";
const STOPPED = ["verify-stop-a", "verify-stop-b", "verify-stop-c"];
const SOLO = "verify-solo";

export async function run({ page, shot, step, expect, call }) {
  const post = (path, body) => call(`${API}${path}`, { method: "POST", body: JSON.stringify(body) });
  const tray = page.getByRole("region", { name: "Background jobs" });
  const dialog = page.getByRole("alertdialog");
  const title = (n) => `Throwaway ${n}`;
  const refused = [];
  page.on("response", (r) => r.status() === 400 && refused.push(`${r.request().method()} ${new URL(r.url()).pathname}`));

  step("make four worktrees of acme-shop, a Claude Code in three of them, and an uncommitted file in verify-stop-c");
  const paths = {};
  for (const n of [...STOPPED, SOLO]) paths[n] = (await post("/locations/acme-shop/worktrees", { name: n })).path;
  for (const n of STOPPED) await post("/sessions", { name: `${n}-claude`, location: `acme-shop/${n}`, agent: "claude", title: title(n) });
  writeFileSync(join(paths["verify-stop-c"], "notes.txt"), "not committed\n");
  const repo = (await call(`${API}/locations`)).find((l) => l.name === "acme-shop").path;
  const git = (...args) => execFileSync("git", ["-C", repo, ...args], { encoding: "utf8" });
  const worktrees = () => git("worktree", "list", "--porcelain");
  const branches = () => git("branch", "--list", "--format=%(refname:short)");
  for (const n of [...STOPPED, SOLO]) if (!worktrees().includes(paths[n])) throw new Error(`git doesn't list ${n}`);
  step(`git on the box lists them:\n${worktrees().trim()}`);

  step("open the Agent Dashboard and pick the three throwaway agents");
  await page.keyboard.press("Meta+j");
  await expect(page.getByRole("heading", { name: "Agent Dashboard" })).toBeVisible();
  for (const n of STOPPED) await expect(page.locator("article[role=button]", { hasText: title(n) })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Select", exact: true }).click();
  for (const n of STOPPED) await page.locator("article[role=button]", { hasText: title(n) }).getByText(title(n)).click();
  await page.getByRole("button", { name: "Stop 3…" }).click();
  const archiveBox = dialog.getByRole("checkbox", { name: /Archive their worktrees where it's safe/ });
  if (!(await archiveBox.isChecked())) await archiveBox.click();
  await expect(archiveBox).toBeChecked();
  await shot("live-1-stop-dialog");

  step("Stop 3 agents: the dialog goes at once, the tray runs it");
  const t0 = Date.now();
  await dialog.getByRole("button", { name: "Stop 3 agents" }).click();
  await expect(dialog).toBeHidden({ timeout: 1000 });
  step(`the dialog closed ${Date.now() - t0} ms after Stop was pressed`);
  const job = tray.getByRole("group", { name: "Stopping 3 agents" });
  await expect(job).toBeVisible();
  await shot("live-2-tray-running");

  step("the report: two archived, the dirty one kept");
  await expect(job).toContainText("3 stopped, 2 archived, 1 kept", { timeout: 60_000 });
  await expect(job.getByRole("listitem")).toHaveCount(1);
  await expect(job.getByRole("listitem")).toContainText("Stopped · Kept: uncommitted changes");
  await shot("live-3-tray-report");

  step("git on the box agrees: a and b's worktrees are gone, c's stays, every branch is kept");
  const after = worktrees();
  for (const n of ["verify-stop-a", "verify-stop-b"]) if (after.includes(paths[n])) throw new Error(`${n}'s worktree is still there`);
  if (!after.includes(paths["verify-stop-c"])) throw new Error("the dirty worktree went");
  const kept = branches();
  for (const n of STOPPED) if (!kept.split("\n").some((b) => b.endsWith(n))) throw new Error(`${n}'s branch is gone`);
  step(`git worktree list:\n${after.trim()}\ngit branch:\n${kept.trim()}`);
  if (refused.join() !== `DELETE ${API}/locations/acme-shop/worktrees/verify-stop-c`) throw new Error(`unexpected 400s: ${refused.join(", ") || "none"}`);
  step(`the one 400 is the box refusing the dirty worktree: ${refused[0]}`);
  const sessions = await call(`${API}/sessions`);
  for (const n of STOPPED) if (sessions.some((s) => s.name === `${n}-claude` && !s.exited)) throw new Error(`${n}-claude still runs`);
  step("the box lists none of the three agents");
  await job.getByRole("button", { name: "Dismiss" }).click();

  step(`Archive… ${SOLO} from the sidebar: the dialog goes at once, a one-row job in the tray`);
  await page.locator(`[data-testid=worktree-row][data-worktree="devq/${SOLO}"]`).click({ button: "right" });
  await page.getByRole("menuitem", { name: "Archive…" }).click();
  await dialog.getByRole("button", { name: "Archive", exact: true }).click();
  await expect(dialog).toBeHidden({ timeout: 1000 });
  const solo = tray.getByRole("group", { name: `Archiving ${SOLO}` });
  await expect(solo).toContainText(`Archived ${SOLO}`, { timeout: 30_000 });
  await shot("live-4-solo-archived");
  if (worktrees().includes(paths[SOLO])) throw new Error(`${SOLO}'s worktree is still there`);
  if (!branches().split("\n").some((b) => b.endsWith(SOLO))) throw new Error(`${SOLO}'s branch is gone`);
  step(`git agrees: ${SOLO}'s worktree is gone, its branch kept`);
}
