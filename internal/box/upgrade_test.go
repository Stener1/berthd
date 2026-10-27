package box

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/version"
)

// daemonScript is a stand-in build that answers `id` with a fingerprint.
func daemonScript(fingerprint string) []byte {
	return []byte("#!/bin/sh\nif [ \"$1\" = id ]; then echo " + fingerprint + "; fi\n")
}

func TestInstallSwapsInAVerifiedBuild(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "berthd")
	os.WriteFile(exe, daemonScript("old-build-fp"), 0o755)
	u := &SelfUpdate{Executable: exe, Fingerprint: "the-box-fp"}
	next := daemonScript("the-box-fp")
	if err := u.Install(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != string(next) {
		t.Fatal("the running executable was not replaced")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("the staging file was left behind")
	}
}

func TestInstallRejectsABuildThatDoesNotRunHere(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "berthd")
	original := daemonScript("the-box-fp")
	os.WriteFile(exe, original, 0o755)
	u := &SelfUpdate{Executable: exe, Fingerprint: "the-box-fp"}
	for name, binary := range map[string][]byte{
		"other identity": daemonScript("someone-else"),
		"not runnable":   []byte("\x7fELF garbage for another platform"),
	} {
		err := u.Install(context.Background(), binary)
		if err == nil || !strings.Contains(err.Error(), "does not run on this box") {
			t.Fatalf("%s: %v", name, err)
		}
		got, _ := os.ReadFile(exe)
		if string(got) != string(original) {
			t.Fatalf("%s: a rejected build replaced the running one", name)
		}
	}
}

func TestBuildIDDistinguishesBuilds(t *testing.T) {
	if BuildID([]byte("a")) == BuildID([]byte("b")) || len(BuildID([]byte("a"))) != 12 {
		t.Fatal("build ids do not identify builds")
	}
}

func TestInfoNamesTheAccountToLogInAs(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "berthd")
	os.WriteFile(exe, []byte("build"), 0o755)
	c, _ := servedBox(t, func(b *Box) { b.Update = &SelfUpdate{Executable: exe} })
	var i Info
	if status := call(t, c, "GET", "/v1/info", "", nil, &i); status != 200 {
		t.Fatalf("info: %d", status)
	}
	me, _ := user.Current()
	home, _ := os.UserHomeDir()
	if i.User != me.Username || i.Home != home {
		t.Fatalf("info = %+v, want user %s home %s", i, me.Username, home)
	}
	// Its release, so an older laptop can tell it isn't outdated.
	if i.Version != version.Version {
		t.Fatalf("info says version %q, want %q", i.Version, version.Version)
	}
}
