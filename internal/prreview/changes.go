package prreview

import (
	"bufio"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// The review sheet lists the changes in a pull request that touch how a
// worktree is set up or what runs in it: Shipyard's own .berth/, install and
// dev scripts, dependencies, Dockerfiles, compose files, migrations, .env
// files, git hooks, and the files the project's kit says it watches. They
// are shown, in plain words, for the reviewer to weigh before anything runs.
// The PR's own .berth/ never runs: a review is set up by the team's kit and
// the default branch's config.

// FileDiff is one file a pull request changes, with its changed lines.
type FileDiff struct {
	Path string `json:"path"`
	// Old is the path before a rename or a deletion.
	Old     string   `json:"old,omitempty"`
	Deleted bool     `json:"deleted,omitempty"`
	Added   []string `json:"-"`
	Removed []string `json:"-"`
	// NoPatch says the lines are not known (the diff was too large to read,
	// or only the names were).
	NoPatch bool `json:"-"`
}

// ParseDiff reads a unified diff (what `gh pr diff` prints) into its files.
func ParseDiff(diff string) []FileDiff {
	var out []FileDiff
	var cur *FileDiff
	inHunk := false
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			out = append(out, FileDiff{})
			cur = &out[len(out)-1]
			inHunk = false
			if a, b, ok := gitPaths(strings.TrimPrefix(line, "diff --git ")); ok {
				cur.Old, cur.Path = a, b
			}
		case cur == nil:
		case !inHunk && strings.HasPrefix(line, "--- "):
			if p := strings.TrimPrefix(line, "--- "); p != "/dev/null" {
				cur.Old = unquote(strings.TrimPrefix(unquote(p), "a/"))
			}
		case !inHunk && strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" {
				cur.Deleted = true
				cur.Path = cur.Old
			} else {
				cur.Path = strings.TrimPrefix(unquote(p), "b/")
			}
		case !inHunk && strings.HasPrefix(line, "rename to "):
			cur.Path = unquote(strings.TrimPrefix(line, "rename to "))
		case !inHunk && strings.HasPrefix(line, "rename from "):
			cur.Old = unquote(strings.TrimPrefix(line, "rename from "))
		case !inHunk && strings.HasPrefix(line, "deleted file mode"):
			cur.Deleted = true
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk && strings.HasPrefix(line, "+"):
			cur.Added = append(cur.Added, line[1:])
		case inHunk && strings.HasPrefix(line, "-"):
			cur.Removed = append(cur.Removed, line[1:])
		}
	}
	files := out[:0]
	for _, f := range out {
		if f.Deleted && f.Path == "" {
			f.Path = f.Old
		}
		if f.Old == f.Path {
			f.Old = ""
		}
		if f.Path != "" {
			files = append(files, f)
		}
	}
	return files
}

// gitPaths splits "a/x b/y" from a diff --git line.
func gitPaths(s string) (string, string, bool) {
	if strings.HasPrefix(s, `"`) {
		return "", "", false
	}
	i := strings.Index(s, " b/")
	if !strings.HasPrefix(s, "a/") || i < 0 {
		return "", "", false
	}
	return s[2:i], s[i+3:], true
}

func unquote(s string) string {
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	return s
}

// Change kinds, in the order the sheet lists them.
const (
	KindBerth        = "berth"
	KindScripts      = "scripts"
	KindDocker       = "docker"
	KindCompose      = "compose"
	KindMigrations   = "migrations"
	KindEnv          = "env"
	KindGitHooks     = "githooks"
	KindKit          = "kit"
	KindDependencies = "dependencies"
	KindLockfile     = "lockfile"
)

var kindOrder = []string{KindBerth, KindScripts, KindDocker, KindCompose, KindMigrations, KindEnv, KindGitHooks, KindKit, KindDependencies, KindLockfile}

// Change is one kind of setup-relevant change, and the files that make it.
type Change struct {
	Kind   string   `json:"kind"`
	Title  string   `json:"title"`
	Detail string   `json:"detail,omitempty"`
	Files  []string `json:"files"`
}

// lifecycleScripts are package.json scripts a package manager or a dev
// server runs: on install, before publishing, or as the service a worktree
// starts.
var lifecycleScripts = regexp.MustCompile(`^\s*"(preinstall|install|postinstall|prepare|prepublish|prepublishOnly|prepack|postpack|predev|dev|postdev|prestart|start|poststart)"\s*:`)

// lockfiles and the manifests whose change they follow.
var lockfiles = map[string]bool{
	"pnpm-lock.yaml": true, "yarn.lock": true, "package-lock.json": true, "npm-shrinkwrap.json": true, "bun.lockb": true, "bun.lock": true,
	"Cargo.lock": true, "go.sum": true, "Gemfile.lock": true, "poetry.lock": true, "uv.lock": true, "Pipfile.lock": true, "composer.lock": true,
}

var manifests = map[string]bool{
	"package.json": true, "Cargo.toml": true, "go.mod": true, "Gemfile": true, "pyproject.toml": true, "Pipfile": true, "composer.json": true,
}

func isManifest(base string) bool {
	return manifests[base] || (strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"))
}

func isDockerfile(base string) bool {
	b := strings.ToLower(base)
	return b == "dockerfile" || strings.HasPrefix(b, "dockerfile.") || strings.HasSuffix(b, ".dockerfile") || b == ".dockerignore"
}

func isCompose(base string) bool {
	b := strings.ToLower(base)
	return strings.HasPrefix(b, "docker-compose") || b == "compose.yml" || b == "compose.yaml" || strings.HasPrefix(b, "compose.") && (strings.HasSuffix(b, ".yml") || strings.HasSuffix(b, ".yaml"))
}

func isMigration(p string) bool {
	segs := strings.Split(strings.ToLower(p), "/")
	for i, s := range segs[:len(segs)-1] {
		if s == "migrations" || s == "migration" || s == "migrate" && i > 0 && segs[i-1] == "db" {
			return true
		}
	}
	base := segs[len(segs)-1]
	return base == "schema.prisma" || base == "structure.sql" || base == "schema.rb" && len(segs) > 1 && segs[len(segs)-2] == "db"
}

func isEnvFile(base string) bool { return strings.HasPrefix(base, ".env") }

func isGitHook(p string) bool {
	return strings.HasPrefix(p, ".husky/") || strings.HasPrefix(p, ".githooks/") || p == "lefthook.yml" || p == "lefthook.yaml" || p == ".pre-commit-config.yaml"
}

// SetupChanges finds the setup-relevant changes among files. watch is the
// kit's own list of files that affect setup (globs, ** for any folders).
func SetupChanges(files []FileDiff, watch []string) []Change {
	by := map[string]*Change{}
	add := func(kind, file string) {
		c, ok := by[kind]
		if !ok {
			c = &Change{Kind: kind}
			by[kind] = c
		}
		for _, f := range c.Files {
			if f == file {
				return
			}
		}
		c.Files = append(c.Files, file)
	}
	var scripts []string
	manifestChanged := false
	for _, f := range files {
		p := strings.TrimPrefix(path.Clean("/"+f.Path), "/")
		base := path.Base(p)
		switch {
		case p == ".berth" || strings.HasPrefix(p, ".berth/"):
			add(KindBerth, p)
		case base == "package.json":
			manifestChanged = true
			names := changedScripts(f)
			if len(names) > 0 || f.NoPatch {
				add(KindScripts, p)
				scripts = append(scripts, names...)
			} else {
				add(KindDependencies, p)
			}
		case isManifest(base):
			manifestChanged = true
			add(KindDependencies, p)
		case lockfiles[base]:
			add(KindLockfile, p)
		case isDockerfile(base):
			add(KindDocker, p)
		case isCompose(base):
			add(KindCompose, p)
		}
		if isMigration(p) {
			add(KindMigrations, p)
		}
		if isEnvFile(base) {
			add(KindEnv, p)
		}
		if isGitHook(p) {
			add(KindGitHooks, p)
		}
		for _, g := range watch {
			if MatchGlob(g, p) {
				add(KindKit, p)
				break
			}
		}
	}
	out := []Change{}
	for _, kind := range kindOrder {
		c, ok := by[kind]
		if !ok {
			continue
		}
		sort.Strings(c.Files)
		c.Title, c.Detail = describe(kind, c.Files, uniq(scripts), manifestChanged)
		out = append(out, *c)
	}
	return out
}

func uniq(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// changedScripts names the lifecycle scripts a package.json diff adds,
// changes or removes.
func changedScripts(f FileDiff) []string {
	var out []string
	for _, l := range append(append([]string{}, f.Added...), f.Removed...) {
		if m := lifecycleScripts.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// describe says what a kind of change means, in plain words.
func describe(kind string, files, scripts []string, manifestChanged bool) (string, string) {
	n := len(files)
	switch kind {
	case KindBerth:
		return "Changes Shipyard's own setup (.berth/)", "Not used for this review: setup comes from your team's kit and the default branch's config, never from the PR"
	case KindScripts:
		if len(scripts) == 0 {
			return "Changes package.json", "Shipyard couldn't read its diff; check its scripts on GitHub"
		}
		return "Changes package.json scripts: " + strings.Join(scripts, ", "), "They run when setup installs dependencies or starts the dev server"
	case KindDocker:
		return "Changes " + plural(n, "a Dockerfile", fmt.Sprintf("%d Dockerfiles", n)), "Used if setup or a service builds an image"
	case KindCompose:
		return "Changes Docker Compose", "Used if setup or a service starts containers"
	case KindMigrations:
		return "Adds or changes " + plural(n, "a database migration", fmt.Sprintf("%d database migrations", n)), "They run against this review's own database if setup migrates"
	case KindEnv:
		return "Changes " + plural(n, "an environment file", fmt.Sprintf("%d environment files", n)), "Your box's keys still apply; the file can set defaults"
	case KindGitHooks:
		return "Changes git hooks", "Off while Shipyard checks the PR out; they would run on a commit made in it"
	case KindKit:
		return "Changes " + plural(n, "a file", fmt.Sprintf("%d files", n)) + " the kit watches", "The kit's setup reads these"
	case KindDependencies:
		return "Changes dependencies", "New packages can run their own install scripts"
	case KindLockfile:
		if !manifestChanged {
			return "Lockfile only", "Dependency versions change without a manifest change"
		}
		return "Updates the lockfile", ""
	}
	return kind, ""
}

// MatchGlob reports whether p matches pattern, where * and ? match within a
// folder name and ** matches any number of folders.
func MatchGlob(pattern, p string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	return matchSegs(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegs(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
