#!/usr/bin/env bash
# Cal.com team setup for Berth: box/init-cal.sh
#
# The main cal.com checkout's one-time setup. Berth runs it once in the
# fresh clone (projects[].init); by hand, run it from the checkout:
#
#   cd ~/code/cal.com && <this folder>/init-cal.sh
#
# It does what the cal-com kit expects of the main checkout and refuses to do
# itself (the kit sets up worktrees and leaves the main checkout alone):
#
#   1. Node: the version the checkout names, through fnm, and corepack
#   2. .env from .env.example (and .env.appStore, apps/api/v2/.env), with
#      DATABASE_URL and REDIS_URL at box/setup.sh's Docker Postgres and Redis,
#      and NEXTAUTH_SECRET and CALENDSO_ENCRYPTION_KEY made on this box
#   3. yarn install (which runs prisma generate), skipped when yarn.lock is
#      what it installed last time
#   4. prisma migrate deploy
#   5. the seed (seed-basic), once: skipped when the database has users
#
# Each new worktree then gets its own copy of this database, .env and the
# shared yarn cache from the kit's own setup. Safe to run again; no sudo.
#
# Settings: CAL_PG_PORT (5450), CAL_REDIS_PORT (6379), CAL_SKIP_SEED=1.
set -euo pipefail

PG_PORT="${CAL_PG_PORT:-5450}"
REDIS_PORT="${CAL_REDIS_PORT:-6379}"
DB_URL="postgresql://postgres:@localhost:$PG_PORT/calendso"
REDIS_URL="redis://localhost:$REDIS_PORT"
HERE="$(cd "$(dirname "$0")" && pwd)"

say()  { printf '\033[1m==> %s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
skip() { printf '    \033[2m%s (already done)\033[0m\n' "$*"; }
die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
timed() { local label="$1" t=$SECONDS; shift; "$@"; printf '\033[32m    ✓ %s\033[0m \033[2m(%ss)\033[0m\n' "$label" "$((SECONDS - t))"; }

[ "$(id -u)" -ne 0 ] || die "run this as yourself, not root"
cd "${BERTH_LOCATION_PATH:-$PWD}"
[ "$(python3 -c 'import json; print(json.load(open("package.json")).get("name",""))' 2>/dev/null)" = calcom-monorepo ] \
  || die "not a Cal.com checkout (package.json is not calcom-monorepo): $PWD"

# --- Node, the version this checkout names ----------------------------------

FNM_DIR="${FNM_DIR:-$HOME/.local/share/fnm}"
export FNM_DIR PATH="$FNM_DIR/aliases/default/bin:$FNM_DIR:$PATH" COREPACK_ENABLE_DOWNLOAD_PROMPT=0
node_setup() {
  local want from
  read -r want from <<<"$(CAL_NODE="${CAL_NODE:-}" "$HERE/setup.sh" node-version "$PWD")"
  if command -v fnm >/dev/null 2>&1; then
    eval "$(fnm env --shell bash)"
    if [ "$want" = lts-latest ]; then fnm install --lts >/dev/null; else fnm install "$want" >/dev/null 2>&1 || fnm install "$want"; fi
    fnm use "$want" >/dev/null
  fi
  command -v node >/dev/null || die "node is missing: run box/setup.sh node"
  case "$(node -v)" in
    "v$want".*) info "node $(node -v) (Cal.com asks for $want: $from)" ;;
    *) info "node $(node -v); Cal.com asks for $want ($from)" ;;
  esac
  command -v corepack >/dev/null || npm install -g --silent corepack
  corepack enable
  info "yarn $(yarn --version)"
}

# --- .env ---------------------------------------------------------------------

# set_env FILE KEY VALUE [only-if-one-of...] sets KEY in a dotenv file,
# keeping every other line. With values after VALUE, it only replaces a
# value that is empty or one of those (the example's default), so a value
# you changed is kept.
set_env() {
  python3 - "$@" <<'PY'
import re, sys
path, key, value, defaults = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
lines = open(path).read().splitlines()
pat = re.compile(r'^\s*(?:export\s+)?' + re.escape(key) + r'\s*=(.*)$')
for i, line in enumerate(lines):
    m = pat.match(line)
    if not m:
        continue
    cur = m.group(1).strip().strip('"').strip("'")
    if cur == value:
        sys.exit(0)
    if defaults and cur and cur not in defaults:
        print(f"    kept {key} as it is ({cur[:40]})")
        sys.exit(0)
    lines[i] = f'{key}="{value}"'
    break
else:
    lines.append(f'{key}="{value}"')
open(path, "w").write("\n".join(lines) + "\n")
print(f"    {key} -> {value if 'SECRET' not in key and 'KEY' not in key else '(made on this box)'}")
PY
}

env_files() {
  local example_db="postgresql://postgres:@localhost:5450/calendso"
  if [ ! -f .env ]; then
    cp .env.example .env
    chmod 600 .env
    info ".env from .env.example"
    set_env .env NEXTAUTH_SECRET "$(openssl rand -base64 32)"
    set_env .env CALENDSO_ENCRYPTION_KEY "$(openssl rand -base64 24)"
  else
    skip ".env is there; checking its database and Redis"
  fi
  set_env .env DATABASE_URL "$DB_URL" "$example_db"
  set_env .env DATABASE_DIRECT_URL "$DB_URL" "$example_db"
  set_env .env REDIS_URL "$REDIS_URL" "redis://localhost:6379"
  if [ -f .env.appStore.example ] && [ ! -f .env.appStore ]; then
    cp .env.appStore.example .env.appStore && info ".env.appStore from .env.appStore.example"
  fi
  # API v2 reads its own .env, and is what uses Redis.
  if [ -f apps/api/v2/.env.example ]; then
    if [ ! -f apps/api/v2/.env ]; then
      cp apps/api/v2/.env.example apps/api/v2/.env && chmod 600 apps/api/v2/.env
      info "apps/api/v2/.env from its .env.example"
    fi
    local k
    for k in DATABASE_URL DATABASE_DIRECT_URL DATABASE_READ_URL DATABASE_WRITE_URL; do
      grep -q "^$k=" apps/api/v2/.env && set_env apps/api/v2/.env "$k" "$DB_URL" "$example_db"
    done
    set_env apps/api/v2/.env REDIS_URL "$REDIS_URL" "redis://localhost:6379"
  fi
}

# The process gets the database from .env too, as the kit's worktrees do.
load_db_env() {
  DATABASE_URL="$(python3 -c 'import re,sys
for l in open(".env"):
    m=re.match(r"\s*DATABASE_URL\s*=\s*(.*)",l)
    if m: print(m.group(1).strip().strip("\"").strip("'"'"'")); break')"
  export DATABASE_URL DATABASE_DIRECT_URL="$DATABASE_URL"
}

# --- dependencies, database ----------------------------------------------------

STAMP="node_modules/.calcom-team-install"
sha() { if command -v sha256sum >/dev/null; then sha256sum; else shasum -a 256; fi | cut -d' ' -f1; }
lock_sum() { { cat yarn.lock; node -v; } | sha; }

install_deps() {
  if [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$(lock_sum)" ]; then
    skip "yarn install (yarn.lock unchanged)"
    return 0
  fi
  # postinstall runs turbo's post-install: prisma generate among them.
  yarn install --immutable
  lock_sum >"$STAMP"
}

prisma_generate() {
  # yarn's postinstall generates the client (cal.com's schema writes it to
  # packages/prisma/generated/prisma); again only when it is missing.
  if [ -f packages/prisma/generated/prisma/client.ts ] || [ -f node_modules/.prisma/client/index.js ]; then
    skip "prisma generate (the client is there)"
  else
    yarn prisma generate
  fi
}

wait_db() {
  local _
  for _ in $(seq 1 30); do
    if command -v pg_isready >/dev/null; then pg_isready -q -h localhost -p "$PG_PORT" && return 0
    else (exec 3<>"/dev/tcp/127.0.0.1/$PG_PORT") 2>/dev/null && return 0; fi
    sleep 1
  done
  die "Postgres does not answer on localhost:$PG_PORT: run box/setup.sh postgres"
}

migrate() { yarn prisma migrate deploy; }

# users in the database: the seed's first rows.
users() {
  psql "$DATABASE_URL" -tAc 'SELECT count(*) FROM "users"' 2>/dev/null \
    || docker exec cal-postgres psql -U postgres -d calendso -tAc 'SELECT count(*) FROM "users"' 2>/dev/null \
    || echo 0
}

# The workspace and script that seed development data, as the kit finds them.
seed_cmd() {
  python3 - <<'PY'
import json, os
for rel in ("packages/infra/adapters/database", "packages/prisma"):
    p = os.path.join(rel, "package.json")
    if os.path.exists(p):
        d = json.load(open(p))
        for s in ("seed-basic", "db-seed"):
            if s in d.get("scripts", {}):
                print(d["name"], s); raise SystemExit
PY
}

seed() {
  if [ -n "${CAL_SKIP_SEED:-}" ]; then skip "seed (CAL_SKIP_SEED)"; return 0; fi
  local n; n="$(users | tr -d '[:space:]')"
  if [ "${n:-0}" != 0 ]; then skip "seed ($n users in the database)"; return 0; fi
  local ws script
  read -r ws script <<<"$(seed_cmd)"
  [ -n "$ws" ] || die "no workspace has a seed-basic or db-seed script"
  yarn workspace "$ws" "$script"
  info "$(users | tr -d '[:space:]') users seeded"
}

say "Cal.com main checkout: $PWD"
timed node node_setup
timed .env env_files
load_db_env
timed "yarn install" install_deps
timed "prisma generate" prisma_generate
wait_db
timed "prisma migrate deploy" migrate
timed seed seed
say "cal.com is set up (${SECONDS}s). New worktrees copy this database through the cal-com kit."
