#!/bin/sh
# Install berthd, Berth's box daemon, on this machine, start it as a service,
# and print a link to pair your laptop with it:
#
#   curl -fsSL https://berthd.app/install | sh
#   curl -fsSL https://berthd.app/install | sh -s -- --listen 0.0.0.0:7444
#
# Run it on the box, as the user your agents will run as. It needs no root:
# berthd goes in ~/.local/bin and runs as a systemd user service (Linux) or a
# launchd agent (macOS), set up by `berthd install`, the same step
# `berth add ssh` runs over SSH. That also installs hooks and skills for the
# agent CLIs it finds (Claude Code, Codex, Cursor Agent), so the app can show
# when an agent needs you. Running it again upgrades in place.
#
# Every download is checked against the release's checksums.txt before it
# runs. If the release (or the version asked for) doesn't exist, it says
# so, and prints how to install berthd over SSH from a source build instead.
# Source: https://github.com/sean-brydon/berthd/blob/main/site/install.sh
#
# Options (or the environment variable after each):
#   --version vX.Y.Z   a release instead of the latest      BERTH_VERSION
#   --listen ADDR      where berthd listens (default: this box's tailnet address)
#                                                           BERTHD_LISTEN
#   --no-pair          don't print a pairing link at the end BERTH_NO_PAIR=1
#   --no-integrations  don't install agent hooks and skills BERTH_NO_INTEGRATIONS=1
#   --yes, -y          ask nothing; see "Questions" below    BERTH_YES=1
#   --system           put berthd in /usr/local/bin (the default as root)
#   BERTH_BIN_DIR      put berthd somewhere else
#   BERTH_DOWNLOAD_BASE  fetch the archives from this URL instead of GitHub
#
# Questions: without a tailnet address it asks before listening on every
# interface; without tmux or git (a fresh Ubuntu has neither) it offers to
# install them with sudo; and on Linux it offers to turn on lingering so
# berthd keeps running after you log out. With --yes (or no terminal to ask
# on) it never listens on every interface unless --listen says so, installs
# tmux and git and turns lingering on only when sudo needs no password, and
# otherwise stops, saying the command to run.
set -eu

repo="sean-brydon/berthd"
version=${BERTH_VERSION:-latest}
listen=${BERTHD_LISTEN:-}
pair=1
[ -z "${BERTH_NO_PAIR:-}" ] || pair=0
integrations=1
[ -z "${BERTH_NO_INTEGRATIONS:-}" ] || integrations=0
yes=0
[ -z "${BERTH_YES:-}" ] || yes=1
system=0
bin_dir=${BERTH_BIN_DIR:-}

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	bold=$(printf '\033[1m') dim=$(printf '\033[2m') reset=$(printf '\033[0m')
else
	bold='' dim='' reset=''
fi
say() { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$bold" "$reset" "$*"; }
die() {
	printf 'berthd install: %s\n' "$*" >&2
	exit 1
}
usage() { sed -n '2,/^set -eu/p' "$0" 2>/dev/null | sed -e '$d' -e 's/^# \{0,1\}//' || true; }
need() { command -v "$1" >/dev/null 2>&1 || die "this needs $1; install it with your package manager and run this again"; }

while [ $# -gt 0 ]; do
	case $1 in
	--version)
		[ $# -ge 2 ] || die "--version needs a release, like --version v0.1.0"
		version=$2
		shift 2
		;;
	--version=*) version=${1#*=} && shift ;;
	--listen)
		[ $# -ge 2 ] || die "--listen needs an address, like --listen 0.0.0.0:7444"
		listen=$2
		shift 2
		;;
	--listen=*) listen=${1#*=} && shift ;;
	--no-pair) pair=0 && shift ;;
	--no-integrations) integrations=0 && shift ;;
	--yes | -y) yes=1 && shift ;;
	--system) system=1 && shift ;;
	-h | --help)
		usage
		say "Usage: curl -fsSL https://berthd.app/install | sh -s -- [--version vX.Y.Z] [--listen ADDR] [--no-pair] [--no-integrations] [--yes] [--system]"
		exit 0
		;;
	*) die "unknown option $1 (try --help)" ;;
	esac
done
case $version in
latest | v*) ;;
*) version="v$version" ;;
esac

# can_ask: a question needs a terminal, and --yes means none is asked. With
# `curl … | sh` the script is stdin, so answers come from /dev/tty.
can_ask() { [ "$yes" = 0 ] && (exec </dev/tty) 2>/dev/null; }
ask() { # ask QUESTION DEFAULT(y|n): true for yes
	printf '%s %s ' "$1" "$([ "$2" = y ] && echo '[Y/n]' || echo '[y/N]')" >/dev/tty
	read -r answer </dev/tty || answer=
	case ${answer:-$2} in [Yy]*) return 0 ;; *) return 1 ;; esac
}

need uname
need tar
need gzip
need mktemp
need sed
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --proto '=https,http' --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	die "this needs curl or wget to download berthd"
fi
if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	die "this needs sha256sum or shasum to check the download"
fi

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "berthd runs on Linux and macOS, not $(uname -s)" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "berthd is built for amd64 and arm64, not $(uname -m)" ;;
esac

user=$(id -un)
uid=$(id -u)
[ "$uid" != 0 ] || system=1
if [ -z "$bin_dir" ]; then
	if [ "$system" = 1 ]; then bin_dir=/usr/local/bin; else bin_dir="$HOME/.local/bin"; fi
fi
# Only --system as someone other than root needs sudo, and only to copy the
# binary; the service is always the user's own.
use_sudo=0
if ! mkdir -p "$bin_dir" 2>/dev/null || [ ! -w "$bin_dir" ]; then
	[ "$system" = 1 ] || die "cannot write to $bin_dir"
	command -v sudo >/dev/null 2>&1 || die "cannot write to $bin_dir, and there is no sudo; run as root or leave out --system"
	use_sudo=1
fi
as_owner() { if [ "$use_sudo" = 1 ]; then sudo "$@"; else "$@"; fi; }

asset="berthd-$os-$arch.tar.gz"
if [ -n "${BERTH_DOWNLOAD_BASE:-}" ]; then
	base=${BERTH_DOWNLOAD_BASE%/}
elif [ "$version" = latest ]; then
	base="https://github.com/$repo/releases/latest/download"
else
	base="https://github.com/$repo/releases/download/$version"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t berthd)
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT
trap 'cleanup; exit 130' INT
trap 'cleanup; exit 143' TERM HUP

# http_status URL: the HTTP status GitHub answers with, or 000 when there was
# no answer (offline, or no curl to ask with).
http_status() {
	command -v curl >/dev/null 2>&1 || {
		printf 000
		return
	}
	curl -s -o /dev/null -w '%{http_code}' --proto '=https' -H 'Accept: application/vnd.github+json' "$1" 2>/dev/null || true
}

# no_release: there's nothing to download (no release at all, or not this
# version), so say that plainly and how to get berthd on this box today.
no_release() {
	host=$(uname -n 2>/dev/null || echo my-box)
	{
		say ""
		if [ "$version" = latest ]; then
			say "${bold}There's no Berth release yet${reset}, so there's no berthd to download."
		else
			say "${bold}Berth $version isn't released${reset}, so there's no berthd to download."
			say "Releases: https://github.com/$repo/releases"
		fi
		say ""
		say "Instead, build Berth on your laptop (Go 1.27) and let it install berthd on"
		say "this box over SSH. It uploads berthd, starts it and pairs, in one step:"
		say ""
		say "  git clone https://github.com/$repo"
		say "  cd berthd && make all"
		if [ "$os" = darwin ]; then
			# make all builds the Linux daemons only.
			say "  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -o bin/berthd-darwin-$arch ./cmd/berthd"
		fi
		say "  bin/berth add ssh $user@$host"
		say ""
		say "Use however you SSH to this box in place of $user@$host. Step by step:"
		say "https://docs.berthd.app/getting-started/add-a-box"
	} >&2
	exit 1
}

step "Downloading berthd ($version, $os/$arch)"
if ! fetch "$base/$asset" "$tmp/$asset" 2>"$tmp/fetch.err"; then
	# A custom download base is the user's own; report what failed there.
	if [ -n "${BERTH_DOWNLOAD_BASE:-}" ]; then
		cat "$tmp/fetch.err" >&2
		die "could not download $base/$asset"
	fi
	if [ "$version" = latest ]; then
		release_api="https://api.github.com/repos/$repo/releases/latest"
	else
		release_api="https://api.github.com/repos/$repo/releases/tags/$version"
	fi
	case $(http_status "$release_api") in
	404) no_release ;;
	200) die "release $version has no berthd for $os/$arch ($asset). Releases: https://github.com/$repo/releases" ;;
	esac
	# GitHub's API didn't say (offline, or rate limited); a 404 for the
	# archive itself still means there's no such release.
	if grep -q 404 "$tmp/fetch.err" 2>/dev/null; then
		no_release
	fi
	cat "$tmp/fetch.err" >&2
	die "could not download $base/$asset. Check this machine can reach github.com, then run this again"
fi
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "could not download $base/checksums.txt"
want=$(sed -n "s/^\([0-9a-f]\{64\}\)  \*\{0,1\}$asset\$/\1/p" "$tmp/checksums.txt")
[ -n "$want" ] || die "checksums.txt has no entry for $asset"
got=$(sha256 "$tmp/$asset")
[ "$got" = "$want" ] || die "$asset does not match its checksum (got $got, want $want); nothing was installed"
mkdir "$tmp/x"
tar -xzf "$tmp/$asset" -C "$tmp/x" berthd || die "$asset has no berthd in it"
new="$tmp/x/berthd"
chmod 755 "$new"
new_version=$("$new" version 2>/dev/null) || die "the downloaded berthd does not run on this machine"
new_version=$(printf '%s\n' "$new_version" | cut -d' ' -f2)
say "  ${dim}verified against checksums.txt${reset}"

target="$bin_dir/berthd"
if [ -x "$target" ]; then
	if cmp -s "$new" "$target"; then
		say "  berthd $new_version is already in $bin_dir; setting its service up again"
	else
		old_version=$("$target" version 2>/dev/null | cut -d' ' -f2) || old_version=""
		say "  upgrading ${old_version:-an older build} → $new_version in place; agent sessions keep running"
	fi
fi
other=$(command -v berthd 2>/dev/null || true)
if [ -n "$other" ] && [ "$other" != "$target" ]; then
	say "  ${dim}note: another berthd is first on your PATH ($other); the service will run $target${reset}"
fi
if [ "$uid" = 0 ]; then
	say "  ${dim}note: installing as root, so agents on this box will run as root. Run this as your own user instead if you can.${reset}"
fi

# Check the service can be set up here, and where it would listen, before
# changing anything. `berthd install` makes the same choices.
step "Checking this machine"
run_install() { # run_install BERTHD [ARGS…]
	b=$1
	shift
	[ "$integrations" = 1 ] || set -- --no-integrations "$@"
	if [ -n "$listen" ]; then
		"$b" install --listen "$listen" "$@"
	else
		"$b" install --keep-listen "$@"
	fi
}
plan=$(run_install "$new" --dry-run) || die "berthd cannot run as a service here (above says why); nothing was installed"

# tmux and git: every terminal, agent and team setup step runs in tmux, and
# worktrees are git's, so a box without them can't start anything. berthd
# install gets them itself when that needs no password (root, sudo without
# one, Homebrew); here, where a person can type sudo's password, it asks.
tools=$(printf '%s\n' "$plan" | sed -n 's/^tools missing //p')
if [ -n "$tools" ]; then
	tools_cmd=$(printf '%s\n' "$plan" | sed -n 's/^tools install //p')
	tools_say=$(printf '%s' "$tools" | sed 's/ / and /')
	them=them
	case $tools in *" "*) ;; *) them=it ;; esac
	case $tools_cmd in
	"") die "Berth needs $tools_say on this box, and there is no package manager this knows: install $them, then run this again" ;;
	sudo\ *)
		if [ "$uid" = 0 ] || { command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; }; then
			say "  $tools_say: not installed; installing $them with the service"
		elif can_ask; then
			say ""
			say "Berth needs $tools_say on this box: every terminal and agent runs in tmux, and"
			say "worktrees are git's. Installing $them needs root, so sudo asks for your password."
			if ask "Install $them now? (runs: $tools_cmd)" y; then
				sh -c "$tools_cmd" </dev/tty || die "could not install $tools_say. Run this, then run the install again: $tools_cmd"
			else
				die "stopped before installing. Berth needs $tools_say; run this, then run the install again: $tools_cmd"
			fi
		else
			die "Berth needs $tools_say on this box, and installing $them needs sudo's password, which this can't ask for (--yes, or no terminal). Run this, then run the install again: $tools_cmd"
		fi
		;;
	*) say "  $tools_say: not installed; installing $them with the service ($tools_cmd)" ;;
	esac
fi
planned=$(printf '%s\n' "$plan" | sed -n 's/^listen //p')
case $planned in
none*)
	say ""
	say "This machine has no tailnet (Tailscale) address, so berthd will not pick where"
	say "to listen by itself. Your choices:"
	say "  - Install Tailscale (https://tailscale.com/download) and run this again. Private, and the default."
	say "  - Listen on every interface, port 7444. Only laptops you pair can connect (both"
	say "    keys are pinned), but anyone on the internet can reach the port."
	say "  - Choose an address yourself: --listen ADDR"
	if can_ask && ask "Listen on every interface (0.0.0.0:7444)?" n; then
		listen=0.0.0.0:7444
		planned=$listen
	else
		die "stopped before installing. To listen on every interface, run: curl -fsSL https://berthd.app/install | sh -s -- --listen 0.0.0.0:7444"
	fi
	;;
esac
say "  service: $(printf '%s\n' "$plan" | sed -n 's/^unit //p')"
say "  listens on: $planned"

# systemd stops a user's services when they log out, unless lingering is on.
if [ "$os" = linux ] && command -v loginctl >/dev/null 2>&1 &&
	[ "$(loginctl show-user "$user" -p Linger 2>/dev/null || true)" != "Linger=yes" ]; then
	if [ "$uid" = 0 ]; then
		loginctl enable-linger "$user" || true
	elif can_ask; then
		say ""
		say "systemd stops your services when you log out, unless lingering is on for $user."
		if ask "Turn it on now? (runs: sudo loginctl enable-linger $user)" y; then
			sudo loginctl enable-linger "$user" || say "  ${dim}could not turn lingering on; berthd stops when you log out until you do${reset}"
		fi
	elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
		sudo -n loginctl enable-linger "$user" || true
	fi
fi

step "Installing berthd $new_version to $target"
as_owner cp "$new" "$target.new"
as_owner chmod 755 "$target.new"
# Renamed into place, so a running berthd keeps its file until it restarts.
as_owner mv -f "$target.new" "$target"

step "Starting the service"
out=$(run_install "$target") || die "berthd is in $target, but its service did not start (above says why)"
printf '%s\n' "$out" | sed -e '/^Next: berthd pair$/d' -e 's/^/  /'

case ":$PATH:" in
*":$bin_dir:"*) ;;
*) say "  ${dim}note: $bin_dir is not on your PATH; add it to run berthd by name: export PATH=\"$bin_dir:\$PATH\"${reset}" ;;
esac

if [ "$pair" = 0 ]; then
	say ""
	say "Done. Pair a laptop with: $target pair"
	exit 0
fi

pairing=$("$target" pair) || die "berthd is running, but could not make a pairing link; try: $target pair"
link=$(printf '%s\n' "$pairing" | sed -n "s/.*\(berth:\/\/[^ '\"]*\).*/\1/p" | sed -n 1p)
if [ -z "$link" ]; then
	printf '%s\n' "$pairing"
	exit 0
fi
dial=$(printf '%s\n' "$pairing" | sed -n 's/^Laptops will dial \([^;]*\);.*/\1/p')
say ""
say "${bold}berthd $new_version is running.${reset} Paste this link into Berth on your laptop"
say "(Add a box). It works once, for ten minutes:"
say ""
say "  ${bold}$link${reset}"
say ""
say "Or on your laptop:  berth pair '$link'"
say ""
say "${dim}Your laptop will dial ${dial:-the address in the link}. A new link any time: berthd pair${reset}"
