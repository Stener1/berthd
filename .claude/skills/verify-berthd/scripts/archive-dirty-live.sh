#!/usr/bin/env bash
# archive-dirty-live.sh: on the box dev-box.sh starts, archive a dirty and a
# clean worktree of acme-shop under an archive script, through the laptop
# agent's API as the app does. The dirty one must be refused up front in
# git's words (400), with its file kept and the script never run; the clean
# one archives (202), the script runs in it, and it goes.
# Run dev-box.sh up first; dev-box.sh down cleans it all up.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
eval "$("$here/dev-box.sh" env 2>/dev/null)"
api=http://127.0.0.1:9378/v1/boxes/devq/api
log=$(mktemp -d)/ran.log
call() { curl -sS -H "Authorization: Bearer $VERIFY_BERTH_TOKEN" -H "Content-Type: application/json" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

call -X PUT "$api/locations/acme-shop/scripts" -d "{\"Archive\":\"echo \\\"ran in \$PWD\\\" >> $log\"}" >/dev/null
dirty=$(call -X POST "$api/locations/acme-shop/worktrees" -d '{"name":"verify-dirty"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["path"])')
call -X POST "$api/locations/acme-shop/worktrees" -d '{"name":"verify-clean"}' >/dev/null
echo "not committed" > "$dirty/notes.txt"

out=$(call -w '\n%{http_code}' -X DELETE "$api/locations/acme-shop/worktrees/verify-dirty")
echo "dirty: $out"
[[ $(tail -1 <<<"$out") == 400 && $out == *"contains modified or untracked files"* ]] || fail "the dirty worktree was not refused up front"
out=$(call -w '\n%{http_code}' -X DELETE "$api/locations/acme-shop/worktrees/verify-clean")
echo "clean: $out"
[[ $(tail -1 <<<"$out") == 202 ]] || fail "the clean worktree did not start archiving"

for _ in $(seq 40); do grep -q verify-clean "$log" 2>/dev/null && break; sleep 0.25; done
grep -q verify-clean "$log" || fail "the archive script did not run on the clean worktree"
! grep -q verify-dirty "$log" || fail "the archive script ran on the dirty worktree"
[[ -f $dirty/notes.txt ]] || fail "the dirty worktree lost its file"
echo "OK: dirty refused before its script, clean archived"
