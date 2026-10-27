package box

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cosscom/shipyard/examples"
)

// Adding projects and starting worktrees from what people actually have in
// hand: a folder on the box, a clone URL, a PR number, an issue link, or a
// branch name.

// FolderEntry is a folder in a listing, with git repositories marked.
type FolderEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Git  bool   `json:"git"`
	Slug string `json:"slug,omitempty"`
}

type Folder struct {
	Path    string        `json:"path"`
	Parent  string        `json:"parent,omitempty"`
	Home    string        `json:"home"`
	Entries []FolderEntry `json:"entries"`
}

const maxFolderEntries = 500

// GitProtocols are the transports a clone may use. Pinning them keeps a
// link from reaching git's ext:: (a command) or file:: helpers, whatever git
// version or environment the box has. Tests add file.
var GitProtocols = "https:http:ssh:git"

func (b *Box) listFolder(w http.ResponseWriter, r *http.Request) error {
	home, _ := os.UserHomeDir()
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "~/work"
		if info, err := os.Stat(expandHome(p)); err != nil || !info.IsDir() {
			p = "~"
		}
	}
	dir, err := filepath.Abs(expandHome(p))
	if err != nil {
		return badRequest("%v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return badRequest("%s: %v", dir, err)
	}
	hidden := r.URL.Query().Get("hidden") == "1"
	out := Folder{Path: dir, Home: home, Entries: []FolderEntry{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = parent
	}
	for _, e := range ents {
		if len(out.Entries) >= maxFolderEntries {
			break
		}
		if !hidden && strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		info, err := os.Stat(full) // follows symlinks to folders
		if err != nil || !info.IsDir() {
			continue
		}
		fe := FolderEntry{Name: e.Name(), Path: full}
		if _, err := os.Stat(filepath.Join(full, ".git")); err == nil {
			fe.Git = true
			fe.Slug = slugOf(remoteURL(r.Context(), full))
		}
		out.Entries = append(out.Entries, fe)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	writeJSON(w, out)
	return nil
}

func remoteURL(ctx context.Context, repo string) string {
	out, err := git(ctx, "-C", repo, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var scpLike = regexp.MustCompile(`^[\w.-]+@([\w.-]+):(.+)$`)

// slugOf turns a remote URL into "owner/repo", whatever its form.
func slugOf(remote string) string {
	if remote == "" {
		return ""
	}
	path := ""
	if m := scpLike.FindStringSubmatch(remote); m != nil {
		path = m[2]
	} else if u, err := url.Parse(remote); err == nil && u.Host != "" {
		path = u.Path
	} else {
		return ""
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return path
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// GitHubSlug is "owner/repo" for a remote on github.com, in any form
// (https://, ssh://, git@github.com:, or an SSH alias named github.com-*),
// and "" for any other host: GitHub Enterprise and other forges are not
// what gh reads by default.
func GitHubSlug(remote string) string {
	host := ""
	if m := scpLike.FindStringSubmatch(remote); m != nil {
		host = m[1]
	} else if u, err := url.Parse(remote); err == nil {
		host = u.Hostname()
	}
	host = strings.ToLower(host)
	switch {
	case host == "github.com", host == "www.github.com", host == "ssh.github.com", strings.HasPrefix(host, "github.com-"):
	default:
		return ""
	}
	s := slugOf(remote)
	if owner, name, ok := strings.Cut(s, "/"); !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	return s
}

// repoName is the folder a clone of remote would get.
func repoName(remote string) string {
	s := slugOf(remote)
	if s == "" {
		s = strings.TrimSuffix(strings.TrimRight(remote, "/"), ".git")
	}
	return filepath.Base(s)
}

func defaultBranch(ctx context.Context, repo string) string {
	if out, err := git(ctx, "-C", repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
	}
	if out, err := git(ctx, "-C", repo, "symbolic-ref", "--short", "HEAD"); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// freeLocationName picks name, or name-2, name-3… if a location has it.
func (b *Box) freeLocationName(ctx context.Context, name string) string {
	all, _ := b.Locations.List(ctx)
	taken := map[string]bool{}
	for _, l := range all {
		taken[l.Name] = true
	}
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", name, i); !taken[n] {
			return n
		}
	}
}

// cloneLocation clones a repository and adds it, streaming git's progress.
func (b *Box) cloneLocation(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		URL    string `json:"url"`
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" || strings.HasPrefix(req.URL, "-") {
		return badRequest("a repository URL is needed")
	}
	if req.Parent == "" {
		req.Parent = "~/work"
	}
	folder := req.Name
	if folder == "" {
		folder = repoName(req.URL)
	}
	if !validFolder(folder) {
		return badRequest("%q is not a folder name", folder)
	}
	dest := filepath.Join(expandHome(req.Parent), folder)
	if _, err := os.Stat(dest); err == nil {
		return badRequest("%s already exists; add it with Browse folder instead", dest)
	}
	name := b.freeLocationName(r.Context(), folder)
	if err := b.before(r, "location.add", map[string]any{"location": name, "path": dest, "url": req.URL}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	enc := json.NewEncoder(w)
	send := func(v any) { enc.Encode(v); rc.Flush() }

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--progress", "--", req.URL, dest)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL="+GitProtocols)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		send(map[string]any{"done": true, "error": err.Error()})
		return nil
	}
	if err := cmd.Start(); err != nil {
		send(map[string]any{"done": true, "error": err.Error()})
		return nil
	}
	sc := bufio.NewScanner(stderr)
	// git redraws progress with \r; each redraw is a line here.
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, c := range data {
			if c == '\n' || c == '\r' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	var last string
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			last = line
			send(map[string]any{"line": line})
		}
	}
	if err := cmd.Wait(); err != nil {
		os.RemoveAll(dest)
		send(map[string]any{"done": true, "error": strings.TrimPrefix(last, "fatal: ")})
		return nil
	}
	loc, err := b.Locations.Add(context.Background(), name, dest)
	if err != nil {
		send(map[string]any{"done": true, "error": err.Error()})
		return nil
	}
	b.publish(r, "location.added", map[string]any{"location": loc.Name, "path": loc.Path, "url": req.URL})
	send(map[string]any{"done": true, "location": loc})
	return nil
}

var folderName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

func validFolder(name string) bool { return folderName.MatchString(name) }

// newLocation makes an empty git repository with one commit, so it can
// have worktrees straight away, and adds it.
func (b *Box) newLocation(w http.ResponseWriter, r *http.Request) error {
	// Sample, when set, names a sample project (package examples) to write
	// into the new repository instead of leaving it empty.
	var req struct{ Parent, Name, Sample string }
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Sample != "" && !slices.Contains(examples.Samples, req.Sample) {
		return badRequest("there is no sample named %q", req.Sample)
	}
	if req.Name == "" {
		req.Name = req.Sample
	}
	if !validFolder(req.Name) {
		return badRequest("%q is not a folder name", req.Name)
	}
	if req.Parent == "" {
		req.Parent = "~/work"
	}
	dest := filepath.Join(expandHome(req.Parent), req.Name)
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		return badRequest("%s already exists and is not empty", dest)
	}
	name := b.freeLocationName(r.Context(), req.Name)
	if err := b.before(r, "location.add", map[string]any{"location": name, "path": dest}); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if out, err := git(r.Context(), "-C", dest, "init", "-q", "-b", "main"); err != nil {
		return fmt.Errorf("git init: %s", strings.TrimSpace(string(out)))
	}
	commit := []string{"-C", dest}
	if out, _ := git(r.Context(), "-C", dest, "config", "user.email"); strings.TrimSpace(string(out)) == "" {
		commit = append(commit, "-c", "user.name=berth", "-c", "user.email=berth@localhost")
	}
	message := "Initial commit"
	if req.Sample != "" {
		if err := writeSample(req.Sample, dest); err != nil {
			return err
		}
		if out, err := git(r.Context(), "-C", dest, "add", "-A"); err != nil {
			return fmt.Errorf("git add: %s", strings.TrimSpace(string(out)))
		}
		message = "The " + req.Sample + " sample project"
	}
	commit = append(commit, "commit", "-q", "--allow-empty", "-m", message)
	if out, err := git(r.Context(), commit...); err != nil {
		return fmt.Errorf("git commit: %s", strings.TrimSpace(string(out)))
	}
	loc, err := b.Locations.Add(r.Context(), name, dest)
	if err != nil {
		return err
	}
	b.publish(r, "location.added", map[string]any{"location": loc.Name, "path": loc.Path})
	writeJSON(w, loc)
	return nil
}

// writeSample copies a sample project's files into dir.
func writeSample(sample, dir string) error {
	return fs.WalkDir(examples.FS, sample, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		to := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(p, sample), "/")))
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := examples.FS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
}

// Branch is one branch a worktree could start from or check out.
type Branch struct {
	Name    string `json:"name"`
	Remote  bool   `json:"remote"`
	Current bool   `json:"current,omitempty"`
}

func (b *Box) listBranches(w http.ResponseWriter, r *http.Request) error {
	loc, err := b.Locations.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		return err
	}
	out, err := git(r.Context(), "-C", loc.Path, "for-each-ref", "--sort=-committerdate", "--format=%(refname)", "refs/heads", "refs/remotes/origin")
	if err != nil {
		return fmt.Errorf("git for-each-ref: %s", strings.TrimSpace(string(out)))
	}
	current := defaultBranchOfCheckout(r.Context(), loc.Path)
	seen := map[string]bool{}
	branches := []Branch{}
	for _, ref := range strings.Fields(string(out)) {
		name, local := strings.CutPrefix(ref, "refs/heads/")
		if !local {
			name = strings.TrimPrefix(ref, "refs/remotes/origin/")
			if name == "HEAD" {
				continue
			}
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		branches = append(branches, Branch{Name: name, Remote: !local, Current: name == current})
	}
	writeJSON(w, map[string]any{"default": defaultBranch(r.Context(), loc.Path), "branches": branches})
	return nil
}

func defaultBranchOfCheckout(ctx context.Context, repo string) string {
	out, err := git(ctx, "-C", repo, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Resolution is what a "create from" input will make.
type Resolution struct {
	Kind   string `json:"kind"` // pr, issue, branch, remote-branch, name
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Base   string `json:"base,omitempty"`
	PR     int    `json:"pr,omitempty"`
	// Ref is fetched into Branch when the branch is not otherwise
	// reachable: a PR's or merge request's head.
	Ref   string `json:"ref,omitempty"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url,omitempty"`
	// Exists is true when Branch is already a branch here or on origin.
	Exists bool   `json:"exists,omitempty"`
	Note   string `json:"note,omitempty"`
}

var (
	githubURL = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+/[^/]+)/(pull|issues|tree)/(.+?)/?$`)
	gitlabURL = regexp.MustCompile(`^https?://[^/]*gitlab[^/]*/(.+?)/-/(merge_requests|issues|tree)/(.+?)/?$`)
	jiraURL   = regexp.MustCompile(`^https?://[^/]+/browse/([A-Z][A-Z0-9]+-\d+)`)
	issueKey  = regexp.MustCompile(`^([A-Z][A-Z0-9]+-\d+)$`)
	number    = regexp.MustCompile(`^#?(\d+)$`)
	nonSlug   = regexp.MustCompile(`[^a-z0-9]+`)
)

// slug makes a worktree-safe name from free text.
func slug(s string, max int) string {
	s = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > max {
		s = strings.TrimRight(s[:max], "-")
	}
	return s
}

// worktreeNameFor is the last part of a branch, as a valid worktree name.
func worktreeNameFor(branch string) string {
	base := branch
	if i := strings.LastIndex(branch, "/"); i >= 0 {
		base = branch[i+1:]
	}
	if n := slug(base, 60); n != "" {
		return n
	}
	return slug(branch, 60)
}

func (b *Box) resolve(w http.ResponseWriter, r *http.Request) error {
	var req struct{ Input, Kind string }
	if err := decode(r, &req); err != nil {
		return err
	}
	loc, err := b.Locations.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		return err
	}
	res, err := resolveInput(r.Context(), loc.Path, strings.TrimSpace(req.Input), req.Kind)
	if err != nil {
		return badRequest("%v", err)
	}
	if res.Base == "" && !res.Exists && res.Ref == "" {
		res.Base = defaultBranch(r.Context(), loc.Path)
	}
	writeJSON(w, res)
	return nil
}

func resolveInput(ctx context.Context, repo, input, kind string) (Resolution, error) {
	if input == "" {
		return Resolution{}, fmt.Errorf("type a name, #1234, a branch, or a link")
	}
	if kind == "name" {
		n := slug(input, 60)
		return Resolution{Kind: "name", Name: n, Branch: n, Exists: branchHere(ctx, repo, n)}, nil
	}
	if kind == "branch" {
		return branchResolution(ctx, repo, input), nil
	}
	if m := githubURL.FindStringSubmatch(input); m != nil {
		switch m[2] {
		case "pull":
			if n, err := strconv.Atoi(strings.SplitN(m[3], "/", 2)[0]); err == nil {
				return githubPR(ctx, repo, n, m[1]), nil
			}
		case "issues":
			if n, err := strconv.Atoi(strings.SplitN(m[3], "/", 2)[0]); err == nil {
				return githubIssue(ctx, repo, n, input, m[1]), nil
			}
		case "tree":
			return branchResolution(ctx, repo, m[3]), nil
		}
	}
	if m := gitlabURL.FindStringSubmatch(input); m != nil {
		n, _ := strconv.Atoi(strings.SplitN(m[3], "/", 2)[0])
		switch {
		case m[2] == "merge_requests" && n > 0:
			return Resolution{Kind: "pr", PR: n, Name: fmt.Sprintf("mr-%d", n), Branch: fmt.Sprintf("mr-%d", n),
				Ref: fmt.Sprintf("merge-requests/%d/head", n), URL: input}, nil
		case m[2] == "issues" && n > 0:
			name := fmt.Sprintf("issue-%d", n)
			return Resolution{Kind: "issue", Name: name, Branch: name, URL: input}, nil
		case m[2] == "tree":
			return branchResolution(ctx, repo, m[3]), nil
		}
	}
	if m := jiraURL.FindStringSubmatch(input); m != nil {
		n := strings.ToLower(m[1])
		return Resolution{Kind: "issue", Name: n, Branch: n, URL: input, Title: m[1]}, nil
	}
	if m := issueKey.FindStringSubmatch(input); m != nil {
		n := strings.ToLower(m[1])
		return Resolution{Kind: "issue", Name: n, Branch: n, Title: m[1]}, nil
	}
	if m := number.FindStringSubmatch(input); m != nil {
		n, _ := strconv.Atoi(m[1])
		if kind == "gitlab" {
			return Resolution{Kind: "pr", PR: n, Name: fmt.Sprintf("mr-%d", n), Branch: fmt.Sprintf("mr-%d", n), Ref: fmt.Sprintf("merge-requests/%d/head", n)}, nil
		}
		return githubNumber(ctx, repo, n), nil
	}
	if !strings.ContainsAny(input, " \t") {
		if r := branchResolution(ctx, repo, input); r.Exists {
			return r, nil
		}
	}
	n := slug(input, 60)
	if n == "" {
		return Resolution{}, fmt.Errorf("%q does not make a name", input)
	}
	if !strings.ContainsAny(input, " \t") && validBranch(ctx, repo, input) {
		// A new branch keeps the name as typed, slashes and all.
		return Resolution{Kind: "name", Name: worktreeNameFor(input), Branch: input}, nil
	}
	return Resolution{Kind: "name", Name: n, Branch: n}, nil
}

func validBranch(ctx context.Context, repo, name string) bool {
	_, err := git(ctx, "-C", repo, "check-ref-format", "--branch", name)
	return err == nil && !strings.HasPrefix(name, "-")
}

func branchHere(ctx context.Context, repo, name string) bool {
	return branchExists(ctx, repo, "refs/heads/"+name) || branchExists(ctx, repo, "refs/remotes/origin/"+name)
}

func branchResolution(ctx context.Context, repo, name string) Resolution {
	r := Resolution{Kind: "name", Name: worktreeNameFor(name), Branch: name}
	switch {
	case branchExists(ctx, repo, "refs/heads/"+name):
		r.Kind, r.Exists = "branch", true
	case branchExists(ctx, repo, "refs/remotes/origin/"+name):
		r.Kind, r.Exists = "remote-branch", true
	}
	return r
}

// gh runs the GitHub CLI in repo, when the box has it and it is signed in.
func gh(ctx context.Context, repo string, out any, args ...string) error {
	bin, err := toolPath("gh")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0")
	b, err := cmd.Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// githubPR asks about PR n of the checkout's repository, or of other when a
// link names another one.
func githubPR(ctx context.Context, repo string, n int, other ...string) Resolution {
	var pr struct {
		HeadRefName string `json:"headRefName"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		BaseRefName string `json:"baseRefName"`
	}
	r := Resolution{Kind: "pr", PR: n, Ref: fmt.Sprintf("pull/%d/head", n)}
	args := append([]string{"pr", "view", strconv.Itoa(n), "--json", "headRefName,title,url,baseRefName"}, repoFlag(other)...)
	if err := gh(ctx, repo, &pr, args...); err != nil || pr.HeadRefName == "" {
		r.Name, r.Branch = fmt.Sprintf("pr-%d", n), fmt.Sprintf("pr-%d", n)
		r.Note = "the box could not ask GitHub about it (is gh installed and signed in?); the PR's head is fetched as pr-" + strconv.Itoa(n)
		return r
	}
	r.Branch, r.Title, r.URL = pr.HeadRefName, pr.Title, pr.URL
	r.Name = worktreeNameFor(pr.HeadRefName)
	r.Exists = branchHere(ctx, repo, pr.HeadRefName)
	return r
}

func githubIssue(ctx context.Context, repo string, n int, link string, other ...string) Resolution {
	var issue struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	name := fmt.Sprintf("issue-%d", n)
	r := Resolution{Kind: "issue", Name: name, Branch: name, URL: link}
	args := append([]string{"issue", "view", strconv.Itoa(n), "--json", "title,url"}, repoFlag(other)...)
	if err := gh(ctx, repo, &issue, args...); err == nil && issue.Title != "" {
		r.Title, r.URL = issue.Title, issue.URL
		if s := slug(issue.Title, 40); s != "" {
			r.Name = fmt.Sprintf("issue-%d-%s", n, s)
			r.Branch = r.Name
		}
	}
	return r
}

func repoFlag(other []string) []string {
	if len(other) > 0 && other[0] != "" {
		return []string{"--repo", other[0]}
	}
	return nil
}

// githubNumber is a bare #1234: a PR if GitHub knows one by that number,
// otherwise an issue.
func githubNumber(ctx context.Context, repo string, n int) Resolution {
	pr := githubPR(ctx, repo, n)
	if pr.Note == "" {
		return pr
	}
	if issue := githubIssue(ctx, repo, n, ""); issue.Title != "" {
		return issue
	}
	return pr
}
