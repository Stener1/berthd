package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/sean-brydon/berthd/internal/box"
)

// Berth needs two tools on a box before anything else works: tmux, which
// every terminal, agent and team setup step runs in, and git, for
// worktrees. A fresh Ubuntu image has neither, so `berthd install` (which
// the install script and `berth add ssh` both run) installs them when it
// can without asking (as root, with sudo that needs no password, or with
// Homebrew), and otherwise stops and says the one command to run.

// toolsPlan is what install would do about tmux and git here.
type toolsPlan struct {
	// Missing are the tools that are not here.
	Missing []string
	// Steps install them, each an argv to run as root unless Brew.
	Steps [][]string
	// Brew is Homebrew's way: as the user, never root.
	Brew bool
	// Help is where to read how, when no package manager is known.
	Help string
}

// planTools looks for tmux and git and the package manager to get them.
func planTools(goos string, found func(string) bool, have func(string) bool) toolsPlan {
	var p toolsPlan
	for _, t := range []string{"tmux", "git"} {
		if !found(t) {
			p.Missing = append(p.Missing, t)
		}
	}
	if len(p.Missing) == 0 {
		return p
	}
	pkgs := p.Missing
	if goos == "darwin" {
		if have("brew") {
			p.Brew, p.Steps = true, [][]string{append([]string{"brew", "install"}, pkgs...)}
		} else {
			p.Help = "https://brew.sh"
		}
		return p
	}
	switch {
	case have("apt-get"):
		p.Steps = [][]string{{"apt-get", "update", "-q"}, append([]string{"apt-get", "install", "-y", "-q"}, pkgs...)}
	case have("dnf"):
		p.Steps = [][]string{append([]string{"dnf", "install", "-y"}, pkgs...)}
	case have("yum"):
		p.Steps = [][]string{append([]string{"yum", "install", "-y"}, pkgs...)}
	case have("pacman"):
		p.Steps = [][]string{append([]string{"pacman", "-S", "--noconfirm", "--needed"}, pkgs...)}
	case have("zypper"):
		p.Steps = [][]string{append([]string{"zypper", "--non-interactive", "install"}, pkgs...)}
	case have("apk"):
		p.Steps = [][]string{append([]string{"apk", "add"}, pkgs...)}
	case have("brew"):
		p.Brew, p.Steps = true, [][]string{append([]string{"brew", "install"}, pkgs...)}
	default:
		p.Help = "https://github.com/tmux/tmux/wiki/Installing"
	}
	return p
}

// command is the plan as one line to type: sudo where it needs root.
func (p toolsPlan) command() string {
	var parts []string
	for _, s := range p.Steps {
		line := strings.Join(s, " ")
		if !p.Brew {
			line = "sudo " + line
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " && ")
}

func (p toolsPlan) what() string { return strings.Join(p.Missing, " and ") }

// instruction says what to run, for when install can't do it itself.
func (p toolsPlan) instruction() string {
	verb := "isn't"
	if len(p.Missing) > 1 {
		verb = "aren't"
	}
	msg := fmt.Sprintf("Berth needs %s on this box, and %s %s installed: every terminal, agent and team setup step runs in tmux, and worktrees are git's.", p.what(), them(len(p.Missing)), verb)
	switch {
	case len(p.Steps) > 0 && p.Brew:
		return msg + " Install " + them2(len(p.Missing)) + ", then run this again:\n  " + p.command()
	case len(p.Steps) > 0:
		return msg + " Installing " + them2(len(p.Missing)) + " needs root, and sudo asks for a password, which this can't type. Run this, then run the install again:\n  " + p.command()
	case p.Help == "https://brew.sh":
		return msg + " Install Homebrew (https://brew.sh), then run: brew install " + strings.Join(p.Missing, " ") + ", then run this again."
	}
	return msg + " Install " + them2(len(p.Missing)) + " with your package manager (" + p.Help + "), then run this again."
}

func them2(n int) string {
	if n > 1 {
		return "them"
	}
	return "it"
}

func them(n int) string {
	if n > 1 {
		return "they"
	}
	return "it"
}

// ensureTools installs what is missing when that needs no question, or
// fails with the command to run. asRoot reports whether to run steps
// directly (root) and canSudo whether sudo needs no password.
func ensureTools(out io.Writer, p toolsPlan, asRoot bool, canSudo func() bool, run func(argv []string) error) error {
	if len(p.Missing) == 0 {
		return nil
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("%s", p.instruction())
	}
	prefix := []string{}
	switch {
	case p.Brew, asRoot:
	case canSudo():
		prefix = []string{"sudo", "-n"}
	default:
		return fmt.Errorf("%s", p.instruction())
	}
	fmt.Fprintf(out, "Installing %s: %s\n", p.what(), p.command())
	for _, s := range p.Steps {
		argv := append(append([]string{}, prefix...), s...)
		if err := run(argv); err != nil {
			return fmt.Errorf("could not install %s (%s: %v). Run it yourself, then run this again:\n  %s", p.what(), strings.Join(s, " "), err, p.command())
		}
	}
	return nil
}

// systemTools is planTools for this machine.
func systemTools() toolsPlan {
	found := func(t string) bool {
		if t == "tmux" {
			_, err := box.TmuxPath()
			return err == nil
		}
		_, err := exec.LookPath(t)
		return err == nil
	}
	have := func(t string) bool {
		if _, err := exec.LookPath(t); err == nil {
			return true
		}
		for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin"} {
			if st, err := os.Stat(d + "/" + t); err == nil && !st.IsDir() {
				return true
			}
		}
		return false
	}
	return planTools(runtime.GOOS, found, have)
}

// installTools is ensureTools here, running package managers with their
// output shown.
func installTools(out io.Writer) error {
	p := systemTools()
	canSudo := func() bool {
		if _, err := exec.LookPath("sudo"); err != nil {
			return false
		}
		return exec.Command("sudo", "-n", "true").Run() == nil
	}
	run := func(argv []string) error {
		if argv[0] == "brew" {
			if path, err := exec.LookPath("brew"); err == nil {
				argv[0] = path
			} else {
				for _, d := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew", "/home/linuxbrew/.linuxbrew/bin/brew"} {
					if _, err := os.Stat(d); err == nil {
						argv[0] = d
					}
				}
			}
		}
		// sudo drops the environment, so apt-get gets its own: it asks
		// nothing.
		for i, a := range argv {
			if a == "apt-get" {
				argv = append(append(append([]string{}, argv[:i]...), "env", "DEBIAN_FRONTEND=noninteractive"), argv[i:]...)
				break
			}
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		cmd.Stdout, cmd.Stderr = out, out
		cmd.Stdin = nil
		return cmd.Run()
	}
	if err := ensureTools(out, p, os.Getuid() == 0, canSudo, run); err != nil {
		return err
	}
	if len(p.Missing) > 0 {
		if still := systemTools(); len(still.Missing) > 0 {
			return fmt.Errorf("installed, but %s still can't be found: %s", still.what(), still.instruction())
		}
	}
	return nil
}
