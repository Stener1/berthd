package box

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/sean-brydon/berthd/internal/events"
	"github.com/sean-brydon/berthd/internal/groups"
)

// A repository's services run in each worktree as managed units, so they
// survive the daemon restarting and come back if they crash, each with the
// worktree's environment and so its own $BERTH_PORT.

// ServiceStatus is one of a worktree's services.
type ServiceStatus struct {
	Name      string `json:"name"`
	Run       string `json:"run"`
	Autostart bool   `json:"autostart,omitempty"`
	// State is the unit's, or "stopped" when it is not installed.
	State string `json:"state"`
	Unit  string `json:"unit"`
	Port  int    `json:"port,omitempty"`
	// Terminal services run in Session, a tmux session the app shows as a
	// tab named Title (the name when the config gives none).
	Terminal bool   `json:"terminal,omitempty"`
	Title    string `json:"title,omitempty"`
	Session  string `json:"session,omitempty"`
}

var ErrUnknownService = errors.New("no service with that name in this repository's config")

func eventf(b *Box, typ string, data map[string]any) events.Event {
	return events.Event{Type: typ, Box: b.Name, Origin: "berth", Data: data}
}

func serviceUnit(loc, wt, svc string) string {
	name := "svc-" + slug(loc, 30) + "-" + slug(wt, 60) + "-" + svc
	if len(name) > 128 {
		name = name[:128]
	}
	return strings.TrimRight(name, "-")
}

// loginShell is the user's shell, which a service runs through so the tools
// the user installed are on PATH even under systemd.
func loginShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if f, err := os.Open("/etc/passwd"); err == nil {
		defer f.Close()
		me := strconv.Itoa(os.Getuid())
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Split(sc.Text(), ":")
			if len(fields) >= 7 && fields[2] == me && fields[6] != "" {
				return fields[6]
			}
		}
	}
	return "/bin/sh"
}

func (b *Box) worktreeRef(ctx context.Context, location, worktree string) (Location, Worktree, error) {
	loc, err := b.Locations.Get(ctx, location)
	if err != nil {
		return Location{}, Worktree{}, err
	}
	for _, w := range loc.Worktrees {
		if w.Name == worktree {
			return loc, w, nil
		}
	}
	return Location{}, Worktree{}, ErrUnknownWorktree
}

// WorktreeServices reports a worktree's services from the location's config.
func (b *Box) WorktreeServices(ctx context.Context, location, worktree string) ([]ServiceStatus, error) {
	loc, wt, err := b.worktreeRef(ctx, location, worktree)
	if err != nil {
		return nil, err
	}
	cfg, err := b.Locations.Config(ctx, loc.Name)
	if err != nil {
		return nil, err
	}
	out := []ServiceStatus{}
	var sessions []Session
	for _, s := range cfg.Effective.Services {
		st := ServiceStatus{Name: s.Name, Run: s.Run, Autostart: s.Autostart, State: "stopped", Unit: serviceUnit(loc.Name, wt.Name, s.Name), Port: wt.Port}
		if s.Terminal {
			st.Terminal, st.Title, st.Session = true, serviceTitle(s), serviceSession(loc.Name, wt.Name, s.Name)
			if sessions == nil && b.Sessions != nil {
				if sessions, err = b.Sessions.List(ctx); err != nil {
					sessions = []Session{}
				}
			}
			st.State = terminalState(sessions, st.Session)
			if st.State == "running" {
				// Watched since this daemon started it, or from now.
				b.watchTerminalService(loc.Name, wt, s.Name, st.Session)
			}
		} else if b.Units != nil {
			if u, err := b.Units.Get(st.Unit); err == nil {
				st.State = u.State
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// StartService (re)installs and starts one service of a worktree.
func (b *Box) StartService(ctx context.Context, location, worktree, name string) (ServiceStatus, error) {
	loc, wt, err := b.worktreeRef(ctx, location, worktree)
	if err != nil {
		return ServiceStatus{}, err
	}
	cfg, err := b.Locations.Config(ctx, loc.Name)
	if err != nil {
		return ServiceStatus{}, err
	}
	var svc *WorktreeService
	for i := range cfg.Effective.Services {
		if cfg.Effective.Services[i].Name == name {
			svc = &cfg.Effective.Services[i]
		}
	}
	if svc == nil {
		return ServiceStatus{}, ErrUnknownService
	}
	if svc.Terminal && b.Sessions == nil || !svc.Terminal && b.Units == nil {
		return ServiceStatus{}, httpError{http.StatusNotImplemented, "this box cannot run managed units"}
	}
	parts, err := b.worktreeEnv(ctx, loc.Name, wt)
	if err != nil {
		return ServiceStatus{}, err
	}
	envMap := map[string]string{}
	for _, kv := range parts.env {
		k, v, _ := strings.Cut(kv, "=")
		envMap[k] = v
	}
	if svc.Terminal {
		if err := b.startTerminalService(ctx, loc, wt, *svc, envMap, parts.refs); err != nil {
			return ServiceStatus{}, err
		}
		b.Events.Publish(eventf(b, "service.started", map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path, "service": name, "port": wt.Port, "session": serviceSession(loc.Name, wt.Name, name)}))
		return b.serviceStatus(ctx, location, worktree, name)
	}
	// It may have run in a terminal before its config changed.
	b.killServiceSession(ctx, loc.Name, wt.Name, name)
	program, args := loginShell(), []string{"-lc", "cd " + shellQuote(wt.Path) + " && " + svc.Run}
	if len(parts.refs) > 0 {
		// A unit file is on disk and the service manager restarts the
		// service without berthd, so the unit keeps the references and
		// resolves them each time it starts, in `berthd secret exec`.
		wrap, err := b.secretWrap(envMap, parts.refs)
		if err != nil {
			return ServiceStatus{}, err
		}
		program, args = wrap[0], append(append(wrap[1:], program), args...)
	}
	// A unit gets the service manager's groups, from before a group the
	// user joined since; sg gives it them.
	if argv := groups.Wrap(append([]string{program}, args...)); argv[0] != program {
		program, args = argv[0], argv[1:]
	}
	unit := serviceUnit(loc.Name, wt.Name, name)
	if _, err := b.Units.Install(ctx, UnitRequest{
		Name:    unit,
		Program: program,
		Args:    args,
		Env:     envMap,
	}); err != nil {
		return ServiceStatus{}, err
	}
	b.Events.Publish(eventf(b, "service.started", map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path, "service": name, "port": wt.Port}))
	return b.serviceStatus(ctx, location, worktree, name)
}

// StopService stops one service and removes its unit. A service in a
// terminal stops as Ctrl-C would stop it, and its terminal stays, with what
// it printed, for the next start.
func (b *Box) StopService(ctx context.Context, location, worktree, name string) (ServiceStatus, error) {
	loc, wt, err := b.worktreeRef(ctx, location, worktree)
	if err != nil {
		return ServiceStatus{}, err
	}
	if b.Units != nil {
		if _, err := b.Units.Remove(serviceUnit(loc.Name, wt.Name, name)); err != nil && !errors.Is(err, ErrUnknownUnit) {
			return ServiceStatus{}, err
		}
	}
	if b.Sessions != nil {
		if err := b.Sessions.stopService(ctx, serviceSession(loc.Name, wt.Name, name)); err != nil {
			return ServiceStatus{}, err
		}
	}
	b.Events.Publish(eventf(b, "service.stopped", map[string]any{"location": loc.Name, "name": wt.Name, "path": wt.Path, "service": name}))
	return b.serviceStatus(ctx, location, worktree, name)
}

func (b *Box) serviceStatus(ctx context.Context, location, worktree, name string) (ServiceStatus, error) {
	all, err := b.WorktreeServices(ctx, location, worktree)
	if err != nil {
		return ServiceStatus{}, err
	}
	for _, s := range all {
		if s.Name == name {
			return s, nil
		}
	}
	return ServiceStatus{}, ErrUnknownService
}

// haltService stops a running service for a while (a paused worktree), its
// terminal kept for when it starts again.
func (b *Box) haltService(ctx context.Context, s ServiceStatus) {
	if s.Terminal {
		if b.Sessions != nil {
			b.Sessions.stopService(ctx, s.Session)
		}
		return
	}
	if b.Units != nil {
		b.Units.Remove(s.Unit)
	}
}

// startAutostart starts the services that start with every new worktree.
func (b *Box) startAutostart(location, worktree string) {
	ctx := context.Background()
	all, err := b.WorktreeServices(ctx, location, worktree)
	if err != nil {
		return
	}
	for _, s := range all {
		if s.Autostart {
			if _, err := b.StartService(ctx, location, worktree, s.Name); err != nil {
				b.Events.Publish(eventf(b, "service.failed", map[string]any{"location": location, "name": worktree, "service": s.Name, "error": err.Error()}))
			}
		}
	}
}

// stopServices stops every service of a worktree that is going away, before
// its archive script runs: a dev server holding the worktree's database
// would stop the script from dropping it.
func (b *Box) stopServices(location, worktree string) {
	ctx := context.Background()
	all, err := b.WorktreeServices(ctx, location, worktree)
	if err != nil {
		return
	}
	for _, s := range all {
		if b.Units != nil {
			b.Units.Remove(s.Unit)
		}
		// The terminal goes with the worktree.
		b.killServiceSession(ctx, location, worktree, s.Name)
	}
}

func (b *Box) listWorktreeServices(w http.ResponseWriter, r *http.Request) error {
	all, err := b.WorktreeServices(r.Context(), r.PathValue("name"), r.PathValue("worktree"))
	if err != nil {
		return err
	}
	writeJSON(w, all)
	return nil
}

func (b *Box) serviceAction(w http.ResponseWriter, r *http.Request) error {
	loc, wt, svc := r.PathValue("name"), r.PathValue("worktree"), r.PathValue("service")
	var (
		st  ServiceStatus
		err error
	)
	switch a := r.PathValue("action"); a {
	case "start", "stop", "restart":
		if err := b.before(r, "service."+a, map[string]any{"location": loc, "worktree": wt, "service": svc}); err != nil {
			return err
		}
	}
	switch r.PathValue("action") {
	case "start":
		st, err = b.StartService(r.Context(), loc, wt, svc)
	case "stop":
		st, err = b.StopService(r.Context(), loc, wt, svc)
	case "restart":
		if _, err = b.StopService(r.Context(), loc, wt, svc); err == nil {
			st, err = b.StartService(r.Context(), loc, wt, svc)
		}
	default:
		return badRequest("use start, stop or restart")
	}
	if err != nil {
		return err
	}
	writeJSON(w, st)
	return nil
}

func (b *Box) serviceLog(w http.ResponseWriter, r *http.Request) error {
	loc, wt, err := b.worktreeRef(r.Context(), r.PathValue("name"), r.PathValue("worktree"))
	if err != nil {
		return err
	}
	svc := r.PathValue("service")
	// A terminal's own screen and history read better than the raw log.
	if out, ok := b.terminalServiceLog(r.Context(), loc.Name, wt.Name, svc, 256<<10); ok {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(out)
		return nil
	}
	if b.Units == nil {
		return httpError{http.StatusNotImplemented, "this box cannot run managed units"}
	}
	out, err := b.Units.Tail(serviceUnit(loc.Name, wt.Name, svc), 256<<10)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
	return nil
}
