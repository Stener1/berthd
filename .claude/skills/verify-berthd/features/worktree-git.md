# Worktrees changed outside Shipyard

The sidebar's projects and worktrees come from the box's location list (GET `/v1/locations`, `internal/box/locations.go` `describe`): each repository's worktrees with their branch, HEAD and lock (`git worktree list`), origin's URL as the project's slug, and the default branch. The box keeps each of those (`internal/box/gitcache.go`) and asks git again only when a file in `.git` that git reads for it has changed, or after a minute. Its worktree watcher (`watch.go`, every 10 s) tells the app when a worktree comes or goes.

## Sub-features

- `wt-git-add`: a worktree made with `git worktree add` in a terminal shows in the sidebar, on its branch.
- `wt-git-head`: a commit in any worktree moves its HEAD in the location list.
- `wt-git-branch`: a branch switched in a worktree (or the main checkout) shows.
- `wt-git-lock`: `git worktree lock` and `unlock` show as the worktree's lock.
- `wt-git-remote`: a new origin URL shows as the project's slug.
- `wt-git-remove`: a worktree removed with `git worktree remove` leaves the sidebar.
- `wt-git-quiet`: a box that nobody changes runs no git for these, however often they are listed (`TestListingLocationsAgainRunsNoGitUntilSomethingChanges`; on a real box, count berthd's git execs with bpftrace, below).

## How to get to it (user POV)

- Work in a project's checkout from a terminal: add a worktree, commit, switch branches, remove it. The sidebar follows without a reload.

## Driving it with drive.mjs

- Real box: `scenarios/worktree-git-live.mjs`, on a dev box of this checkout (`../scripts/dev-box.sh`):

```sh
.claude/skills/verify-berthd/scripts/dev-box.sh up
eval "$(.claude/skills/verify-berthd/scripts/dev-box.sh env)"   # the token stays in env
node .claude/skills/verify-berthd/scripts/drive.mjs worktree-git-live --port $PORT --agent http://127.0.0.1:9378
.claude/skills/verify-berthd/scripts/dev-box.sh down
```

It adds and removes a worktree `cache-probe` beside acme-shop and puts acme-shop's remote back as it was.

- How many git commands a box runs, for 40 s (Linux, root):

```sh
sudo timeout -s INT 40 bpftrace -e 'tracepoint:syscalls:sys_enter_execve /curtask->real_parent->tgid == '"$(pgrep -f 'berthd serve' | head -1)"'/ { @[str(args->argv[3]), str(args->argv[4])] = count(); }'
```

## Not covered here

- A global `url.*.insteadOf` or an included git config changing a remote's URL shows within a minute, not at once: the box doesn't watch files outside `.git` other than `~/.gitconfig` and `~/.config/git/config`.
