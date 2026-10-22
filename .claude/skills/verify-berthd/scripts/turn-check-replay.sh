#!/usr/bin/env bash
# Replays one finished Claude Code turn through this box's own turn check,
# the way a real turn reaches it: a throwaway location and a session whose
# command is a stand-in `claude`, a transcript holding the turn, and the
# UserPromptSubmit and Stop hooks through `berthd hook`. It prints how the
# box then reads the session, and cleans everything up.
#
#   turn-check-replay.sh NAME TITLE PROMPT_FILE REPLY_FILE
#
# NAME is short ([a-z0-9-]); TITLE is the session's title (turn check sends
# it as the objective); the files hold the turn's last prompt and reply.
# BERTHD picks the berthd (default: the one on PATH, the installed box).
# Turn check must be on for this box (Settings -> Agents). It sends the turn
# to the provider set there, as any turn on this box does.
#
# Prints one line: NAME agent_state=... unfinished=... parked=... (what
# turn check made of it: unfinished is "working", parked is "parked",
# neither is done, a waiting agent_state is "needs you").
set -euo pipefail

name=$1 title=$2 prompt_file=$3 reply_file=$4
BERTHD=${BERTHD:-berthd}
[[ $name =~ ^[a-z0-9-]+$ ]] || { echo "NAME must be [a-z0-9-]" >&2; exit 2; }

root=$(mktemp -d /tmp/turn-check-replay.XXXXXX)
repo=$root/repo
bin=$root/bin
loc=replay-$name
sess=replay-$name
id=$(python3 -c 'import uuid; print(uuid.uuid4())')
proj=$HOME/.claude/projects/$(printf '%s' "$repo" | sed 's/[^a-zA-Z0-9]/-/g')

cleanup() {
  "$BERTHD" session kill "$sess" >/dev/null 2>&1 || true
  "$BERTHD" location rm "$loc" >/dev/null 2>&1 || true
  rm -rf "$proj" "$root"
}
trap cleanup EXIT

mkdir -p "$repo" "$bin"
git -C "$repo" init -q
git -C "$repo" -c user.name=replay -c user.email=replay@localhost commit -q --allow-empty -m replay
# The box knows an agent by its command's name; this one only waits.
printf '#!/bin/sh\nexec sleep 1800\n' > "$bin/claude"
chmod +x "$bin/claude"

"$BERTHD" location add "$loc" "$repo" >/dev/null
"$BERTHD" session new "$loc" --name "$sess" --title "$title" -- "$bin/claude" >/dev/null
sleep 1

transcript=$proj/$id.jsonl
python3 - "$transcript" "$prompt_file" "$reply_file" <<'EOF'
import datetime, json, os, sys
os.makedirs(os.path.dirname(sys.argv[1]), exist_ok=True)
path, prompt, reply = sys.argv[1], open(sys.argv[2]).read(), open(sys.argv[3]).read()
at = datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")
with open(path, "w") as f:
    f.write(json.dumps({"type": "user", "timestamp": at, "message": {"role": "user", "content": prompt}}) + "\n")
    f.write(json.dumps({"type": "assistant", "timestamp": at, "message": {"role": "assistant", "content": [{"type": "text", "text": reply}]}}) + "\n")
EOF

hook() {
  python3 -c 'import json, sys; print(json.dumps({"session_id": sys.argv[1], "transcript_path": sys.argv[2], "cwd": sys.argv[3], "hook_event_name": sys.argv[4], "prompt": sys.argv[5]}))' \
    "$id" "$transcript" "$repo" "$1" "${2:-}" | env -u BERTH_DRIVER BERTH_SESSION="$sess" "$BERTHD" hook claude "$1" >/dev/null
}
hook UserPromptSubmit "$(head -c 200 "$prompt_file")"
sleep 1
hook Stop

# Turn check settles, then asks (up to 10 s); read the session once it has
# had the time.
sleep 15
"$BERTHD" sessions --json | python3 -c '
import json, sys
name = sys.argv[1]
data = json.load(sys.stdin)
for s in data if isinstance(data, list) else data.get("sessions", []):
    if s.get("name") == name:
        print(name, "agent_state=%s unfinished=%s parked=%s" % (s.get("agent_state"), bool(s.get("unfinished")), bool(s.get("parked"))))
        break
else:
    print(f"{name}: not listed"); sys.exit(1)
' "$sess"
