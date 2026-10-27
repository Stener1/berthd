package box

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cosscom/shipyard/internal/statefile"
)

// The agent browser's size: the page's viewport in CSS pixels and its
// device scale factor, which an agent chooses (berthd browser resize
// 390x844, or open --size), kept per worktree until it changes, in
// <Dir>/viewports.json. A browser starts at its worktree's size; one
// already running changes at once. Shots and the app's live view follow
// it.

// Viewport is a page size: CSS pixels wide and tall, at a device scale.
type Viewport struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
}

// DefaultViewport is the size a worktree's browser opens at until an agent
// chooses another: a common desktop.
var DefaultViewport = Viewport{Width: 1920, Height: 1080, Scale: 1}

// Bounds an agent's size must keep to.
const (
	minViewportW, maxViewportW = 320, 3840
	minViewportH, maxViewportH = 240, 2160
	minViewportScale           = 1.0
	maxViewportScale           = 3.0
)

// ViewportPreset is a named size, the app's Preview sizes among them.
type ViewportPreset struct {
	Name   string
	Detail string
	Viewport
}

// ViewportPresets are the sizes agents can name. Phone, Phone Max and
// Tablet are the app's Preview devices.
var ViewportPresets = []ViewportPreset{
	{"phone", "iPhone 15 / 16", Viewport{390, 844, 1}},
	{"phone-max", "iPhone 15 / 16 Pro Max", Viewport{430, 932, 1}},
	{"tablet", "iPad Air", Viewport{820, 1180, 1}},
	{"laptop", "a 13-inch laptop", Viewport{1280, 800, 1}},
	{"desktop", "the default", DefaultViewport},
}

// String is the size as people read it: 1920×1080, or 390×844 @3x.
func (v Viewport) String() string {
	s := fmt.Sprintf("%d×%d", v.Width, v.Height)
	if v.Scale != 1 && v.Scale != 0 {
		s += " @" + strconv.FormatFloat(v.Scale, 'f', -1, 64) + "x"
	}
	return s
}

// Pixels is the size of a frame at the device scale.
func (v Viewport) Pixels() (int, int) {
	s := v.scale()
	return int(math.Round(float64(v.Width) * s)), int(math.Round(float64(v.Height) * s))
}

func (v Viewport) scale() float64 {
	if v.Scale <= 0 {
		return 1
	}
	return v.Scale
}

// Check says what is wrong with a size, in words an agent can act on.
func (v Viewport) Check() error {
	switch {
	case v.Width < minViewportW || v.Width > maxViewportW:
		return fmt.Errorf("width %d is out of range: %d to %d CSS pixels", v.Width, minViewportW, maxViewportW)
	case v.Height < minViewportH || v.Height > maxViewportH:
		return fmt.Errorf("height %d is out of range: %d to %d CSS pixels", v.Height, minViewportH, maxViewportH)
	case math.IsNaN(v.Scale) || v.Scale < minViewportScale || v.Scale > maxViewportScale:
		return fmt.Errorf("scale %s is out of range: %g to %g", strconv.FormatFloat(v.Scale, 'f', -1, 64), minViewportScale, maxViewportScale)
	}
	return nil
}

// presetNames lists the presets, for an error: phone (390x844), ….
func presetNames() string {
	var n []string
	for _, p := range ViewportPresets {
		n = append(n, fmt.Sprintf("%s (%dx%d)", p.Name, p.Width, p.Height))
	}
	return strings.Join(n, ", ")
}

// ParseSize reads WIDTHxHEIGHT (x, X or ×) or a preset's name. A preset
// brings its own scale; a plain size has none (0), for the caller to keep.
func ParseSize(s string) (Viewport, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return Viewport{}, fmt.Errorf("give a size, WIDTHxHEIGHT such as 1280x800, or one of %s", presetNames())
	}
	if s == "default" {
		return DefaultViewport, nil
	}
	for _, p := range ViewportPresets {
		if p.Name == s {
			return p.Viewport, nil
		}
	}
	ws, hs, ok := strings.Cut(strings.ReplaceAll(s, "×", "x"), "x")
	if !ok {
		return Viewport{}, fmt.Errorf("%q is not a size: give WIDTHxHEIGHT such as 1280x800, or one of %s", s, presetNames())
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(ws))
	h, err2 := strconv.Atoi(strings.TrimSpace(hs))
	if err1 != nil || err2 != nil {
		return Viewport{}, fmt.Errorf("%q is not a size: give whole CSS pixels, WIDTHxHEIGHT such as 1280x800", s)
	}
	return Viewport{Width: w, Height: h}, nil
}

// ParseScale reads a device scale factor: 1, 2, 2.5 or 3, with or without
// an x.
func ParseScale(s string) (float64, error) {
	s = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "x")
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%q is not a scale: give a number from %g to %g, such as 2", s, minViewportScale, maxViewportScale)
	}
	return f, nil
}

// Resolve applies a size and a scale (either may be empty) to cur: what
// isn't given stays as it was, except that a preset brings its own scale
// unless one is given.
func (cur Viewport) Resolve(size, scale string) (Viewport, error) {
	v := cur
	if v.Width == 0 {
		v = DefaultViewport
	}
	if strings.TrimSpace(size) == "" && strings.TrimSpace(scale) == "" {
		return Viewport{}, fmt.Errorf("give a size (WIDTHxHEIGHT such as 1280x800, or one of %s), a --scale, or both", presetNames())
	}
	if strings.TrimSpace(size) != "" {
		n, err := ParseSize(size)
		if err != nil {
			return Viewport{}, err
		}
		v.Width, v.Height = n.Width, n.Height
		if n.Scale != 0 {
			v.Scale = n.Scale
		}
	}
	if strings.TrimSpace(scale) != "" {
		f, err := ParseScale(scale)
		if err != nil {
			return Viewport{}, err
		}
		v.Scale = f
	}
	if v.Scale == 0 {
		v.Scale = 1
	}
	return v, v.Check()
}

func (m *Browsers) viewportsPath() string { return filepath.Join(m.Dir, "viewports.json") }

// viewports are the sizes agents chose, by worktree path, and for a named
// tab by viewportKey.
func (m *Browsers) viewports() map[string]Viewport {
	out := map[string]Viewport{}
	if b, err := os.ReadFile(m.viewportsPath()); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

// viewportKey is where a tab's size is kept: the worktree's path for its
// main tab, with "#tab=NAME" after it for a named one.
func viewportKey(path, tab string) string {
	path = filepath.Clean(path)
	if tab == "" {
		return path
	}
	return path + "#tab=" + tab
}

// Viewport is the size a worktree's tab has, or opens at: a named tab
// without one of its own opens at the main tab's.
func (m *Browsers) Viewport(path, tab string) Viewport {
	m.vpMu.Lock()
	defer m.vpMu.Unlock()
	all := m.viewports()
	if v, ok := all[viewportKey(path, tab)]; ok && v.Check() == nil {
		return v
	}
	if v, ok := all[viewportKey(path, "")]; ok && v.Check() == nil {
		return v
	}
	return DefaultViewport
}

// setViewport keeps a tab's size; the default is kept as no entry.
func (m *Browsers) setViewport(path, tab string, v Viewport) error {
	m.vpMu.Lock()
	defer m.vpMu.Unlock()
	all := m.viewports()
	key := viewportKey(path, tab)
	if v == DefaultViewport {
		if _, ok := all[key]; !ok {
			return nil
		}
		delete(all, key)
	} else {
		all[key] = v
	}
	return m.writeViewports(all)
}

func (m *Browsers) writeViewports(all map[string]Viewport) error {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(all, "", "  ")
	return statefile.Write(m.viewportsPath(), append(data, '\n'))
}

// forgetViewport drops a removed worktree's sizes, its tabs' too.
func (m *Browsers) forgetViewport(path string) {
	m.vpMu.Lock()
	defer m.vpMu.Unlock()
	all := m.viewports()
	key, n := viewportKey(path, ""), len(all)
	for k := range all {
		if k == key || strings.HasPrefix(k, key+"#tab=") {
			delete(all, k)
		}
	}
	if len(all) != n {
		m.writeViewports(all)
	}
}

// Resize sets a tab's size, keeps it, and applies it to the tab if it is
// open (returned, or nil).
func (m *Browsers) Resize(ctx context.Context, path, tab string, v Viewport) (*browser, error) {
	if err := v.Check(); err != nil {
		return nil, err
	}
	if err := m.setViewport(path, tab, v); err != nil {
		return nil, err
	}
	br := m.Lookup(path, tab)
	if br == nil {
		return nil, nil
	}
	return br, br.resize(ctx, v)
}

// resize applies a size to the running page: its viewport and scale, the
// screencast's frames, and a fresh frame for whoever watches.
func (br *browser) resize(ctx context.Context, v Viewport) error {
	defer br.begin()()
	if err := br.applyViewport(ctx, v); err != nil {
		return err
	}
	br.mu.Lock()
	casting := br.casting
	var watchers []chan frame
	for ch := range br.watchers {
		watchers = append(watchers, ch)
	}
	br.mu.Unlock()
	if casting {
		br.cdp.call(ctx, br.session, "Page.stopScreencast", nil, nil)
		br.cdp.call(ctx, br.session, "Page.startScreencast", br.castParams(v), nil)
	}
	for _, ch := range watchers {
		go br.firstFrame(context.Background(), ch)
	}
	return nil
}

// applyViewport sets the page's size and scale.
func (br *browser) applyViewport(ctx context.Context, v Viewport) error {
	if err := br.cdp.call(ctx, br.session, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": v.Width, "height": v.Height, "deviceScaleFactor": v.scale(), "mobile": false,
		"screenWidth": v.Width, "screenHeight": v.Height,
	}, nil); err != nil {
		return fmt.Errorf("resizing the page: %w", err)
	}
	br.mu.Lock()
	br.viewport = v
	br.mu.Unlock()
	return nil
}

// maxCastSide caps a screencast frame's longer side in device pixels: a
// 4K page at 2x would be 7680 pixels wide, far more than any pane shows,
// and every frame crosses the network to the laptop.
const maxCastSide = 2560

// castParams are the screencast's: frames at the page's own pixels, so the
// app can show them sharp, up to maxCastSide.
func (br *browser) castParams(v Viewport) map[string]any {
	w, h := v.Pixels()
	if long := max(w, h); long > maxCastSide {
		w, h = w*maxCastSide/long, h*maxCastSide/long
	}
	return map[string]any{"format": "jpeg", "quality": 60, "maxWidth": w, "maxHeight": h, "everyNthFrame": 1}
}

// captureFrame takes the page as it is now, a JPEG at the page's scale
// (its longer side at most maxCastSide pixels), as a frame for watchers.
func (br *browser) captureFrame(ctx context.Context) (frame, error) {
	v := br.currentViewport()
	params := map[string]any{"format": "jpeg", "quality": 60}
	if w, h := v.Pixels(); max(w, h) > maxCastSide {
		var metrics struct {
			Visual struct {
				PageX float64 `json:"pageX"`
				PageY float64 `json:"pageY"`
			} `json:"cssVisualViewport"`
		}
		br.cdp.call(ctx, br.session, "Page.getLayoutMetrics", nil, &metrics)
		params["clip"] = map[string]any{"x": metrics.Visual.PageX, "y": metrics.Visual.PageY, "width": v.Width, "height": v.Height, "scale": float64(maxCastSide) / float64(max(w, h))}
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := br.cdp.call(ctx, br.session, "Page.captureScreenshot", params, &shot); err != nil {
		return frame{}, err
	}
	if shot.Data == "" {
		return frame{}, errors.New("no image")
	}
	return frame{Data: shot.Data, Width: v.Width, Height: v.Height, Scale: v.scale(), URL: br.currentURL()}, nil
}

// recaptureLocked takes a sharp frame for watchers soon, one at a time: a
// change while one is taken is taken next. br.mu is held.
func (br *browser) recaptureLocked() {
	if br.recapturing {
		br.dirty = true
		return
	}
	br.recapturing = true
	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			f, err := br.captureFrame(ctx)
			cancel()
			br.mu.Lock()
			if err == nil {
				for ch := range br.watchers {
					select {
					case ch <- f:
					default:
					}
				}
			}
			if !br.dirty || len(br.watchers) == 0 {
				br.recapturing, br.dirty = false, false
				br.mu.Unlock()
				return
			}
			br.dirty = false
			br.mu.Unlock()
			// At most the screencast's rate: the box sends 8 a second.
			time.Sleep(castEvery)
		}
	}()
}

// castEvery is the least time between two frames sent to the laptop.
const castEvery = 125 * time.Millisecond

func (br *browser) currentViewport() Viewport {
	br.mu.Lock()
	defer br.mu.Unlock()
	if br.viewport.Width == 0 {
		return DefaultViewport
	}
	return br.viewport
}
