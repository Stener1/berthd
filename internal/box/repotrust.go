package box

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// A repository's committed .berth/config.json is code: its setup script,
// services, hooks, flows, env and agent presets all run on the box. Anyone
// who can commit to a repository — or who wrote one the user clones — could
// otherwise run commands here the moment a worktree is made. So a box runs a
// repository's config only once someone has trusted it for that location,
// and only the exact bytes they trusted: a change to the file asks again.
// Until then the config is shown, never run. A kit's layer and the box's own
// config are the user's, and apply regardless.

// Repo trust states.
const (
	// RepoTrustNone: the repository has no .berth/config.json.
	RepoTrustNone = "none"
	// RepoTrustTrusted: this box runs the config as it is.
	RepoTrustTrusted = "trusted"
	// RepoTrustUntrusted: nobody has trusted the config on this box.
	RepoTrustUntrusted = "untrusted"
	// RepoTrustChanged: an earlier version was trusted; this one is not.
	RepoTrustChanged = "changed"
)

// RepoTrust says whether a box runs a repository's committed config.
type RepoTrust struct {
	State string `json:"state"`
	// Hash is the sha256 of the file as it is now; trusting it means
	// sending this hash back.
	Hash string `json:"hash,omitempty"`
	// Wants is everything the committed config would run, set while it is
	// not trusted.
	Wants *RepoConfig `json:"wants,omitempty"`
}

// Pending reports whether the repository asks for something the box does not
// run yet.
func (t RepoTrust) Pending() bool {
	return t.State == RepoTrustUntrusted || t.State == RepoTrustChanged
}

// readRepoFile reads repo's .berth/config.json and the hash of its bytes.
func readRepoFile(repo string) (c RepoConfig, hash string, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(repo, RepoConfigFile))
	if os.IsNotExist(err) {
		return c, "", false, nil
	}
	if err != nil {
		return c, "", false, err
	}
	sum := sha256.Sum256(b)
	hash = hex.EncodeToString(sum[:])
	if err := json.Unmarshal(b, &c); err != nil {
		return c, hash, false, fmt.Errorf("%s: %w", RepoConfigFile, err)
	}
	return c, hash, true, nil
}

// runsAnything reports whether c holds anything that runs or changes what
// runs. Ports alone only reserve ports.
func (c RepoConfig) runsAnything() bool {
	return c.Setup != "" || c.Archive != "" || len(c.Env) > 0 || len(c.Services) > 0 ||
		len(c.Hooks) > 0 || len(c.Flows) > 0 || len(c.Agents) > 0 || len(c.BrowserAllow) > 0 ||
		c.Login != nil
}

// repoLayer is the part of a location's repository config the box applies,
// and whether it is trusted. An untrusted config contributes its port count
// and nothing else. The file is read afresh every time, so a change after it
// was trusted takes effect — as untrusted — at once.
func repoLayer(saved savedLocation) (RepoConfig, RepoTrust, error) {
	full, hash, ok, err := readRepoFile(saved.Path)
	if err != nil {
		// A broken file applies nothing.
		return RepoConfig{}, RepoTrust{State: RepoTrustUntrusted, Hash: hash}, err
	}
	if !ok {
		return RepoConfig{}, RepoTrust{State: RepoTrustNone}, nil
	}
	if saved.RepoTrust == hash || !full.runsAnything() {
		return full, RepoTrust{State: RepoTrustTrusted, Hash: hash}, nil
	}
	t := RepoTrust{State: RepoTrustUntrusted, Hash: hash, Wants: &full}
	if saved.RepoTrust != "" {
		t.State = RepoTrustChanged
	}
	return RepoConfig{Ports: full.Ports}, t, nil
}

// ErrRepoConfigChanged is returned when trusting a hash that is no longer the
// file's: what was reviewed is not what would run.
var ErrRepoConfigChanged = errors.New("the repository's .berth/config.json changed since it was shown; review it again")

// TrustRepo trusts the location's repository config, if hash is still the
// file's hash.
func (l *Locations) TrustRepo(name, hash string) error {
	return l.update(func(all []savedLocation) ([]savedLocation, error) {
		for i := range all {
			if all[i].Name != name {
				continue
			}
			_, now, ok, err := readRepoFile(all[i].Path)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, httpError{http.StatusNotFound, "the repository has no " + RepoConfigFile}
			}
			if hash == "" || hash != now {
				return nil, httpError{http.StatusConflict, ErrRepoConfigChanged.Error()}
			}
			all[i].RepoTrust = now
			return all, nil
		}
		return nil, ErrUnknownLocation
	})
}

// UntrustRepo stops running the location's repository config.
func (l *Locations) UntrustRepo(name string) error {
	return l.update(func(all []savedLocation) ([]savedLocation, error) {
		for i := range all {
			if all[i].Name == name {
				all[i].RepoTrust = ""
				return all, nil
			}
		}
		return nil, ErrUnknownLocation
	})
}

// wantsSummary lists what an untrusted config would run, for events and logs.
func wantsSummary(c *RepoConfig) map[string]any {
	if c == nil {
		return nil
	}
	out := map[string]any{}
	if c.Setup != "" {
		out["setup"] = c.Setup
	}
	if c.Archive != "" {
		out["archive"] = c.Archive
	}
	if len(c.Services) > 0 {
		names := []string{}
		for _, s := range c.Services {
			names = append(names, s.Name)
		}
		out["services"] = names
	}
	if len(c.Hooks) > 0 {
		out["hooks"] = len(c.Hooks)
	}
	if len(c.Flows) > 0 {
		out["flows"] = len(c.Flows)
	}
	if len(c.Env) > 0 {
		keys := []string{}
		for k := range c.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out["env"] = keys
	}
	if len(c.Agents) > 0 {
		out["agents"] = len(c.Agents)
	}
	if c.Login != nil {
		out["login"] = c.Login.Script
	}
	return out
}

// trustRepoConfig trusts a location's repository config as it is now. The
// request names the hash it was shown, so a file changed in between is
// refused rather than trusted unseen.
func (b *Box) trustRepoConfig(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Hash string `json:"hash"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	name := r.PathValue("name")
	if err := b.before(r, "config.change", map[string]any{"location": name, "trust": true, "hash": req.Hash}); err != nil {
		return err
	}
	if err := b.Locations.TrustRepo(name, req.Hash); err != nil {
		return err
	}
	b.publish(r, "config.changed", map[string]any{"location": name, "trust": RepoTrustTrusted, "hash": req.Hash})
	return b.getConfig(w, r)
}

// untrustRepoConfig stops running a location's repository config.
func (b *Box) untrustRepoConfig(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if err := b.before(r, "config.change", map[string]any{"location": name, "trust": false}); err != nil {
		return err
	}
	if err := b.Locations.UntrustRepo(name); err != nil {
		return err
	}
	b.publish(r, "config.changed", map[string]any{"location": name, "trust": RepoTrustUntrusted})
	return b.getConfig(w, r)
}
