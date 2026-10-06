#!/usr/bin/env bash
# Cal.com team setup for Berth: box/setup.sh
#
# What a Cal.com engineer's box needs once, before any repository: system
# updates and packages, Docker, gh and the 1Password CLI, Node (fnm), Yarn
# (corepack), and Postgres and Redis in Docker. Berth runs it in a
# terminal on the box, as you, one step at a time; you can run it by hand
# the same way.
#
#   box/setup.sh all            every step in order, then gh auth login if
#                               this box is not signed in to GitHub yet
#   box/setup.sh <step>         one step: update packages docker cli node yarn postgres redis
#   box/setup.sh check <step>   exit 0 when the step has nothing left to do
#   box/setup.sh plan           the steps, one per line, "sudo" after those that need it
#   box/setup.sh github         gh auth login on this box (Berth does this step itself)
#   box/setup.sh node-version [DIR]
#                               the Node version Cal.com asks for, and where that came from
#
# Safe to run again: every step checks first and skips what is done.
# Never run it as root. Steps that need root call sudo, which asks for your
# password once, in this terminal; nothing here stores it.
#
# Supported: Ubuntu 22.04 and 24.04, Debian 12. macOS 14+ with Homebrew is
# best effort (Docker from OrbStack, Docker Desktop or Colima).
#
# The Postgres and Redis versions are set in one place, team.json's
# box.settings (CAL_POSTGRES_VERSION, CAL_REDIS_VERSION), which Berth passes
# as BERTH_SETTING_<NAME>; by hand they are read from team.json, and the
# environment can override them. Other settings (environment, all optional):
#   CAL_REPO=calcom/cal.com   CAL_DIR=~/code/cal.com   CAL_NODE=<version>
#   CAL_PG_PORT=5450          CAL_REDIS_PORT=6379      CAL_SKIP_GITHUB=1
#   CAL_UPDATE_EVERY_HOURS=24 (how long a system update counts as done)
set -euo pipefail

CAL_REPO="${CAL_REPO:-calcom/cal.com}"
CAL_DIR="${CAL_DIR:-$HOME/code/cal.com}"
PG_PORT="${CAL_PG_PORT:-5450}"       # what Cal.com's .env.example points at
REDIS_PORT="${CAL_REDIS_PORT:-6379}" # what apps/api/v2/.env.example points at
HERE="$(cd "$(dirname "$0")" && pwd)"
TEAM_JSON="${CAL_TEAM_JSON:-$HERE/../team.json}"
PG_NAME="cal-postgres"
REDIS_NAME="cal-redis"
NODE_FALLBACK="20"                   # only when nothing in Cal.com names a version
STATE="${XDG_STATE_HOME:-$HOME/.local/state}/calcom-team"
FNM_DIR="${FNM_DIR:-$HOME/.local/share/fnm}"
UPDATE_HOURS="${CAL_UPDATE_EVERY_HOURS:-24}"

# setting KEY: the environment's value, else what Berth passes from
# team.json's box.settings (BERTH_SETTING_KEY), else team.json itself, read
# with grep so it works before python3 or jq are installed.
setting() {
  local v="${!1:-}" from_berth="BERTH_SETTING_$1"
  [ -n "$v" ] || v="${!from_berth:-}"
  if [ -z "$v" ] && [ -r "$TEAM_JSON" ]; then
    v="$(grep -oE "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" "$TEAM_JSON" | head -1 | sed -E 's/.*:[[:space:]]*"([^"]*)"$/\1/')"
  fi
  [ -n "$v" ] || die "$1 is not set: team.json's box.settings has it (looked in $TEAM_JSON)"
  echo "$v"
}

STEPS=(update packages docker cli node yarn postgres redis)
SUDO_STEPS=" update packages docker cli "

say()  { printf '\033[1m==> %s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
skip() { printf '    \033[2m%s (already done)\033[0m\n' "$*"; }
warn() { printf '    \033[33mnote:\033[0m %s\n' "$*"; }
die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

[ "$(id -u)" -ne 0 ] || die "run this as yourself, not root: steps that need root ask for your password with sudo"

OS="$(uname -s)"
case "$OS" in
  Linux)
    [ -r /etc/os-release ] || die "unknown Linux: no /etc/os-release"
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${ID:-}" in
      ubuntu|debian) PLATFORM=debian ;;
      *) case " ${ID_LIKE:-} " in
           *" debian "*|*" ubuntu "*) PLATFORM=debian ;;
           *) die "this setup supports Ubuntu 22.04/24.04 and Debian 12 (found ${PRETTY_NAME:-$ID})" ;;
         esac ;;
    esac
    # Docker's and gh's apt repositories are per distribution; derivatives
    # (Mint, Pop!_OS) use their parent's.
    DISTRO="${ID}"; CODENAME="${VERSION_CODENAME:-}"
    case "$DISTRO" in ubuntu|debian) ;; *) DISTRO=ubuntu; CODENAME="${UBUNTU_CODENAME:-$CODENAME}" ;; esac
    ;;
  Darwin)
    PLATFORM=mac ;;
  *) die "unsupported OS: $OS" ;;
esac

# --- helpers -----------------------------------------------------------------

# need_sudo asks for the password once per terminal: sudo then remembers it
# for a few minutes (in this terminal only), and each later step finds it
# remembered. Only steps marked sudo call it.
need_sudo() {
  [ "$PLATFORM" = mac ] && return 0
  sudo -n true 2>/dev/null && return 0
  say "This step installs system software, which needs root."
  info "sudo will ask for $(id -un)'s password. You type it here, in this terminal;"
  info "this script and Berth never see or store it."
  sudo -v
}

apt_install() {
  local missing=() p
  for p in "$@"; do dpkg -s "$p" >/dev/null 2>&1 || missing+=("$p"); done
  if [ ${#missing[@]} -eq 0 ]; then skip "apt: $*"; return 0; fi
  need_sudo
  info "installing ${missing[*]}"
  sudo apt-get update -qq
  sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${missing[@]}" >/dev/null
}

brew_install() {
  local p
  have brew || die "install Homebrew first: https://brew.sh"
  for p in "$@"; do
    if brew list --formula "$p" >/dev/null 2>&1 || brew list --cask "$p" >/dev/null 2>&1; then
      skip "brew: $p"
    else
      brew install "$p"
    fi
  done
}

# add_apt_repo NAME KEY_URL "deb line" adds a vendor's apt repository once,
# its key in /etc/apt/keyrings.
add_apt_repo() {
  local name="$1" key="$2" line="$3"
  [ -f "/etc/apt/sources.list.d/$name.list" ] && [ -s "/etc/apt/keyrings/$name.gpg" ] && return 0
  need_sudo
  sudo install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "$key" | sudo gpg --dearmor --yes -o "/etc/apt/keyrings/$name.gpg"
  sudo chmod a+r "/etc/apt/keyrings/$name.gpg"
  echo "$line" | sudo tee "/etc/apt/sources.list.d/$name.list" >/dev/null
}

in_docker_group() { getent group docker 2>/dev/null | cut -d: -f4 | tr ',' '\n' | grep -qx "$(id -un)"; }

# dk runs docker as you. Right after the docker step adds you to the docker
# group, this login does not have the group yet: sg gives it to one command
# (no password, since /etc/group lists you), so no later step needs sudo.
dk() {
  if docker info >/dev/null 2>&1; then
    docker "$@"
  elif [ "$PLATFORM" = debian ] && in_docker_group; then
    sg docker -c "$(printf '%q ' docker "$@")"
  else
    return 1
  fi
}

# Berth runs setup and services through a login shell, so a tool only
# counts once a login shell finds it.
login_has() { "${SHELL:-/bin/bash}" -lc "command -v $1" >/dev/null 2>&1; }

container_running() { [ "$(dk inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = true ]; }
container_exists()  { dk inspect "$1" >/dev/null 2>&1; }

port_taken() {
  if have ss; then ss -ltnH "sport = :$1" 2>/dev/null | grep -q .
  elif have lsof; then lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null 2>&1
  else return 1; fi
}

# marked_block FILE ID TEXT keeps one block of lines in a dotfile, between
# markers, so running again never adds it twice.
marked_block() {
  local file="$1" id="$2" text="$3"
  touch "$file"
  grep -qsF "# >>> $id >>>" "$file" && return 0
  printf '\n# >>> %s >>>\n%s\n# <<< %s <<<\n' "$id" "$text" "$id" >>"$file"
  info "added $id to ${file/#$HOME/~}"
}

# --- the Node version Cal.com asks for --------------------------------------

# major_of turns ".nvmrc"-style values (v20.11.1, 20.x, >=20, lts/iron) into
# what fnm installs.
major_of() {
  local v="${1//[[:space:]]/}"
  v="${v#v}"
  case "$v" in
    lts/*|lts-*|"") echo "lts-latest"; return ;;
  esac
  v="$(printf '%s' "$v" | grep -oE '[0-9]+(\.[0-9]+){0,2}' | head -1)"
  [ -n "$v" ] && echo "${v%%.*}"
}

# read_version SOURCE reads a version from one checkout or from GitHub.
# SOURCE is a directory, or "github" for $CAL_REPO's default branch.
fetch() {
  local path="$1"
  if have gh && gh auth status --hostname github.com >/dev/null 2>&1; then
    gh api -H 'Accept: application/vnd.github.raw' "repos/$CAL_REPO/contents/$path" 2>/dev/null && return 0
  fi
  curl -fsSL --max-time 15 "https://raw.githubusercontent.com/$CAL_REPO/HEAD/$path" 2>/dev/null
}

node_version() {
  local dir="${1:-}" raw="" from="" f
  if [ -n "${CAL_NODE:-}" ]; then echo "$(major_of "$CAL_NODE") CAL_NODE"; return; fi
  if [ -n "$dir" ] && [ -f "$dir/package.json" ]; then
    for f in .nvmrc .node-version; do
      if [ -s "$dir/$f" ]; then raw="$(head -1 "$dir/$f")"; from="$f"; break; fi
    done
    if [ -z "$raw" ]; then
      raw="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("engines",{}).get("node",""))' "$dir/package.json" 2>/dev/null || true)"
      [ -n "$raw" ] && from="package.json engines.node"
    fi
    if [ -z "$raw" ] && [ -f "$dir/.github/actions/yarn-install/action.yml" ]; then
      raw="$(ci_node <"$dir/.github/actions/yarn-install/action.yml")"
      [ -n "$raw" ] && from="its CI (.github/actions/yarn-install)"
    fi
  else
    for f in .nvmrc .node-version; do
      if raw="$(fetch "$f" | head -1)" && [ -n "$raw" ]; then from="$CAL_REPO's $f"; break; fi
      raw=""
    done
    if [ -z "$raw" ]; then
      raw="$(fetch package.json | python3 -c 'import json,sys; print(json.load(sys.stdin).get("engines",{}).get("node",""))' 2>/dev/null || true)"
      [ -n "$raw" ] && from="$CAL_REPO's package.json engines.node"
    fi
    if [ -z "$raw" ]; then
      raw="$(fetch .github/actions/yarn-install/action.yml | ci_node)"
      [ -n "$raw" ] && from="$CAL_REPO's CI (.github/actions/yarn-install)"
    fi
  fi
  if [ -n "$raw" ] && [ -n "$(major_of "$raw")" ]; then
    echo "$(major_of "$raw") $from"
  else
    echo "$NODE_FALLBACK fallback (nothing in $CAL_REPO names a Node version)"
  fi
}

# ci_node reads the default node_version input of Cal.com's yarn-install
# action ("default: v20.x"), which is what its CI runs.
ci_node() { awk '/node_version:/ {f=1} f && /default:/ {gsub(/["'\'' ]/, "", $2); print $2; exit}'; }

# --- checks: exit 0 when the step has nothing left to do -------------------

check_update() {
  local stamp="$STATE/system-updated"
  [ -f "$stamp" ] || return 1
  [ -z "$(find "$stamp" -mmin +"$((UPDATE_HOURS * 60))" 2>/dev/null)" ]
}
check_packages() {
  local t
  for t in git curl jq python3 psql pg_dump openssl unzip; do have "$t" || return 1; done
  [ "$PLATFORM" = mac ] || have make || return 1
  pg_client_ok
}
# pg_dump can only dump a server of its own major or older: the cal-com
# kit's worktree copies need the client at least as new as the server.
pg_client_major() { pg_dump --version 2>/dev/null | grep -oE '[0-9]+' | head -1; }
pg_client_ok() { [ "$(pg_client_major)" -ge "$(setting CAL_POSTGRES_VERSION)" ] 2>/dev/null; }
check_docker() { have docker && dk info >/dev/null 2>&1; }
check_cli()    { have gh && have op; }
check_node()   { { [ -x "$FNM_DIR/fnm" ] || have fnm; } && login_has node; }
check_yarn()   { login_has yarn; }
pg_image()    { echo "postgres:$(setting CAL_POSTGRES_VERSION)"; }
redis_image() { echo "redis:$(setting CAL_REDIS_VERSION)"; }
image_of()    { dk inspect -f '{{.Config.Image}}' "$1" 2>/dev/null || true; }
check_postgres() { [ "$(image_of "$PG_NAME")" = "$(pg_image)" ] && container_running "$PG_NAME" && dk exec "$PG_NAME" pg_isready -q -U postgres >/dev/null 2>&1; }
check_redis()    { [ "$(image_of "$REDIS_NAME")" = "$(redis_image)" ] && container_running "$REDIS_NAME" && [ "$(dk exec "$REDIS_NAME" redis-cli ping 2>/dev/null)" = PONG ]; }

# --- steps -------------------------------------------------------------------

step_update() {
  say "System updates"
  if check_update; then skip "updated within the last ${UPDATE_HOURS}h"; return 0; fi
  if [ "$PLATFORM" = debian ]; then
    need_sudo
    info "apt-get update"
    sudo apt-get update -qq
    info "apt-get upgrade (this can take a few minutes on a new box)"
    sudo DEBIAN_FRONTEND=noninteractive apt-get upgrade -y -qq \
      -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold >/dev/null
    [ -f /var/run/reboot-required ] && warn "the box asks for a reboot to finish updating; setup can carry on first"
  else
    # best effort on macOS: Homebrew's own index only; macOS updates are yours to run.
    have brew || die "install Homebrew first: https://brew.sh"
    brew update
  fi
  mkdir -p "$STATE"
  touch "$STATE/system-updated"
}

step_packages() {
  say "System packages"
  local pg; pg="$(setting CAL_POSTGRES_VERSION)"
  if check_packages; then skip "git, curl, jq, python3, build tools, psql and pg_dump $(pg_client_major)"; return 0; fi
  if [ "$PLATFORM" = debian ]; then
    apt_install git curl ca-certificates gnupg jq unzip build-essential python3 openssl
    # psql and pg_dump of the server's version, from the PostgreSQL apt
    # repository (the distribution's own client is older).
    add_apt_repo pgdg https://www.postgresql.org/media/keys/ACCC4CF8.asc \
      "deb [signed-by=/etc/apt/keyrings/pgdg.gpg] https://apt.postgresql.org/pub/repos/apt $CODENAME-pgdg main"
    apt_install "postgresql-client-$pg"
  else
    xcode-select -p >/dev/null 2>&1 || die "install the Xcode command line tools first: xcode-select --install"
    brew_install git jq python@3 libpq openssl
    brew link --force libpq >/dev/null 2>&1 || true   # psql and pg_dump on the PATH
  fi
}

step_docker() {
  say "Docker"
  if check_docker; then skip "docker $(dk version --format '{{.Server.Version}}' 2>/dev/null)"; return 0; fi
  if [ "$PLATFORM" = debian ]; then
    if ! have docker; then
      # Docker's own apt repository, not the distribution's older docker.io.
      add_apt_repo docker "https://download.docker.com/linux/$DISTRO/gpg" \
        "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$DISTRO $CODENAME stable"
      apt_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    fi
    need_sudo
    # Started now and at every boot, so Postgres and Redis come back on their own.
    sudo systemctl enable --now docker.service containerd.service >/dev/null 2>&1 \
      || die "could not start Docker: see sudo journalctl -u docker"
    if ! in_docker_group; then
      sudo usermod -aG docker "$(id -un)"
      info "added $(id -un) to the docker group (new logins have it; this run uses sg)"
      info "Berth gives the group to what it starts from now on; a terminal you had open needs a new login"
    fi
  else
    # best effort on macOS: any running Docker passes; otherwise OrbStack if
    # installed, else Colima (no Docker Desktop licence needed).
    if have orb; then
      orb start >/dev/null 2>&1 || true
    else
      brew_install colima docker
      colima status >/dev/null 2>&1 || colima start --cpu 4 --memory 8
    fi
  fi
  check_docker || die "Docker is installed but does not answer: docker info"
}

step_cli() {
  say "GitHub CLI and 1Password CLI"
  if check_cli; then skip "gh $(gh --version | head -1 | awk '{print $3}'), op $(op --version 2>/dev/null)"; return 0; fi
  if [ "$PLATFORM" = debian ]; then
    local arch; arch="$(dpkg --print-architecture)"
    have gh || add_apt_repo githubcli https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      "deb [arch=$arch signed-by=/etc/apt/keyrings/githubcli.gpg] https://cli.github.com/packages stable main"
    have op || add_apt_repo 1password https://downloads.1password.com/linux/keys/1password.asc \
      "deb [arch=$arch signed-by=/etc/apt/keyrings/1password.gpg] https://downloads.1password.com/linux/debian/$arch stable main"
    apt_install gh 1password-cli
  else
    brew_install gh 1password-cli
  fi
  info "Berth signs op in to 1Password as a step of its own, after GitHub (nothing is read now)"
}

step_node() {
  local want from
  read -r want from <<<"$(node_version "$( [ -f "$CAL_DIR/package.json" ] && echo "$CAL_DIR")")"
  say "Node $want with fnm (from $from)"
  if check_node; then skip "node $("${SHELL:-/bin/bash}" -lc 'node -v' 2>/dev/null)"; return 0; fi
  if ! have fnm && [ ! -x "$FNM_DIR/fnm" ]; then
    if [ "$PLATFORM" = mac ]; then brew_install fnm; else
      curl -fsSL https://fnm.vercel.app/install | bash -s -- --skip-shell --install-dir "$FNM_DIR" >/dev/null
    fi
  fi
  export FNM_DIR PATH="$FNM_DIR:$PATH"
  # Login shells (Berth's setup and services): fnm's default Node on the
  # PATH, in plain sh so any login shell reads it. Interactive shells also
  # switch Node per repository with --use-on-cd.
  # shellcheck disable=SC2016
  local login='export FNM_DIR="$HOME/.local/share/fnm"
export PATH="$FNM_DIR/aliases/default/bin:$FNM_DIR:$PATH"
export COREPACK_ENABLE_DOWNLOAD_PROMPT=0'
  case "${SHELL:-/bin/bash}" in
    */zsh)
      marked_block "$HOME/.zprofile" "calcom team: node" "$login"
      # shellcheck disable=SC2016
      marked_block "$HOME/.zshrc" "calcom team: fnm" 'command -v fnm >/dev/null && eval "$(fnm env --use-on-cd --shell zsh)"' ;;
    *)
      # bash reads the first of these that exists as a login shell.
      local f="$HOME/.profile"
      [ -f "$HOME/.bash_login" ] && f="$HOME/.bash_login"
      [ -f "$HOME/.bash_profile" ] && f="$HOME/.bash_profile"
      marked_block "$f" "calcom team: node" "$login"
      # shellcheck disable=SC2016
      marked_block "$HOME/.bashrc" "calcom team: fnm" 'command -v fnm >/dev/null && eval "$(fnm env --use-on-cd --shell bash)"' ;;
  esac
  if [ "$want" = lts-latest ]; then fnm install --progress=never --lts; else fnm install --progress=never "$want"; fi
  fnm default "$want"
  login_has node || die "fnm installed Node, but a login shell does not find it"
}

step_yarn() {
  say "Yarn with corepack"
  if check_yarn; then skip "yarn (each repo's packageManager picks the version)"; return 0; fi
  login_has node || die "Node is missing: run the node step first"
  # Node 25 no longer ships corepack; npm installs it next to Node (fnm's
  # folder, no sudo).
  "${SHELL:-/bin/bash}" -lc 'command -v corepack >/dev/null || npm install -g --silent corepack; corepack enable'
  login_has yarn || die "corepack is enabled, but a login shell does not find yarn"
}

# start_or_run NAME ... starts a container that exists, else runs one.
start_or_run() {
  local name="$1"; shift
  if container_exists "$name"; then
    container_running "$name" || { info "starting $name"; dk start "$name" >/dev/null; }
    return 0
  fi
  local image; image="$(printf '%s\n' "$@" | grep -E '^[a-z0-9./-]+:[A-Za-z0-9._-]+$' | tail -1)"
  if [ -n "$image" ] && ! dk image inspect "$image" >/dev/null 2>&1; then
    info "pulling $image"
    dk pull -q "$image" >/dev/null
  fi
  dk run -d --name "$name" --restart unless-stopped "$@" >/dev/null
  info "started $name"
}

step_postgres() {
  local image major have_image
  image="$(pg_image)"; major="${image#postgres:}"; major="${major%%[.-]*}"
  say "Postgres $major in Docker on localhost:$PG_PORT"
  if check_postgres; then skip "$PG_NAME ($image)"; return 0; fi
  check_docker || die "Docker does not answer: run the docker step first"
  have_image="$(image_of "$PG_NAME")"
  if [ -n "$have_image" ] && [ "$have_image" != "$image" ]; then
    # A new major cannot read the old one's data; moving it is yours to do.
    warn "$PG_NAME runs $have_image, and team.json now asks for $image."
    warn "Its data needs moving to the new version (pg_dump, then restore); this step leaves it as it is."
    warn "To start afresh instead: docker rm -f $PG_NAME && docker volume rm cal-postgres-data, then run this step."
    container_running "$PG_NAME" || dk start "$PG_NAME" >/dev/null
    return 0
  fi
  if ! container_exists "$PG_NAME" && port_taken "$PG_PORT"; then
    die "port $PG_PORT is taken by something else; stop it, or set CAL_PG_PORT and point DATABASE_URL there"
  fi
  # Cal.com's .env.example: user postgres with no password, database
  # calendso, on 5450. Published on localhost only; data in a named volume,
  # mounted where the image keeps it (18 and later: /var/lib/postgresql).
  local data=/var/lib/postgresql
  [ "$major" -ge 18 ] 2>/dev/null || data=/var/lib/postgresql/data
  start_or_run "$PG_NAME" -p "127.0.0.1:$PG_PORT:5432" \
    -e POSTGRES_HOST_AUTH_METHOD=trust -e POSTGRES_DB=calendso \
    -v "cal-postgres-data:$data" "$image"
  local _
  for _ in $(seq 1 60); do check_postgres && break; sleep 1; done
  check_postgres || die "Postgres did not start: docker logs $PG_NAME"
}

step_redis() {
  local image have_image
  image="$(redis_image)"
  say "Redis ${image#redis:} in Docker on localhost:$REDIS_PORT"
  if check_redis; then skip "$REDIS_NAME ($image)"; return 0; fi
  check_docker || die "Docker does not answer: run the docker step first"
  have_image="$(image_of "$REDIS_NAME")"
  if [ -n "$have_image" ] && [ "$have_image" != "$image" ]; then
    # Redis reads an older version's data, which stays in the volume.
    info "$REDIS_NAME runs $have_image; moving it to $image"
    dk rm -f "$REDIS_NAME" >/dev/null
  fi
  if ! container_exists "$REDIS_NAME" && port_taken "$REDIS_PORT"; then
    die "port $REDIS_PORT is taken by something else; stop it, or set CAL_REDIS_PORT"
  fi
  start_or_run "$REDIS_NAME" -p "127.0.0.1:$REDIS_PORT:6379" \
    -v cal-redis-data:/data "$image" redis-server --appendonly yes
  local _
  for _ in $(seq 1 30); do check_redis && break; sleep 1; done
  check_redis || die "Redis did not start: docker logs $REDIS_NAME"
}

# github signs this box in to GitHub with its own gh, the way Berth's own
# last step does: a device code you enter on your laptop.
step_github() {
  say "GitHub on this box"
  have gh || die "gh is missing: run the cli step first"
  if gh auth status --hostname github.com >/dev/null 2>&1; then
    gh auth setup-git --hostname github.com >/dev/null 2>&1 || true
    skip "gh is signed in"; return 0
  fi
  gh auth login --hostname github.com --git-protocol https --web
  gh auth setup-git --hostname github.com
}

# --- commands ----------------------------------------------------------------

is_step() { case " ${STEPS[*]} " in *" $1 "*) return 0 ;; esac; return 1; }

run_step() {
  is_step "$1" || die "no step $1 (steps: ${STEPS[*]})"
  local started=$SECONDS
  "step_$1"
  printf '\033[32m    ✓ %s\033[0m \033[2m(%ss)\033[0m\n' "$1" "$((SECONDS - started))"
}

case "${1:-all}" in
  all)
    for s in "${STEPS[@]}"; do run_step "$s"; done
    if [ -z "${CAL_SKIP_GITHUB:-}" ] && [ -t 0 ]; then step_github; fi
    say "The box is ready for Cal.com (${SECONDS}s)."
    ;;
  check)
    [ -n "${2:-}" ] || die "check what? (${STEPS[*]})"
    is_step "$2" || die "no step $2 (steps: ${STEPS[*]})"
    "check_$2" ;;
  plan)
    for s in "${STEPS[@]}"; do case "$SUDO_STEPS" in *" $s "*) echo "$s sudo" ;; *) echo "$s" ;; esac; done ;;
  github) step_github ;;
  node-version) node_version "${2:-}" ;;
  -h|--help|help) sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//' ;;
  *) run_step "$1" ;;
esac
