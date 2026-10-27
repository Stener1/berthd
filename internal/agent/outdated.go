package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Which boxes run an older berthd than the one this berth ships, so the app
// can say so once, calmly, and update them in one go. The CLI answers it
// (berth upgrade BOX --check --json), as it does the upgrade itself, so the
// two can never disagree about what "older" means.

// OutdatedBox is one box's answer. Outdated is false, with Error, when the
// check could not tell (the box is away, or this berth ships no daemon for
// it).
type OutdatedBox struct {
	Box       string `json:"box"`
	Current   string `json:"current,omitempty"`
	Available string `json:"available,omitempty"`
	Outdated  bool   `json:"outdated"`
	// Newer is a box on a newer release than this berth: not outdated,
	// and never downgraded by Update all.
	Newer bool   `json:"newer,omitempty"`
	Error string `json:"error,omitempty"`
}

// outdatedFresh is how long an answer stands: each check reads the box's
// info and hashes the daemon it would upload.
const outdatedFresh = 2 * time.Minute

type outdatedCache struct {
	mu    sync.Mutex
	at    time.Time
	boxes []OutdatedBox
	// key is the online boxes the answer was for; a box coming online asks
	// again.
	key string
}

func (c *outdatedCache) forget() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

// outdatedBoxes checks every online box, at most four at a time.
func (a *Agent) outdatedBoxes(ctx context.Context, fresh bool) []OutdatedBox {
	var online []string
	for _, b := range a.status().Boxes {
		if b.State == StateOnline {
			online = append(online, b.Name)
		}
	}
	key, _ := json.Marshal(online)
	c := &a.outdated
	c.mu.Lock()
	defer c.mu.Unlock()
	if !fresh && c.key == string(key) && a.cfg.Now().Sub(c.at) < outdatedFresh {
		return c.boxes
	}
	out := make([]OutdatedBox, len(online))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, name := range online {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = a.checkBuild(ctx, name)
		}()
	}
	wg.Wait()
	c.boxes, c.at, c.key = out, a.cfg.Now(), string(key)
	return out
}

func (a *Agent) checkBuild(ctx context.Context, box string) OutdatedBox {
	if err := argOK(box); err != nil {
		return OutdatedBox{Box: box, Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	b, err := a.cli(ctx, "upgrade", box, "--check", "--json").Output()
	if err != nil {
		return OutdatedBox{Box: box, Error: cliError(err)}
	}
	var r OutdatedBox
	if err := json.Unmarshal(b, &r); err != nil {
		return OutdatedBox{Box: box, Error: "the build check said something unexpected"}
	}
	r.Box = box
	return r
}

func (a *Agent) outdatedRoutes(mux *http.ServeMux) {
	// ?fresh=1 checks again rather than answering from the last check.
	mux.HandleFunc("GET /v1/boxes/outdated", func(w http.ResponseWriter, r *http.Request) {
		a.sync()
		writeJSON(w, http.StatusOK, map[string]any{"boxes": a.outdatedBoxes(r.Context(), r.URL.Query().Get("fresh") == "1")})
	})
}
