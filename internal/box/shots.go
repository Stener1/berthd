package box

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Visual before/after: `berthd shots compare` screenshots a worktree's
// pages and the same pages of a base, at a few widths, with settings that
// make two loads of an unchanged page match pixel for pixel
// (shotscapture.go), diffs each pair (pixeldiff.go), and keeps the result
// as a visual diff: a berth.visualdiff/v1 manifest and its images
// (shotsmanifest.go, shotsstore.go).
//
// The base is, by default, the project's main checkout's dev server,
// reached by its proxy name (LOC.BOX.localhost) exactly as the worktree's
// is (WT.LOC.BOX.localhost); when main's dev server isn't running, the
// worktree's "turn-start" baseline if it has one. Or a baseline asked for
// by name: "turn-start" (`berthd shots baseline`, at the start of a turn),
// "accepted" (Accept as baseline), or any other.

// ShotsConfig is .berth/config.json's "shots": what to compare by default.
type ShotsConfig struct {
	// Pages are paths, "/" first: ["/", "/login", "/pricing"].
	Pages []string `json:"pages,omitempty"`
	// Sizes are viewport widths in CSS pixels (default 375, 768, 1280).
	Sizes []int `json:"sizes,omitempty"`
	// Mask hides dynamic content from the diff: CSS selectors, such as
	// "time", "[data-testid=avatar]", ".relative-date".
	Mask []string `json:"mask,omitempty"`
	// Threshold is the colour distance (0-1) under which two pixels are
	// the same (default 0.03: both sides render in one Chromium, so there
	// is no cross-machine noise to forgive, and pixelmatch's 0.1 misses a
	// white card moving over an off-white page).
	Threshold float64 `json:"threshold,omitempty"`
	// Unchanged is the share of changed pixels, in percent, under which a
	// shot counts as unchanged (default 0.02).
	Unchanged float64 `json:"unchanged_below,omitempty"`
	// MaxHeight caps a full-page shot, in CSS pixels (default 4000).
	MaxHeight int `json:"max_height,omitempty"`
	// Scale is the device pixel ratio (default 1).
	Scale float64 `json:"scale,omitempty"`
	// ColorScheme is the prefers-color-scheme pages see: light (default),
	// dark, or both (every shot twice).
	ColorScheme string `json:"color_scheme,omitempty"`
	// WaitFor is a selector every page must show before its shot.
	WaitFor string `json:"wait_for,omitempty"`
	// Seed makes Math.random repeat the same numbers on every load.
	Seed bool `json:"seed_random,omitempty"`
	// Parallel caps the browser tabs shooting at once (default: one per
	// side, size and scheme, at most the box's CPUs and 8).
	Parallel int `json:"parallel,omitempty"`
}

const (
	minShotSize = 240
	maxShotSize = 2560
	maxShotPage = 50
)

func (c ShotsConfig) withDefaults() ShotsConfig {
	if len(c.Pages) == 0 {
		c.Pages = []string{"/"}
	}
	if len(c.Sizes) == 0 {
		c.Sizes = []int{375, 768, 1280}
	}
	if c.Threshold <= 0 {
		c.Threshold = 0.03
	}
	if c.Unchanged <= 0 {
		c.Unchanged = 0.02
	}
	if c.MaxHeight <= 0 {
		c.MaxHeight = 4000
	}
	if c.Scale <= 0 {
		c.Scale = 1
	}
	if c.ColorScheme == "" {
		c.ColorScheme = "light"
	}
	if c.Parallel <= 0 {
		c.Parallel = min(8, max(2, runtime.NumCPU()))
	}
	return c
}

// schemes are the colour schemes each page is shot in.
func (c ShotsConfig) schemes() []string {
	switch c.ColorScheme {
	case "dark":
		return []string{"dark"}
	case "both":
		return []string{"light", "dark"}
	}
	return []string{"light"}
}

func (c ShotsConfig) check() error {
	if len(c.Pages) > maxShotPage {
		return badRequest("a compare takes at most %d pages, not %d", maxShotPage, len(c.Pages))
	}
	for _, p := range c.Pages {
		if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, " \t\n#") {
			return badRequest("%q isn't a page path such as /pricing", p)
		}
	}
	if len(c.Sizes) > 6 {
		return badRequest("a compare takes at most 6 sizes")
	}
	for _, s := range c.Sizes {
		if s < minShotSize || s > maxShotSize {
			return badRequest("a size is a width from %d to %d pixels, not %d", minShotSize, maxShotSize, s)
		}
	}
	switch c.ColorScheme {
	case "light", "dark", "both":
	default:
		return badRequest("--color-scheme is light, dark or both, not %q", c.ColorScheme)
	}
	if c.Threshold >= 1 || c.Scale > 3 || c.MaxHeight > 20000 {
		return badRequest("threshold is under 1, scale at most 3, max_height at most 20000")
	}
	return nil
}

// viewportHeight is the window height a width is shot at: a phone's, a
// tablet's, a laptop's.
func viewportHeight(w int) int {
	switch {
	case w < 600:
		return 812
	case w < 1024:
		return 1024
	default:
		return 800
	}
}

// ShotsRequest is what `berthd shots compare` asks for; empty fields come
// from the repository's "shots" config, then the defaults.
type ShotsRequest struct {
	Pages []string `json:"pages,omitempty"`
	Sizes []int    `json:"sizes,omitempty"`
	Mask  []string `json:"mask,omitempty"`
	// Base is main (the default), turn-start, accepted, or a baseline's
	// name.
	Base        string `json:"base,omitempty"`
	ColorScheme string `json:"color_scheme,omitempty"`
	Session     string `json:"session,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Title       string `json:"title,omitempty"`
	Note        string `json:"note,omitempty"`
	New         bool   `json:"new,omitempty"` // a new visual diff, not a new version
	// Save, for `shots baseline`: shoot the worktree only and keep it as
	// this baseline.
	Save string `json:"save,omitempty"`
	// As logs every side in as this email first (login.go): each side's
	// own login runs against its own dev server and database.
	As string `json:"as,omitempty"`
}

// ShotsResult is the text an agent reads, and where the result is.
type ShotsResult struct {
	Text     string `json:"text"`
	Artifact string `json:"artifact,omitempty"`
	Version  int    `json:"version,omitempty"`
	Dir      string `json:"dir,omitempty"`
}

// shotsDir is the box's folder for baselines (and, without an artifact
// store, visual diffs).
func (b *Box) shotsDir() string {
	if b.ShotsDir != "" {
		return b.ShotsDir
	}
	if b.Browsers != nil {
		return filepath.Join(filepath.Dir(b.Browsers.Dir), "shots")
	}
	return filepath.Join(os.TempDir(), "berth-shots")
}

func shotsConfigFor(wt Worktree) ShotsConfig {
	if c, ok, _ := ReadRepoConfig(wt.Path); ok && c.Shots != nil {
		return *c.Shots
	}
	return ShotsConfig{}
}

func gitInfo(dir string) (commit string, dirty int) {
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output(); err == nil {
		commit = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l != "" {
				dirty++
			}
		}
	}
	return
}

// answers says whether something listens on the port, so a compare never
// runs against a dev server that is not there.
func answers(port int) bool {
	if port <= 0 {
		return false
	}
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// shotsPlan is what a compare will shoot, against what.
type shotsPlan struct {
	loc        Location
	wt, main   Worktree
	cfg        ShotsConfig
	base       string // main, or a baseline's name
	notice     string
	headURL    string
	baseURL    string // main's, when base is main
	saved      *baseline
	savedDir   string
	scopes     []browserScope
	headScope  browserScope
	mainScope  browserScope
	mainOnline bool
}

// planShots works out the sides of a compare, refusing up front what
// can't work: no dev server in the worktree, or none in main without a
// baseline to fall back to.
func (b *Box) planShots(ctx context.Context, locName, wtName string, req ShotsRequest) (*shotsPlan, error) {
	loc, err := b.Locations.Get(ctx, locName)
	if err != nil {
		return nil, err
	}
	p := &shotsPlan{loc: loc}
	for _, w := range loc.Worktrees {
		if w.Name == wtName {
			p.wt = w
		}
		if w.Main {
			p.main = w
		}
	}
	if p.wt.Path == "" {
		return nil, ErrUnknownWorktree
	}
	cfg := shotsConfigFor(p.wt)
	if len(req.Pages) > 0 {
		cfg.Pages = req.Pages
	}
	if len(req.Sizes) > 0 {
		cfg.Sizes = req.Sizes
	}
	if req.ColorScheme != "" {
		cfg.ColorScheme = req.ColorScheme
	}
	cfg.Mask = append(append([]string{}, cfg.Mask...), req.Mask...)
	cfg = cfg.withDefaults()
	for i, pg := range cfg.Pages {
		if !strings.HasPrefix(pg, "/") {
			cfg.Pages[i] = "/" + pg
		}
	}
	if err := cfg.check(); err != nil {
		return nil, err
	}
	p.cfg = cfg
	if req.Save != "" && !baselineNameRe.MatchString(req.Save) {
		return nil, badRequest("a baseline's name is lowercase letters, digits, dots and dashes, not %q", req.Save)
	}
	p.headURL = worktreeURL(b.Name, loc.Name, p.wt)
	if p.headURL == "" {
		return nil, badRequest("%s/%s has no URL (its name is not a hostname label)", loc.Name, p.wt.Name)
	}

	// One listing of the box's ports for both sides.
	ports, _ := ListPorts(ctx)
	if ports == nil {
		ports = []Port{}
	}
	hs, ok := b.browserScopeWith(ctx, p.wt.Path, ports)
	if !ok || !answers(hs.dev) {
		return nil, badRequest("%s/%s has no dev server running (nothing answers on port %d); start it on $BERTH_PORT first", loc.Name, p.wt.Name, hs.dev)
	}
	p.headScope = hs
	p.scopes = []browserScope{hs}
	if req.Save != "" {
		return p, nil
	}

	explicit := req.Base != ""
	p.base = req.Base
	if p.base == "" {
		p.base = "main"
	}
	if p.base == "main" {
		why := ""
		switch {
		case p.main.Path == "":
			why = loc.Name + " has no main checkout"
		case p.wt.Main:
			why = "this is the main checkout"
		default:
			ms, ok := b.browserScopeWith(ctx, p.main.Path, ports)
			if ok && answers(ms.dev) {
				p.mainScope, p.mainOnline = ms, true
				p.baseURL = worktreeURL(b.Name, loc.Name, p.main)
				p.scopes = append(p.scopes, ms)
				return p, nil
			}
			why = fmt.Sprintf("main's dev server isn't running (nothing answers on port %d in %s)", ms.dev, p.main.Path)
		}
		dir := b.baselineDir(loc, p.wt, "turn-start")
		if _, err := os.Stat(filepath.Join(dir, "baseline.json")); explicit || err != nil {
			return nil, badRequest("%s, so there is nothing to compare with. Start main's dev server, or compare with a baseline: take one with `berthd shots baseline` before you change anything, then `berthd shots compare --base turn-start`", why)
		}
		p.base = "turn-start"
		p.notice = why + "; compared with the turn-start baseline instead"
	}
	if !baselineNameRe.MatchString(p.base) {
		return nil, badRequest("--base is main, turn-start, accepted or a baseline's name, not %q", p.base)
	}
	p.savedDir = b.baselineDir(loc, p.wt, p.base)
	saved, err := readBaseline(p.savedDir)
	if err != nil {
		return nil, badRequest("%s/%s has no %q baseline yet; take one with `berthd shots baseline --name %s` (or Accept as baseline in Shipyard, for accepted)", loc.Name, p.wt.Name, p.base, p.base)
	}
	p.saved = saved
	return p, nil
}

// shotsLogin logs both sides of a compare in as email: the worktree with
// its own login, and main with main's, since each side has its own dev
// server and database. A baseline side was shot as it was.
func (b *Box) shotsLogin(ctx context.Context, sh *shooter, p *shotsPlan, email string) error {
	tab, err := sh.newTab(ctx, p.cfg, "light")
	if err != nil {
		return err
	}
	sides := []struct {
		wt  Worktree
		url string
	}{{p.wt, p.headURL}}
	if p.baseURL != "" {
		sides = append(sides, struct {
			wt  Worktree
			url string
		}{p.main, p.baseURL})
	}
	for _, s := range sides {
		res, err := b.loginIn(ctx, "shots", p.loc, s.wt, email)
		if err != nil {
			return fmt.Errorf("logging %s in as %s: %w", s.wt.Name, email, err)
		}
		if err := setLoginCookies(ctx, sh.cdp, tab.session, s.url, res.Cookies); err != nil {
			return err
		}
	}
	return nil
}

// ShotsCompare runs a compare (or, with Save, takes a baseline).
func (b *Box) ShotsCompare(ctx context.Context, locName, wtName string, req ShotsRequest) (ShotsResult, error) {
	t0 := time.Now()
	if b.Browsers == nil {
		return ShotsResult{}, errors.New("this box has no browser")
	}
	if b.Artifacts == nil && req.Save == "" {
		return ShotsResult{}, errNoArtifacts
	}
	p, err := b.planShots(ctx, locName, wtName, req)
	if err != nil {
		return ShotsResult{}, err
	}
	cfg := p.cfg

	px, err := b.newShotsProxy(p.scopes...)
	if err != nil {
		return ShotsResult{}, err
	}
	defer px.Close()
	tStart := time.Now()
	sh, err := b.Browsers.startShooter(ctx, px.Addr())
	if err != nil {
		return ShotsResult{}, err
	}
	defer sh.close()
	if req.As != "" {
		if err := b.shotsLogin(ctx, sh, p, req.As); err != nil {
			return ShotsResult{}, err
		}
	}
	startMS := int(time.Since(tStart).Milliseconds())

	type pair struct{ before, after shot }
	results := map[string]*pair{}
	for _, pg := range cfg.Pages {
		for _, sc := range cfg.schemes() {
			for _, s := range cfg.Sizes {
				results[shotKey(pg, s, sc)] = &pair{}
			}
		}
	}
	tShoot := time.Now()
	// Each side shoots each size and scheme in a tab of its own, in
	// parallel up to cfg.Parallel: a tab keeps one viewport for all its
	// pages.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	sem := make(chan struct{}, cfg.Parallel)
	side := func(root string, size int, scheme string, set func(*pair, shot)) {
		defer wg.Done()
		sem <- struct{}{}
		defer func() { <-sem }()
		tab, err := sh.newTab(ctx, cfg, scheme)
		if err != nil {
			mu.Lock()
			firstErr = err
			mu.Unlock()
			return
		}
		for _, pg := range cfg.Pages {
			s := tab.shoot(ctx, strings.TrimSuffix(root, "/")+pg, size, cfg)
			mu.Lock()
			set(results[shotKey(pg, size, scheme)], s)
			mu.Unlock()
		}
	}
	for _, sc := range cfg.schemes() {
		for _, size := range cfg.Sizes {
			wg.Add(1)
			go side(p.headURL, size, sc, func(pr *pair, s shot) { pr.after = s })
			if p.baseURL != "" {
				wg.Add(1)
				go side(p.baseURL, size, sc, func(pr *pair, s shot) { pr.before = s })
			}
		}
	}
	wg.Wait()
	if firstErr != nil {
		return ShotsResult{}, firstErr
	}
	shootMS := int(time.Since(tShoot).Milliseconds())

	if req.Save != "" {
		return b.saveBaseline(p.loc, p.wt, req.Save, cfg, sh.chromiumName(), func(k string) shot { return results[k].after })
	}

	key := "visualdiff:" + p.base
	id, imgDir, existing, err := b.prepareDiff(p.wt, key, req.New)
	if err != nil {
		return ShotsResult{}, err
	}
	imgs := &imgStore{dir: imgDir}

	headCommit, headDirty := gitInfo(p.wt.Path)
	vd := VisualDiff{
		Schema:  VisualDiffSchema,
		Created: time.Now().UTC(),
		Head:    vdSide{Kind: "worktree", Label: p.wt.Name, URL: p.headURL, Commit: headCommit, Dirty: headDirty},
		Settings: vdSettings{Sizes: cfg.Sizes, Scale: cfg.Scale, Threshold: cfg.Threshold, Unchanged: cfg.Unchanged, MaxHeight: cfg.MaxHeight,
			ColorScheme: cfg.ColorScheme, ColorSchemes: cfg.schemes(), ReducedMotion: true, Mask: cfg.Mask, Chromium: sh.chromiumName(), Login: req.As},
		Note:   req.Note,
		Notice: p.notice,
	}
	if p.saved != nil {
		vd.Base = vdSide{Kind: "baseline", Label: p.base, Commit: p.saved.Commit, Taken: p.saved.Taken.UTC().Format(time.RFC3339)}
		if p.saved.Chromium != "" && p.saved.Chromium != vd.Settings.Chromium {
			vd.Notice = strings.TrimPrefix(vd.Notice+"; the baseline was shot with "+p.saved.Chromium+", so text may differ by a pixel", "; ")
		}
	} else {
		c, d := gitInfo(p.main.Path)
		vd.Base = vdSide{Kind: "main", Label: p.main.Branch, URL: p.baseURL, Commit: c, Dirty: d}
		if vd.Base.Label == "" {
			vd.Base.Label = "main"
		}
	}
	vd.Title = req.Title
	if vd.Title == "" {
		vd.Title = "Visual changes: " + p.wt.Name + " vs " + vd.Base.Label
	}

	tDiff := time.Now()
	type job struct {
		page, scheme string
		size         int
	}
	var jobs []job
	for _, pg := range cfg.Pages {
		for _, sc := range cfg.schemes() {
			for _, s := range cfg.Sizes {
				jobs = append(jobs, job{pg, sc, s})
			}
		}
	}
	shots := make([]vdShot, len(jobs))
	titles := map[string]string{}
	var tmu sync.Mutex
	dsem := make(chan struct{}, max(2, runtime.NumCPU()/2))
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			dsem <- struct{}{}
			defer func() { <-dsem }()
			k := shotKey(j.page, j.size, j.scheme)
			pr := results[k]
			if p.saved != nil {
				pr.before = loadBaselineShot(p.savedDir, p.saved, k)
			}
			s := diffPair(imgs, pr.before, pr.after, j.size, cfg)
			s.Scheme = j.scheme
			shots[i] = s
			tmu.Lock()
			if pr.after.title != "" {
				titles[j.page] = pr.after.title
			} else if pr.before.title != "" && titles[j.page] == "" {
				titles[j.page] = pr.before.title
			}
			tmu.Unlock()
		}(i, j)
	}
	wg.Wait()
	diffMS := int(time.Since(tDiff).Milliseconds())

	for _, pg := range cfg.Pages {
		page := vdPage{Path: pg, Title: titles[pg]}
		for i, j := range jobs {
			if j.page == pg {
				page.Shots = append(page.Shots, shots[i])
				page.MaxPct = max(page.MaxPct, rank(shots[i]))
			}
		}
		vd.Pages = append(vd.Pages, page)
	}
	sort.SliceStable(vd.Pages, func(i, j int) bool { return vd.Pages[i].MaxPct > vd.Pages[j].MaxPct })
	vd.Summary = summarize(vd.Pages)
	vd.Timing = vdTiming{StartMS: startMS, ShootMS: shootMS, DiffMS: diffMS, WriteMS: imgs.ms, Bytes: imgs.bytes, AllBytes: dirSize(imgDir)}
	vd.Timing.TotalMS = int(time.Since(t0).Milliseconds())
	raw, _ := json.MarshalIndent(vd, "", "  ")
	if err := ValidateVisualDiff(raw); err != nil {
		return ShotsResult{}, fmt.Errorf("the visual diff came out invalid: %w", err)
	}
	a, err := b.commitDiff(p.loc, p.wt, id, existing, key, raw, vd.Title, req.Note, ArtifactBy{Session: clipRunes(req.Session, 80), Agent: clipRunes(req.Agent, 40)})
	if err != nil {
		return ShotsResult{}, err
	}
	n := a.Latest().N
	return ShotsResult{Text: agentText(vd, a, imgDir), Artifact: a.ID, Version: n, Dir: filepath.Dir(imgDir)}, nil
}

// diffPair compares one shot's two sides and stores its images.
func diffPair(store *imgStore, before, after shot, size int, cfg ShotsConfig) vdShot {
	out := vdShot{Size: size, Viewport: [2]int{size, viewportHeight(size)}}
	img := func(s shot) *vdImage {
		if s.img == nil {
			return &vdImage{Status: s.status, Errors: s.errs, MS: s.ms}
		}
		name := store.put(s.png)
		return &vdImage{Img: name, W: s.w, H: s.h, Status: s.status, Errors: s.errs, MS: s.ms, Cut: s.cut, Overflow: s.overflow}
	}
	describe := func(s shot) string {
		switch {
		case s.why != "":
			return s.why
		case s.status >= 400:
			return "HTTP " + strconv.Itoa(s.status)
		}
		return "no image"
	}
	// A 404 on one side is a page that is new or gone; anything else that
	// failed (a 500, a refused connection, a page that never settled) is an
	// error, said plainly.
	missing := func(s shot) bool { return s.status == 404 || s.why == "no baseline" }
	bad := func(s shot) bool { return s.img == nil || s.why != "" || s.status >= 400 }
	verdict := ""
	switch {
	case bad(after) && !missing(after):
		verdict, out.Why = "error", "after: "+describe(after)
	case bad(before) && !missing(before):
		verdict, out.Why = "error", "before: "+describe(before)
	case missing(before) && missing(after):
		// Not there on either side: nothing changed (a page listed in
		// the config that neither side has yet).
		verdict, out.Why = "unchanged", "404 on both sides"
	case missing(after):
		verdict, out.Why = "removed", fmt.Sprintf("404 after, %d before", before.status)
	case missing(before):
		verdict, out.Why = "new", "404 before"
		if before.why == "no baseline" {
			out.Why = "not in the baseline"
		}
	}
	if verdict != "" {
		out.Verdict, out.Before, out.After = verdict, img(before), img(after)
		return out
	}
	// Each side's masks are painted on it (so moved content still matches
	// itself), and both sides' are left out of the diff.
	masks := append(append([]image.Rectangle{}, before.masks...), after.masks...)
	if len(before.masks) > 0 {
		paintMasks(before.img, before.masks)
		before.png = encodePNG(before.img)
	}
	if len(after.masks) > 0 {
		paintMasks(after.img, after.masks)
		after.png = encodePNG(after.img)
	}
	t := time.Now()
	d := PixelDiff(before.img, after.img, DiffOptions{Threshold: cfg.Threshold, Masks: masks})
	out.DiffMS = int(time.Since(t).Milliseconds())
	out.Before, out.After = img(before), img(after)
	if after.status >= 300 || before.status >= 300 {
		out.Why = fmt.Sprintf("HTTP %d before, %d after", before.status, after.status)
	}
	out.Changed, out.AA, out.Extra, out.Shift = d.Changed, d.AA, d.Extra, d.Shift
	out.Pct = round(d.Percent(), 3)
	for _, m := range masks {
		out.Masks = append(out.Masks, [4]int{m.Min.X, m.Min.Y, m.Dx(), m.Dy()})
	}
	overflow := after.overflow > 0 && before.overflow == 0
	if overflow {
		out.Checks = append(out.Checks, fmt.Sprintf("overflow_x:%d", after.overflow))
	}
	if (d.Changed == 0 || out.Pct < cfg.Unchanged) && !overflow {
		out.Verdict = "unchanged"
		return out
	}
	out.Verdict = "changed"
	out.Regions, out.RegionsTotal = d.Regions, len(d.Regions)
	if len(out.Regions) > 12 {
		out.Regions = out.Regions[:12]
	}
	for i := range out.Regions {
		out.Regions[i].El = nameRegion(out.Regions[i], after.els)
	}
	if d.Changed > 0 {
		out.Heat = store.put(encodePNG(d.Heat))
	}
	for i, r := range out.Regions {
		if i == 3 {
			break
		}
		out.Crops = append(out.Crops, store.put(encodePNG(regionCrop(before.img, after.img, r, 800))))
	}
	return out
}

func round(x float64, places int) float64 {
	p := 1.0
	for range places {
		p *= 10
	}
	return float64(int(x*p+0.5)) / p
}

// --- API ----------------------------------------------------------------------

// POST /v1/worktrees/{loc}/{wt}/shots/compare  ShotsRequest → ShotsResult
// POST /v1/worktrees/{loc}/{wt}/shots/accept   {artifact} → ShotsResult
// GET  /v1/worktrees/{loc}/{wt}/shots/baselines → []BaselineInfo
func (b *Box) mountShots(route func(string, func(http.ResponseWriter, *http.Request) error)) {
	route("POST /v1/worktrees/{loc}/{wt}/shots/compare", func(w http.ResponseWriter, r *http.Request) error {
		var req ShotsRequest
		if err := decode(r, &req); err != nil {
			return err
		}
		res, err := b.ShotsCompare(r.Context(), r.PathValue("loc"), r.PathValue("wt"), req)
		if err != nil {
			return err
		}
		writeJSON(w, res)
		return nil
	})
	route("GET /v1/worktrees/{loc}/{wt}/shots/baselines", func(w http.ResponseWriter, r *http.Request) error {
		list, err := b.Baselines(r.Context(), r.PathValue("loc"), r.PathValue("wt"))
		if err != nil {
			return err
		}
		writeJSON(w, list)
		return nil
	})
	route("POST /v1/worktrees/{loc}/{wt}/shots/accept", func(w http.ResponseWriter, r *http.Request) error {
		var req struct {
			Artifact string `json:"artifact"`
		}
		if err := decode(r, &req); err != nil {
			return err
		}
		res, err := b.AcceptBaseline(r.Context(), r.PathValue("loc"), r.PathValue("wt"), req.Artifact)
		if err != nil {
			return err
		}
		writeJSON(w, res)
		return nil
	})
}
