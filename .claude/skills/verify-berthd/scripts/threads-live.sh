#!/usr/bin/env bash
# threads-live.sh: on the box dev-box.sh starts (run it with every BERTH_*
# unset, and VERIFY_BOX_DIR set to your own folder), prove thread keys the way
# scripts reach them, reading each back from both the API and the CLI, and from
# the journal: a claim at a session's start and the newest live claimant owning
# its key; a held prompt cancelled and never typed, undone, typed and handed
# back, and lost when its session is killed; a structured session resumed in
# place and under a new name, and a terminal one refused.
# dev-box.sh down cleans it all up.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(git -C "$here" rev-parse --show-toplevel)
dir=${VERIFY_BOX_DIR:-/tmp/verify-berthd-box}
eval "$("$here/dev-box.sh" env 2>/dev/null)"
api=http://127.0.0.1:9378/v1/boxes/devq/api
box() { env BERTH_HOME="$dir/box-home" BERTH_USER_DIR="$dir/box-user" TMUX_TMPDIR="$dir/tmux" "$root/bin/berthd" "$@"; }
tm() { env TMUX_TMPDIR="$dir/tmux" tmux -L berth "$@"; }
call() { curl -sS -H "Authorization: Bearer $VERIFY_BERTH_TOKEN" -H "Content-Type: application/json" -H "X-Berth-Origin: app" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok: $*"; }
session() { call "$api/sessions" | jq -c --arg n "$1" '.[] | select(.name==$n)'; }
wait_for() { # wait_for SECONDS WHAT COMMAND...
  local n=$1 what=$2; shift 2
  for _ in $(seq $((n * 2))); do "$@" >/dev/null 2>&1 && return 0; sleep 0.5; done
  fail "$what, after ${n}s"
}
thread_api() { call --get "$api/work/threads" --data-urlencode "key=$1"; }
# The API and the CLI read the same thread, and it's what's returned. A session
# can change between the two reads, so only differing every time is a failure.
find_key() {
  local a b
  for _ in 1 2 3 4 5 6 7 8; do
    a=$(thread_api "$1" | jq -S .)
    b=$(box work find --key "$1" --json | jq -S .)
    [[ $a == "$b" ]] && { printf '%s' "$a"; return 0; }
    sleep 1
  done
  fail "the API and the CLI differ on $1:\n$a\n$b"
}
owner() { find_key "$1" | jq -r '.owner.name // ""'; }
delivery() { # delivery ID: the same from both, and it's what's returned
  local a b
  a=$(call "$api/work/deliveries/$1" | jq -S .)
  b=$(box work delivery "$1" --json | jq -S .)
  [[ $a == "$b" ]] || fail "the API and the CLI differ on $1:\n$a\n$b"
  printf '%s' "$a"
}
state_is() { [[ $(delivery "$1" | jq -r .state) == "$2" ]]; }
journal() { cat "$dir"/box-home/box/journal/*.jsonl 2>/dev/null || true; }
restart_box() {
  local pid
  pid=$(cat "$dir/berthd.pid")
  kill "$pid"
  for _ in $(seq 150); do kill -0 "$pid" 2>/dev/null || break; sleep 0.2; done
  if kill -0 "$pid" 2>/dev/null; then fail "berthd ($pid) is still running after 30 s"; fi
  env BERTH_HOME="$dir/box-home" BERTH_USER_DIR="$dir/box-user" TMUX_TMPDIR="$dir/tmux" nohup "$root/bin/berthd" serve --listen "${VERIFY_BOX_ADDR:-127.0.0.1:7544}" >>"$dir/berthd.log" 2>&1 &
  echo $! >"$dir/berthd.pid"
  wait_for 30 "the box to answer after its restart" sh -c "curl -sSf -H 'Authorization: Bearer $VERIFY_BERTH_TOKEN' $api/work >/dev/null"
}

# --- A claim at a session's start, and who owns the key.
box session new acme-shop --name verify-a --title 'the first worker' --claim ACME-70 --claim 'acme-shop#12' -- sleep 900 >/dev/null
got=$(find_key ACME-70)
[[ $(jq -r .owner.name <<<"$got") == verify-a && $(jq -r .provenance <<<"$got") == claimed && $(jq -r .owner.live <<<"$got") == true ]] || fail "after a claim at start: $got"
record=$(jq -r .record.id <<<"$got")
[[ $record != null ]] || fail "no record: $got"
[[ $(jq -r '.record.keys[] | select(.key=="ACME-70") | .provenance' <<<"$got") == claimed ]] || fail "the record's key: $got"
[[ $(owner 'acme-shop#12') == verify-a ]] || fail "a key with a # in it"
[[ $(call "$api/work/$record" | jq -r '.sessions[0].claims | length') == 2 ]] || fail "the record's line: $(call "$api/work/$record")"
pass "claim at start: verify-a owns ACME-70, the same from the API and the CLI"

# A title is a mention: it joins and subscribes, and never owns.
box session new acme-shop --name verify-guess --title 'ACME-70 and ACME-71, a guess' -- claude --help >/dev/null
got=$(find_key ACME-70)
[[ $(jq -r .owner.name <<<"$got") == verify-a ]] || fail "a title took the key: $got"
jq -e 'any(.subscribers[]; .name=="verify-guess")' <<<"$got" >/dev/null || fail "a session naming the key subscribes to it: $got"
[[ $(jq -r .record.id <<<"$got") == "$record" ]] || fail "it should have joined the thread's record: $got"
got=$(find_key ACME-71)
[[ $(jq -r .owner <<<"$got") == null && $(jq -r .provenance <<<"$got") == mentioned && $(jq -r '.claimants | length' <<<"$got") == 0 ]] || fail "a key only a title names: $got"
pass "mentions: a title's id subscribes, joins the record, and owns nothing"

# End the session; start another claiming the same key.
call -X DELETE "$api/sessions/verify-a" >/dev/null
got=$(find_key ACME-70)
[[ $(jq -r .owner <<<"$got") == null && $(jq -r '.claimants[0].name' <<<"$got") == verify-a && $(jq -r '.claimants[0].live' <<<"$got") == false ]] || fail "after it ended: $got"
pass "owner: it ended, so there's no owner, and it's still named as who had the key"
box session new acme-shop --name verify-b --claim ACME-70 -- sleep 900 >/dev/null
got=$(find_key ACME-70)
[[ $(jq -r .owner.name <<<"$got") == verify-b && $(jq -r .record.id <<<"$got") == "$record" ]] || fail "the new claimant: $got"
same_a=$(call "$api/work/$record" | jq -S .)
same_b=$(box work get "$record" --json | jq -S .)
[[ $same_a == "$same_b" ]] || fail "the record differs between the API and the CLI"
pass "owner: verify-b took over, on the same record, the same from the API and the CLI"

# Routers don't own; a claim verb does.
box session new acme-shop --name router-verify --claim ACME-70 -- sleep 900 >/dev/null
[[ $(owner ACME-70) == verify-b ]] || fail "a router took the key: $(find_key ACME-70)"
box session new acme-shop --name verify-c -- sleep 900 >/dev/null
box work claim --key ACME-70 --session verify-c >/dev/null
got=$(find_key ACME-70)
[[ $(jq -r .owner.name <<<"$got") == verify-c ]] || fail "the claim verb: $got"
jq -e 'any(.subscribers[]; .name=="verify-b")' <<<"$got" >/dev/null || fail "an older claimant follows: $got"
pass "owner: a router is skipped, and a claim verb makes the newest claimant"
box work subscribe --key gap:the-other --session verify-b >/dev/null
[[ $(find_key gap:the-other | jq -r '.subscribers[0].name') == verify-b ]] || fail "subscribe"
[[ $(find_key gap:the-other | jq -r .owner) == null ]] || fail "subscribing made an owner"
box work unsubscribe --key gap:the-other --session verify-b >/dev/null
[[ $(find_key gap:the-other | jq -r '.subscribers | length') == 0 ]] || fail "unsubscribe"
pass "subscribe and unsubscribe"

restart_box
[[ $(owner ACME-70) == verify-c ]] || fail "a restart lost the owner: $(find_key ACME-70)"
pass "restart: claims are still there"

# --- Resume: a terminal session is refused as the route refuses it.
refused=$(box session resume ask 2>&1) && fail "resuming a terminal session should fail: $refused"
[[ $refused == *"isn't a structured session"* ]] || fail "the refusal: $refused"
route=$(call -X POST "$api/sessions/ask/resume")
[[ $(jq -r .error <<<"$route") == *"isn't a structured session"* ]] || fail "the route: $route"
pass "resume: a terminal session gets the route's refusal: $refused"

# A structured session: resumed in place, then under a new name.
repo=$dir/acme-chat
if [[ ! -d $repo/.git ]]; then
  git init -q -b main "$repo" && echo "# acme chat" >"$repo/README.md"
  git -C "$repo" add . && git -C "$repo" -c user.email=dev@acme.test -c user.name=acme commit -qm init
fi
box location add acme-chat "$repo" >/dev/null
call -X PUT "$api/agents/defaults" -d '{"structured":true,"mode":"bypassPermissions"}' >/dev/null
box session new acme-chat --name verify-chat --agent claude --claim ACME-90 --prompt 'Reply with the single word ready.' >/dev/null
finished() { [[ $(session "$1" | jq -r .agent_state) =~ ^(finished|idle)$ ]]; }
wait_for 180 "the chat session to finish its first turn" finished verify-chat
[[ $(owner ACME-90) == verify-chat ]] || fail "the structured claim: $(find_key ACME-90)"
conversation=$(find_key ACME-90 | jq -r .owner.conversation)
[[ $conversation != null && -n $conversation ]] || fail "no conversation to resume from: $(find_key ACME-90)"
# The agent's host, not the pane's shell: a host left running holds its socket.
kill "$(pgrep -f "agent-host.*$conversation" | head -1)"
exited() { [[ $(session "$1" | jq -r .exited) == true ]]; }
wait_for 30 "the chat session's agent to end" exited verify-chat
got=$(find_key ACME-90)
[[ $(jq -r .owner <<<"$got") == null && $(jq -r '.claimants[0].exited' <<<"$got") == true ]] || fail "an ended agent still owns: $got"
pass "resume: its agent ended, so verify-chat owns nothing for now"
[[ $(box session resume verify-chat --json | jq -r .name) == verify-chat ]] || fail "session resume"
alive() { [[ $(session "$1" | jq -r .exited) == false ]]; }
wait_for 60 "the chat session's agent to come back" alive verify-chat
[[ $(owner ACME-90) == verify-chat ]] || fail "resumed, it should own again: $(find_key ACME-90)"
pass "resume: an ended structured session started again in place, and owns the key again"
call -X DELETE "$api/sessions/verify-chat" >/dev/null
[[ $(find_key ACME-90 | jq -r .owner) == null ]] || fail "it's gone, and still owns"
# A killed host takes a moment to go, and holds the conversation's socket till then.
host_gone() { ! pgrep -f "agent-host.*$conversation" >/dev/null; }
wait_for 30 "the killed session's host to go" host_gone
new=$(box conversation resume "$conversation" --json | jq -r .name)
[[ -n $new && $new != null && $new != verify-chat ]] || fail "conversation resume answered $new"
[[ $(owner ACME-90) == "$new" ]] || fail "resumed under a new name, $new should own ACME-90: $(find_key ACME-90)"
pass "resume: a conversation came back as $new, and took its claim with it"
call -X DELETE "$api/sessions/$new" >/dev/null
call -X PUT "$api/agents/defaults" -d '{"structured":false,"mode":"bypassPermissions"}' >/dev/null

# --- Held prompts, on the real Claude Code in session ask.
ready() { tm capture-pane -p -t ask | grep -q '❯' && ! tm capture-pane -p -t ask | grep -qi 'esc to interrupt'; }
working() { [[ $(session ask | jq -r .agent_state) == running ]]; }
wait_for 120 "Claude to be ready" ready
busy() { # keep Claude in a long tool call
  box session send ask "Run this with your Bash tool, then reply with the single word done: sleep $1" --no-notify >/dev/null
  wait_for 120 "Claude to start working" working
}
hold() { # hold KEY TEXT [FLAG...]: an idle send to the busy agent; prints its answer
  local key=$1 text=$2; shift 2
  box session send ask "$text" --when idle --key "$key" --source verify --no-notify --json "$@"
}
busy 150
res=$(hold ACME-80 'Reply with the single word ACMEONE.')
[[ $(jq -r .queued <<<"$res") == true ]] || fail "the send wasn't held: $res"
d1=$(jq -r .delivery <<<"$res"); turn=$(jq -r .turn <<<"$res")
got=$(delivery "$d1")
[[ $(jq -r .state <<<"$got") == held && $(jq -r .key <<<"$got") == ACME-80 && $(jq -r .to.name <<<"$got") == ask && $(jq -r .to.box <<<"$got") != null && $(jq -r .turn <<<"$got") == "$turn" && $(jq -r .source <<<"$got") == verify && $(jq -r .from <<<"$got") != null ]] || fail "the delivery: $got"
[[ $(box session queue ask --json | jq -r '.[0].turn') == "$turn" ]] || fail "session queue: $(box session queue ask --json)"
pass "held: the delivery reads held, and session queue lists its turn"
box session unqueue ask "$turn" >/dev/null
state_is "$d1" cancelled || fail "after session unqueue: $(delivery "$d1")"
[[ $(box session queue ask --json | jq 'length') == 0 ]] || fail "still queued: $(box session queue ask --json)"
pass "held: session unqueue cancelled it, and the delivery says so"

res=$(hold ACME-80 'Reply with the single word ACMETWO.')
d2=$(jq -r .delivery <<<"$res")
got=$(box work undo "$d2" --json)
[[ $(jq -r .state <<<"$got") == undone && $(jq -r .retracted <<<"$got") == true ]] || fail "undo while held: $got"
[[ $(box session queue ask --json | jq 'length') == 0 ]] || fail "undo left it queued"
pass "undo: a held one is retracted"

# The same idem is the same delivery.
res=$(hold ACME-81 'Reply with the single word ACMETHREE.' --idem verify-three)
d3=$(jq -r .delivery <<<"$res")
again=$(hold ACME-81 'Reply with the single word ACMETHREE.' --idem verify-three)
[[ $(jq -r .delivery <<<"$again") == "$d3" && $(jq -r .duplicate <<<"$again") == true ]] || fail "a retried send: $again"
[[ $(box work deliveries --key ACME-81 --json | jq 'length') == 1 ]] || fail "a retry made a second delivery"
pass "delivery: a retried send with --idem is the same delivery"
restart_box
state_is "$d3" held || fail "a restart ended a held delivery: $(delivery "$d3")"
[[ $(box session queue ask --json | jq -r '.[0].turn') == $(jq -r .turn <<<"$res") ]] || fail "a restart lost the held prompt: $(box session queue ask --json)"
pass "restart: a held delivery and its prompt are still held"
wait_for 300 "the held prompt to be typed" state_is "$d3" typed
wait_for 180 "Claude to answer" sh -c "env TMUX_TMPDIR='$dir/tmux' tmux -L berth capture-pane -p -S - -t ask | grep -q ACMETHREE"
pass "typed: the agent went idle, the prompt reached it, and the delivery reads typed"
screen=$(tm capture-pane -p -S - -t ask)
grep -q 'ACMEONE\|ACMETWO' <<<"$screen" && fail "a cancelled prompt was typed"
pass "cancelled: neither cancelled prompt ever reached the agent"
got=$(box work hand-back "$d3" --why 'not the work here' --json)
[[ $(jq -r .state <<<"$got") == handed_back && $(jq -r .why <<<"$got") == 'not the work here' ]] || fail "hand back: $got"
refused=$(box work undo "$d3" 2>&1) && fail "undoing a handed back one should fail: $refused"
pass "hand back: it reads handed_back, and can't be undone after"
box session send ask 'Reply with the single word ACMEFOUR.' --when now --key ACME-82 --no-notify --json >"$dir/typed.json"
d4=$(jq -r .delivery <"$dir/typed.json")
state_is "$d4" typed || fail "a send typed at once: $(delivery "$d4")"
got=$(box work undo "$d4" --json)
[[ $(jq -r .state <<<"$got") == undone && $(jq -r '.retracted // false' <<<"$got") == false ]] || fail "undo once typed: $got"
pass "undo: one already typed says it wasn't retracted"

# --- Held when its session is killed: lost, and the event fired.
wait_for 180 "Claude to be ready again" ready
wait_for 120 "Claude's turn to end" finished ask
busy 300
res=$(hold ACME-83 'Reply with the single word ACMEFIVE.')
d5=$(jq -r .delivery <<<"$res")
state_is "$d5" held || fail "not held: $(delivery "$d5")"
call -X DELETE "$api/sessions/ask" >/dev/null
state_is "$d5" lost || fail "after its session was killed: $(delivery "$d5")"
lost_event() { journal | grep '"type":"work.lost"' | grep -q "\"id\":\"$d5\""; }
wait_for 10 "work.lost in the journal" lost_event
pass "lost: killed with a prompt held, the delivery reads lost, and work.lost fired"

# --- The journal, and the file: a second view of the same.
for t in work.claimed work.subscribed work.unsubscribed work.delivered work.cancelled work.undone work.handed_back work.lost; do
  grep -q "\"type\":\"$t\"" <<<"$(journal)" || fail "no $t in the journal"
done
if grep '"type":"work\.' <<<"$(journal)" | grep -qE 'ACMEONE|ACMETWO|ACMETHREE|ACMEFOUR|ACMEFIVE'; then fail "a work event carries a prompt"; fi
if jq -e '.deliveries[]? | tostring | test("ACMEONE|ACMETWO|ACMETHREE|ACMEFOUR|ACMEFIVE")' "$dir/box-home/box/work.json" >/dev/null 2>&1; then fail "work.json holds a prompt"; fi
[[ $(jq --arg d "$d5" -r '.deliveries[] | select(.id==$d) | .state' "$dir/box-home/box/work.json") == lost ]] || fail "work.json doesn't say lost"
pass "events: claims and deliveries are in the journal and the file, and no prompt is"
echo "PASS threads"
