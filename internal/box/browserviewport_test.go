package box

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosscom/shipyard/internal/events"
)

func TestBrowserSizesParseWithClearErrors(t *testing.T) {
	if DefaultViewport != (Viewport{1920, 1080, 1}) || DefaultViewport.String() != "1920×1080" {
		t.Fatalf("default %+v %s", DefaultViewport, DefaultViewport)
	}
	cur := Viewport{1280, 800, 2}
	for _, c := range []struct {
		size, scale string
		want        Viewport
	}{
		{"1280x800", "", Viewport{1280, 800, 2}}, // the scale stays
		{"390X844", "3", Viewport{390, 844, 3}},
		{"390×844", "2x", Viewport{390, 844, 2}},
		{" 1440x900 ", "1.5", Viewport{1440, 900, 1.5}},
		{"phone", "", Viewport{390, 844, 1}}, // a preset brings its scale
		{"phone", "3", Viewport{390, 844, 3}},
		{"Tablet", "", Viewport{820, 1180, 1}},
		{"phone-max", "", Viewport{430, 932, 1}},
		{"default", "", DefaultViewport},
		{"", "1", Viewport{1280, 800, 1}}, // only the scale
		{"320x240", "", Viewport{320, 240, 2}},
		{"3840x2160", "", Viewport{3840, 2160, 2}},
	} {
		got, err := cur.Resolve(c.size, c.scale)
		if err != nil || got != c.want {
			t.Errorf("Resolve(%q, %q) = %+v, %v; want %+v", c.size, c.scale, got, err, c.want)
		}
	}
	if got, _ := (Viewport{}).Resolve("800x600", ""); got != (Viewport{800, 600, 1}) {
		t.Errorf("from nothing: %+v", got)
	}
	for _, c := range []struct{ size, scale, says string }{
		{"", "", "give a size"},
		{"big", "", `"big" is not a size`},
		{"12ax800", "", "whole CSS pixels"},
		{"1280.5x800", "", "whole CSS pixels"},
		{"319x800", "", "width 319 is out of range: 320 to 3840"},
		{"3841x800", "", "width 3841 is out of range"},
		{"1280x239", "", "height 239 is out of range: 240 to 2160"},
		{"1280x2161", "", "height 2161 is out of range"},
		{"-1x800", "", "out of range"},
		{"1280x800", "0.5", "scale 0.5 is out of range: 1 to 3"},
		{"1280x800", "4", "scale 4 is out of range"},
		{"1280x800", "two", `"two" is not a scale`},
		{"1280x800", "NaN", "is not a scale"},
	} {
		_, err := cur.Resolve(c.size, c.scale)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("Resolve(%q, %q) = %v; want it to say %q", c.size, c.scale, err, c.says)
		}
	}
	if s := (Viewport{390, 844, 3}).String(); s != "390×844 @3x" {
		t.Errorf("String %q", s)
	}
	if w, h := (Viewport{390, 844, 3}).Pixels(); w != 1170 || h != 2532 {
		t.Errorf("Pixels %d×%d", w, h)
	}
	// A big page at 2x casts at most maxCastSide pixels on its longer side.
	br := &browser{}
	p := br.castParams(Viewport{3840, 2160, 2})
	if p["maxWidth"] != maxCastSide || p["maxHeight"] != 1440 {
		t.Errorf("cast %v", p)
	}
	if p := br.castParams(Viewport{390, 844, 2}); p["maxWidth"] != 780 || p["maxHeight"] != 1688 {
		t.Errorf("cast %v", p)
	}
}

func TestABrowserSizeIsKeptPerWorktreeUntilChanged(t *testing.T) {
	dir := t.TempDir()
	b := &Box{Name: "devbox", Events: &events.Bus{}}
	m := b.NewBrowsers(dir, 1)
	if v := m.Viewport("/w/cal-billing", ""); v != DefaultViewport {
		t.Fatalf("before any: %+v", v)
	}
	phone := Viewport{390, 844, 3}
	if br, err := m.Resize(context.Background(), "/w/cal-billing", "", phone); err != nil || br != nil {
		t.Fatalf("resize with no browser: %v %v", br, err)
	}
	if _, err := m.Resize(context.Background(), "/w/cal-other", "", Viewport{100, 100, 1}); err == nil {
		t.Fatal("a size out of range was kept")
	}
	// Kept on disk: a berthd that restarts still has it.
	again := (&Box{Name: "devbox", Events: &events.Bus{}}).NewBrowsers(dir, 1)
	if v := again.Viewport("/w/cal-billing/", ""); v != phone {
		t.Fatalf("after a restart: %+v", v)
	}
	if v := again.Viewport("/w/cal-other", ""); v != DefaultViewport {
		t.Fatalf("another worktree: %+v", v)
	}
	// A size that isn't sane any more (a hand edit) is the default.
	os.WriteFile(filepath.Join(dir, "viewports.json"), []byte(`{"/w/cal-billing":{"width":99999,"height":1,"scale":1}}`), 0o600)
	if v := again.Viewport("/w/cal-billing", ""); v != DefaultViewport {
		t.Fatalf("a bad saved size: %+v", v)
	}
	again.Resize(context.Background(), "/w/cal-billing", "", phone)
	// Back to the default leaves nothing behind.
	again.Resize(context.Background(), "/w/cal-billing", "", DefaultViewport)
	if raw, _ := os.ReadFile(filepath.Join(dir, "viewports.json")); strings.Contains(string(raw), "cal-billing") {
		t.Fatalf("the default is kept as an entry: %s", raw)
	}
	// A removed worktree's size goes with it.
	again.Resize(context.Background(), "/w/cal-billing", "", phone)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { again.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for again.Viewport("/w/cal-billing", "") == phone && time.Now().Before(deadline) {
		again.b.Events.Publish(events.Event{Type: "worktree.removed", Data: map[string]any{"path": "/w/cal-billing"}})
		time.Sleep(50 * time.Millisecond)
	}
	if v := again.Viewport("/w/cal-billing", ""); v != DefaultViewport {
		t.Fatalf("a removed worktree's size stayed: %+v", v)
	}
}

func TestBrowserResizeOverTheAPI(t *testing.T) {
	dir := t.TempDir()
	c, _ := servedBox(t, func(b *Box) { b.NewBrowsers(filepath.Join(dir, "browser"), 1) })
	repo := gitRepo(t)
	call(t, c, "POST", "/v1/locations", "", map[string]string{"name": "cal", "path": repo}, nil)
	var locs []Location
	call(t, c, "GET", "/v1/locations", "", nil, &locs)
	wt := locs[0].Worktrees[0].Name
	base := "/v1/worktrees/cal/" + wt + "/browser/"

	var st struct {
		Running  bool
		Size     string
		Viewport Viewport
		Text     string
	}
	call(t, c, "GET", base+"status", "", nil, &st)
	if st.Running || st.Size != "1920×1080" || !strings.Contains(st.Text, "opens at 1920×1080") {
		t.Fatalf("status before: %+v", st)
	}
	var h BrowserHealth
	call(t, c, "GET", "/v1/browser/health", "", nil, &h)
	if h.Size != "1920×1080" || h.Viewport != DefaultViewport {
		t.Fatalf("health: %+v", h)
	}

	var res struct {
		Size    string
		Running bool
		Text    string
	}
	if status := call(t, c, "POST", base+"resize", "", map[string]string{"size": "phone", "scale": "3"}, &res); status != 200 || res.Size != "390×844 @3x" || res.Running || !strings.Contains(res.Text, "opens at this size") {
		t.Fatalf("resize: %d %+v", status, res)
	}
	call(t, c, "GET", base+"status", "", nil, &st)
	if st.Size != "390×844 @3x" || st.Viewport != (Viewport{390, 844, 3}) {
		t.Fatalf("status after: %+v", st)
	}
	// Only the scale: the size stays.
	call(t, c, "POST", base+"resize", "", map[string]string{"scale": "2"}, &res)
	if res.Size != "390×844 @2x" {
		t.Fatalf("scale only: %+v", res)
	}
	var bad struct{ Error string }
	for body, says := range map[string]string{
		`{"size":"5000x800"}`: "width 5000 is out of range: 320 to 3840",
		`{"scale":"9"}`:       "scale 9 is out of range: 1 to 3",
		`{}`:                  "give a size",
	} {
		var in map[string]string
		json.Unmarshal([]byte(body), &in)
		if status := call(t, c, "POST", base+"resize", "", in, &bad); status != 400 || !strings.Contains(bad.Error, says) {
			t.Errorf("resize %s: %d %q", body, status, bad.Error)
		}
		// Open checks the size before it starts anything.
		if body == `{}` {
			continue
		}
		if status := call(t, c, "POST", base+"open", "", in, &bad); status != 400 || !strings.Contains(bad.Error, says) {
			t.Errorf("open %s: %d %q", body, status, bad.Error)
		}
	}
	call(t, c, "GET", base+"status", "", nil, &st)
	if st.Size != "390×844 @2x" {
		t.Fatalf("a refused size changed it: %+v", st)
	}
}

// TestTheBrowserSizeReachesARealChromium checks that the page, its shots
// and its screencast take the size an agent chose.
func TestTheBrowserSizeReachesARealChromium(t *testing.T) {
	if testing.Short() {
		t.Skip("starts Chromium")
	}
	if os.Getenv("BERTH_TEST_CHROMIUM") == "" && os.Getenv("CI") != "" {
		t.Skip("set BERTH_TEST_CHROMIUM=1 to start a real Chromium in CI")
	}
	if _, err := FindChromium(); err != nil {
		t.Skip(err)
	}
	b, wt, _ := browserBox(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><meta name="viewport" content="width=device-width"><title>Sizes</title><body style="margin:0;background:#4f46e5"><h1 style="color:#fff">Hello</h1><button>Go</button>`)
	})
	m := b.NewBrowsers(t.TempDir(), 2)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	loc, _ := b.Locations.Get(ctx, "cal")
	br, err := m.get(ctx, loc, wt, "")
	if errors.Is(err, ErrBrowserSandbox) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll("test")
	res, err := br.Open(ctx, "http://billing.cal.mybox.localhost:1377/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "size: 1920×1080") {
		t.Fatalf("open does not say the size:\n%s", res.Text)
	}
	inner := func() string {
		ev, _ := br.Eval(ctx, "[innerWidth, innerHeight, devicePixelRatio].join(' ')")
		return ev.Text
	}
	shotSize := func(native bool, width int) (int, int, string) {
		s, err := br.Shot(ctx, "", false, width, native)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(s.File)
		w, h := pngSize(raw)
		return w, h, s.Text
	}
	if got := inner(); got != `"1920 1080 1"` {
		t.Fatalf("default page: %s", got)
	}
	if w, h, text := shotSize(true, 0); w != 1920 || h != 1080 || !strings.Contains(text, "(1920x1080 of the 1920×1080 page)") {
		t.Fatalf("native shot %dx%d: %s", w, h, text)
	}
	if w, h, text := shotSize(false, 800); w != 800 || h != 450 || !strings.Contains(text, "(800x450 of the 1920×1080 page)") {
		t.Fatalf("an 800-wide shot %dx%d: %s", w, h, text)
	}

	// A phone at 2x, applied to the running page at once.
	if _, err := m.Resize(ctx, wt.Path, "", Viewport{390, 844, 2}); err != nil {
		t.Fatal(err)
	}
	if got := inner(); got != `"390 844 2"` {
		t.Fatalf("phone page: %s", got)
	}
	if w, h, _ := shotSize(true, 0); w != 780 || h != 1688 {
		t.Fatalf("native phone shot %dx%d", w, h)
	}
	// 800 wide at most: a 780-pixel phone stays as it is.
	if w, h, text := shotSize(false, 800); w != 780 || h != 1688 || !strings.Contains(text, "390×844 @2x page") {
		t.Fatalf("phone shot %dx%d: %s", w, h, text)
	}
	if w, _, _ := shotSize(false, 390); w != 390 {
		t.Fatalf("a 390-wide shot of a 2x page is %d wide", w)
	}
	if st := br.status(); st.Size != "390×844 @2x" {
		t.Fatalf("status %+v", st)
	}
	// The live view gets frames of the page's size, at its scale.
	frames, stop := br.Watch(ctx)
	checkFrame := func(wantW, wantH, pxW, pxH int) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		last := "none"
		for {
			select {
			case f := <-frames:
				if f.Width != wantW || f.Height != wantH {
					continue // a frame from before the change
				}
				raw, _ := base64.StdEncoding.DecodeString(f.Data)
				img, _, err := image.DecodeConfig(strings.NewReader(string(raw)))
				if err != nil {
					t.Fatalf("frame: %v", err)
				}
				if img.Width != pxW || img.Height != pxH {
					// Chromium can cast a frame or two of the old window
					// under the new size while it resizes; a slow runner
					// sees them.
					last = fmt.Sprintf("%dx%d", img.Width, img.Height)
					continue
				}
				return
			case <-deadline:
				t.Fatalf("no %dx%d frame of %dx%d pixels (the last was %s)", wantW, wantH, pxW, pxH, last)
			}
		}
	}
	checkFrame(390, 844, 780, 1688)
	// A change to the page is cast sharp too, not at Chromium's own 1x.
	time.Sleep(300 * time.Millisecond)
	for len(frames) > 0 {
		<-frames
	}
	br.Eval(ctx, "document.querySelector('h1').textContent = 'Changed'")
	checkFrame(390, 844, 780, 1688)
	// A size changed while someone watches reaches them.
	if _, err := m.Resize(ctx, wt.Path, "", Viewport{1280, 800, 1}); err != nil {
		t.Fatal(err)
	}
	checkFrame(1280, 800, 1280, 800)
	stop()

	// A browser that starts again starts at the kept size.
	m.CloseAll("test")
	br, err = m.get(ctx, loc, wt, "")
	if err != nil {
		t.Fatal(err)
	}
	br.Open(ctx, "http://billing.cal.mybox.localhost:1377/")
	if got := inner(); got != `"1280 800 1"` {
		t.Fatalf("restarted page: %s", got)
	}
}
