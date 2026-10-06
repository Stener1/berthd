#!/bin/sh
# shellcheck disable=SC2016 # the $ in the files written here are theirs
# Makes a synthetic GitHub for the team setup test (scripts/team-setup-test.sh)
# in ~/.fake-gh, the fake gh's folder: the org acme with a team setup in
# acme/.berth, two repositories the engineer can read and one they can't.
# Run on the laptop container as dev; git daemon serves the same folders to
# the box. Called again with "update", it commits a newer team setup.
set -eu
R="$HOME/.fake-gh"
export GIT_AUTHOR_NAME=keith GIT_AUTHOR_EMAIL=keith@acme.test GIT_COMMITTER_NAME=keith GIT_COMMITTER_EMAIL=keith@acme.test

repo() { # repo SLUG DIR: push DIR's files as SLUG's main
  bare="$R/$1.git"
  [ -d "$bare" ] || git init -q --bare -b main "$bare"
  touch "$bare/git-daemon-export-ok"
  (cd "$2" && git init -q -b main 2>/dev/null; git add -A && git commit -q -m "${3:-Update $1}" && git push -q -f "$bare" HEAD:main)
}

if [ "${1:-}" = update ]; then
  w=$(mktemp -d)
  git clone -q "$R/acme/.berth.git" "$w"
  python3 - "$w/team.json" <<'PY'
import json, sys
p = sys.argv[1]
t = json.load(open(p))
t["box"]["steps"].append({"id": "node", "title": "Node 22", "detail": "with fnm", "sudo": True})
json.dump(t, open(p, "w"), indent=2)
PY
  printf '\nstep_node() { sudo true; }\n' >> "$w/box/setup.sh"
  (cd "$w" && git add -A && git commit -q -m "Node 22" && git push -q origin HEAD:main)
  exit 0
fi

rm -rf "$R" && mkdir -p "$R/acme"
echo "dev" > "$R/.auth"
echo '{"login":"acme","name":"Acme Inc","is_verified":true,"avatar_url":"https://avatars.githubusercontent.com/acme"}' > "$R/acme/org.json"

w=$(mktemp -d)
mkdir -p "$w/box" "$w/kits/api"
cat > "$w/team.json" <<'J'
{
  "schema": "berth.team/v1", "id": "acme", "name": "Acme", "org": "acme",
  "description": "A synthetic team setup for Berth's tests.", "contact": "#onboarding",
  "box": { "script": "box/setup.sh", "steps": [
    { "id": "tools", "title": "Tools", "detail": "/opt/acme, made with sudo", "sudo": true },
    { "id": "db", "title": "Database", "detail": "a stand-in" },
    { "id": "slow", "title": "Slow step", "detail": "waits for /tmp/go" } ] },
  "projects": [
    { "id": "web", "repo": "acme/web", "required": true, "first_task": "Say hello" },
    { "id": "api", "repo": "acme/api", "kit": "./kits/api", "init": "box/init-api.sh" },
    { "id": "secret", "repo": "acme/secret" } ],
  "keys": { "web": { "from": ".env.example", "shared": { "STRIPE_KEY": "op://Dev/Stripe/key" }, "ask": ["MAIL_KEY"] } }
}
J
cat > "$w/box/setup.sh" <<'S'
#!/bin/sh
set -eu
[ "$(id -u)" -ne 0 ] || { echo "run this as yourself, not root" >&2; exit 1; }
step_tools() {
  sudo mkdir -p /opt/acme
  sudo chown "$(id -un)" /opt/acme
}
step_db() {
  [ ! -f /tmp/fail-db ] || { echo "port 5450 is taken" >&2; exit 3; }
  touch "$HOME/.acme-db"
}
step_slow() {
  while [ ! -f /tmp/go ]; do sleep 1; done
  touch "$HOME/.acme-slow"
}
case "${1:-all}" in
  plan) echo "tools sudo"; echo db; echo slow ;;
  check) case "$2" in tools) [ -d /opt/acme ] ;; db) [ -f "$HOME/.acme-db" ] ;; slow) [ -f "$HOME/.acme-slow" ] ;; *) exit 1 ;; esac ;;
  all) for s in tools db slow; do "step_$s"; done ;;
  *) "step_$1" ;;
esac
S
printf '#!/bin/sh\nset -eu\necho "api init in $(pwd)" > "$HOME/.acme-api-init"\n' > "$w/box/init-api.sh"
chmod +x "$w/box/setup.sh" "$w/box/init-api.sh"
echo '{"id":"acme-api","name":"Acme API","config":{"ports":2,"services":[{"name":"api","run":"python3 -m http.server $BERTH_PORT"}]}}' > "$w/kits/api/kit.json"
repo acme/.berth "$w" "Team setup"

w=$(mktemp -d); mkdir -p "$w/.berth"
echo '{"setup":"echo web set up","services":[{"name":"web","run":"python3 -m http.server $BERTH_PORT","title":"Web"}]}' > "$w/.berth/config.json"
printf 'STRIPE_KEY=\nMAIL_KEY=\nPORT=3000\n' > "$w/.env.example"
repo acme/web "$w"
w=$(mktemp -d); echo "print('api')" > "$w/main.py"; repo acme/api "$w"
w=$(mktemp -d); echo secret > "$w/x"; repo acme/secret "$w"; touch "$R/acme/secret.git/berth-noaccess"
git --git-dir "$R/acme/.berth.git" rev-parse main
