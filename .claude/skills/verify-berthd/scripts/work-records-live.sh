#!/usr/bin/env bash
# work-records-live.sh: on the box dev-box.sh starts (run it in its terminal
# driver, with every BERTH_* unset, and VERIFY_BOX_DIR set to your own folder),
# prove work records and session parents the way people and agents reach them:
# started_by from a real session's own environment, across a daemon restart;
# create, pin, Not now and a session ending, read back the same from the API and
# the CLI; a range forming a group; last_seen moved by the app and by a prompt
# typed at the terminal, and not by an agent's send or a restart.
# dev-box.sh down cleans it all up.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(git -C "$here" rev-parse --show-toplevel)
dir=${VERIFY_BOX_DIR:-/tmp/verify-berthd-box}
eval "$("$here/dev-box.sh" env 2>/dev/null)"
api=http://127.0.0.1:9378/v1/boxes/devq/api
box() { env BERTH_HOME="$dir/box-home" BERTH_USER_DIR="$dir/box-user" TMUX_TMPDIR="$dir/tmux" "$root/bin/berthd" "$@"; }
tm() { env TMUX_TMPDIR="$dir/tmux" tmux -L berth "$@"; }
# As the app does: through the laptop agent, as origin app.
call() { curl -sS -H "Authorization: Bearer $VERIFY_BERTH_TOKEN" -H "Content-Type: application/json" -H "X-Berth-Origin: app" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }
session() { call "$api/sessions" | jq -c --arg n "$1" '.[] | select(.name==$n)'; }
record_of() { call "$api/work?all=1" | jq -c --arg n "$1" '[.[] | select(any(.sessions[]; .name==$n and (.ended|not)))][0]'; }
# berthd only: tmux, and the agents in it, keep running. Until it has exited,
# not just let go of the port: its cleanup would take the new one's socket.
restart_box() {
  local pid
  pid=$(cat "$dir/berthd.pid")
  kill "$pid"
  for _ in $(seq 150); do kill -0 "$pid" 2>/dev/null || break; sleep 0.2; done
  if kill -0 "$pid" 2>/dev/null; then fail "berthd ($pid) is still running after 30 s"; fi
  env BERTH_HOME="$dir/box-home" BERTH_USER_DIR="$dir/box-user" TMUX_TMPDIR="$dir/tmux" nohup "$root/bin/berthd" serve --listen "${VERIFY_BOX_ADDR:-127.0.0.1:7544}" >>"$dir/berthd.log" 2>&1 &
  echo $! >"$dir/berthd.pid"
}
wait_for() { # wait_for SECONDS WHAT COMMAND...
  local n=$1 what=$2; shift 2
  for _ in $(seq $((n * 2))); do "$@" >/dev/null 2>&1 && return 0; sleep 0.5; done
  fail "$what, after ${n}s"
}

# --- Parent links: a real session starts others, from its own environment.
box session new acme-shop --name verify-parent >/dev/null
sleep 1
tm send-keys -t verify-parent "\"$root/bin/berthd\" session new acme-shop --name verify-child -- claude --help" Enter
tm send-keys -t verify-parent "\"$root/bin/berthd\" task new acme-shop/verify-task --title 'a task' -- sleep 600" Enter
wait_for 20 "the session's child to start" sh -c "curl -sS -H 'Authorization: Bearer $VERIFY_BERTH_TOKEN' $api/sessions | jq -e 'any(.[]; .started_by==\"verify-parent\" and .location==\"acme-shop/verify-task\")'"
[[ $(session verify-child | jq -r .started_by) == verify-parent ]] || fail "session new from a session records no started_by: $(session verify-child)"
task=$(call "$api/sessions" | jq -r '.[] | select(.location=="acme-shop/verify-task") | .name')
pass "started_by: verify-child and $task, started from verify-parent"
[[ $(session verify-parent | jq -r '.started_by // ""') == "" ]] || fail "verify-parent has a parent it never had"
# A paired computer can't claim one.
call -X POST "$api/sessions" -d '{"name":"verify-spoof","location":"acme-shop","from_session":"verify-parent"}' >/dev/null
[[ $(session verify-spoof | jq -r '.started_by // ""') == "" ]] || fail "a paired computer named a parent: $(session verify-spoof)"
pass "started_by: a paired computer's word for it is ignored"

# --- A range forms a group.
box session new acme-shop --name verify-range --title 'ACME-1..3 sweep' -- claude --help >/dev/null
box session new acme-shop --name verify-single --title 'ACME-2 worker' -- claude --help >/dev/null
box session new acme-shop --name verify-other --title 'ACME-9 elsewhere' -- claude --help >/dev/null
range=$(record_of verify-range)
[[ $(jq -r .id <<<"$range") == $(record_of verify-single | jq -r .id) ]] || fail "ACME-2 should join ACME-1..3: $(call "$api/work")"
[[ $(jq -r .id <<<"$range") != $(record_of verify-other | jq -r .id) ]] || fail "ACME-9 isn't in the range"
range=$(record_of verify-range)
[[ $(jq -c '.issue_ids | sort' <<<"$range") == '["ACME-1","ACME-2","ACME-3"]' && $(jq -r .issue <<<"$range") == 'ACME-1..3' && $(jq -r .title <<<"$range") == 'ACME-1..3 sweep' ]] || fail "the range's record: $range"
[[ $(record_of verify-child | jq -r .id) != null ]] || fail "verify-child has no record"
pass "records: ACME-2 joined ACME-1..3, ACME-9 stayed apart"
# The id pattern is the box's setting.
box work settings --id-pattern '(?i)\bzed-[0-9]+\b' >/dev/null
box session new acme-shop --name verify-zed-a --title 'zed-1 lower' -- claude --help >/dev/null
box session new acme-shop --name verify-zed-b --title 'ZED-1 upper' -- claude --help >/dev/null
[[ $(record_of verify-zed-a | jq -r .id) == $(record_of verify-zed-b | jq -r .id) ]] || fail "the box's id pattern wasn't used"
box work settings --default >/dev/null
pass "records: the id pattern comes from the box's settings"

# --- Create, pin, Not now, close a session: the record stays, the same from both.
box session new acme-shop --name verify-work --title 'verify work' -- sleep 600 >/dev/null
id=$(box work create --title 'Verify the launch' --session verify-work --issue ACME-50 --project 'Big launch' --json | jq -r .id)
box work pin work "$id" >/dev/null
box work pin project 'Big launch' >/dev/null
call -X POST "$api/work/pins" -d '{"kind":"repo","target":"acme-shop"}' >/dev/null
box work update "$id" --not-until 2099-01-02 >/dev/null
call -X DELETE "$api/sessions/verify-work" >/dev/null
same() { # the API and the CLI read the same
  local a b
  a=$(call "$api/work/$id" | jq -S .)
  b=$(box work get "$id" --json | jq -S .)
  [[ $a == "$b" ]] || fail "the API and the CLI differ:\n$a\n$b"
  a=$(call "$api/work/pins" | jq -S .)
  b=$(box work pins --json | jq -S .)
  [[ $a == "$b" ]] || fail "pins differ between the API and the CLI:\n$a\n$b"
}
same
got=$(call "$api/work/$id")
[[ $(jq -r .status <<<"$got") == parked && $(jq -r .not_until <<<"$got") == 2099-01-0* && $(jq -r .pinned <<<"$got") == true && $(jq -r .project <<<"$got") == 'Big launch' ]] || fail "the record: $got"
[[ $(jq -r '.sessions[0].ended // ""' <<<"$got") != "" ]] || fail "its session ended and its line doesn't say so: $got"
[[ $(call "$api/work/pins" | jq 'length') == 3 ]] || fail "pins: $(call "$api/work/pins")"
pass "work: created, pinned, Not now, its session ended; the record stays, the same from API and CLI"

# --- Across a restart.
before=$(call "$api/work?all=1" | jq -S '[.[] | {id, title, status, issue, issue_ids, sessions: [.sessions[] | {name, started_by}]}]')
pins=$(call "$api/work/pins" | jq -S .)
restart_box
wait_for 30 "the box to answer after its restart" sh -c "curl -sSf -H 'Authorization: Bearer $VERIFY_BERTH_TOKEN' $api/work >/dev/null"
[[ $(session verify-child | jq -r .started_by) == verify-parent ]] || fail "started_by was lost in a restart: $(session verify-child)"
[[ $(call "$api/work?all=1" | jq -S '[.[] | {id, title, status, issue, issue_ids, sessions: [.sessions[] | {name, started_by}]}]') == "$before" ]] || fail "records changed in a restart"
[[ $(call "$api/work/pins" | jq -S .) == "$pins" ]] || fail "pins changed in a restart"
same
pass "restart: started_by, records and pins are all still there"

# --- A pinned record that closes loses its pin; a repo's stays.
box work update "$id" --status closed >/dev/null
[[ $(call "$api/work/pins" | jq -c '[.[].kind] | sort') == '["project","repo"]' ]] || fail "pins after closing: $(call "$api/work/pins")"
[[ $(box work list --json | jq --arg id "$id" 'any(.[]; .id==$id)') == false && $(box work list --all --json | jq --arg id "$id" 'any(.[]; .id==$id)') == true ]] || fail "closed records list only with --all"
pass "pins: closing dropped the record's pin, and it lists only with --all"

# --- last_seen, on the real Claude Code in session ask.
seen() { call "$api/work?all=1" | jq -r '[.[] | select(any(.sessions[]; .name=="ask"))][0].last_seen // ""'; }
idle() { tm capture-pane -p -t ask | grep -q '❯' && ! tm capture-pane -p -t ask | grep -qi 'esc to interrupt'; }
start=$(seen)
wait_for 90 "Claude to be ready" idle
box session send ask 'Reply with the single word agent.' --no-notify >/dev/null
sleep 25
wait_for 120 "Claude to finish the agent's prompt" idle
sleep 10
[[ $(seen) == "$start" ]] || fail "an agent's send moved last_seen: $start → $(seen)"
pass "last_seen: an agent's send didn't move it"
call -X POST "$api/sessions/ask/send" -d '{"text":"Reply with the single word app.","when":"idle"}' >/dev/null
appseen=""
for _ in $(seq 30); do appseen=$(seen); [[ $appseen != "$start" ]] && break; sleep 1; done
[[ $appseen != "$start" ]] || fail "the app's send didn't move last_seen"
pass "last_seen: the app's send moved it to $appseen"
wait_for 120 "Claude to finish the app's prompt" idle
restart_box
wait_for 30 "the box to answer after its restart" sh -c "curl -sSf -H 'Authorization: Bearer $VERIFY_BERTH_TOKEN' $api/work >/dev/null"
sleep 12
[[ $(seen) == "$appseen" ]] || fail "a restart moved last_seen: $appseen → $(seen)"
pass "last_seen: a restart didn't move it"
wait_for 60 "Claude to be ready" idle
sleep 20 # past the window that ties a prompt to a send
tm send-keys -t ask -l 'Reply with the single word typed.'
sleep 1
tm send-keys -t ask Enter
typed=""
for _ in $(seq 90); do typed=$(seen); [[ $typed != "$appseen" ]] && break; sleep 1; done
[[ $typed != "$appseen" ]] || fail "a prompt typed at the terminal didn't move last_seen"
pass "last_seen: a prompt typed at the terminal moved it to $typed"

# --- Hooks can follow it, and no title or prompt reaches the journal.
journal=$(cat "$dir"/box-home/box/journal/*.jsonl 2>/dev/null || true)
for t in work.created work.joined work.pinned work.unpinned work.updated work.closed work.seen session.started; do
  grep -q "\"type\":\"$t\"" <<<"$journal" || fail "no $t in the journal"
done
# A pin says what it pins, a project's name included; nothing else names work.
if grep '"type":"work\.' <<<"$journal" | grep -v '"type":"work\.\(un\)\?pinned"' | grep -qE 'Verify the launch|Big launch|ACME-1\.\.3 sweep'; then fail "a work event carries a title"; fi
pass "events: work.* are in the journal, without titles"
echo "PASS work-records"
