#!/usr/bin/env bash
# upgrade-zombies.sh FROM TO: does a box's self-upgrade leave zombies?
#
# Builds berthd from git ref FROM and from TO ("." is this checkout as it
# is), starts a box of the FROM build on its own state, tmux server and
# ports, attaches terminals to a session with `berth attach`, then upgrades
# the box to the TO build the way the app does (`berth upgrade`). berthd
# execs the new build in its own process, so what the old build had
# started is still its child. The script counts berthd's zombie children
# before, then 5, 15, 30 and 60 seconds after, and prints one JSON line.
#
#   upgrade-zombies.sh origin/main origin/main   # the bug: zombies stay
#   upgrade-zombies.sh origin/main .             # the fixed build reaps them
#   upgrade-zombies.sh . .                       # none are left at all
#
# ATTACH=3 terminals, PORT=7545 for the box, VERIFY_BOX_DIR for its state
# (deleted at the end). Linux (it reads /proc).
set -euo pipefail
from=${1:?usage: upgrade-zombies.sh FROM TO}
to=${2:?usage: upgrade-zombies.sh FROM TO}
root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
dir=${VERIFY_BOX_DIR:-/tmp/vb-upgrade-zombies}
port=${PORT:-7545}
attach=${ATTACH:-3}
addr=127.0.0.1:$port
if ss -ltn | grep -q ":$port "; then echo "port $port is taken" >&2; exit 1; fi
rm -rf "$dir" && mkdir -p "$dir"/{box-home,box-user,lap-home,lap-user,tmux,from,to} && chmod 700 "$dir/tmux"
box_env=(BERTH_HOME="$dir/box-home" BERTH_USER_DIR="$dir/box-user" TMUX_TMPDIR="$dir/tmux")
# The laptop agent the CLI starts gets ports of its own, never 1377-1379.
lap_env=(BERTH_HOME="$dir/lap-home" BERTH_USER_DIR="$dir/lap-user" BERTH_UI_ADDR=127.0.0.1:$((port + 1900)) BERTH_PROXY_ADDR=127.0.0.1:$((port + 1899)))
pids=()
cleanup() {
  for p in "${pids[@]}"; do kill -- "-$p" 2>/dev/null || true; done
  [[ -n ${box_pid:-} ]] && kill "$box_pid" 2>/dev/null || true
  env TMUX_TMPDIR="$dir/tmux" tmux -L berth kill-server 2>/dev/null || true
  for p in $(ss -ltnpH "sport = :$((port + 1900))" 2>/dev/null | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u); do
    tr '\0' '\n' <"/proc/$p/environ" | grep -qx "BERTH_HOME=$dir/lap-home" && kill "$p"
  done
  rm -rf "$dir"
}
trap cleanup EXIT

# build REF OUT [ldflags]: berth and berthd of REF into OUT.
build() {
  local src=$root
  if [[ $1 != . ]]; then
    src=$dir/src-$(basename "$2") && mkdir -p "$src"
    git -C "$root" archive "$1" | tar -x -C "$src"
  fi
  (cd "$src" && go build -trimpath -ldflags="$3" -o "$2/" ./cmd/berth ./cmd/berthd)
}
build "$from" "$dir/from" ""
# Stripped, so the TO build's bytes differ from FROM's even when the refs
# are the same.
build "$to" "$dir/to" "-s -w"
# berth upgrade sends berthd-<os>-<arch> for the arch the box reports.
mv "$dir/to/berthd" "$dir/to/berthd-linux-$(go env GOARCH)"
box() { env "${box_env[@]}" "$dir/from/berthd" "$@"; }
lap() { env "${lap_env[@]}" "$dir/to/berth" "$@"; }

env "${box_env[@]}" nohup "$dir/from/berthd" serve --listen "$addr" >"$dir/berthd.log" 2>&1 &
box_pid=$!
for _ in $(seq 40); do
  grep -q "berthd serving" "$dir/berthd.log" && break
  kill -0 "$box_pid" 2>/dev/null || break
  sleep 0.5
done
grep -q "berthd serving" "$dir/berthd.log" || { echo "berthd did not start:" >&2; cat "$dir/berthd.log" >&2; exit 1; }
lap pair "$(box pair --address "$addr" --json | jq -r .link)" --name devz >/dev/null
repo=$dir/acme-shop
git init -q -b main "$repo" && echo "# acme shop" >"$repo/README.md"
git -C "$repo" add . && git -C "$repo" -c user.email=dev@acme.test -c user.name=acme commit -qm init
box location add acme-shop "$repo" >/dev/null
box session new acme-shop --name z1 -- sh -c 'while :; do sleep 60; done' >/dev/null

for _ in $(seq "$attach"); do
  # A held-open stdin: the terminal stays attached until the box goes. Its
  # own process group, so cleanup ends the sleep too.
  setsid env "${lap_env[@]}" bash -c 'sleep 600 | script -qfc "$(printf %q "$1") attach devz/z1" /dev/null' _ "$dir/to/berth" </dev/null >/dev/null 2>&1 &
  pids+=($!)
done
clients() { ps -eo ppid=,comm= | awk -v p="$box_pid" '$1==p && $2=="tmux:"' | wc -l; }
zombies() { ps -eo stat=,ppid= | awk -v p="$box_pid" '$1~/Z/ && $2==p' | wc -l; }
for _ in $(seq 40); do (($(clients) >= attach)) && break; sleep 0.5; done
attached=$(clients)
# Fewer clients than asked for would read as fewer zombies, not as a failure.
((attached == attach)) || { echo "only $attached of $attach terminals attached" >&2; exit 1; }
before=$(zombies)

lap upgrade devz --force >"$dir/upgrade.log" 2>&1
sleep 5; z5=$(zombies)
sleep 10; z15=$(zombies)
sleep 15; z30=$(zombies)
sleep 30; z60=$(zombies)
# 2: the new build took over in the same process.
serving=$(grep -c "berthd serving" "$dir/berthd.log" || true)
reaped=$(grep -o "reaped [0-9]* of [0-9]* processes[^\"]*" "$dir/berthd.log" | tail -1 || true)
jq -nc --arg from "$from" --arg to "$to" --argjson attached "$attached" --argjson before "$before" \
  --argjson z5 "$z5" --argjson z15 "$z15" --argjson z30 "$z30" --argjson z60 "$z60" --argjson serving "$serving" \
  --arg upgrade "$(tail -1 "$dir/upgrade.log")" --arg reaped "$reaped" \
  '{from:$from,to:$to,attached:$attached,zombies:{before:$before,"5s":$z5,"15s":$z15,"30s":$z30,"60s":$z60},serving_lines:$serving,upgrade:$upgrade,log:$reaped}'
