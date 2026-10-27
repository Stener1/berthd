package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/cosscom/shipyard/internal/agent"
	"github.com/cosscom/shipyard/internal/doctor"
	"github.com/cosscom/shipyard/internal/service"
	"github.com/cosscom/shipyard/internal/version"
)

func agentService(l laptop) (service.Spec, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Spec{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	home, err := filepath.Abs(filepath.Dir(l.dir))
	if err != nil {
		return service.Spec{}, err
	}
	name := "berth-agent"
	if runtime.GOOS == "darwin" {
		name = "dev.berth.agent"
	}
	return service.Spec{
		Name:        name,
		Description: "berth agent",
		Program:     exe,
		Args:        []string{"agent"},
		Env:         map[string]string{"BERTH_HOME": home},
		LogPath:     filepath.Join(l.dir, "agent.log"),
	}, nil
}

// ensureAgent returns a client for a running agent, starting one if needed:
// through the supervisor when installed, otherwise as a detached process.
func ensureAgent(l laptop) (*agent.Client, error) {
	c := agent.NewClient(l.socket())
	if c.Running(context.Background()) {
		return c, nil
	}
	spec, err := agentService(l)
	if err != nil {
		return nil, err
	}
	if service.Installed(spec) {
		if err := service.Start(spec); err != nil {
			return nil, err
		}
	} else if err := spawnAgent(l, spec.Program); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.Running(context.Background()) {
			return c, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("the berth agent did not start; see %s", filepath.Join(l.dir, "agent.log"))
}

func spawnAgent(l laptop, exe string) error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(l.dir, "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "agent")
	cmd.Env = append(os.Environ(), "BERTH_HOME="+filepath.Dir(l.dir))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// runAgent is `berth agent`: the agent in the foreground, as a supervisor
// runs it. Losing the singleton race exits cleanly so it is not restarted.
func runAgent(l laptop) error {
	ctx, stop := signalContext()
	defer stop()
	cfg := agent.Config{Dir: l.dir, Socket: l.socket()}
	if spec, err := agentService(l); err == nil {
		if _, err := service.Migrate(spec); err != nil {
			fmt.Fprintf(os.Stderr, "berth agent: updating its service: %v\n", err)
		}
	}
	// The app's Copy diagnostics shows what `berth doctor` would (GET /v1/doctor).
	cfg.Doctor = func(ctx context.Context) []doctor.Check { return laptopChecks(ctx, l) }
	// A second agent (a test, a throwaway home) needs its own ports.
	if a := os.Getenv("BERTH_PROXY_ADDR"); a != "" {
		cfg.ProxyAddrs = strings.Split(a, ",")
	}
	if a := os.Getenv("BERTH_UI_ADDR"); a != "" {
		cfg.UIAddr = a
	}
	// Another plugin listing than the public one (Settings → Plugins → Browse).
	cfg.PluginRegistry = os.Getenv("BERTH_PLUGIN_REGISTRY")
	err := agent.Run(ctx, cfg)
	if errors.Is(err, agent.ErrAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "berth: an agent is already running for this home; exiting")
		return nil
	}
	return err
}

func agentCommand(l laptop, args []string) error {
	if len(args) == 0 {
		return runAgent(l)
	}
	spec, err := agentService(l)
	if err != nil {
		return err
	}
	switch args[0] {
	case "start":
		// What the desktop app runs when the agent is not running: through
		// the service when one is installed, otherwise detached from the
		// caller, so it outlives the app.
		if _, err := ensureAgent(l); err != nil {
			return err
		}
		fmt.Println("The berth agent is running.")
		return nil
	case "install":
		// The supervisor starts its own agent; a running one would hold the lock.
		if c := agent.NewClient(l.socket()); c.Running(context.Background()) {
			c.Stop(context.Background())
			time.Sleep(200 * time.Millisecond)
		}
		path, err := service.Install(spec)
		if err != nil {
			return err
		}
		fmt.Printf("Installed %s\nThe agent now starts at login and restarts after a crash; berth stop still stops it.\n", path)
		return nil
	case "restart":
		// What the app runs as it starts (--if-stale): an agent older than
		// this berth (the app was updated under it) is stopped once its
		// work under way is done, and started again from this one.
		ifStale, asJSON := false, false
		for _, a := range args[1:] {
			switch a {
			case "--if-stale":
				ifStale = true
			case "--json":
				asJSON = true
			default:
				return errors.New("usage: berth agent restart [--if-stale] [--json]")
			}
		}
		res, err := restartAgent(l, spec, ifStale)
		if err != nil {
			return err
		}
		if asJSON {
			return printJSON(res)
		}
		fmt.Println(res.Message)
		return nil
	case "uninstall":
		path, err := service.Uninstall(spec)
		if err != nil {
			return err
		}
		if path == "" {
			fmt.Println("The agent service is not installed.")
			return nil
		}
		fmt.Printf("Removed %s\n", path)
		return nil
	case "status":
		running := agent.NewClient(l.socket()).Running(context.Background())
		if len(args) > 1 && args[1] == "--json" {
			return printJSON(map[string]bool{"installed": service.Installed(spec), "running": running})
		}
		fmt.Printf("service installed: %v\nagent running: %v\n", service.Installed(spec), running)
		return nil
	}
	return errors.New("usage: berth agent [start|restart|install|uninstall|status]")
}

// RestartResult is what `berth agent restart --json` reports.
type RestartResult struct {
	Restarted bool `json:"restarted"`
	// Running: an agent runs now (false: none was running, with --if-stale).
	Running bool   `json:"running"`
	Reason  string `json:"reason,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	// Service: it runs as the login service (berth agent install).
	Service bool   `json:"service,omitempty"`
	Message string `json:"message"`
}

// restartAgent stops the running agent, letting it finish its work under
// way, and starts this berth's: through the login service when that is
// installed for this berth, otherwise detached, as berth agent start does.
// With ifStale, only an agent older than this berth is restarted.
func restartAgent(l laptop, spec service.Spec, ifStale bool) (RestartResult, error) {
	ctx := context.Background()
	c := agent.NewClient(l.socket())
	if !c.Running(ctx) {
		if ifStale {
			return RestartResult{Message: "The berth agent is not running."}, nil
		}
		if _, err := ensureAgent(l); err != nil {
			return RestartResult{}, err
		}
		return RestartResult{Restarted: true, Running: true, To: version.Version, Message: "Started the berth agent."}, nil
	}
	ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
	info, infoErr := c.Info(ictx)
	cancel()
	build := ""
	if b, err := os.ReadFile(spec.Program); err == nil {
		build = version.BuildID(b)
	}
	stale, why := agent.Stale(info, infoErr, spec.Program, build, version.Version)
	if ifStale && !stale {
		return RestartResult{Running: true, From: info.Version, Message: "The berth agent is up to date."}, nil
	}
	from := info.Version
	if errors.Is(infoErr, agent.ErrNoAgentInfo) {
		from = "an older release"
	}
	if err := c.StopDrained(ctx); err != nil {
		return RestartResult{}, fmt.Errorf("stopping the berth agent: %w", err)
	}
	wctx, cancel := context.WithTimeout(ctx, agentDrainWait)
	err := c.WaitStopped(wctx, l.dir)
	cancel()
	if err != nil {
		return RestartResult{}, err
	}
	asService := false
	if u, ok, _ := service.Read(spec.Name); ok && u.Program == spec.Program && u.Env["BERTH_HOME"] == spec.Env["BERTH_HOME"] {
		// Installed for this berth: installing it again (as it is, or as
		// this berth writes it now) starts it.
		if _, err := service.Install(spec); err != nil {
			return RestartResult{}, err
		}
		asService = true
	} else if err := spawnAgent(l, spec.Program); err != nil {
		return RestartResult{}, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for !c.Running(ctx) {
		if time.Now().After(deadline) {
			return RestartResult{}, fmt.Errorf("the berth agent did not start again; see %s", filepath.Join(l.dir, "agent.log"))
		}
		time.Sleep(50 * time.Millisecond)
	}
	msg := "Restarted the berth agent"
	if why != "" {
		msg += ": " + why
	}
	return RestartResult{Restarted: true, Running: true, Reason: why, From: from, To: version.Version, Service: asService, Message: msg + "."}, nil
}

// How long a restart waits for the agent to finish its work under way and
// stop: its own limit, and a little more.
const agentDrainWait = 2*time.Minute + 30*time.Second
