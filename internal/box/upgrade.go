package box

import (
	"context"
	"errors"
	"fmt"
	"github.com/sean-brydon/berthd/internal/integrations/adapters"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/sean-brydon/berthd/internal/version"
)

// maxDaemonSize bounds an uploaded daemon; real builds are under 10 MB.
const maxDaemonSize = 64 << 20

// Info describes the running daemon, so a laptop can pick the right build to
// upload and tell whether the box already runs it.
type Info struct {
	Name  string `json:"name"`
	OS    string `json:"os"`
	Arch  string `json:"arch"`
	Build string `json:"build"`
	// User and Home are the account berthd runs as, which is the one to
	// log in as over SSH, for editors.
	User  string   `json:"user,omitempty"`
	Home  string   `json:"home,omitempty"`
	Tools []string `json:"tools"`
	// Agents are the agent presets this box can start.
	Agents []AgentPreset `json:"agents"`
	// Capabilities name the API features this box has, so clients can use
	// them when present: "turns" (turn IDs from send, turn waits),
	// "journal" (GET /v1/events?since=SEQ).
	Capabilities []string `json:"capabilities"`
	// Adapters say what each agent can report, for the app.
	Adapters map[string]adapters.Caps `json:"adapters,omitempty"`
}

// BuildID identifies a daemon build by its bytes.
func BuildID(binary []byte) string { return version.BuildID(binary) }

// SelfUpdate replaces the running daemon with an uploaded build, over the same
// authenticated connection as everything else, so upgrading never needs SSH.
type SelfUpdate struct {
	// Executable is the running daemon's path.
	Executable string
	// Fingerprint is what `<new build> id` must print: proof the new build
	// runs on this box and reads the same identity.
	Fingerprint string
	// BeforeRestart runs just before the new build takes over; berthd
	// stops public shares so none outlive the daemon that manages them.
	BeforeRestart func()
	// Restart starts the new build in place of this process. The default
	// re-executes it with the same arguments, keeping the PID, so the
	// supervisor sees no restart and agent sessions are untouched.
	Restart func(path string) error
}

func (u *SelfUpdate) info() (Info, error) {
	b, err := os.ReadFile(u.Executable)
	if err != nil {
		return Info{}, err
	}
	return Info{OS: runtime.GOOS, Arch: runtime.GOARCH, Build: BuildID(b), Tools: Tools()}, nil
}

// Install verifies binary and swaps it in for the running executable. The old
// build stays in place if anything goes wrong.
func (u *SelfUpdate) Install(ctx context.Context, binary []byte) error {
	tmp := u.Executable + ".new"
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tmp, "id").Output()
	if err != nil || strings.TrimSpace(string(out)) != u.Fingerprint {
		os.Remove(tmp)
		if err == nil {
			err = errors.New("it reported a different identity")
		}
		return fmt.Errorf("the uploaded berthd does not run on this box: %v", err)
	}
	return os.Rename(tmp, u.Executable)
}

func (u *SelfUpdate) restart() error {
	if u.Restart != nil {
		return u.Restart(u.Executable)
	}
	return syscall.Exec(u.Executable, os.Args, os.Environ())
}

func (b *Box) handleInfo(w http.ResponseWriter, r *http.Request) error {
	if b.Update == nil {
		return httpError{http.StatusNotFound, "this box cannot report its build"}
	}
	i, err := b.Update.info()
	if err != nil {
		return err
	}
	i.Name, i.Agents = b.Name, Presets(nil)
	if u, err := user.Current(); err == nil {
		i.User = u.Username
	}
	i.Home, _ = os.UserHomeDir()
	if i.Agents == nil {
		i.Agents = []AgentPreset{}
	}
	i.Capabilities = b.Capabilities()
	i.Adapters = map[string]adapters.Caps{}
	for _, a := range adapters.All() {
		i.Adapters[a.Name] = a.Caps
	}
	writeJSON(w, i)
	return nil
}

// Capabilities are the optional API features this box serves.
func (b *Box) Capabilities() []string {
	// diff: GET .../diff; titles: sessions carry a title (PATCH
	// /v1/sessions/{name} renames); sample: POST /v1/locations/new makes a
	// sample project; history: older transcript pages, helpers' own
	// conversations, fork and rewind (history.go); commands: GET
	// .../commands and .../files for the chat's "/" and "@".
	// service.terminal: a service can run in a terminal of its own
	// ("terminal": true), listed as a session with "service" set. answer:
	// POST .../answer fills in Claude Code's question form (answer.go).
	// session.home: POST /v1/sessions takes "home": true, a terminal in
	// the box user's home folder, tied to no worktree. files: a worktree's
	// ⌘P list, its files read and written with etags, and what the agents'
	// latest turns touched (worktreefiles.go, touched.go). files.dir:
	// GET .../files?dir= lists one folder for the Files panel
	// (worktreefolder.go), and touched files say live.
	// agents.install: GET /v1/agents and POST /v1/agents/install, agent
	// CLIs added to the box without sudo (agentinstall.go).
	caps := []string{"transcript", "diff", "titles", "sample", "history", "commands", "service.terminal", "answer", "session.home", "files", "files.dir", "agents.install"}
	if b.Turns != nil {
		// controls: POST .../keys, .../interrupt and .../mode, GET
		// .../controls (controls.go).
		caps = append(caps, "turns", "queue", "ask", "controls")
	}
	// draft: GET .../draft, the reply Claude Code is writing, read from its
	// screen (draft.go); and transcript.changed when a shown chat's
	// transcript is written to (transcriptwatch.go).
	caps = append(caps, "draft")
	if b.Runs != nil {
		caps = append(caps, "runs", "exec.detach")
	}
	if b.Team != nil {
		// team: GET/POST /v1/team and POST /v1/team/{id}/retry run team
		// setups (team.go).
		caps = append(caps, "team")
	}
	if b.Browsers != nil {
		// browser.health: GET /v1/browser/health, PUT /v1/browser/settings
		// and POST /v1/browser/check (browsersandbox.go).
		caps = append(caps, "browser", "browser.health")
	}
	if b.Events.Journal != nil {
		caps = append(caps, "journal")
	}
	return caps
}

func (b *Box) handleUpgrade(w http.ResponseWriter, r *http.Request) error {
	if b.Update == nil {
		return httpError{http.StatusNotFound, "this box cannot upgrade itself"}
	}
	binary, err := io.ReadAll(io.LimitReader(r.Body, maxDaemonSize+1))
	if err != nil {
		return err
	}
	if len(binary) > maxDaemonSize {
		return badRequest("uploaded daemon is too large")
	}
	if err := b.before(r, "box.upgrade", map[string]any{"build": BuildID(binary)}); err != nil {
		return err
	}
	if err := b.Update.Install(r.Context(), binary); err != nil {
		return err
	}
	b.publish(r, "box.upgraded", map[string]any{"build": BuildID(binary)})
	writeJSON(w, map[string]string{"build": BuildID(binary)})
	http.NewResponseController(w).Flush()
	go func() {
		// Let the reply reach the laptop before this process is replaced.
		time.Sleep(300 * time.Millisecond)
		if b.Update.BeforeRestart != nil {
			b.Update.BeforeRestart()
		}
		if err := b.Update.restart(); err != nil {
			fmt.Fprintf(os.Stderr, "berthd: restarting into the new build failed: %v\n", err)
		}
	}()
	return nil
}
