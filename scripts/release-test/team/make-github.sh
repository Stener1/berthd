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
  "box": { "script": "box/setup.sh", "settings": { "ACME_PG": "16", "$why": "a comment" }, "steps": [
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
# tools, as a team's docker step does, adds you to a group (acmedocker), and
# puts a tool on a login shell's PATH only (as fnm does with node).
step_tools() {
  sudo mkdir -p /opt/acme
  sudo chown "$(id -un)" /opt/acme
  sudo groupadd -f acmedocker
  sudo usermod -aG acmedocker "$(id -un)"
  mkdir -p "$HOME/.local/share/acme/bin"
  printf '#!/bin/sh\necho acmetool\n' > "$HOME/.local/share/acme/bin/acmetool"
  chmod +x "$HOME/.local/share/acme/bin/acmetool"
  grep -q acme/bin "$HOME/.profile" 2>/dev/null || echo 'PATH="$HOME/.local/share/acme/bin:$PATH"' >> "$HOME/.profile"
}
# The steps after tools need the group it added, in the same terminal.
group() { id -nG | grep -qw acmedocker || { echo "$1: this terminal is not in acmedocker ($(id -nG))" >&2; exit 4; }; }
step_db() {
  group db
  [ ! -f /tmp/fail-db ] || { echo "port 5450 is taken" >&2; exit 3; }
  echo "pg=${BERTH_SETTING_ACME_PG:-unset}" > "$HOME/.acme-db"
}
step_slow() {
  group slow
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
printf '#!/bin/sh\nset -eu\n{ echo "api init in $(pwd)"; echo "groups $(id -nG)"; env | grep -E "^BERTH_(KIT_DIR|LOCATION|ROOT_PATH|SETTING_ACME_PG)=" | sort; . "$BERTH_KIT_DIR/lib.sh" && echo "kit lib: $(acme_kit_lib)"; } > "$HOME/.acme-api-init"\n' > "$w/box/init-api.sh"
chmod +x "$w/box/setup.sh" "$w/box/init-api.sh"
# The kit's own code, which the init reuses through BERTH_KIT_DIR.
echo 'acme_kit_lib() { echo "from the kit"; }' > "$w/kits/api/lib.sh"
echo '{"id":"acme-api","name":"Acme API","requires":[{"tool":"acmetool","hint":"the tools step installs it"}],"config":{"ports":2,"services":[{"name":"api","run":"python3 -m http.server $BERTH_PORT"}]}}' > "$w/kits/api/kit.json"
repo acme/.berth "$w" "Team setup"

w=$(mktemp -d); mkdir -p "$w/.berth"
echo '{"setup":"echo web set up","services":[{"name":"web","run":"python3 -m http.server $BERTH_PORT","title":"Web"}]}' > "$w/.berth/config.json"
printf 'STRIPE_KEY=\nMAIL_KEY=\nPORT=3000\n' > "$w/.env.example"
repo acme/web "$w"
w=$(mktemp -d); echo "print('api')" > "$w/main.py"; repo acme/api "$w"
w=$(mktemp -d); echo secret > "$w/x"; repo acme/secret "$w"; touch "$R/acme/secret.git/berth-noaccess"

# A draft of the same team setup, in someone's own repository, on a branch,
# in a folder: what a direct link loads before the org publishes .berth.
mkdir -p "$R/draft"
d=$(mktemp -d); echo kits > "$d/README.md"; repo draft/kits "$d" "Kits"
mkdir -p "$d/team" && git clone -q "$R/acme/.berth.git" "$d/b" && cp -R "$d/b/team.json" "$d/b/box" "$d/b/kits" "$d/team/" && rm -rf "$d/b"
(cd "$d" && git checkout -q -b team-setup && git add -A && git commit -q -m "Acme team setup, a draft" && git push -q "$R/draft/kits.git" team-setup)
# Another branch whose project id has a dot, which can't be part of a URL.
(cd "$d" && git checkout -q -b dotted && sed -i 's/"id": "web", "repo"/"id": "web.app", "repo"/; s/"keys": { "web"/"keys": { "web.app"/' team/team.json && git commit -qam "web.app" && git push -q "$R/draft/kits.git" dotted)
git --git-dir "$R/acme/.berth.git" rev-parse main
