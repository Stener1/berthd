package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func has(names ...string) func(string) bool {
	return func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
}

func TestPlanTools(t *testing.T) {
	// A fresh Ubuntu 24.04: neither tmux nor git.
	p := planTools("linux", has(), has("apt-get"))
	if strings.Join(p.Missing, " ") != "tmux git" || p.command() != "sudo apt-get update -q && sudo apt-get install -y -q tmux git" {
		t.Fatalf("%+v %q", p, p.command())
	}
	if p := planTools("linux", has("git"), has("dnf")); p.command() != "sudo dnf install -y tmux" {
		t.Fatal(p.command())
	}
	if p := planTools("linux", has("tmux", "git"), has("apt-get")); len(p.Missing) != 0 || p.command() != "" {
		t.Fatalf("%+v", p)
	}
	if p := planTools("darwin", has("git"), has("brew")); !p.Brew || p.command() != "brew install tmux" {
		t.Fatalf("%+v", p)
	}
	if p := planTools("darwin", has("git"), has()); !strings.Contains(p.instruction(), "Install Homebrew (https://brew.sh), then run: brew install tmux") {
		t.Fatal(p.instruction())
	}
	if p := planTools("linux", has(), has()); !strings.Contains(p.instruction(), "with your package manager") {
		t.Fatal(p.instruction())
	}
}

func TestEnsureTools(t *testing.T) {
	p := planTools("linux", has(), has("apt-get"))
	var ran []string
	run := func(argv []string) error { ran = append(ran, strings.Join(argv, " ")); return nil }
	never := func() bool { t.Fatal("asked sudo"); return false }
	// As root, it installs them.
	if err := ensureTools(io.Discard, p, true, never, run); err != nil || strings.Join(ran, "; ") != "apt-get update -q; apt-get install -y -q tmux git" {
		t.Fatalf("%v %v", err, ran)
	}
	// With sudo that needs no password, through sudo -n.
	ran = nil
	if err := ensureTools(io.Discard, p, false, func() bool { return true }, run); err != nil || strings.Join(ran, "; ") != "sudo -n apt-get update -q; sudo -n apt-get install -y -q tmux git" {
		t.Fatalf("%v %v", err, ran)
	}
	// sudo would ask for a password nobody can type: it stops, saying what
	// to run.
	ran = nil
	err := ensureTools(io.Discard, p, false, func() bool { return false }, run)
	if err == nil || len(ran) != 0 || !strings.Contains(err.Error(), "Berth needs tmux and git on this box, and they aren't installed") ||
		!strings.HasSuffix(err.Error(), "\n  sudo apt-get update -q && sudo apt-get install -y -q tmux git") {
		t.Fatalf("%v %v", err, ran)
	}
	// Homebrew needs no root.
	ran = nil
	if err := ensureTools(io.Discard, planTools("darwin", has("git"), has("brew")), false, never, run); err != nil || strings.Join(ran, "; ") != "brew install tmux" {
		t.Fatalf("%v %v", err, ran)
	}
	// A failed install says so, and what to run.
	err = ensureTools(io.Discard, p, true, never, func([]string) error { return errors.New("exit status 100") })
	if err == nil || !strings.Contains(err.Error(), "could not install tmux and git") || !strings.Contains(err.Error(), "sudo apt-get update -q && sudo apt-get install") {
		t.Fatal(err)
	}
	// Nothing missing, nothing run.
	if err := ensureTools(io.Discard, planTools("linux", has("tmux", "git"), has("apt-get")), false, never, run); err != nil {
		t.Fatal(err)
	}
}

func TestPrintToolsForTheInstallScript(t *testing.T) {
	var b strings.Builder
	printTools(&b, planTools("linux", has("git"), has("apt-get")))
	if b.String() != "tools missing tmux\ntools install sudo apt-get update -q && sudo apt-get install -y -q tmux\n" {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	printTools(&b, planTools("linux", has("git", "tmux"), has("apt-get")))
	if b.String() != "" {
		t.Fatalf("%q", b.String())
	}
}
