package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/cosscom/shipyard/internal/statefile"
)

// A plugin in ~/.berth/plugins runs with the app's full access and its hooks
// run on this computer, so none is ever on until the user has allowed it.
// Allowing one records a hash of exactly what was reviewed, its manifest and
// its main module, in plugins.json beside the plugins folder (never inside a
// plugin's own folder, which the plugin ships). When either file changes the
// plugin is off again until it is allowed again. The plugins that ship inside
// the app are the only ones that can be on by default, and the app decides
// that for them.

// PluginGrant is the user's permission for one plugin.
type PluginGrant struct {
	// Hash is what was reviewed, as PluginHash returns it. A grant written by
	// hand with no hash trusts whatever the folder holds.
	Hash string    `json:"hash,omitempty"`
	At   time.Time `json:"at,omitzero"`
}

type pluginGrants struct {
	Plugins map[string]PluginGrant `json:"plugins"`
}

var grantsMu sync.Mutex

// PluginGrantsPath is where the grants for pluginsDir are kept:
// ~/.berth/plugins.json for ~/.berth/plugins.
func PluginGrantsPath(pluginsDir string) string {
	return filepath.Clean(pluginsDir) + ".json"
}

func readGrants(pluginsDir string) map[string]PluginGrant {
	b, err := os.ReadFile(PluginGrantsPath(pluginsDir))
	if err != nil {
		return map[string]PluginGrant{}
	}
	var g pluginGrants
	if json.Unmarshal(b, &g) != nil || g.Plugins == nil {
		return map[string]PluginGrant{}
	}
	return g.Plugins
}

// PluginGrantFor returns the grant for the plugin in dir, if it has one.
func PluginGrantFor(dir string) (PluginGrant, bool) {
	g, ok := readGrants(filepath.Dir(dir))[filepath.Base(dir)]
	return g, ok
}

// AllowPlugin records that the user allowed the plugin in dir as it is now.
// hash is what they reviewed; it must still be what the folder holds, so a
// plugin that changed during the review is not allowed.
func AllowPlugin(dir, hash string) error {
	now, err := PluginHash(dir)
	if err != nil {
		return err
	}
	if hash != now {
		return ErrPluginChanged
	}
	return updateGrants(filepath.Dir(dir), func(m map[string]PluginGrant) {
		m[filepath.Base(dir)] = PluginGrant{Hash: hash, At: time.Now().UTC()}
	})
}

// ForbidPlugin forgets the user's permission for the plugin in dir, so
// turning it on again asks again.
func ForbidPlugin(dir string) error {
	return updateGrants(filepath.Dir(dir), func(m map[string]PluginGrant) {
		delete(m, filepath.Base(dir))
	})
}

// ErrPluginChanged is returned when a plugin's files are no longer the ones
// that were reviewed.
var ErrPluginChanged = errors.New("the plugin changed while it was being reviewed; review it again")

func updateGrants(pluginsDir string, change func(map[string]PluginGrant)) error {
	grantsMu.Lock()
	defer grantsMu.Unlock()
	m := readGrants(pluginsDir)
	change(m)
	b, err := json.MarshalIndent(pluginGrants{Plugins: m}, "", "  ")
	if err != nil {
		return err
	}
	return statefile.Write(PluginGrantsPath(pluginsDir), append(b, '\n'))
}

// maxPluginFile bounds what is read to hash a plugin.
const maxPluginFile = 32 << 20

// PluginHash identifies what a plugin would run: its manifest and the main
// module the manifest names, read from inside its folder only. The app
// computes the same over the bytes it is about to import (see
// app/src/plugins/consent.ts), so the two must stay in step:
//
//	sha256("berth-plugin-v1\n" + len(manifest) + "\n" + manifest + "\n" + len(main) + "\n" + main)
//
// rendered as "sha256:<hex>", with an empty main when the manifest names none.
func PluginHash(dir string) (string, error) {
	return pluginHashWith(dir, nil)
}

// pluginHashWith is PluginHash over a manifest already read, when one is
// given, so what is checked is what is then used.
func pluginHashWith(dir string, manifest []byte) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if manifest == nil {
		if manifest, err = readIn(root, PluginManifest); err != nil {
			return "", err
		}
	}
	var m struct {
		Main string `json:"main"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return "", fmt.Errorf("%s: %w", PluginManifest, err)
	}
	var main []byte
	if m.Main != "" {
		if main, err = readIn(root, path.Clean("/" + m.Main)[1:]); err != nil {
			return "", err
		}
	}
	return HashPluginFiles(manifest, main), nil
}

// HashPluginFiles is PluginHash over bytes already read.
func HashPluginFiles(manifest, main []byte) string {
	h := sha256.New()
	io.WriteString(h, "berth-plugin-v1\n")
	io.WriteString(h, strconv.Itoa(len(manifest))+"\n")
	h.Write(manifest)
	io.WriteString(h, "\n"+strconv.Itoa(len(main))+"\n")
	h.Write(main)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func readIn(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPluginFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxPluginFile {
		return nil, fmt.Errorf("%s is larger than %d MB", name, maxPluginFile>>20)
	}
	return b, nil
}

// PluginState is whether a plugin in ~/.berth/plugins may run.
type PluginState struct {
	// Enabled: the user allowed it and it is still what they allowed.
	Enabled bool
	// Allowed is the hash the user allowed, if any.
	Allowed string
	// Changed: it was allowed, but its files have changed since.
	Changed bool
}

// PluginStatus works out a plugin's state. A file named "disabled" beside
// its manifest keeps it off whatever was allowed. The manifest's
// defaultEnabled never turns on a plugin from this folder: only the app's
// built-in plugins are on by default.
func PluginStatus(dir string) PluginState { return pluginStatusWith(dir, nil) }

func pluginStatusWith(dir string, manifest []byte) PluginState {
	g, ok := PluginGrantFor(dir)
	if !ok {
		return PluginState{}
	}
	s := PluginState{Allowed: g.Hash}
	if g.Hash != "" {
		now, err := pluginHashWith(dir, manifest)
		if err != nil || now != g.Hash {
			s.Changed = true
			return s
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "disabled")); err == nil {
		return s
	}
	s.Enabled = true
	return s
}

// PluginEnabled says whether the plugin in dir may run: see PluginStatus.
func PluginEnabled(dir string) bool { return PluginStatus(dir).Enabled }

// AllowedPluginManifest reads the manifest of the plugin in dir and says
// whether the plugin may run, judged on those very bytes: what the caller
// then takes from the manifest (the helper a plugin runs on a box, say) is
// what the user allowed, and not what the file became a moment later.
func AllowedPluginManifest(dir string) (manifest []byte, state PluginState, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, PluginState{}, err
	}
	defer root.Close()
	if manifest, err = readIn(root, PluginManifest); err != nil {
		return nil, PluginState{}, err
	}
	return manifest, pluginStatusWith(dir, manifest), nil
}
