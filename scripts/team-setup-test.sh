#!/usr/bin/env bash
# Team setup, end to end, in Docker: a fresh Ubuntu 24.04 box (berthd as its
# systemd user service, sudo asking dev for a password) and a laptop, with a
# synthetic GitHub: a fake gh on both answers from bare repositories on the
# laptop, which git daemon serves to the box, so nothing reaches GitHub.
#
#   scripts/team-setup-test.sh [--out DIR] [--keep] [--version vX.Y.Z]
#
# Steps: the drafted Cal.com box/setup.sh's plan and check subcommands (and
# that it refuses root); the laptop reads acme/.berth with its gh (access
# check: 2 of 3 repos); setup on the box runs the steps in a terminal there,
# where the password is typed (Berth never sees it); a failed step, then
# Retry from it; berthd restarting mid-step and resuming; the box's own gh
# sign-in by device code; the repos cloned, the web repo's own config trusted
# at the reviewed hash, the api repo's team kit and init; keys on the box;
# a second run skipping what is done; a newer .berth commit offered as an
# update with a diff, not run. Needs Docker and Go.
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
	docker cp "$here/release-test/team/make-github.sh" "$LAPC:/tmp/make-github.sh" || return 1
	COMMIT=$(lp "sh /tmp/make-github.sh" | tail -1) || fail "could not make the synthetic GitHub" || return 1
	docker exec -d -u dev "$LAPC" git daemon --reuseaddr --base-path=/home/dev/.fake-gh --export-all /home/dev/.fake-gh || return 1
	bx "git config --global url.git://$LAPC/.insteadOf https://github.com/" || return 1
	until_ok 20 bx "git ls-remote https://github.com/acme/web.git" || fail "the box can't reach the laptop's git daemon" || return 1
	bx "gh auth status" 2>/dev/null && fail "the box's gh is signed in already" && return 1
	detail "acme/.berth at ${COMMIT:0:7}; the box clones https://github.com/acme/* from git://$LAPC/; dev's sudo asks for a password"
}

s_install() {
	box_install || fail "install.sh failed" || return 1
	laptop_cli || fail "could not install berth on the laptop" || return 1
	lp "berth pair '$PAIR_LINK' --json" || fail "berth pair failed" || return 1
	BOX=$(lp "berth boxes --json" | jq_py '[b["name"] for b in j][0]') || fail "no box after pairing" || return 1
	until_ok 30 lp "berth ping $BOX" || fail "$BOX does not answer" || return 1
	detail "paired with $BOX"
}

# The drafted Cal.com script's read-only subcommands, on a fresh box.
s_calcom_script() {
	docker cp "$REPO/examples/team/calcom-dot-berth/box/setup.sh" "$BOXC:/tmp/cal-setup.sh" || return 1
	docker exec "$BOXC" chmod 755 /tmp/cal-setup.sh
	local plan
	plan=$(bx "/tmp/cal-setup.sh plan") || fail "plan failed" || return 1
	echo "$plan"
	[ "$(echo "$plan" | wc -l | tr -d ' ')" = 7 ] || fail "plan lists $(echo "$plan" | wc -l | tr -d ' ') steps, not 7" || return 1
	[ "$(echo "$plan" | grep -c ' sudo$')" = 3 ] || fail "plan marks $(echo "$plan" | grep -c ' sudo$') steps sudo, not 3" || return 1
	local s done=() todo=()
	for s in packages docker cli node yarn postgres redis; do
		if bx "/tmp/cal-setup.sh check $s" >/dev/null 2>&1; then done+=("$s"); else todo+=("$s"); fi
	done
	[ ${#done[@]} = 0 ] || fail "a fresh box passes check ${done[*]}" || return 1
	local out
	out=$(docker exec "$BOXC" /tmp/cal-setup.sh plan 2>&1) && fail "it ran as root: $out" && return 1
	echo "$out" | grep -q "not root" || fail "as root it said: $out" || return 1
	out=$(bx "/tmp/cal-setup.sh nope" 2>&1) && fail "an unknown step passed" && return 1
	echo "$out" | grep -q "no step nope" || fail "an unknown step said: $out" || return 1
	detail "plan: 7 steps, 3 with sudo; check fails for all 7 on a fresh box (${todo[*]}); refuses root; rejects unknown steps"
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
	echo "$v" | jq_py '[s["id"] for s in j["steps"]] == ["tools", "db", "slow", "github"]' >/dev/null || fail "steps: $(echo "$v" | jq_py '[s["id"] for s in j["steps"]]')" || return 1
	echo "$v" | jq_py '[p["source"] for p in j["projects"]] == ["repo", "kit", "none"]' >/dev/null || fail "sources" || return 1
	detail "acme/.berth at ${COMMIT:0:7}, read with the laptop's gh: 2 of 3 repos readable (acme/secret is not), 4 steps with the box's GitHub sign-in"
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
	detail "sudo asked in the box's terminal; the password was typed there; tools made /opt/acme"
}

s_fail() {
	until_ok 30 team_is 'j["phase"] == "failed"' || fail "db did not fail: $(team_json)" || return 1
	team_is 'j["steps"][1]["state"] == "failed" and j["steps"][0]["state"] == "done" and j["steps"][2]["state"] == "todo"' >/dev/null || fail "$(team_json)" || return 1
	screen | grep -q "port 5450 is taken" || fail "the terminal does not show why" || return 1
	detail "db failed with its output in the terminal; tools stays done"
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
	detail "Retry from db ran db (not tools); berthd restarted mid-step and picked the step up when it finished"
}

s_box_github() {
	until_ok 60 team_is 'j["steps"][3]["code"] == "4F2A-9C1E"' || fail "no device code: $(team_json)" || return 1
	team_is 'j["steps"][3]["state"] == "waiting" and j["steps"][3]["url"] == "https://github.com/login/device"' >/dev/null || fail "$(team_json)" || return 1
	send "" || return 1
	until_ok 120 team_is 'j["phase"] in ("done", "failed")' || fail "the setup did not finish: $(team_json)" || return 1
	team_is 'j["phase"] == "done"' >/dev/null || fail "it failed: $(team_json)" || return 1
	bx "gh auth status" || fail "the box's gh is not signed in" || return 1
	detail "the box signed in with its own gh (device code 4F2A-9C1E shown to the laptop); the laptop's credential never left it"
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
	cfg=$(laptop_api GET "/v1/boxes/$BOX/api/locations/web/config") || return 1
	echo "$cfg" | jq_py 'j["local"]["env"]["MAIL_KEY"] == "SG.e2e" and j["local"]["env"]["STRIPE_KEY"] == "op://Dev/Stripe/key"' >/dev/null || fail "web's keys: $cfg" || return 1
	bx "test ! -e ~/code/secret" || fail "acme/secret was cloned" || return 1
	# The password went into sudo, in the terminal, and nowhere else.
	bx "grep -rqs '$PASSWORD' ~/.berth ~/.config ~/code /tmp 2>/dev/null" && fail "the password is on disk" && return 1
	screen | grep -q "$PASSWORD" && fail "the password shows in the terminal" && return 1
	detail "web and api cloned to ~/code; web's .berth/config.json trusted at the reviewed hash; api got the team kit and its init; MAIL_KEY and the STRIPE_KEY reference in web's own config; the password is nowhere"
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
	echo "$out" | grep -q "Acme (acme) *$BOX *${COMMIT:0:7} *set up *4/4 steps, 2/2 repos" || fail "status: $out" || return 1
	detail "berth team show lists the plan, access and the update; berth team status: set up, 4/4 steps, 2/2 repos"
}

RT_CRITICAL=1 step "Build berthd, berth and the Ubuntu image" s_build
RT_CRITICAL=1 step "Start a box and a laptop" s_machines
RT_CRITICAL=1 step "A synthetic GitHub, sudo with a password" s_github
RT_CRITICAL=1 step "Install berthd and pair" s_install
step "Cal.com's box/setup.sh: plan and check" s_calcom_script
RT_CRITICAL=1 step "The laptop reads acme/.berth with gh" s_show
RT_CRITICAL=1 step "Set up the box for Acme" s_setup
RT_CRITICAL=1 step "sudo asks in the box's terminal" s_sudo
RT_CRITICAL=1 step "A failing step stops the setup" s_fail
RT_CRITICAL=1 step "Retry from it; berthd restarts mid-step" s_retry_restart
RT_CRITICAL=1 step "The box's own GitHub sign-in" s_box_github
step "Repos cloned and set up" s_projects
step "A second run skips what is done" s_rerun
step "A newer .berth is an update to review" s_update
step "berth team show and status" s_cli
rt_finish
