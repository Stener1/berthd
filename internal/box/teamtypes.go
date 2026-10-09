package box

import (
	"time"

	"github.com/cosscom/shipyard/internal/team"
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
	// GitHub adds Shipyard's own step: gh auth login on the box.
	GitHub bool `json:"github"`
	// OnePassword adds Shipyard's other step, after GitHub, when the keys are
	// 1Password references: op signed in on the box, in its terminal.
	OnePassword bool `json:"onepassword,omitempty"`
	// OnePasswordSkipped says the engineer skipped 1Password: the shared
	// keys' references are kept aside (each project's Deferred), op is
	// never called for them, and Use 1Password puts them back later.
	OnePasswordSkipped bool `json:"onepassword_skipped,omitempty"`
	// Agents adds Shipyard's agents step, after the team's own: these agent
	// CLIs installed on the box, with their hooks and skills.
	Agents []string `json:"agents,omitempty"`
	// Settings are team.json's box.settings, which the script (and each
	// project's init) gets as BERTH_SETTING_<NAME>.
	Settings map[string]string `json:"settings,omitempty"`
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
	// ReviewButton is team.json's review_button: PRs opened from the
	// project's worktrees get a "Review in Shipyard" button, unless the
	// box's own config or the repository's says otherwise.
	ReviewButton bool `json:"review_button,omitempty"`
	// Env is laid into the project's own config on this box: secret
	// references for shared keys, and the values the engineer typed.
	Env map[string]string `json:"env,omitempty"`
	// Keys are the names of every key the team lists for the project
	// (shared and asked), so the box can say which are still missing.
	Keys []string `json:"keys,omitempty"`
	// Ask are the keys each engineer has their own of (team.json's "ask"):
	// a pull request opened for review never gets them.
	Ask []string `json:"ask,omitempty"`
	// Deferred are the shared keys' 1Password references, name → op://…,
	// when the engineer skipped 1Password: not in the project's config, so
	// nothing reads them with op, until Use 1Password lays them in.
	Deferred map[string]string `json:"deferred,omitempty"`
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
	KeysSet []string `json:"keys_set"`
	// OnePasswordSkipped says 1Password was skipped for the shared keys,
	// which Use 1Password (POST /v1/team/{id}/onepassword) can undo.
	OnePasswordSkipped bool      `json:"onepassword_skipped,omitempty"`
	Started            time.Time `json:"started"`
	Updated            time.Time `json:"updated"`
	Error              string    `json:"error,omitempty"`
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
	// Line is the latest line of what it is doing (git's progress, then its
	// init terminal's last line).
	Line string `json:"line,omitempty"`
	// Session is the terminal its init runs in, and Waiting says that
	// terminal waits for an answer (a [Y/n], a password): the project is
	// not ready until someone gives it there.
	Session string `json:"session,omitempty"`
	Waiting bool   `json:"waiting,omitempty"`
	// Keys are the names of the keys the team lists for it; Missing, those
	// its config on this box has no value for yet (left blank when asked),
	// to add in Project settings. Missing is worked out when read.
	Keys    []string `json:"keys,omitempty"`
	Missing []string `json:"missing,omitempty"`
	// Deferred are the keys 1Password would give it, while skipped.
	Deferred []string `json:"deferred,omitempty"`
}
