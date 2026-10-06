package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sean-brydon/berthd/internal/hooks"
	"github.com/sean-brydon/berthd/internal/sshsetup"
	"github.com/sean-brydon/berthd/internal/trust"
)

// Adding, pairing, upgrading and forgetting boxes are the CLI's job, and the
// app asks for them here: the agent runs its own berth binary, so the app and
// the CLI can never do these differently. Long ones stream their output.

func (a *Agent) cliPath() (string, error) {
	if a.cfg.CLI != "" {
		return a.cfg.CLI, nil
	}
	return os.Executable()
}

func (a *Agent) cli(ctx context.Context, args ...string) *exec.Cmd {
	bin, err := a.cliPath()
	if err != nil {
		bin = "berth"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	// The CLI finds this laptop's state the same way the agent did.
	cmd.Env = append(os.Environ(), "BERTH_HOME="+filepath.Dir(a.cfg.Dir), sshsetup.FailureEnv+"=1")
	return cmd
}

// sshFinder finds key agents on this computer; tests replace it.
var sshFinder = sshsetup.DefaultFinder

// argOK refuses values that the CLI would read as flags.
func argOK(values ...string) error {
	for _, v := range values {
		if strings.HasPrefix(v, "-") || strings.ContainsAny(v, "\x00\n") {
			return errors.New("invalid value " + v)
		}
	}
	return nil
}

// runJSON runs a CLI command that prints JSON and returns its output as is.
func (a *Agent) runJSON(w http.ResponseWriter, r *http.Request, args ...string) {
	done, err := a.work.begin("berth " + args[0])
	if err != nil {
		writeCoded(w, http.StatusServiceUnavailable, err.Error(), "agent_restarting")
		return
	}
	defer done()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := a.cli(ctx, args...).Output()
	if err != nil {
		writeError(w, http.StatusBadRequest, cliError(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

func cliError(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return strings.TrimPrefix(strings.TrimSpace(string(ee.Stderr)), "berth: ")
	}
	return err.Error()
}

// StreamLine is one line of a long CLI command's progress; the last one has
// Done set, and Error when it failed.
type StreamLine struct {
	Line  string `json:"line,omitempty"`
	Done  bool   `json:"done,omitempty"`
	Error string `json:"error,omitempty"`
	// SSH explains a failed SSH login (sshsetup.Failure), with Error.
	SSH json.RawMessage `json:"ssh,omitempty"`
	// Box names the box a successful set up paired (Use this Mac).
	Box string `json:"box,omitempty"`
}

// runStream runs a CLI command and streams its output as NDJSON lines.
func (a *Agent) runStream(w http.ResponseWriter, r *http.Request, timeout time.Duration, args ...string) {
	finished, err := a.work.begin("berth " + strings.Join(args[:min(2, len(args))], " "))
	if err != nil {
		writeCoded(w, http.StatusServiceUnavailable, err.Error(), "agent_restarting")
		return
	}
	defer finished()
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	cmd := a.cli(ctx, args...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	send := func(l StreamLine) {
		enc.Encode(l)
		rc.Flush()
	}
	if err := cmd.Start(); err != nil {
		send(StreamLine{Done: true, Error: err.Error()})
		return
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); pw.Close() }()
	// The CLI's own "berth: …" error is the stream's error, not also a line
	// of output: held back until another line follows it, and dropped when
	// the command then fails with it.
	var last, held string
	// errText is the CLI's own error, from its "berth: " line to the end:
	// an error can go on over lines, ending with a command to run, and
	// only all of it says what to do.
	var errText []string
	var failure json.RawMessage
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if rest, ok := strings.CutPrefix(line, sshsetup.FailurePrefix); ok && json.Valid([]byte(rest)) {
			failure = json.RawMessage(rest)
			continue
		}
		if held != "" && line == "" {
			continue
		}
		if held != "" {
			send(StreamLine{Line: held})
			held = ""
		}
		if line != "" {
			last = line
			if errText != nil {
				errText = append(errText, line)
			}
		}
		if strings.HasPrefix(line, "berth: ") {
			held = line
			errText = []string{strings.TrimPrefix(line, "berth: ")}
			continue
		}
		send(StreamLine{Line: line})
	}
	if err := <-done; err != nil {
		msg := strings.TrimPrefix(last, "berth: ")
		if len(errText) > 0 {
			msg = strings.Join(errText, "\n")
		}
		if msg == "" {
			msg = err.Error()
		}
		send(StreamLine{Done: true, Error: msg, SSH: failure})
		return
	}
	if held != "" {
		send(StreamLine{Line: held})
	}
	send(StreamLine{Done: true})
}

func (a *Agent) manageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/discover", func(w http.ResponseWriter, r *http.Request) {
		args := []string{"discover", "--json"}
		if n := r.URL.Query().Get("network"); n != "" {
			if err := argOK(n); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			args = append(args, "--network", n)
		}
		a.runJSON(w, r, args...)
	})
	mux.HandleFunc("POST /v1/boxes/pair", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Link, Name, Network string }
		if !decodeBody(w, r, &req) {
			return
		}
		if err := argOK(req.Link, req.Name, req.Network); err != nil || req.Link == "" {
			writeError(w, http.StatusBadRequest, "a pairing link is needed")
			return
		}
		if req.Network != "" && !trust.ValidName(req.Network) {
			writeError(w, http.StatusBadRequest, "not a network name: "+req.Network)
			return
		}
		args := []string{"pair", req.Link, "--json"}
		if req.Name != "" {
			args = append(args, "--name", req.Name)
		}
		if req.Network != "" {
			args = append(args, "--network", req.Network)
		}
		a.runJSON(w, r, args...)
		a.checkSoon()
	})
	mux.HandleFunc("POST /v1/boxes/add-ssh", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Host, Name, Network, Address string
			Identity                     string `json:"identity"`
			TrustHostKey                 string `json:"trust_host_key"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		if err := argOK(req.Host, req.Name, req.Network, req.Address, req.Identity, req.TrustHostKey); err != nil || req.Host == "" {
			writeError(w, http.StatusBadRequest, "an SSH host is needed, like me@devbox")
			return
		}
		if req.Network != "" && !trust.ValidName(req.Network) {
			writeError(w, http.StatusBadRequest, "not a network name: "+req.Network)
			return
		}
		if req.TrustHostKey != "" && !strings.HasPrefix(req.TrustHostKey, "SHA256:") {
			writeError(w, http.StatusBadRequest, "trust_host_key must be a SHA256:… fingerprint")
			return
		}
		args := []string{"add", "ssh", req.Host}
		for _, f := range [][2]string{{"--name", req.Name}, {"--network", req.Network}, {"--address", req.Address}, {"--identity", req.Identity}, {"--trust-host-key", req.TrustHostKey}} {
			if f[1] != "" {
				args = append(args, f[0], f[1])
			}
		}
		a.runStream(w, r, 10*time.Minute, args...)
		a.checkSoon()
	})
	// What berth will log in with, before connecting: ssh -G for the host
	// (~/.ssh/config applied) and which key agent answers.
	mux.HandleFunc("GET /v1/ssh/plan", func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimSpace(r.URL.Query().Get("host"))
		if err := argOK(host); err != nil || host == "" {
			writeError(w, http.StatusBadRequest, "an SSH host is needed, like me@devbox")
			return
		}
		cfg, err := sshsetup.ReadConfig(r.Context(), nil, host)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sshFinder().MakePlan(host, cfg, ""))
	})
	// The hosts ~/.ssh/config names, for completing a host field.
	mux.HandleFunc("GET /v1/ssh/hosts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, sshsetup.Hosts(sshFinder().Home))
	})
	mux.HandleFunc("POST /v1/boxes/{box}/upgrade", func(w http.ResponseWriter, r *http.Request) {
		if err := argOK(r.PathValue("box")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.runStream(w, r, 5*time.Minute, "upgrade", r.PathValue("box"))
		// The box's build has changed (or not): check it again next time.
		a.outdated.forget()
	})
	mux.HandleFunc("DELETE /v1/boxes/{box}", func(w http.ResponseWriter, r *http.Request) {
		box := r.PathValue("box")
		if err := argOK(box); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := a.cli(r.Context(), "forget", box).Output(); err != nil {
			writeError(w, http.StatusBadRequest, cliError(err))
			return
		}
		a.sync()
		writeJSON(w, http.StatusOK, map[string]string{"removed": box})
	})
	// Port 80 on loopback drops the port from URLs; macOS asks for an
	// administrator password in its own dialog.
	mux.HandleFunc("POST /v1/setup/port80", func(w http.ResponseWriter, r *http.Request) {
		args := []string{"setup", "port80"}
		if r.URL.Query().Get("remove") == "1" {
			args = append(args, "--remove")
		}
		a.runStream(w, r, 3*time.Minute, args...)
	})

	mux.HandleFunc("GET /v1/hooks", a.getHooks)
	mux.HandleFunc("PUT /v1/hooks", func(w http.ResponseWriter, r *http.Request) {
		var doc struct {
			Hooks []hooks.Hook `json:"hooks"`
		}
		if !decodeBody(w, r, &doc) {
			return
		}
		if err := a.hooks.Save(doc.Hooks); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.publish(Event{Type: "hooks.changed"})
		a.getHooks(w, r)
	})
	mux.HandleFunc("POST /v1/plugins/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		id, action := r.PathValue("id"), r.PathValue("action")
		dir := filepath.Join(a.cfg.UserDir, "plugins", id)
		if strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
			writeError(w, http.StatusNotFound, "no such plugin")
			return
		}
		if _, err := os.Stat(filepath.Join(dir, hooks.PluginManifest)); err != nil {
			writeError(w, http.StatusNotFound, "no such plugin")
			return
		}
		var err error
		switch action {
		case "enable":
			// A plugin runs with the app's access and its hooks run here, so
			// it is turned on only with the hash of the files the user
			// reviewed, and only while those are still the files.
			var req struct {
				Hash string `json:"hash"`
			}
			if !decodeBody(w, r, &req) {
				return
			}
			if req.Hash == "" {
				writeError(w, http.StatusBadRequest, "review the plugin in the app to turn it on")
				return
			}
			if err = hooks.AllowPlugin(dir, req.Hash); errors.Is(err, hooks.ErrPluginChanged) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			if err == nil {
				// An older way of turning a plugin off.
				if err = os.Remove(filepath.Join(dir, "disabled")); os.IsNotExist(err) {
					err = nil
				}
			}
		case "disable":
			err = hooks.ForbidPlugin(dir)
		default:
			writeError(w, http.StatusNotFound, "use enable or disable")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, p := range a.plugins() {
			if p.ID == id {
				writeJSON(w, http.StatusOK, p)
				return
			}
		}
	})
}

func (a *Agent) getHooks(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.hooks.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg.Hooks == nil {
		cfg.Hooks = []hooks.Hook{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": a.hooks.Path, "hooks": cfg.Hooks})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}
