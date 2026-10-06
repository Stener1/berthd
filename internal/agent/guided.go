package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/sean-brydon/berthd/internal/agentcli"
	"github.com/sean-brydon/berthd/internal/guided"
	"github.com/sean-brydon/berthd/internal/terminal"
	"github.com/sean-brydon/berthd/internal/trust"
)

// The guided install: the app shows the plan (GET /v1/ssh/install-plan),
// then runs `berth add ssh` in a pseudo-terminal here, on the laptop,
// relayed over a WebSocket (GET /v1/boxes/add-ssh/terminal) to a terminal
// it shows full screen. The person presses Enter there to start and types
// sudo's password when it asks on the box: those bytes pass through this
// relay to ssh and are never read, kept or logged. The CLI prints each
// step's state as a marker (guided.StepsEnv); the relay takes the markers
// out of what the terminal shows and sends them as JSON text messages
// ({"type":"step",...}) for the checklist beside it, then {"type":"exit"}.

// installRequest is what the app asks to set up.
type installRequest struct {
	Host, Name, Network, Address, Identity, TrustHostKey, Listen, From string
	Agents                                                             []string
	NoIntegrations                                                     bool
}

func readInstallRequest(q map[string][]string) (installRequest, error) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	r := installRequest{Host: get("host"), Name: get("name"), Network: get("network"), Address: get("address"), Identity: get("identity"), TrustHostKey: get("trust_host_key"), Listen: get("listen"), From: get("from"), NoIntegrations: get("no_integrations") == "1"}
	if err := argOK(r.Host, r.Name, r.Network, r.Address, r.Identity, r.TrustHostKey, r.Listen, r.From); err != nil || r.Host == "" {
		return r, errors.New("an SSH host is needed, like me@devbox")
	}
	if r.Network != "" && !trust.ValidName(r.Network) {
		return r, errors.New("not a network name: " + r.Network)
	}
	if r.TrustHostKey != "" && !strings.HasPrefix(r.TrustHostKey, "SHA256:") {
		return r, errors.New("trust_host_key must be a SHA256:… fingerprint")
	}
	if r.From != "" && guided.Index(r.From) < 0 {
		return r, errors.New("no step " + r.From)
	}
	agents, err := agentcli.ParseList(get("agents"))
	if err != nil {
		return r, err
	}
	r.Agents = agents
	return r, nil
}

func (r installRequest) args() []string {
	args := []string{"add", "ssh", r.Host, "--agents", strings.Join(r.Agents, ",")}
	if len(r.Agents) == 0 {
		args[4] = "none"
	}
	for _, f := range [][2]string{{"--name", r.Name}, {"--network", r.Network}, {"--address", r.Address}, {"--identity", r.Identity}, {"--trust-host-key", r.TrustHostKey}, {"--listen", r.Listen}, {"--from", r.From}} {
		if f[1] != "" {
			args = append(args, f[0], f[1])
		}
	}
	if r.NoIntegrations {
		args = append(args, "--no-integrations")
	}
	return args
}

// installMessage is a JSON text message on the install terminal's socket.
type installMessage struct {
	Type string `json:"type"`
	guided.Event
	// SSH is a failed login, explained (sshsetup.Failure), for "failure".
	SSH json.RawMessage `json:"ssh,omitempty"`
	// Code is the command's exit status, for "exit".
	Code int `json:"code"`
}

func (a *Agent) guidedRoutes(mux *http.ServeMux) {
	// The plan before connecting: each step, which need sudo, and the
	// exact commands. The app shows it, with the agents to choose.
	mux.HandleFunc("GET /v1/ssh/install-plan", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		host := strings.TrimSpace(q.Get("host"))
		if host == "" {
			host = "me@box"
		}
		agents, err := agentcli.ParseList(q.Get("agents"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		exe, _ := a.cliPath()
		bundled := bundledTmux(exe)
		writeJSON(w, http.StatusOK, map[string]any{
			"steps":  guided.Plan(guided.Options{Target: host, Agents: agents, Listen: q.Get("listen"), NoIntegrations: q.Get("no_integrations") == "1", Ask: true, BundledTmux: bundled}, nil),
			"agents": agentcli.Catalog,
			"tmux":   map[string]bool{"bundled": bundled},
		})
	})
	mux.HandleFunc("GET /v1/boxes/add-ssh/terminal", a.addSSHTerminal)
}

// bundledTmux is whether berth carries Berth's tmux for Linux boxes, beside
// it as it carries the daemons.
func bundledTmux(exe string) bool {
	for _, arch := range []string{"amd64", "arm64"} {
		if !besideCLI(exe, guided.TmuxName(arch)) {
			return false
		}
	}
	return true
}

func (a *Agent) addSSHTerminal(w http.ResponseWriter, r *http.Request) {
	req, err := readInstallRequest(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	finished, err := a.work.begin("berth add ssh")
	if err != nil {
		writeCoded(w, http.StatusServiceUnavailable, err.Error(), "agent_restarting")
		return
	}
	defer finished()
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost", "localhost:14[23][0-9]", "tauri.localhost"},
	})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Minute)
	defer cancel()

	var mu sync.Mutex
	send := func(m installMessage) {
		b, _ := json.Marshal(m)
		mu.Lock()
		defer mu.Unlock()
		ws.Write(ctx, websocket.MessageText, b)
	}
	cmd := a.cli(ctx, req.args()...)
	cmd.Env = append(cmd.Env, guided.StepsEnv+"=1", "TERM=xterm-256color")
	master, err := terminal.Start(cmd, max(cols, 40), max(rows, 10))
	if err != nil {
		send(installMessage{Type: "exit", Code: -1, Event: guided.Event{Message: err.Error()}})
		ws.Close(websocket.StatusInternalError, "could not start")
		return
	}
	defer master.Close()
	// What the person types goes to the terminal, and nowhere else.
	go func() {
		defer cancel()
		for {
			typ, msg, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				var m struct {
					Type       string `json:"type"`
					Cols, Rows int
				}
				if json.Unmarshal(msg, &m) == nil && m.Type == "resize" {
					terminal.Resize(master, m.Cols, m.Rows)
				}
				continue
			}
			if _, err := master.Write(msg); err != nil {
				return
			}
		}
	}()
	out := &wsWriter{ctx: ctx, ws: ws, mu: &mu}
	filter := &guided.Filter{
		Out:       out,
		OnStep:    func(e guided.Event) { send(installMessage{Type: "step", Event: e}) },
		OnFailure: func(raw json.RawMessage) { send(installMessage{Type: "failure", SSH: raw}) },
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	io.Copy(filter, master)
	filter.Flush()
	code := 0
	if err := <-done; err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	send(installMessage{Type: "exit", Code: code})
	ws.Close(websocket.StatusNormalClosure, "done")
	a.checkSoon()
}

// wsWriter sends terminal output as binary messages.
type wsWriter struct {
	ctx context.Context
	ws  *websocket.Conn
	mu  *sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ws.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// besideCLI reports whether a file berth uploads to boxes is beside it (or
// in the app's Resources), where readDaemon in cmd/berth looks.
func besideCLI(exe, name string) bool {
	if exe == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	for _, p := range []string{filepath.Join(dir, name), filepath.Join(dir, "..", "Resources", name)} {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	return false
}
