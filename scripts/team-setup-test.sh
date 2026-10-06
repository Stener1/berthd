#!/usr/bin/env bash
# Team setup, end to end, in Docker: a fresh Ubuntu 24.04 box (berthd as its
# systemd user service, sudo asking dev for a password) and a laptop, with a
# synthetic GitHub: a fake gh on both answers from bare repositories on the
# laptop, which git daemon serves to the box, so nothing reaches GitHub.
#
#   scripts/team-setup-test.sh [--out DIR] [--keep] [--version vX.Y.Z]
#
# Steps: install.sh on a box without tmux (it stops, saying the command,
# when sudo needs a password; installs it when sudo needs none); the
# Cal.com example's box/setup.sh plan and check subcommands (and that it
# refuses root); the laptop reads acme/.berth with its gh (access check: 2 of
# 3 repos); op, not signed in, fails at once and never asks; setup on the
# box runs the steps in a terminal there, where the password is typed (Berth
# never sees it), with box.settings in the script's environment; the tools
# step adds dev to a group and the steps after it have it (same terminal,
# then through sg in terminals berthd starts); a failed step, then Retry from
# it; berthd restarting mid-step and resuming; the box's own gh sign-in by
# device code; Berth's 1Password step, op's question answered in the
# terminal; the repos cloned, the web repo's own config trusted at the
# reviewed hash, the api repo's team kit (its requires found on a login
# shell's PATH) and init (with BERTH_KIT_DIR); keys on the box; a second run
# skipping what is done; a newer .berth commit offered as an update with a
# diff, not run; a project id with a dot refused; terminals reading op://
# keys with op's session, and without it saying how to sign in. A fake gh
# and a fake op only. Needs Docker and Go.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
# shellcheck source=release-test/common.sh
source "$here/release-test/common.sh"
# shellcheck source=release-test/linux/lib.sh
source "$here/release-test/linux/lib.sh"

VERSION=dev OUT="" KEEP=0
while [ $# -gt 0 ]; do
	case $1 in
	--version) VERSION=$2 && shift 2 ;;
	--out) OUT=$2 && shift 2 ;;
	--keep) KEEP=1 && shift ;;
	-h | --help) sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//' && exit 0 ;;
	*) echo "unknown option $1 (try --help)" >&2 && exit 2 ;;
	esac
done
export KEEP
OUT=${OUT:-$REPO/dist/release-test/team-setup-$(date +%Y%m%d-%H%M%S)}
rt_init "Berth team setup test (Linux, Docker)" "$OUT"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/berth-team-test.XXXXXX")
trap 'linux_cleanup >>"$RT_LOG" 2>&1; rm -rf "$WORK"' EXIT
trap 'exit 130' INT TERM

BOX="" COMMIT="" PASSWORD=correct-horse-battery

s_build() {
	docker info >/dev/null 2>&1 || fail "Docker is not running" || return 1
	build_dist "$WORK/dist" "$VERSION" || fail "could not build berthd and berth for linux/$(docker_arch)" || return 1
	build_image || fail "could not build the Ubuntu 24.04 image" || return 1
}

s_machines() {
	start_machines "berth-team-$$" "$WORK/dist" || return 1
	detail "box $BOXC and laptop $LAPC on $NET"
}

# The box asks dev for a password with sudo, as a real one does; both get
# the fake gh; the box clones github.com through the laptop's git daemon.
s_github() {
	docker exec "$BOXC" sh -c "echo 'dev:$PASSWORD' | chpasswd && echo 'dev ALL=(ALL) ALL' >/etc/sudoers.d/dev" || return 1
	for c in "$BOXC" "$LAPC"; do
		docker cp "$REPO/internal/team/testdata/fake-gh" "$c:/usr/local/bin/gh" || return 1
		docker exec "$c" chmod 755 /usr/local/bin/gh || return 1
	done
	docker cp "$here/release-test/team/fake-op" "$BOXC:/usr/local/bin/op" || return 1
	docker exec "$BOXC" chmod 755 /usr/local/bin/op || return 1
	docker cp "$here/release-test/team/make-github.sh" "$LAPC:/tmp/make-github.sh" || return 1
	COMMIT=$(lp "sh /tmp/make-github.sh" | tail -1) || fail "could not make the synthetic GitHub" || return 1
	docker exec -d -u dev "$LAPC" git daemon --reuseaddr --base-path=/home/dev/.fake-gh --export-all /home/dev/.fake-gh || return 1
	bx "git config --global url.git://$LAPC/.insteadOf https://github.com/" || return 1
	until_ok 20 bx "git ls-remote https://github.com/acme/web.git" || fail "the box can't reach the laptop's git daemon" || return 1
	bx "gh auth status" 2>/dev/null && fail "the box's gh is signed in already" && return 1
	detail "acme/.berth at ${COMMIT:0:7}; the box clones https://github.com/acme/* from git://$LAPC/; dev's sudo asks for a password"
}

s_install() {
	# A fresh Ubuntu 24.04 has no tmux. Where sudo asks for a password and
	# nobody can type it (--yes), install.sh stops before installing
	# anything, with the command to run.
	docker exec "$BOXC" dpkg -r tmux >/dev/null || return 1
	bx "command -v tmux" >/dev/null && fail "tmux is still installed" && return 1
	local out
	out=$(box_install 2>&1) && fail "install.sh went ahead without tmux: $out" && return 1
	echo "$out" | tail -3
	echo "$out" | grep -q "Berth needs tmux on this box, and installing it needs sudo's password" || fail "install.sh said: $out" || return 1
	echo "$out" | grep -q "sudo apt-get update -q && sudo apt-get install -y -q tmux" || fail "install.sh named no command: $out" || return 1
	bx "test ! -e ~/.local/bin/berthd" || fail "berthd was installed all the same" || return 1
	# Where sudo needs no password, berthd install gets tmux itself.
	docker exec "$BOXC" sh -c "echo 'dev ALL=(ALL) NOPASSWD:ALL' >/etc/sudoers.d/dev" || return 1
	box_install || fail "install.sh failed" || return 1
	docker exec "$BOXC" sh -c "echo 'dev ALL=(ALL) ALL' >/etc/sudoers.d/dev" || return 1
	bx "command -v tmux" || fail "install.sh did not install tmux" || return 1
	laptop_cli || fail "could not install berth on the laptop" || return 1
	lp "berth pair '$PAIR_LINK' --json" || fail "berth pair failed" || return 1
	BOX=$(lp "berth boxes --json" | jq_py '[b["name"] for b in j][0]') || fail "no box after pairing" || return 1
	until_ok 30 lp "berth ping $BOX" || fail "$BOX does not answer" || return 1
	detail "without tmux, install.sh stopped with the command (sudo asks for a password); with sudo that needs none it installed tmux; paired with $BOX"
}

# The Cal.com example's read-only subcommands, on a fresh box.
s_calcom_script() {
	docker cp "$REPO/examples/team/calcom-dot-berth" "$BOXC:/tmp/cal" || return 1
	docker exec "$BOXC" chmod -R a+rX /tmp/cal
	local plan
	plan=$(bx "/tmp/cal/box/setup.sh plan") || fail "plan failed" || return 1
	echo "$plan"
	[ "$(echo "$plan" | wc -l | tr -d ' ')" = 8 ] || fail "plan lists $(echo "$plan" | wc -l | tr -d ' ') steps, not 8" || return 1
	[ "$(echo "$plan" | grep -c ' sudo$')" = 4 ] || fail "plan marks $(echo "$plan" | grep -c ' sudo$') steps sudo, not 4" || return 1
	local s done=() todo=()
	# Without /usr/local/bin, where this test's fake gh and op are.
	for s in update packages docker cli node yarn postgres redis; do
		if bx "PATH=/usr/bin:/bin /tmp/cal/box/setup.sh check $s" >/dev/null 2>&1; then done+=("$s"); else todo+=("$s"); fi
	done
	[ ${#done[@]} = 0 ] || fail "a fresh box passes check ${done[*]}" || return 1
	local out
	out=$(docker exec "$BOXC" /tmp/cal/box/setup.sh plan 2>&1) && fail "it ran as root: $out" && return 1
	echo "$out" | grep -q "not root" || fail "as root it said: $out" || return 1
	out=$(bx "/tmp/cal/box/setup.sh nope" 2>&1) && fail "an unknown step passed" && return 1
	echo "$out" | grep -q "no step nope" || fail "an unknown step said: $out" || return 1
	detail "plan: 8 steps, 4 with sudo; check fails for all 8 on a fresh box (${todo[*]}); refuses root; rejects unknown steps"
}

# op is on the box but not signed in: a read fails at once, saying how to
# sign in, and op never gets a terminal to ask on.
s_op_signed_out() {
	local out t0 secs
	t0=$(date +%s)
	out=$(lp "berth secret test $BOX op://Dev/Stripe/key" 2>&1) && fail "it resolved: $out" && return 1
	secs=$(($(date +%s) - t0))
	echo "$out"
	echo "$out" | grep -q "1Password isn't signed in on" || fail "it said: $out" || return 1
	echo "$out" | grep -q "berthd secret signin" || fail "it doesn't say how to sign in: $out" || return 1
	[ "$secs" -lt 15 ] || fail "op took ${secs}s" || return 1
	bx "test ! -e ~/.fake-op/asked" || fail "op asked at a terminal" || return 1
	detail "not signed in, a read failed in ${secs}s: $(echo "$out" | grep -o "1Password isn't signed in.*" | head -1)"
}

team_json() { laptop_api GET "/v1/boxes/$BOX/api/team/acme"; }
team_is() { team_json | jq_py "$1"; }
send() { laptop_api POST "/v1/boxes/$BOX/api/sessions/team-acme/send" "{\"text\":\"$1\",\"enter\":true}" >/dev/null; }
screen() { laptop_api GET "/v1/boxes/$BOX/api/sessions/team-acme/screen" | jq_py 'j["screen"]'; }

s_show() {
	local v
	v=$(laptop_api GET "/v1/team/acme?box=$BOX") || return 1
	echo "$v" | head -c 3000
	echo "$v" | jq_py 'j["state"] == "found" and j["commit"]["sha"] == "'"$COMMIT"'"' >/dev/null || fail "the laptop did not read acme/.berth at $COMMIT" || return 1
	echo "$v" | jq_py 'j["access"]["readable"] == 2 and j["access"]["missing"] == ["acme/secret"]' >/dev/null || fail "access check: $(echo "$v" | jq_py 'j["access"]')" || return 1
	echo "$v" | jq_py '[s["id"] for s in j["steps"]] == ["tools", "db", "slow", "github", "1password"]' >/dev/null || fail "steps: $(echo "$v" | jq_py '[s["id"] for s in j["steps"]]')" || return 1
	echo "$v" | jq_py '[p["source"] for p in j["projects"]] == ["repo", "kit", "none"]' >/dev/null || fail "sources" || return 1
	detail "acme/.berth at ${COMMIT:0:7}, read with the laptop's gh: 2 of 3 repos readable (acme/secret is not), 5 steps with the box's GitHub and 1Password sign-ins"
}

s_setup() {
	bx "touch /tmp/fail-db"
	local res
	res=$(laptop_api POST "/v1/team/acme/setup" "{\"box\":\"$BOX\",\"commit\":\"${COMMIT:0:7}\",\"keys\":{\"web\":{\"MAIL_KEY\":\"SG.e2e\"}}}") || return 1
	echo "$res"
	echo "$res" | jq_py 'j["phase"] == "steps" and j["session"] == "team-acme"' >/dev/null || fail "setup said: $res" || return 1
	detail "started: the steps run in the box's terminal team-acme"
}

s_sudo() {
	until_ok 60 team_is 'j["steps"][0]["state"] == "waiting"' || fail "tools never waited for the password: $(team_json)" || return 1
	screen | tail -3
	screen | grep -q "\[sudo\] password for dev" || fail "no sudo prompt in the terminal" || return 1
	send "$PASSWORD" || return 1
	until_ok 30 team_is 'j["steps"][0]["state"] == "done"' || fail "tools did not finish: $(team_json)" || return 1
	bx "test -d /opt/acme && stat -c %U /opt/acme" | grep -qx dev || fail "/opt/acme was not made" || return 1
	detail "sudo asked in the box's terminal; the password was typed there; tools made /opt/acme and added dev to acmedocker"
}

s_fail() {
	until_ok 30 team_is 'j["phase"] == "failed"' || fail "db did not fail: $(team_json)" || return 1
	team_is 'j["steps"][1]["state"] == "failed" and j["steps"][0]["state"] == "done" and j["steps"][2]["state"] == "todo"' >/dev/null || fail "$(team_json)" || return 1
	screen | grep -q "port 5450 is taken" || fail "the terminal does not show why: $(screen | tail -5)" || return 1
	# db checks its terminal has the group tools added (else it exits 4).
	screen | grep -q "You are in acmedocker now" || fail "the steps left did not get the new group" || return 1
	team_is 'j["steps"][1]["error"] == "exited with 3"' >/dev/null || fail "db: $(team_json)" || return 1
	detail "the steps after tools ran with its new group (sg, in the same terminal); db failed with its output in the terminal; tools stays done"
}

s_retry_restart() {
	bx "rm -f /tmp/fail-db"
	laptop_api POST "/v1/team/acme/retry" "{\"box\":\"$BOX\",\"from\":\"db\"}" || return 1
	until_ok 30 team_is 'j["steps"][2]["state"] == "running"' || fail "slow never started: $(team_json)" || return 1
	team_is 'j["steps"][1]["state"] == "done"' >/dev/null || fail "db: $(team_json)" || return 1
	# berthd restarts while a step runs: tmux keeps the terminal, and the
	# step finishes while berthd is away.
	bx "systemctl --user restart berthd" || return 1
	bx "touch /tmp/go"
	until_ok 30 lp "berth ping $BOX" || fail "$BOX did not come back" || return 1
	until_ok 60 team_is 'j["steps"][2]["state"] == "done"' || fail "slow was not resumed: $(team_json)" || return 1
	# berthd itself still lacks the group (a systemd user unit gets the
	# user manager's groups): the terminal it started for Retry had it.
	bx 'grep ^Groups: /proc/$(systemctl --user show -p MainPID --value berthd)/status' | grep -qw "$(bx 'getent group acmedocker | cut -d: -f3')" && fail "berthd has acmedocker: the test proves nothing" && return 1
	bx "berthd doctor" | grep -q "acmedocker" || fail "berthd doctor does not mention acmedocker" || return 1
	detail "Retry from db ran db (not tools), in a terminal berthd started with acmedocker through sg although berthd lacks it (doctor says so); berthd restarted mid-step and picked the step up when it finished"
}

s_box_github() {
	until_ok 60 team_is 'j["steps"][3]["code"] == "4F2A-9C1E"' || fail "no device code: $(team_json)" || return 1
	team_is 'j["steps"][3]["state"] == "waiting" and j["steps"][3]["url"] == "https://github.com/login/device"' >/dev/null || fail "$(team_json)" || return 1
	send "" || return 1
	until_ok 60 team_is 'j["steps"][3]["state"] == "done"' || fail "GitHub did not finish: $(team_json)" || return 1
	bx "gh auth status" || fail "the box's gh is not signed in" || return 1
	detail "the box signed in with its own gh (device code 4F2A-9C1E shown to the laptop); the laptop's credential never left it"
}

# Berth's own 1Password step: op's question waits in the box's terminal.
s_op_signin() {
	until_ok 60 team_is 'j["steps"][4]["id"] == "1password" and j["steps"][4]["state"] == "waiting"' || fail "1Password never waited: $(team_json); $(screen | tail -5)" || return 1
	screen | tail -2
	screen | grep -q "Enter the password for dev@acme.test" || fail "no op question in the terminal" || return 1
	send "op-pass" || return 1
	until_ok 120 team_is 'j["phase"] in ("done", "failed")' || fail "the setup did not finish: $(team_json)" || return 1
	team_is 'j["phase"] == "done" and j["steps"][4]["state"] == "done"' >/dev/null || fail "it failed: $(team_json)" || return 1
	bx 'test "$(stat -c %a ~/.config/berth/box/op-session)" = 600' || fail "op's session is not kept, or not private" || return 1
	lp "berth secret test $BOX op://Dev/Stripe/key" | grep -q "Resolved op://Dev/Stripe/key: 17 characters" || fail "berthd can't read 1Password after signing in" || return 1
	detail "1Password on the box waited at op's question in the terminal; answered there, berthd reads op://Dev/Stripe/key with op's session (kept 0600)"
}

s_projects() {
	local locs cfg
	locs=$(laptop_api GET "/v1/boxes/$BOX/api/locations") || return 1
	echo "$locs" | head -c 2000
	echo "$locs" | jq_py 'sorted(l["name"] for l in j) == ["api", "web"]' >/dev/null || fail "locations: $locs" || return 1
	echo "$locs" | jq_py '[l["repo_trust"] for l in j if l["name"] == "web"] == ["trusted"]' >/dev/null || fail "web's own config is not trusted" || return 1
	cfg=$(laptop_api GET "/v1/boxes/$BOX/api/locations/api/config") || return 1
	echo "$cfg" | jq_py 'j["kit"]["id"] == "acme-api"' >/dev/null || fail "api has no kit: $cfg" || return 1
	bx "cat ~/.acme-api-init" | grep -q "/home/dev/code/api" || fail "api's init did not run in its clone" || return 1
	local init
	init=$(bx "cat ~/.acme-api-init")
	echo "$init"
	echo "$init" | grep -q "^kit lib: from the kit$" || fail "init could not reuse the kit's code through BERTH_KIT_DIR" || return 1
	echo "$init" | grep -q "^BERTH_LOCATION=api$" || fail "init had no BERTH_LOCATION" || return 1
	echo "$init" | grep -q "^BERTH_SETTING_ACME_PG=16$" || fail "init had no settings" || return 1
	echo "$init" | grep -q "acmedocker" || fail "init had not the new group" || return 1
	bx "cat ~/.acme-db" | grep -qx "pg=16" || fail "the script had no BERTH_SETTING_ACME_PG: $(bx 'cat ~/.acme-db')" || return 1
	# acmetool is on a login shell's PATH only, as fnm's node is.
	bx 'tr "\0" "\n" </proc/$(systemctl --user show -p MainPID --value berthd)/environ | grep ^PATH=' | grep -q acme/bin && fail "berthd's PATH has acmetool: the test proves nothing" && return 1
	team_is '[p for p in j["projects"] if p["id"] == "api"][0]["state"] == "ready" and not any("acmetool" in w for w in ([p for p in j["projects"] if p["id"] == "api"][0].get("warnings") or []))' >/dev/null || fail "api warns about acmetool: $(team_json)" || return 1
	cfg=$(laptop_api GET "/v1/boxes/$BOX/api/locations/web/config") || return 1
	echo "$cfg" | jq_py 'j["local"]["env"]["MAIL_KEY"] == "SG.e2e" and j["local"]["env"]["STRIPE_KEY"] == "op://Dev/Stripe/key"' >/dev/null || fail "web's keys: $cfg" || return 1
	bx "test ! -e ~/code/secret" || fail "acme/secret was cloned" || return 1
	# The password went into sudo, in the terminal, and nowhere else.
	bx "grep -rqs '$PASSWORD' ~/.berth ~/.config ~/code /tmp 2>/dev/null" && fail "the password is on disk" && return 1
	screen | grep -q "$PASSWORD" && fail "the password shows in the terminal" && return 1
	detail "web and api cloned to ~/code; web's .berth/config.json trusted at the reviewed hash; api got the team kit (acmetool, on a login shell's PATH only, not warned about) and its init, with BERTH_KIT_DIR, the settings and the new group; the script had BERTH_SETTING_ACME_PG=16; MAIL_KEY and the STRIPE_KEY reference in web's own config; the password is nowhere"
}

s_rerun() {
	laptop_api POST "/v1/team/acme/setup" "{\"box\":\"$BOX\",\"commit\":\"${COMMIT:0:7}\"}" >/dev/null || return 1
	until_ok 60 team_is 'j["phase"] == "done"' || fail "the second run did not finish: $(team_json)" || return 1
	team_is 'all(s["state"] == "skipped" for s in j["steps"])' >/dev/null || fail "steps ran again: $(team_json)" || return 1
	detail "a second run skips every step (their checks pass) and finds the clones"
}

s_update() {
	lp "sh /tmp/make-github.sh update" || return 1
	local u
	u=$(laptop_api GET "/v1/team/acme/update") || return 1
	echo "$u"
	echo "$u" | jq_py 'j["update"]["sudo"] == ["Node 22"] and any(c["id"] == "node" and c["kind"] == "add" for c in j["update"]["changes"])' >/dev/null || fail "update: $u" || return 1
	team_is 'j["commit"] == "'"$COMMIT"'"' >/dev/null || fail "the update ran by itself" || return 1
	detail "a newer acme/.berth shows as an update: + Node 22, which asks for the password; the box stays at ${COMMIT:0:7}"
}

s_cli() {
	local out
	out=$(lp "berth team show acme") || fail "berth team show failed" || return 1
	echo "$out"
	echo "$out" | grep -q "Acme team setup" || fail "show: $out" || return 1
	echo "$out" | grep -q "Repos (2 of 3 you can read)" || fail "show: no access line" || return 1
	echo "$out" | grep -q "Update: ${COMMIT:0:7} →" || fail "show: no update" || return 1
	out=$(lp "berth team status") || fail "berth team status failed" || return 1
	echo "$out"
	echo "$out" | grep -q "Acme (acme) *$BOX *${COMMIT:0:7} *set up *5/5 steps, 2/2 repos" || fail "status: $out" || return 1
	detail "berth team show lists the plan, access and the update; berth team status: set up, 5/5 steps, 2/2 repos"
}

s_link() {
	local link=github.com/draft/kits/tree/team-setup/team out
	out=$(lp "berth team show $link") || fail "berth team show $link failed" || return 1
	echo "$out"
	echo "$out" | grep -q "From draft/kits · team-setup branch · team/ on GitHub" || fail "the card does not name the repo it came from" || return 1
	echo "$out" | grep -q "Published by" && fail "a link's setup claims the org published it" && return 1
	out=$(lp "berth team setup $link $BOX --yes" 2>&1) || fail "berth team setup $link failed: $out" || return 1
	echo "$out"
	echo "$out" | grep -q "is set up for Acme" || fail "setup from the link did not finish" || return 1
	team_is 'j["commit"] == "'"$(lp "git --git-dir ~/.fake-gh/draft/kits.git rev-parse team-setup")"'"' >/dev/null || fail "the box is not at the branch's commit: $(team_json)" || return 1
	detail "berth team show/setup with a link to a branch and folder of another repo: the card says where it came from, and the box ran it at that branch's commit"
}

# A project id with a dot can't be part of a worktree's URL: refused, with
# the fix.
s_ids() {
	local out
	out=$(lp "berth team show github.com/draft/kits/tree/dotted/team" 2>&1) && fail "a dotted project id was accepted: $out" && return 1
	echo "$out"
	echo "$out" | grep -q 'id "web.app" can.t be part of a URL' || fail "it said: $out" || return 1
	echo "$out" | grep -q '"web-app"' || fail "no suggestion: $out" || return 1
	detail "$(echo "$out" | grep -o 'id "web.app".*' | cut -c1-120)…"
}

sess_shows() { laptop_api GET "/v1/boxes/$BOX/api/sessions/$1/screen" | grep -q "$2"; }

# Terminals in a worktree read its op:// keys with op's session; without one
# they start without the key, say how to sign in, and op never asks.
s_op_terminals() {
	local out
	laptop_api POST "/v1/boxes/$BOX/api/sessions" '{"location":"web","name":"op-in","command":"echo STRIPE=${STRIPE_KEY:-unset}; id -nG; sleep 600"}' >/dev/null || return 1
	until_ok 20 sess_shows op-in "STRIPE=" || fail "op-in printed nothing" || return 1
	out=$(laptop_api GET "/v1/boxes/$BOX/api/sessions/op-in/screen" | jq_py 'j["screen"]')
	echo "$out" | grep -q "STRIPE=sk_test_synthetic" || fail "op-in: $out" || return 1
	echo "$out" | grep -q "acmedocker" || fail "op-in has not the new group: $out" || return 1
	bx "rm ~/.config/berth/box/op-session" || return 1
	laptop_api POST "/v1/boxes/$BOX/api/sessions" '{"location":"web","name":"op-out","command":"echo STRIPE=${STRIPE_KEY:-unset}; sleep 600"}' >/dev/null || return 1
	until_ok 30 sess_shows op-out "STRIPE=" || fail "op-out never started: $(laptop_api GET "/v1/boxes/$BOX/api/sessions/op-out/screen")" || return 1
	out=$(laptop_api GET "/v1/boxes/$BOX/api/sessions/op-out/screen" | jq_py 'j["screen"]')
	echo "$out"
	echo "$out" | grep -q "STRIPE=unset" || fail "op-out: $out" || return 1
	echo "$out" | grep -q "1Password isn't signed in on" || fail "op-out does not say why: $out" || return 1
	echo "$out" | grep -qi "\[Y/n\]" && fail "op asked in the terminal" && return 1
	bx "test ! -e ~/.fake-op/asked" || fail "op asked at a terminal" || return 1
	detail "a terminal in web got STRIPE_KEY from 1Password (and the new group); signed out, the next started without it, saying 1Password isn't signed in, and op never asked"
}

RT_CRITICAL=1 step "Build berthd, berth and the Ubuntu image" s_build
RT_CRITICAL=1 step "Start a box and a laptop" s_machines
RT_CRITICAL=1 step "A synthetic GitHub, sudo with a password" s_github
RT_CRITICAL=1 step "Install berthd and pair" s_install
step "Cal.com's box/setup.sh: plan and check" s_calcom_script
RT_CRITICAL=1 step "The laptop reads acme/.berth with gh" s_show
step "op, signed out, fails at once and never asks" s_op_signed_out
RT_CRITICAL=1 step "Set up the box for Acme" s_setup
RT_CRITICAL=1 step "sudo asks in the box's terminal" s_sudo
RT_CRITICAL=1 step "A failing step stops the setup" s_fail
RT_CRITICAL=1 step "Retry from it; berthd restarts mid-step" s_retry_restart
RT_CRITICAL=1 step "The box's own GitHub sign-in" s_box_github
RT_CRITICAL=1 step "Berth's 1Password step" s_op_signin
step "Repos cloned and set up" s_projects
step "A second run skips what is done" s_rerun
step "A newer .berth is an update to review" s_update
step "berth team show and status" s_cli
step "A team setup from a direct link" s_link
step "A project id with a dot is refused" s_ids
step "Terminals read op:// keys; signed out, they say so" s_op_terminals
rt_finish
