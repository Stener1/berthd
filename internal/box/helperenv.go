package box

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/wire"
)

// A helper learns of a worktree when berthd starts one of its services
// (helperattach.go). A project whose dev servers Shipyard does not start
// never gets there, and what a helper would have been told (a database's
// address, say) is in the worktree's dotenv files all along. So the app may
// ask berthd to look:
//
//	POST /v1/helpers/{plugin}/attach-env   {"location": "acme", "worktree": "fix-orders"}
//
// berthd reads the worktree's dotenv files (dotenv.go: four names in the
// worktree's own folder, and nothing else), keeps the variables the helper's
// manifest names (attach_env), and tells the helper as it does before a
// service starts: the same body, the same token, the same two seconds, with
// ".env" where the service's name would be. A helper files what it is told
// by that name, so looking again replaces what the last look found, and what
// a real service was started with is its own.
//
// Nothing is started, so nothing the helper answers is put anywhere: its
// "env" is dropped, and the worktree is not noted as a service that points
// at the helper (attached.json). When the worktree is removed the helper is
// told as for any other (helperdetach.go).
//
// The answer says what was found and never what it holds: the files read,
// the names of the variables, and the helper's own answer without "env".
// No value is in it, in a log or in an error.
//
// The person may also say which database a worktree has, when nothing else
// does: the same route with "env", the variables themselves,
//
//	{"location": "acme", "worktree": "fix-orders", "env": {"DATABASE_URL": "postgres://…"}}
//
// Then no file is read, and the helper is told those variables (only ones
// its manifest names) with ".manual" where the service's name would be: what
// was said by hand, what the files say and what a service was started with
// are three things, each replaced on its own. An empty "env" takes back what
// was said by hand. A value given this way is a secret like any other: it
// comes in the request's body over Shipyard's own connection, goes to the
// helper, and is not logged, answered, announced or kept by berthd.

// HelperEnvLook is the answer of POST /v1/helpers/{plugin}/attach-env.
type HelperEnvLook struct {
	Plugin   string `json:"plugin"`
	Location string `json:"location"`
	// Worktree is the worktree's name on the box, its main checkout's too.
	Worktree string `json:"worktree"`
	// Command is what the helper was told in place of a service's name:
	// ".env" for a look in the worktree's files, ".manual" for variables the
	// person gave.
	Command string `json:"command"`
	// Files are the dotenv files that were read, in the order they were
	// read: a later one's value for a variable wins.
	Files []string `json:"files"`
	// Skipped are the ones that are there and were not read, and why:
	// "outside" (a link out of the worktree), "not_a_file", "too_large",
	// "not_text" or "unreadable".
	Skipped []dotenvSkipped `json:"skipped"`
	// Variables are the variables of those files that the helper's manifest
	// names: never a value.
	Variables []HelperEnvVariable `json:"variables"`
	// Answer is what the helper answered, as it wrote it, without "env".
	Answer map[string]json.RawMessage `json:"answer"`
}

// HelperEnvVariable is one variable a look found.
type HelperEnvVariable struct {
	Name string `json:"name"`
	// File is the file whose value it is; empty for one the person gave.
	File string `json:"file"`
	// Sent says the helper was told its value. When it was not, Reason says
	// why: "secret" (it names a secret and holds none), "unresolved" (it
	// refers to a variable these files do not set), "empty" or "too_long".
	Sent   bool   `json:"sent"`
	Reason string `json:"reason,omitempty"`
}

// helperManualCommand is the Command a helper is told with variables the
// person gave by hand (AttachGiven).
const helperManualCommand = ".manual"

// AttachEnv tells a running helper what a worktree's dotenv files have of
// the variables its manifest names. about is the worktree, as for a service.
func (h *Helpers) AttachEnv(ctx context.Context, plugin string, about HelperAttach) (HelperEnvLook, error) {
	return h.attachTold(ctx, plugin, about, helperEnvCommand, nil)
}

// AttachGiven tells a running helper the variables a person gave for a
// worktree, in place of what it was last told that way: with none, what was
// given before is taken back. Every one must be a variable the helper's
// manifest names. No file is read, and no error repeats a value, or a name
// that is not one: someone may have put the address where the name goes.
func (h *Helpers) AttachGiven(ctx context.Context, plugin string, about HelperAttach, env map[string]string) (HelperEnvLook, error) {
	if env == nil {
		env = map[string]string{}
	}
	return h.attachTold(ctx, plugin, about, helperManualCommand, env)
}

// attachTold is AttachEnv (given nil: the worktree's files are read) and
// AttachGiven (given: what to tell the helper, and nothing is read).
func (h *Helpers) attachTold(ctx context.Context, plugin string, about HelperAttach, command string, given map[string]string) (HelperEnvLook, error) {
	out := HelperEnvLook{Plugin: plugin, Command: command, Files: []string{}, Skipped: []dotenvSkipped{}, Variables: []HelperEnvVariable{}}
	off := helperProblem(http.StatusConflict, "helper_off", "the %s helper is not running on this box", plugin)
	h.mu.Lock()
	p := h.procs[plugin]
	h.mu.Unlock()
	if p == nil {
		return out, off
	}
	if p.rec.Attach == "" {
		return out, helperProblem(http.StatusConflict, "helper_no_attach", "the %s helper is not one berthd tells of a worktree: its plugin's manifest names no attach path", plugin)
	}
	about.Command, about.Env = command, map[string]string{}
	if given != nil {
		names := make([]string, 0, len(given))
		for name := range given {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			value := given[name]
			switch {
			case !envName.MatchString(name) || len(name) > 64:
				return out, badRequest("env has a name that is not a variable's")
			case !p.rec.matchesEnv(name) || strings.HasPrefix(name, "BERTH_"):
				return out, badRequest("%s is not a variable the %s helper is told: its plugin's manifest does not name it", name, plugin)
			case value == "" || len(value) > maxAttachValue || strings.ContainsRune(value, 0):
				return out, badRequest("%s must be text of 1 to %d bytes", name, maxAttachValue)
			case IsSecretRef(value):
				return out, badRequest("%s names a secret; give the value itself", name)
			}
			about.Env[name] = value
			out.Variables = append(out.Variables, HelperEnvVariable{Name: name, Sent: true})
		}
	}
	timeout := orDuration(h.attachTimeout, 2*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p.waitStarted(ctx, timeout)
	state, api := p.snapshot()
	if state != HelperRunning || api == nil {
		return out, off
	}
	if given == nil {
		// Only now: with no helper to tell, no file is opened.
		set := readDotenvWithin(ctx, about.Path)
		if set == nil {
			return out, helperProblem(http.StatusServiceUnavailable, CodeInternal, "the worktree's .env files could not be read in time")
		}
		out.Files, out.Skipped = set.files, set.skipped
		names := set.names()
		sort.Strings(names)
		for _, name := range names {
			if !p.rec.matchesEnv(name) || strings.HasPrefix(name, "BERTH_") {
				continue
			}
			v := HelperEnvVariable{Name: name, File: set.file(name)}
			value, why := set.value(name, nil)
			if why == "" {
				about.Env[name], v.Sent = value, true
			} else {
				v.Reason = why
			}
			out.Variables = append(out.Variables, v)
		}
	}
	// Also with nothing to say: what it was last told this way is no
	// longer so.
	raw, err := askHelperRaw(ctx, api, p.rec.Attach, about)
	if err == nil && json.Unmarshal(raw, &out.Answer) != nil {
		err = errHelperAnswer
	}
	if err != nil {
		// Never the request or the answer: they hold the environment.
		h.logf("helper %s: not told of %s/%s's %s: %v", plugin, about.Project, about.Worktree, command, err)
		return out, helperProblem(http.StatusBadGateway, "helper_unreachable", "the %s helper was not told: %v", plugin, err)
	}
	// What it would set in a service's environment: there is no service.
	delete(out.Answer, "env")
	if out.Answer == nil {
		out.Answer = map[string]json.RawMessage{}
	}
	return out, nil
}

// wantsMore reports whether the helper names a variable that the
// environment Shipyard assembled for a service (env, and the secrets' names
// in refs) does not settle: a name that is not there, or a prefix, which may
// match anything. Only then are the worktree's dotenv files worth reading
// for it: a project that sets every variable a helper names never has its
// files opened for that helper.
func (s HelperSpec) wantsMore(env, refs map[string]string) bool {
	for _, p := range s.AttachEnv {
		if strings.HasSuffix(p, "*") {
			return true
		}
		_, set := env[p]
		_, secret := refs[p]
		if !set && !secret {
			return true
		}
	}
	return false
}

// readDotenvWithin is readDotenv for as long as ctx lasts: nil when the
// files were not read by then (a folder on a disk that does not answer).
func readDotenvWithin(ctx context.Context, dir string) *dotenvSet {
	if dir == "" {
		return nil
	}
	done := make(chan *dotenvSet, 1)
	go func() { done <- readDotenv(dir) }()
	select {
	case set := <-done:
		return set
	case <-ctx.Done():
		return nil
	}
}

// dotenvForService is what a worktree's dotenv files add to a service's
// environment as its helpers are shown it: the variables that are in the
// files and not in the environment Shipyard assembled (own, and the secrets'
// names in refs), each with its value. A variable that names a secret, that
// refers to something nothing defines, or that is empty or too long is not
// among them, and neither is one of Shipyard's own.
//
// The files are read when the result is first asked for, once, for as long
// as that caller's ctx lasts; one that gave up leaves nothing for the rest,
// so a disk that does not answer costs a service's start one wait.
func dotenvForService(dir string, own, refs map[string]string) func(context.Context) map[string]string {
	var once sync.Once
	var vars map[string]string
	return func(ctx context.Context) map[string]string {
		once.Do(func() {
			set := readDotenvWithin(ctx, dir)
			if set == nil {
				return
			}
			// A reference is filled in from the service's environment first,
			// as the dev server's own loader would find it there.
			outer := func(name string) (string, bool, bool) {
				if v, ok := own[name]; ok {
					return v, true, true
				}
				_, secret := refs[name]
				return "", secret, false
			}
			vars = map[string]string{}
			for _, name := range set.names() {
				_, assembled := own[name]
				_, secret := refs[name]
				if assembled || secret || strings.HasPrefix(name, "BERTH_") {
					continue
				}
				if v, why := set.value(name, outer); why == "" {
					vars[name] = v
				}
			}
		})
		return vars
	}
}

// helperAttachEnv is POST /v1/helpers/{plugin}/attach-env.
//
// Like installing a helper, it is a paired laptop's to ask: the laptop is
// where the person allowed the plugin and what its helper is told, and
// opening the plugin's tab there is the asking. What runs on the box itself
// cannot have a helper told of a worktree.
//
// With "env" in the body the helper is told those variables and no file is
// read (AttachGiven); they are never in a gate's data, an event or a log.
func (b *Box) helperAttachEnv(w http.ResponseWriter, r *http.Request) error {
	plugin := r.PathValue("plugin")
	h, err := b.helpers(plugin)
	if err != nil {
		return err
	}
	if wire.IsLocal(r.Context()) {
		return helperProblem(http.StatusForbidden, CodeRefused, "a plugin's helper is told of a worktree's .env files from a paired laptop, where the plugin was allowed")
	}
	var req struct {
		Location string `json:"location"`
		// Worktree is its name; empty is the location's main checkout.
		Worktree string `json:"worktree"`
		// Env, when it is there at all, is what to tell the helper in place
		// of reading the worktree's files; empty takes it back.
		Env *map[string]string `json:"env"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Location == "" {
		return badRequest("location is required")
	}
	loc, err := b.Locations.Get(r.Context(), req.Location)
	if err != nil {
		return err
	}
	var wt *Worktree
	for i, w := range loc.Worktrees {
		if w.Name == req.Worktree || req.Worktree == "" && w.Main {
			wt = &loc.Worktrees[i]
			break
		}
	}
	if wt == nil {
		return ErrUnknownWorktree
	}
	from := helperEnvCommand
	if req.Env != nil {
		from = helperManualCommand
	}
	// Never what was given: a gate sees which helper, which worktree and how.
	if err := b.before(r, "helper.attach", map[string]any{"plugin": plugin, "location": loc.Name, "worktree": wt.Name, "path": wt.Path, "from": from}); err != nil {
		return err
	}
	about := HelperAttach{Box: b.Name, Project: loc.Name, Worktree: wt.Name, Branch: wt.Branch, Path: wt.Path}
	if wt.Main {
		about.Worktree = ""
	}
	var look HelperEnvLook
	if req.Env != nil {
		look, err = h.AttachGiven(r.Context(), plugin, about, *req.Env)
	} else {
		look, err = h.AttachEnv(r.Context(), plugin, about)
	}
	if err != nil {
		return err
	}
	look.Location, look.Worktree = loc.Name, wt.Name
	writeJSON(w, look)
	return nil
}
