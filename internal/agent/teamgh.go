package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GitHub, for team setups, goes through the laptop's own gh CLI: Berth has
// no GitHub sign-in of its own and keeps no token. Reading <org>/.berth and
// each repository with the person's gh is also the access check — if gh can
// read it, they may set it up. The box signs in with its own gh, in a
// terminal there, so it never gets this laptop's credential.

var errGHNotFound = errors.New("not found")

// ghMissing is returned when gh is not installed.
var ghMissing = errors.New("the GitHub CLI (gh) is not installed")

// ghDirs are where gh is looked for beyond PATH: the agent may run from
// launchd, whose PATH lacks Homebrew's.
var ghDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/home/linuxbrew/.linuxbrew/bin"}

// findGH finds the gh executable.
func findGH() (string, error) {
	if p, err := exec.LookPath("gh"); err == nil {
		return p, nil
	}
	dirs := ghDirs
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append([]string{filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin")}, dirs...)
	}
	for _, d := range dirs {
		p := filepath.Join(d, "gh")
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", ghMissing
}

type ghCLI struct{ bin string }

func newGH() (ghCLI, error) {
	bin, err := findGH()
	return ghCLI{bin: bin}, err
}

// run runs gh with args and returns what it printed.
func (g ghCLI) run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.bin, args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// api runs `gh api PATH` and decodes its JSON into out. A 404 is
// errGHNotFound: GitHub answers 404 both for what doesn't exist and for
// what this account can't read.
func (g ghCLI) api(ctx context.Context, path string, out any) error {
	stdout, stderr, err := g.run(ctx, "api", path)
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if strings.Contains(msg, "HTTP 404") {
			return errGHNotFound
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("gh api %s: %s", path, strings.TrimPrefix(msg, "gh: "))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("gh api %s: %w", path, err)
	}
	return nil
}

// GitHubState is whether this laptop's gh can read GitHub for a team setup.
type GitHubState struct {
	// State is missing (gh not installed), signed-out, or ready.
	State     string     `json:"state"`
	Login     string     `json:"login,omitempty"`
	Name      string     `json:"name,omitempty"`
	AvatarURL string     `json:"avatar_url,omitempty"`
	Install   *GHInstall `json:"install,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// GHInstall is how to install gh on this computer.
type GHInstall struct {
	OS      string `json:"os"`
	Command string `json:"command"`
	URL     string `json:"url"`
}

func ghInstallHint(goos string) *GHInstall {
	switch goos {
	case "darwin":
		return &GHInstall{OS: "darwin", Command: "brew install gh", URL: "https://cli.github.com"}
	case "windows":
		return &GHInstall{OS: "windows", Command: "winget install --id GitHub.cli", URL: "https://cli.github.com"}
	default:
		return &GHInstall{OS: "linux", Command: "sudo apt install gh   # or your distribution's package; see the link", URL: "https://github.com/cli/cli/blob/trunk/docs/install_linux.md"}
	}
}

// githubState checks gh is installed and signed in, and as whom.
func githubState(ctx context.Context) GitHubState {
	g, err := newGH()
	if err != nil {
		return GitHubState{State: "missing", Install: ghInstallHint(runtime.GOOS)}
	}
	st := GitHubState{State: "signed-out"}
	if _, stderr, err := g.run(ctx, "auth", "status", "--hostname", "github.com"); err != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" && !strings.Contains(msg, "not logged") {
			st.Error = firstLine(msg)
		}
		return st
	}
	var u struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := g.api(ctx, "user", &u); err != nil {
		st.Error = err.Error()
		return st
	}
	st.State, st.Login, st.Name, st.AvatarURL = "ready", u.Login, u.Name, u.AvatarURL
	return st
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// ghLoginCommand is what "Connect GitHub" runs in a terminal on this laptop.
const ghLoginCommand = "gh auth login --hostname github.com --git-protocol https --web"

// openTerminal runs a command in a new terminal window on this computer;
// tests replace it.
var openTerminal = func(script string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-a", "Terminal", script).Run()
	case "linux":
		for _, term := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xterm"} {
			if p, err := exec.LookPath(term); err == nil {
				if term == "gnome-terminal" {
					return exec.Command(p, "--", script).Start()
				}
				return exec.Command(p, "-e", script).Start()
			}
		}
		return errors.New("no terminal app found")
	}
	return errors.New("opening a terminal is not supported here")
}

// openGHLogin opens a terminal running gh auth login. The app checks
// again (GET /v1/team/github) until gh is signed in.
func (a *Agent) openGHLogin() (bool, error) {
	g, err := newGH()
	if err != nil {
		return false, err
	}
	dir := filepath.Join(a.cfg.Dir, "team")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	script := filepath.Join(dir, "gh-login.command")
	body := fmt.Sprintf("#!/bin/sh\n# Berth: sign this computer's GitHub CLI in, so Berth can read your team's setup.\n# Berth keeps no token; gh does, and you can sign out with: gh auth logout\nclear\n%q auth login --hostname github.com --git-protocol https --web\necho\necho \"Done. Berth checks again on its own; you can close this window.\"\n", g.bin)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return false, err
	}
	if err := openTerminal(script); err != nil {
		return false, err
	}
	return true, nil
}

// ghFile reads one file of a repository at ref ("" for its default
// branch) and its blob sha.
func (g ghCLI) file(ctx context.Context, slug, path, ref string) ([]byte, string, error) {
	p := "repos/" + slug + "/contents/" + path
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var f struct {
		Type     string `json:"type"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		SHA      string `json:"sha"`
	}
	if err := g.api(ctx, p, &f); err != nil {
		return nil, "", err
	}
	if f.Type != "file" || f.Encoding != "base64" {
		return nil, "", fmt.Errorf("%s in %s is not a file", path, slug)
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	return b, f.SHA, err
}

// ghCommit is a commit as Berth shows it.
type ghCommit struct {
	SHA          string `json:"sha"`
	Short        string `json:"short"`
	Author       string `json:"author"`
	AuthorAvatar string `json:"author_avatar,omitempty"`
	Date         string `json:"date"`
	Message      string `json:"message"`
	tree         string
}

func (g ghCLI) commit(ctx context.Context, slug, ref string) (ghCommit, error) {
	var c struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		} `json:"commit"`
		Author *struct {
			Login     string `json:"login"`
			AvatarURL string `json:"avatar_url"`
		} `json:"author"`
	}
	if err := g.api(ctx, "repos/"+slug+"/commits/"+url.PathEscape(ref), &c); err != nil {
		return ghCommit{}, err
	}
	out := ghCommit{SHA: c.SHA, Short: shortSHA(c.SHA), Author: c.Commit.Author.Name, Date: c.Commit.Author.Date, Message: firstLine(c.Commit.Message), tree: c.Commit.Tree.SHA}
	if c.Author != nil && c.Author.Login != "" {
		out.Author, out.AuthorAvatar = c.Author.Login, c.Author.AvatarURL
	}
	return out, nil
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// maxTeamFile and maxTeamFiles bound what a team setup (or a kit read
// through gh) may carry: scripts and small configs, not assets.
const (
	maxTeamFile  = 1 << 20
	maxTeamFiles = 200
	maxTeamBytes = 6 << 20
)

// tree reads every file under sub ("" for all) of tree sha, path → bytes,
// with paths relative to sub.
func (g ghCLI) tree(ctx context.Context, slug, treeSHA, sub string) (map[string][]byte, error) {
	var t struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
			Mode string `json:"mode"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := g.api(ctx, "repos/"+slug+"/git/trees/"+treeSHA+"?recursive=1", &t); err != nil {
		return nil, err
	}
	if t.Truncated {
		return nil, fmt.Errorf("%s has more files than a team setup carries", slug)
	}
	prefix := ""
	if sub != "" {
		prefix = strings.Trim(sub, "/") + "/"
	}
	files := map[string][]byte{}
	total := int64(0)
	for _, e := range t.Tree {
		if e.Type != "blob" || !strings.HasPrefix(e.Path, prefix) || e.Mode == "120000" {
			continue
		}
		rel := strings.TrimPrefix(e.Path, prefix)
		if len(files) >= maxTeamFiles {
			return nil, fmt.Errorf("%s carries more than %d files", slug, maxTeamFiles)
		}
		if e.Size > maxTeamFile {
			return nil, fmt.Errorf("%s/%s is larger than 1 MB", slug, e.Path)
		}
		if total += e.Size; total > maxTeamBytes {
			return nil, fmt.Errorf("%s carries more than 6 MB", slug)
		}
		var blob struct {
			Content string `json:"content"`
		}
		if err := g.api(ctx, "repos/"+slug+"/git/blobs/"+e.SHA, &blob); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(blob.Content, "\n", ""))
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", slug, e.Path, err)
		}
		files[rel] = data
	}
	return files, nil
}

// ghRepo is a repository as the access check sees it.
type ghRepo struct {
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	Description   string `json:"description"`
	PushedAt      string `json:"pushed_at"`
	Size          int64  `json:"size"`
}

// repo reads a repository; errGHNotFound means this account can't read it.
func (g ghCLI) repo(ctx context.Context, slug string) (ghRepo, error) {
	var r ghRepo
	err := g.api(ctx, "repos/"+slug, &r)
	return r, err
}
