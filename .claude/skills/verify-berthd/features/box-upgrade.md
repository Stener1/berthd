# Upgrading a box

`berth upgrade BOX` (and the app's Upgrade, which runs it) uploads a new berthd to the box. The box checks it runs, swaps it in and execs it in its own process (`internal/box/upgrade.go`), so sessions, the tmux server and the box's process id all stay. There is nothing in the app to look at; this is proved on a real box, with the process table as the second view.

## Sub-features

- `upgrade-sessions`: sessions keep running across an upgrade, and `berth upgrade` says "Sessions kept running" once the box answers on the new build.
- `upgrade-no-zombies`: nothing the old build started stays a zombie under berthd. Before the exec the box ends its terminals' tmux clients and waits for them (`Sessions.Detach`, from `BeforeRestart` in `cmd/berthd/main.go`); the new build lists the children it inherited before it starts any of its own and waits for exactly those as they end (`internal/childreap`), never for any child, which would take exit statuses os/exec is waiting for. berthd.log then says `reaped N of M processes the build before this one left`.

## How to get to it (user POV)

- Settings → Boxes → a box's Upgrade, or `berth upgrade BOX [--force]` in a terminal.
- On the box: `ps -eo stat,ppid | awk -v p=<berthd pid> '$1~/Z/ && $2==p'` lists berthd's zombie children; it should be empty within a minute of an upgrade.

## Driving it

`../scripts/upgrade-zombies.sh FROM TO` builds berthd from two git refs (`.` is the checkout as it is), starts a box of FROM on its own state, tmux server and ports (box 7545, its laptop agent 9444/9445), attaches `ATTACH=3` terminals with `berth attach`, upgrades it to TO with `berth upgrade --force`, and prints one JSON line: how many tmux clients were attached, berthd's zombie children before and 5/15/30/60 s after, and berthd.log's reap line. Linux only; it deletes its state at the end. A run takes the builds' time plus about a minute.

- `origin/main` → `origin/main` on a build before the fix: the bug, one zombie per attached terminal at 60 s.
- an old build → this one: the new build reaps what the old one left (`log` names them).
- this build → this build: none at any point.

`go test ./internal/childreap` re-execs its test binary the same way (children left before an exec, reaped after; a child of the new build's own keeps its exit status), and `TestDetachEndsAttachedClientsAndWaitsForThem` (`internal/box`) covers the drain.

## Gotchas

- The TO build is stripped (`-s -w`) so its bytes differ from FROM's even when both refs are the same; `berth upgrade` does nothing for a box already on the build.
- The attach helpers run in process groups of their own (`setsid`): a plain background pipeline would keep the script's stdout open for as long as its `sleep` lives.
