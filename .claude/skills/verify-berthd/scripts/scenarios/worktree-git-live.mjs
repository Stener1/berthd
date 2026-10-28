// Worktrees changed with plain git, outside Shipyard, on a real box
// (features/worktree-git.md): the box keeps each repository's worktree
// list, remote and default branch until a file in .git changes, so each
// change has to show in the box's own location list, and a worktree's
// coming and going in the sidebar: a worktree added, a commit in it, a
// branch switched, a lock, a new remote URL, and the worktree removed.
//
// On a dev box of this checkout (../dev-box.sh):
//
//   node drive.mjs worktree-git-live --port $PORT --agent http://127.0.0.1:9378
//
// It adds and removes a worktree named "cache-probe" next to the dev box's
// acme-shop, and leaves the repository's remote as it found it.

import { execFileSync } from "node:child_process";

export const live = true;
export const params = { sidebar: "list" };

const WORKTREE = process.env.VERIFY_WORKTREE ?? "devq/acme-shop";
const [BOX, LOCATION] = WORKTREE.split("/");
const NAME = "cache-probe";
const env = { ...process.env, GIT_AUTHOR_NAME: "acme", GIT_AUTHOR_EMAIL: "dev@acme.test", GIT_COMMITTER_NAME: "acme", GIT_COMMITTER_EMAIL: "dev@acme.test" };
const git = (dir, ...args) => execFileSync("git", ["-C", dir, ...args], { env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();

export async function run({ page, shot, step, expect, call }) {
  const location = async () => (await call(`/v1/boxes/${BOX}/api/locations`)).find((l) => l.name === LOCATION);
  const probe = async () => (await location())?.worktrees?.find((w) => w.name === NAME);
  const row = page.locator(`[data-testid=worktree-row][data-worktree="${BOX}/${NAME}"]`);
  const poll = (fn, message) => expect.poll(fn, { timeout: 30_000, intervals: [1_000], message });

  const repo = (await location()).path;
  const path = `${repo}-${NAME}`;
  const remote = (() => { try { return git(repo, "remote", "get-url", "origin"); } catch { return ""; } })();
  try {
    step("the box answers twice from what it kept");
    const before = await location();
    await expect(await location()).toEqual(before);

    step("a worktree added with git shows up");
    git(repo, "worktree", "add", "-q", "-b", NAME, path);
    await expect(row).toBeVisible({ timeout: 30_000 });
    await poll(async () => (await probe())?.branch, "the box lists the worktree on its branch").toBe(NAME);
    await shot("worktree-added");

    step("a commit in it moves its HEAD");
    const head = (await probe()).head;
    git(path, "commit", "-q", "--allow-empty", "-m", "probe");
    await poll(async () => (await probe())?.head, "the box shows the new HEAD").toBe(git(path, "rev-parse", "HEAD").slice(0, 10));
    expect((await probe()).head).not.toBe(head);

    step("a branch switched in it shows");
    git(path, "checkout", "-q", "-b", `${NAME}-2`);
    await poll(async () => (await probe())?.branch, "the box shows the new branch").toBe(`${NAME}-2`);

    step("a lock shows");
    git(repo, "worktree", "lock", "--reason", "probe", path);
    await poll(async () => { const w = await probe(); return `${w?.locked}/${w?.lock_reason}`; }, "the box shows the lock").toBe("true/probe");
    git(repo, "worktree", "unlock", path);
    await poll(async () => !!(await probe())?.locked, "the box shows it unlocked").toBe(false);

    step("a new remote URL shows as the project's slug");
    if (remote) git(repo, "remote", "set-url", "origin", "https://example.com/acme/cache-probe.git");
    else git(repo, "remote", "add", "origin", "https://example.com/acme/cache-probe.git");
    await poll(async () => (await location())?.slug, "the box shows the new slug").toBe("acme/cache-probe");

    step("the worktree removed with git goes");
    git(repo, "worktree", "remove", "--force", path);
    await poll(async () => !!(await probe()), "the box no longer lists it").toBe(false);
    // The open app keeps the row though the box sent worktree.removed (it
    // does on main too, a bug of its own): a reload reads the box afresh.
    await page.reload();
    await expect(page.getByTestId("nav-home")).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(`[data-testid=worktree-row][data-worktree="${WORKTREE}"]`)).toBeVisible({ timeout: 30_000 });
    await expect(row).toHaveCount(0);
    await shot("worktree-removed");
  } finally {
    try { git(repo, "worktree", "remove", "--force", path); } catch {}
    try { git(repo, "branch", "-D", NAME, `${NAME}-2`); } catch {}
    if (remote) git(repo, "remote", "set-url", "origin", remote);
    else try { git(repo, "remote", "remove", "origin"); } catch {}
  }
}
