// Package teamtest gives tests a GitHub of their own: a fake gh CLI first on
// PATH, answering from bare git repositories in a temporary folder, with
// GH_CONFIG_DIR pointed at another, so no test reaches GitHub or reads the
// person's own gh sign-in.
package teamtest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// GitHub is a fake GitHub for one test.
type GitHub struct {
	t    *testing.T
	Root string
	// Bin is the folder holding the fake gh, first on PATH.
	Bin string
	Log string
}

// FakeGH is the path of the fake gh script.
func FakeGH() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata", "fake-gh")
}

// New puts a fake gh first on PATH for the rest of the test. It is
// signed out until SignIn.
func New(t *testing.T) *GitHub {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("the fake gh needs python3")
	}
	dir := t.TempDir()
	g := &GitHub{t: t, Root: filepath.Join(dir, "github"), Bin: filepath.Join(dir, "bin"), Log: filepath.Join(dir, "gh.log")}
	os.MkdirAll(g.Root, 0o755)
	os.MkdirAll(g.Bin, 0o755)
	b, err := os.ReadFile(FakeGH())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.Bin, "gh"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", g.Bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_FAKE_ROOT", g.Root)
	t.Setenv("GH_FAKE_LOG", g.Log)
	t.Setenv("GH_CONFIG_DIR", filepath.Join(dir, "gh-config"))
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	return g
}

// SignIn signs the fake gh in as login; "" signs it out.
func (g *GitHub) SignIn(login string) {
	p := filepath.Join(g.Root, ".auth")
	if login == "" {
		os.Remove(p)
		return
	}
	os.WriteFile(p, []byte(login), 0o600)
}

// Org makes owner an org with a name and verified flag.
func (g *GitHub) Org(owner, name string, verified bool) {
	os.MkdirAll(filepath.Join(g.Root, owner), 0o755)
	b, _ := json.Marshal(map[string]any{"login": owner, "name": name, "is_verified": verified, "avatar_url": "https://avatars.githubusercontent.com/" + owner})
	os.WriteFile(filepath.Join(g.Root, owner, "org.json"), b, 0o644)
}

// Repo opts.
type Repo struct {
	Private  bool
	NoAccess bool
	// Author is the commit author's email ("keith@acme.test" shows as keith).
	Author string
	// Branch is where the commit goes; default main.
	Branch string
}

// Repo creates owner/name with files committed on main and returns the
// commit. Calling it again for the same repository adds a commit.
func (g *GitHub) Repo(slug string, files map[string]string, o Repo) string {
	g.t.Helper()
	bare := g.Bare(slug)
	work := g.t.TempDir()
	if _, err := os.Stat(bare); err != nil {
		os.MkdirAll(filepath.Dir(bare), 0o755)
		g.git("", "init", "-q", "--bare", "-b", "main", bare)
	} else if g.try("--git-dir", bare, "rev-parse", "--verify", "-q", "main") {
		g.git("", "clone", "-q", bare, work)
		if o.Branch != "" && g.try("--git-dir", bare, "rev-parse", "--verify", "-q", o.Branch) {
			g.git(work, "checkout", "-q", o.Branch)
		}
		// Start from what is there; files given replace or add, "" removes.
	}
	if _, err := os.Stat(filepath.Join(work, ".git")); err != nil {
		g.git(work, "init", "-q", "-b", "main")
		g.git(work, "remote", "add", "origin", bare)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		full := filepath.Join(work, filepath.FromSlash(p))
		if files[p] == "" {
			os.Remove(full)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		mode := os.FileMode(0o644)
		if strings.HasPrefix(files[p], "#!") {
			mode = 0o755
		}
		os.WriteFile(full, []byte(files[p]), mode)
	}
	author := o.Author
	if author == "" {
		author = "keith@acme.test"
	}
	g.git(work, "add", "-A")
	g.git(work, "-c", "user.name="+strings.Split(author, "@")[0], "-c", "user.email="+author, "commit", "-q", "--allow-empty", "-m", "Update "+slug)
	branch := o.Branch
	if branch == "" {
		branch = "main"
	}
	g.git(work, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	mark := func(name string, on bool) {
		p := filepath.Join(bare, name)
		if on {
			os.WriteFile(p, nil, 0o644)
		} else {
			os.Remove(p)
		}
	}
	mark("berth-private", o.Private)
	mark("berth-noaccess", o.NoAccess)
	return strings.TrimSpace(g.git(work, "rev-parse", "HEAD"))
}

// Bare is the bare repository behind slug.
func (g *GitHub) Bare(slug string) string { return filepath.Join(g.Root, slug+".git") }

// Calls lists the gh commands run so far.
func (g *GitHub) Calls() []string {
	b, _ := os.ReadFile(g.Log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (g *GitHub) git(dir string, args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (g *GitHub) try(args ...string) bool {
	return exec.Command("git", args...).Run() == nil
}
