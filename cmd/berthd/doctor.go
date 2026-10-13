package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/cosscom/shipyard/internal/box"
	"github.com/cosscom/shipyard/internal/doctor"
	"github.com/cosscom/shipyard/internal/service"
)

// daemonChecks runs inside berthd serve, where the listen address is known.
// daemonChecks describes the running daemon. listening is the bound address;
// listenFlag is the --listen it was started with, which its unit names.
func daemonChecks(b boxHome, listening, listenFlag string) []doctor.Check {
	const area = "berthd"
	checks := []doctor.Check{}
	host, _, _ := net.SplitHostPort(listening)
	switch ip := net.ParseIP(host); {
	case ip != nil && ip.IsUnspecified():
		checks = append(checks, doctor.Check{Area: area, Name: "listening", Status: doctor.Warn,
			Detail: listening + " answers on every interface, including the internet",
			Fix:    "berthd install  (listens on the tailnet address only)"})
	case ip != nil && ip.IsLoopback():
		checks = append(checks, doctor.Check{Area: area, Name: "listening", Status: doctor.OK, Detail: listening + " (this box only; reach it through a tunnel)"})
	default:
		checks = append(checks, doctor.Check{Area: area, Name: "listening", Status: doctor.OK, Detail: listening})
	}
	// install adds the login shell's PATH as it was then; doctor can't know
	// it, so any PATH counts.
	if service.InstalledAnyPATH(daemonService(b, listenFlag)) {
		checks = append(checks, doctor.Check{Area: area, Name: "starts at boot", Status: doctor.OK, Detail: "installed as a user service"})
	} else {
		checks = append(checks, doctor.Check{Area: area, Name: "starts at boot", Status: doctor.Warn, Detail: "berthd runs, but not as a service", Fix: "berthd install"})
	}
	if runtime.GOOS == "linux" {
		if lingering() {
			checks = append(checks, doctor.Check{Area: area, Name: "survives logout", Status: doctor.OK, Detail: "user lingering is on"})
		} else {
			checks = append(checks, doctor.Check{Area: area, Name: "survives logout", Status: doctor.Warn, Detail: "berthd stops when you log out", Fix: "sudo loginctl enable-linger " + currentUser()})
		}
	}
	peers, err := b.clients().List()
	switch {
	case err != nil:
		checks = append(checks, doctor.Check{Area: area, Name: "paired laptops", Status: doctor.Fail, Detail: err.Error()})
	case len(peers) == 0:
		checks = append(checks, doctor.Check{Area: area, Name: "paired laptops", Status: doctor.Warn, Detail: "none yet", Fix: "berthd pair"})
	default:
		checks = append(checks, doctor.Check{Area: area, Name: "paired laptops", Status: doctor.OK, Detail: fmt.Sprintf("%d", len(peers))})
	}
	return checks
}

// runDoctor asks the running daemon for its report; without one, it reports
// that first and still checks the tools on this box.
func runDoctor(b boxHome, args []string) error {
	asJSON := len(args) > 0 && args[0] == "--json"
	var checks []doctor.Check
	if _, err := os.Stat(b.socket()); err == nil {
		checks, err = box.NewClient(box.NewLocal(b.socket())).Doctor(context.Background())
		if err != nil {
			return err
		}
	} else {
		checks = append(checks, doctor.Check{Area: "berthd", Name: "running", Status: doctor.Fail, Detail: "berthd serve is not running", Fix: "berthd install"})
		checks = append(checks, doctor.ToolCheck("Worktrees and sessions", "git", "git", "Install git with your package manager", true), box.TmuxCheck())
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(checks)
	}
	if problems := doctor.Print(os.Stdout, checks); problems > 0 {
		fmt.Printf("\n%d thing(s) to fix.\n", problems)
	} else {
		fmt.Println("\nAll good.")
	}
	return nil
}
