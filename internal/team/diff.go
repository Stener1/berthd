package team

import (
	"fmt"
	"sort"
	"strings"
)

// Change is one difference between two commits of a team setup, as an
// engineer reviews an update.
type Change struct {
	// Kind is add, remove or change; Area is step, project, key, file or
	// setup.
	Kind   string `json:"kind"`
	Area   string `json:"area"`
	ID     string `json:"id"`
	Text   string `json:"text"`
	Detail string `json:"detail,omitempty"`
	// Sudo marks a new or changed step that asks for the password.
	Sudo bool `json:"sudo,omitempty"`
}

// Diff lists what changed from old to new: steps, projects, keys and the
// files .berth carries (path → content; nil maps compare no files). The
// box script's own changes are reported per file, since a step's
// subcommand may change without its title changing.
func Diff(old, new *Setup, oldFiles, newFiles map[string]string) []Change {
	var out []Change
	if old.Name != new.Name {
		out = append(out, Change{Kind: "change", Area: "setup", ID: "name", Text: fmt.Sprintf("Name: %s → %s", old.Name, new.Name)})
	}
	oldSteps := map[string]Step{}
	for _, s := range old.Box.Steps {
		oldSteps[s.ID] = s
	}
	newSteps := map[string]bool{}
	for _, s := range new.Box.Steps {
		newSteps[s.ID] = true
		was, ok := oldSteps[s.ID]
		if !ok {
			out = append(out, Change{Kind: "add", Area: "step", ID: s.ID, Text: s.Title, Detail: s.Detail, Sudo: s.Sudo})
			continue
		}
		var what []string
		if was.Title != s.Title {
			what = append(what, fmt.Sprintf("%s → %s", was.Title, s.Title))
		}
		if was.Detail != s.Detail && s.Detail != "" {
			what = append(what, s.Detail)
		}
		if was.Sudo != s.Sudo {
			if s.Sudo {
				what = append(what, "now asks for your password")
			} else {
				what = append(what, "no longer asks for your password")
			}
		}
		if len(what) > 0 {
			out = append(out, Change{Kind: "change", Area: "step", ID: s.ID, Text: s.Title, Detail: strings.Join(what, " · "), Sudo: s.Sudo && !was.Sudo})
		}
	}
	for _, s := range old.Box.Steps {
		if !newSteps[s.ID] {
			out = append(out, Change{Kind: "remove", Area: "step", ID: s.ID, Text: s.Title, Detail: "no longer run; what it installed stays"})
		}
	}

	oldProjects := map[string]Project{}
	for _, p := range old.Projects {
		oldProjects[p.ID] = p
	}
	newProjects := map[string]bool{}
	for _, p := range new.Projects {
		newProjects[p.ID] = true
		was, ok := oldProjects[p.ID]
		if !ok {
			detail := "new repo"
			if p.Required {
				detail += " · required"
			}
			out = append(out, Change{Kind: "add", Area: "project", ID: p.ID, Text: p.Repo, Detail: detail})
			continue
		}
		var what []string
		if was.Repo != p.Repo {
			what = append(what, fmt.Sprintf("repo %s → %s", was.Repo, p.Repo))
		}
		if was.ProjectPath() != p.ProjectPath() {
			what = append(what, fmt.Sprintf("path %s → %s", was.ProjectPath(), p.ProjectPath()))
		}
		if was.Kit != p.Kit {
			what = append(what, fmt.Sprintf("kit %s → %s", orNone(was.Kit), orNone(p.Kit)))
		}
		if was.Init != p.Init {
			what = append(what, fmt.Sprintf("init %s → %s", orNone(was.Init), orNone(p.Init)))
		}
		if was.Required != p.Required {
			if p.Required {
				what = append(what, "now required")
			} else {
				what = append(what, "now optional")
			}
		}
		if was.FirstTask != p.FirstTask && p.FirstTask != "" {
			what = append(what, "a new first task")
		}
		if len(what) > 0 {
			out = append(out, Change{Kind: "change", Area: "project", ID: p.ID, Text: p.Repo, Detail: strings.Join(what, " · ")})
		}
	}
	for _, p := range old.Projects {
		if !newProjects[p.ID] {
			out = append(out, Change{Kind: "remove", Area: "project", ID: p.ID, Text: p.Repo, Detail: "no longer in the team setup; your clone stays"})
		}
	}

	ids := map[string]bool{}
	for id := range old.Keys {
		ids[id] = true
	}
	for id := range new.Keys {
		ids[id] = true
	}
	var keyIDs []string
	for id := range ids {
		if !strings.HasPrefix(id, "$") {
			keyIDs = append(keyIDs, id)
		}
	}
	sort.Strings(keyIDs)
	for _, id := range keyIDs {
		o, n := old.Keys[id], new.Keys[id]
		for _, name := range sortedKeys(n.Shared) {
			if was, ok := o.Shared[name]; !ok {
				out = append(out, Change{Kind: "add", Area: "key", ID: id + "/" + name, Text: name, Detail: "from 1Password: " + n.Shared[name]})
			} else if was != n.Shared[name] {
				out = append(out, Change{Kind: "change", Area: "key", ID: id + "/" + name, Text: name, Detail: "now from " + n.Shared[name]})
			}
		}
		for _, name := range sortedKeys(o.Shared) {
			if _, ok := n.Shared[name]; !ok {
				out = append(out, Change{Kind: "remove", Area: "key", ID: id + "/" + name, Text: name, Detail: "no longer shared"})
			}
		}
		for _, name := range n.Ask {
			if !contains(o.Ask, name) {
				out = append(out, Change{Kind: "add", Area: "key", ID: id + "/" + name, Text: name, Detail: "yours to enter, once"})
			}
		}
	}

	if oldFiles != nil && newFiles != nil {
		var paths []string
		for p := range newFiles {
			paths = append(paths, p)
		}
		for p := range oldFiles {
			if _, ok := newFiles[p]; !ok {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		for _, p := range paths {
			if p == File {
				continue
			}
			o, had := oldFiles[p]
			n, has := newFiles[p]
			switch {
			case !had:
				out = append(out, Change{Kind: "add", Area: "file", ID: p, Text: p})
			case !has:
				out = append(out, Change{Kind: "remove", Area: "file", ID: p, Text: p})
			case o != n:
				out = append(out, Change{Kind: "change", Area: "file", ID: p, Text: p, Detail: lineDelta(o, n)})
			}
		}
	}
	return out
}

// NewSudo lists the titles of steps an update adds or changes that ask for
// the password, so the review can say so.
func NewSudo(changes []Change) []string {
	var out []string
	for _, c := range changes {
		if c.Area == "step" && c.Sudo {
			out = append(out, c.Text)
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// lineDelta summarises a file's change as lines added and removed.
func lineDelta(old, new string) string {
	count := map[string]int{}
	for _, l := range strings.Split(old, "\n") {
		count[l]++
	}
	added := 0
	for _, l := range strings.Split(new, "\n") {
		if count[l] > 0 {
			count[l]--
		} else {
			added++
		}
	}
	removed := 0
	for _, n := range count {
		removed += n
	}
	return fmt.Sprintf("+%d −%d lines", added, removed)
}
