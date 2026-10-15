---
name: integrate-branches
description: Merge several feature branches (often from parallel agents) into one branch for a single PR, resolving the repo's usual conflicts and checking the result. Use when two or more branches need to land together.
---

# Integrating branches

1. **Start in a scratch worktree from the current main**, never the user's checkout:
   ```sh
   git fetch origin
   git worktree add -b integ-<topic> /tmp/…/integ-wt origin/main
   ```
2. **Merge one branch at a time, smallest and most foundational first**, so the big UI branches land last on top of everything they read:
   ```sh
   git merge --no-ff --no-edit origin/<branch>
   ```
   Don't rebase other people's branches. `git merge-tree --write-tree origin/main origin/<branch>` previews the conflicts.
3. **Resolve the usual conflicts:**
   - `docs/changelog.mdx`: keep every bullet from both sides under `## Unreleased`.
   - Other `package.json` scripts and dependencies: keep both sides.
   - Mocks (`app/src/lib/mock*.ts`) and capability lists: the union of both.
   - Real code: keep both intents. Read both sides; don't pick one.
4. **Look for breaks that merged cleanly but fail in practice**, e.g. one branch renames a control another branch's test clicks, or one slows a poll another relies on. Run the specs of every area both branches touched.
5. **Run the checks** in AGENTS.md on the final tree, with a fresh `CI=true pnpm install --frozen-lockfile` and `go test -race ./...`.
6. **Push the branch and open one PR.** Its body lists the merge order, each conflict and how it was resolved, the cross-branch fixes, and the check results. After it merges, delete the merged branches and their worktrees.
