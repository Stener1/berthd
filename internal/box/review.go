package box

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Review lists agents' finished work: each worktree whose agent has
// finished its turn (or is waiting for someone) and left changes, with
// what changed against HEAD and which commits are ahead of the base branch,
// so the app can show one inbox of work to approve, send back or discard.

// ReviewFile is one changed file and its line counts.
type ReviewFile struct {
	Path    string `json:"path"`
	From    string `json:"from,omitempty"`
	Code    string `json:"code"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary,omitempty"`
}

// ReviewCommit is a commit on the branch that the base does not have.
type ReviewCommit struct {
	SHA     string    `json:"sha"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	When    time.Time `json:"when"`
}

// ReviewItem is one worktree with an agent's work in it.
type ReviewItem struct {
	Location string `json:"location"`
	Worktree string `json:"worktree"`
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	// Head is the commit checked out, so a reviewed state can be told
	// apart from new work.
	Head string `json:"head,omitempty"`
	Main bool   `json:"main,omitempty"`
	// Base is the ref the branch is compared with, such as origin/main.
	Base string `json:"base,omitempty"`
	// Upstream, Ahead and Behind compare the branch with what it tracks.
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	// Files are uncommitted changes, untracked files included.
	Files   []ReviewFile `json:"files"`
	Added   int          `json:"added"`
	Removed int          `json:"removed"`
	// Commits and Committed are what the branch has that Base does not.
	Commits   []ReviewCommit `json:"commits"`
	BaseAhead int            `json:"base_ahead"`
	Committed []ReviewFile   `json:"committed"`
	// The agent whose turn ended here.
	Session    string    `json:"session"`
	Agent      string    `json:"agent"`
	AgentState string    `json:"agent_state"`
	StateSince time.Time `json:"state_since,omitzero"`
	// Browser is what the worktree's agent browser left: its last shots,
	// the URL it ended on, and its console errors.
	Browser *BrowserArtifacts `json:"browser,omitempty"`
}

const maxReviewCommits = 20

func (b *Box) review(w http.ResponseWriter, r *http.Request) error {
	if id := r.URL.Query().Get("run"); id != "" {
		return b.reviewRun(w, r, id)
	}
	items, err := b.Review(r.Context(), r.URL.Query().Get("all") == "1")
	if err != nil {
		return err
	}
	writeJSON(w, items)
	return nil
}

// Review gathers the worktrees with work to review. With all set it
// includes worktrees whose agent is still working.
func (b *Box) Review(ctx context.Context, all bool) ([]ReviewItem, error) {
	sessions, err := b.Sessions.List(ctx)
	if err != nil {
		return nil, err
	}
	rank := map[string]int{"waiting": 3, "finished": 2, "idle": 1, "running": 0}
	type pick struct {
		loc  Location
		wt   Worktree
		sess Session
	}
	byPath := map[string]pick{}
	for _, s := range b.enrich(ctx, sessions) {
		if s.Agent == "" || s.Exited {
			continue
		}
		// Work turn check read as still going on is not ready for review yet.
		if !all && (s.AgentState != "finished" && s.AgentState != "waiting" || s.Unfinished) {
			continue
		}
		loc, wt, ok := b.worktreeAt(ctx, s.Dir)
		if !ok {
			continue
		}
		cur, seen := byPath[wt.Path]
		if seen && (rank[cur.sess.AgentState] > rank[s.AgentState] || (rank[cur.sess.AgentState] == rank[s.AgentState] && cur.sess.StateSince.After(s.StateSince))) {
			continue
		}
		byPath[wt.Path] = pick{loc, wt, s}
	}

	out := make([]ReviewItem, 0, len(byPath))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, p := range byPath {
		wg.Add(1)
		go func(p pick) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item := gitReview(ctx, p.loc, p.wt)
			if len(item.Files) == 0 && item.BaseAhead == 0 && item.Ahead == 0 {
				return
			}
			item.Session, item.Agent, item.AgentState, item.StateSince = p.sess.Name, p.sess.Agent, p.sess.AgentState, p.sess.StateSince
			if b.Browsers != nil {
				item.Browser = b.Browsers.Artifacts(p.loc.Name, p.wt.Name, p.wt.Path)
			}
			mu.Lock()
			out = append(out, item)
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].StateSince.After(out[j].StateSince) })
	return out, nil
}

// gitReview reads a worktree's changes and its commits ahead of the base.
func gitReview(ctx context.Context, loc Location, wt Worktree) ReviewItem {
	item := ReviewItem{Location: loc.Name, Worktree: wt.Name, Path: wt.Path, Branch: wt.Branch, Main: wt.Main, Files: []ReviewFile{}, Commits: []ReviewCommit{}, Committed: []ReviewFile{}}
	if out, err := git(ctx, "-C", wt.Path, "status", "--porcelain=v1", "-b", "-z"); err == nil {
		item.Files = parsePorcelain(out, &item)
	}
	if out, err := git(ctx, "-C", wt.Path, "diff", "--numstat", "HEAD"); err == nil {
		applyNumstat(item.Files, out)
	}
	if out, err := git(ctx, "-C", wt.Path, "rev-parse", "HEAD"); err == nil {
		item.Head = strings.TrimSpace(string(out))
	}
	for i := range item.Files {
		f := &item.Files[i]
		if f.Code == "??" {
			f.Added, f.Binary = countLines(filepath.Join(wt.Path, f.Path))
		}
		item.Added += f.Added
		item.Removed += f.Removed
	}

	def := defaultBranch(ctx, loc.Path)
	switch {
	case def != "" && branchExists(ctx, wt.Path, "refs/remotes/origin/"+def):
		item.Base = "origin/" + def
	case def != "":
		item.Base = def
	}
	// A worktree on the default branch has nothing ahead of it, except the
	// main checkout against origin.
	if item.Base == "" || (item.Branch == def && (!wt.Main || item.Base == def)) {
		return item
	}
	if out, err := git(ctx, "-C", wt.Path, "rev-list", "--count", item.Base+"..HEAD"); err == nil {
		item.BaseAhead, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	}
	if item.BaseAhead == 0 {
		return item
	}
	if out, err := git(ctx, "-C", wt.Path, "log", "-n", strconv.Itoa(maxReviewCommits), "--format=%H%x1f%s%x1f%an%x1f%cI", item.Base+"..HEAD"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Split(line, "\x1f")
			if len(f) != 4 {
				continue
			}
			when, _ := time.Parse(time.RFC3339, f[3])
			item.Commits = append(item.Commits, ReviewCommit{SHA: f[0], Subject: f[1], Author: f[2], When: when})
		}
	}
	if out, err := git(ctx, "-C", wt.Path, "diff", "--name-status", "-M", "-z", item.Base+"...HEAD"); err == nil {
		item.Committed = parseNameStatus(out)
	}
	if out, err := git(ctx, "-C", wt.Path, "diff", "--numstat", "-M", item.Base+"...HEAD"); err == nil {
		applyNumstat(item.Committed, out)
	}
	return item
}

// parsePorcelain reads `git status --porcelain=v1 -b -z`, filling the
// branch's upstream and ahead/behind counts into item.
func parsePorcelain(out []byte, item *ReviewItem) []ReviewFile {
	files := []ReviewFile{}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if e == "" {
			continue
		}
		if strings.HasPrefix(e, "## ") {
			head := strings.TrimPrefix(e, "## ")
			head = strings.TrimPrefix(head, "No commits yet on ")
			if j := strings.Index(head, " ["); j >= 0 {
				counts := strings.TrimSuffix(head[j+2:], "]")
				head = head[:j]
				for _, part := range strings.Split(counts, ", ") {
					if n, ok := strings.CutPrefix(part, "ahead "); ok {
						item.Ahead, _ = strconv.Atoi(n)
					}
					if n, ok := strings.CutPrefix(part, "behind "); ok {
						item.Behind, _ = strconv.Atoi(n)
					}
				}
			}
			if branch, upstream, ok := strings.Cut(head, "..."); ok {
				item.Upstream = upstream
				if item.Branch == "" {
					item.Branch = branch
				}
			}
			continue
		}
		if len(e) < 4 {
			continue
		}
		f := ReviewFile{Code: e[:2], Path: e[3:]}
		// A rename or copy is followed by the path it came from.
		if (f.Code[0] == 'R' || f.Code[0] == 'C') && i+1 < len(entries) {
			i++
			f.From = entries[i]
		}
		files = append(files, f)
	}
	return files
}

// parseNameStatus reads `git diff --name-status -z`: a status, then one
// path, or two for a rename or copy.
func parseNameStatus(out []byte) []ReviewFile {
	files := []ReviewFile{}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		code := entries[i]
		if code == "" || i+1 >= len(entries) {
			continue
		}
		f := ReviewFile{Code: code[:1] + " "}
		if code[0] == 'R' || code[0] == 'C' {
			if i+2 >= len(entries) {
				break
			}
			f.From, f.Path = entries[i+1], entries[i+2]
			i += 2
		} else {
			f.Path = entries[i+1]
			i++
		}
		files = append(files, f)
	}
	return files
}

// applyNumstat adds `git diff --numstat` line counts to files. Renames are
// printed as "old => new" or "dir/{old => new}".
func applyNumstat(files []ReviewFile, out []byte) {
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		for i := range files {
			f := &files[i]
			if path != f.Path && !strings.HasSuffix(path, "=> "+f.Path) && !strings.HasSuffix(path, "=> "+filepath.Base(f.Path)+"}") {
				continue
			}
			if parts[0] == "-" {
				f.Binary = true
			} else {
				f.Added, _ = strconv.Atoi(parts[0])
				f.Removed, _ = strconv.Atoi(parts[1])
			}
			break
		}
	}
}

// countLines counts an untracked file's lines, the way git would add them.
// Large files are not read; binary files count as none.
func countLines(path string) (int, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > 1<<20 {
		return 0, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return 0, true
	}
	n := bytes.Count(b, []byte("\n"))
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n, false
}
