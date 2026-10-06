package guided

import (
	"fmt"
	"strings"

	"github.com/sean-brydon/berthd/internal/agentcli"
)

// Script is the shell script that runs the box's steps (run, in Order) on
// the box, as the person, printing a marker as each starts and ends. berth
// add ssh uploads it and runs it in a terminal (ssh -t) when a person is
// there, so sudo asks them for their password on the box; without one it
// uses sudo only when sudo needs no password, and otherwise stops at that
// step with the command to run. It refuses to run as root, and each step
// checks before it changes anything, so it is safe to run again.
func Script(o Options, p Probe, run []string) string {
	var b strings.Builder
	ask := 0
	if o.Ask {
		ask = 1
	}
	listen := "--keep-listen"
	if o.Listen != "" {
		listen = "--listen " + shellQuote(o.Listen)
	}
	fmt.Fprintf(&b, `#!/bin/sh
# Berth's guided install, on this box, as you. berth add ssh wrote this and
# runs it; running it again is safe: each step checks before it changes
# anything. Steps that need root ask for your password with sudo, here, on
# the box: Berth never sees it or keeps it.
B="$HOME/.local/bin"
BERTHD="$B/berthd"
PATH="$B:$PATH"
export PATH
ASK=%d
m() { printf '%s%%s\n' "$*"; }
title() { printf '\n\033[1m==> %%s\033[0m\n' "$1"; }
note() { printf '    %%s\n' "$*"; }
# failed STEP WHY: stop at STEP; Retry starts again from it.
failed() {
  m "$1" fail "$2"
  printf '\n\033[31mStopped at this step: %%s\033[0m\n' "$2"
  exit 1
}
# by_hand STEP WHY COMMAND: stop at STEP, saying the command to run.
by_hand() {
  m "$1" cmd "$3"
  m "$1" fail "$2"
  printf '\n\033[31m%%s\033[0m\nRun this on the box, then set it up again:\n  %%s\n' "$2" "$3"
  exit 1
}
# asroot COMMAND...: run COMMAND as root. sudo asks for the password here
# when a person is at this terminal; without one, 99 means it would have.
told=
asroot() {
  if [ "$(id -u)" -eq 0 ]; then "$@"; return; fi
  if ! command -v sudo >/dev/null 2>&1; then return 98; fi
  if sudo -n true 2>/dev/null; then sudo -n "$@"; return; fi
  [ "$ASK" = 1 ] || return 99
  if [ -z "$told" ]; then
    note "This needs root: sudo asks for $(id -un)'s password on this box."
    note "Type it and press Enter. It goes to sudo here; Berth never sees it or keeps it."
    told=1
  fi
  sudo "$@"
}
if [ "$(id -u)" -eq 0 ]; then
  m %[3]s fail "Berth installs as the user your agents run as, not root"
  echo "Berth installs as the user your agents will run as, not as root. Log in as that"
  echo "user (make one with: adduser me && usermod -aG sudo me) and set the box up again."
  exit 1
fi
`, ask, MarkerPrefix, firstOr(run, StepBerthd))

	for _, step := range run {
		switch step {
		case StepBerthd:
			fmt.Fprintf(&b, `
m berthd start
title "berthd, as your user service"
out=$("$BERTHD" install --no-tools --no-integrations %s 2>&1)
c=$?
printf '%%s\n' "$out" | sed -e '/^Next: berthd pair$/d' -e '/user lingering is off/d' -e '/^Enable it once with/d' -e 's/^/    /'
[ "$c" = 0 ] || failed berthd "berthd's service did not start (above says why)"
m berthd done
`, listen)
		case StepLinger:
			b.WriteString(`
m linger start
title "Keeping berthd running after you log out"
u=$(id -un)
if [ "$(loginctl show-user "$u" -p Linger 2>/dev/null)" = Linger=yes ]; then
  note "Lingering is already on for $u."
else
  asroot loginctl enable-linger "$u"
  c=$?
  [ "$c" != 99 ] || by_hand linger "Turning lingering on needs root, and sudo asks for a password, which Berth can't type without a terminal." "sudo loginctl enable-linger $u"
  [ "$c" != 98 ] || by_hand linger "Turning lingering on needs root, and this box has no sudo." "loginctl enable-linger $u   (as root)"
  [ "$c" = 0 ] || { m linger cmd "sudo loginctl enable-linger $u"; failed linger "Turning lingering on didn't finish (exit $c); the terminal says why"; }
  note "Lingering is on: berthd keeps running when you log out."
fi
m linger done
`)
		case StepTools:
			writeTools(&b, o, p)
		case StepAgents:
			fmt.Fprintf(&b, `
m agents start
title %s
"$BERTHD" agents install %s || failed agents "an agent CLI did not install (above says why)"
m agents done
`, shellQuote("Agent CLIs: "+agentcli.Names(o.Agents)), strings.Join(o.Agents, " "))
		case StepIntegrations:
			b.WriteString(`
m integrations start
title "Agent integrations"
out=$("$BERTHD" integrations install present 2>&1)
c=$?
printf '%s\n' "$out" | sed 's/^/    /'
[ "$c" = 0 ] || failed integrations "the integrations did not install (above says why)"
m integrations done
`)
		}
	}
	return b.String()
}

func firstOr(run []string, def string) string {
	if len(run) > 0 {
		return run[0]
	}
	return def
}

// writeTools is the tools step: Berth's tmux (uploaded already) checked,
// and what is still missing installed with the package manager.
func writeTools(b *strings.Builder, o Options, p Probe) {
	upload, pkgs := toolsNeed(o, p)
	b.WriteString(`
m tools start
title "tmux and git"
`)
	if upload {
		b.WriteString(`if [ -x "$B/tmux" ]; then
  v=$("$B/tmux" -V 2>&1) || failed tools "Berth's tmux in $B does not run on this box: $v"
  note "tmux: Berth's own build, $v, in $B (no sudo needed)"
fi
`)
	}
	if len(pkgs) == 0 {
		b.WriteString(`note "git: $(git --version 2>/dev/null || echo missing)"
m tools done
`)
		return
	}
	steps, brew := PackageSteps(p.Manager, pkgs)
	lines := CommandLines(steps, brew)
	missing := "need="
	for _, t := range pkgs {
		missing += fmt.Sprintf(`
command -v %[1]s >/dev/null 2>&1 || [ -x "$B/%[1]s" ] || need="$need %[1]s"`, t)
	}
	b.WriteString(missing + "\n")
	if steps == nil {
		fmt.Fprintf(b, `if [ -n "$need" ]; then
  failed tools "Berth needs$need here, and knows no package manager on this box: install$need, then retry"
fi
m tools done
`)
		return
	}
	b.WriteString(`if [ -n "$need" ]; then
  note "Installing$need with ` + p.Manager + `"
`)
	for _, s := range steps {
		argv := strings.Join(s, " ")
		if brew {
			fmt.Fprintf(b, "  %s || { m tools cmd %s; failed tools %s; }\n", argv, shellQuote(argv), shellQuote("Installing with Homebrew didn't finish; the terminal says why"))
			continue
		}
		if s[0] == "apt-get" {
			argv = "env DEBIAN_FRONTEND=noninteractive " + argv
		}
		fmt.Fprintf(b, `  asroot %s
  c=$?
  [ "$c" != 99 ] || by_hand tools %s %s
  [ "$c" != 98 ] || by_hand tools %s %s
  [ "$c" = 0 ] || { m tools cmd %s; failed tools %s; }
`, argv,
			shellQuote("Installing"+" "+strings.Join(pkgs, " and ")+" needs root, and sudo asks for a password, which Berth can't type without a terminal."), shellQuote(strings.Join(lines, " && ")),
			shellQuote("Installing "+strings.Join(pkgs, " and ")+" needs root, and this box has no sudo."), shellQuote(strings.Join(lines, " && ")+"   (as root, without sudo)"),
			shellQuote(CommandLines([][]string{s}, false)[0]), shellQuote("Installing "+strings.Join(pkgs, " and ")+" with "+p.Manager+" didn't finish; the terminal says why"))
	}
	b.WriteString(`fi
for t in` + " " + strings.Join(pkgs, " ") + `; do
  command -v "$t" >/dev/null 2>&1 || [ -x "$B/$t" ] || failed tools "$t is still not installed"
done
note "git: $(git --version 2>/dev/null)"
m tools done
`)
}
