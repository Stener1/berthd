#!/usr/bin/env bash
# calcom/.berth box/init-cal.sh (example)
#
# Runs once in the main cal.com checkout, right after Berth clones it, so the
# cal-com kit finds what it expects: a .env, dependencies, and a migrated,
# seeded local database on localhost:5450 (box/setup.sh postgres). No sudo.
# Safe to run again.
set -euo pipefail
cd "${BERTH_LOCATION_PATH:-$PWD}"

[ "$(jq -r .name package.json 2>/dev/null)" = calcom-monorepo ] || { echo "not a Cal.com checkout: $PWD" >&2; exit 1; }

if [ ! -f .env ]; then
  cp .env.example .env
  # Two secrets every checkout needs, made here and kept in .env on the box.
  secret() { openssl rand -base64 32 | tr -d '\n'; }
  sed -i.bak \
    -e "s|^NEXTAUTH_SECRET=.*|NEXTAUTH_SECRET=$(secret)|" \
    -e "s|^CALENDSO_ENCRYPTION_KEY=.*|CALENDSO_ENCRYPTION_KEY=$(openssl rand -hex 16)|" .env && rm -f .env.bak
fi
[ -f .env.appStore ] || cp .env.appStore.example .env.appStore

yarn install --immutable
yarn workspace @calcom/prisma db-deploy   # UNSURE: script name; `yarn prisma migrate deploy` otherwise
yarn workspace @calcom/prisma seed-basic || echo "seed-basic failed; worktrees will seed their own"
echo "cal.com is ready: the cal-com kit gives each new worktree its own copy of this database."
