package box

import (
	"time"

	"github.com/sean-brydon/berthd/internal/team"
)

// TeamBundle is a team setup on its way to a box: what the laptop read
// through its own gh at the commit the engineer reviewed, so the box needs
// no GitHub access for .berth itself. Repositories the box clones with its
// own gh sign-in (the "github" step).
type TeamBundle struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Org    string `json:"org"`
	Commit string `json:"commit"`
	// Script runs the steps (team.Box.Script, inside Files).
	Script string      `json:"script,omitempty"`
	Steps  []team.Step `json:"steps"`
	// GitHub adds Berth's own step: gh auth login on the box.
	GitHub bool `json:"github"`
	// Files are .berth's files at Commit, path → base64.
	Files    map[string]string `json:"files"`
	Projects []TeamProjectPlan `json:"projects"`
	// Start, when set, starts from that step rather than the first.
	Start string `json:"start,omitempty"`
}

// TeamProjectPlan is one repository to clone and set up.
type TeamProjectPlan struct {
	ID   string `json:"id"`
	Repo string `json:"repo"`
	// URL is what git clones; the box's gh sign-in authenticates it.
	URL      string `json:"url"`
	Path     string `json:"path"`
	Required bool   `json:"required,omitempty"`
	// Source is "repo", "kit" or "none".
	Source string `json:"source"`
	// TrustHash is the sha256 of the repository's .berth/config.json the
	// engineer reviewed: the clone's file is trusted only if it is that one.
	TrustHash string      `json:"trust_hash,omitempty"`
	Kit       *KitInstall `json:"kit,omitempty"`
	// Init, a path inside the bundle's files, runs once in the clone.
	Init      string `json:"init,omitempty"`
	FirstTask string `json:"first_task,omitempty"`
	// Env is laid into the project's own config on this box: secret
	// references for shared keys, and the values the engineer typed.
	Env map[string]string `json:"env,omitempty"`
}

// Team step and project states.
const (
	TeamTodo      = "todo"
	TeamRunning   = "running"
	TeamWaiting   = "waiting"
	TeamDone      = "done"
	TeamSkipped   = "skipped"
	TeamFailed    = "failed"
	TeamQueued    = "queued"
	TeamCloning   = "cloning"
	TeamSettingUp = "setting-up"
	TeamReady     = "ready"
)

// TeamStatus is how a team setup stands on a box.
type TeamStatus struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Org    string `json:"org"`
	Commit string `json:"commit"`
	Box    string `json:"box"`
	// Phase is steps, projects, done or failed.
	Phase string `json:"phase"`
	// Session is the box terminal the steps run in.
	Session  string              `json:"session,omitempty"`
	Steps    []TeamStepStatus    `json:"steps"`
	Projects []TeamProjectStatus `json:"projects"`
	// KeysSet are "project/KEY" the box has a value or reference for.
	KeysSet []string  `json:"keys_set"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	Error   string    `json:"error,omitempty"`
}

type TeamStepStatus struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Sudo  bool   `json:"sudo"`
	State string `json:"state"`
	Secs  int    `json:"secs,omitempty"`
	Error string `json:"error,omitempty"`
	// Code and URL are GitHub's device code while the github step waits.
	Code    string     `json:"code,omitempty"`
	URL     string     `json:"url,omitempty"`
	Started *time.Time `json:"started,omitempty"`
}

type TeamProjectStatus struct {
	ID       string   `json:"id"`
	Repo     string   `json:"repo"`
	State    string   `json:"state"`
	Location string   `json:"location,omitempty"`
	Error    string   `json:"error,omitempty"`
	Trust    string   `json:"trust,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	// Line is the latest line of what it is doing (git's progress, say).
	Line string `json:"line,omitempty"`
}
