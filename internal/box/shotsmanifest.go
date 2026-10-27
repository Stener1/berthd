package box

import (
	"encoding/json"
	"fmt"
	"image"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The visual-diff manifest, berth.visualdiff/v1: everything a compare
// found, as data the app draws (no code runs), with its images named by
// their content hash. See docs/guides/visual-diffs.mdx.

// VisualDiffSchema is the manifest's $schema.
const VisualDiffSchema = "berth.visualdiff/v1"

// VisualDiff is a visual diff's manifest.
type VisualDiff struct {
	Schema   string     `json:"$schema"`
	Title    string     `json:"title"`
	Created  time.Time  `json:"created"`
	Base     vdSide     `json:"base"`
	Head     vdSide     `json:"head"`
	Settings vdSettings `json:"settings"`
	Summary  vdSummary  `json:"summary"`
	Pages    []vdPage   `json:"pages"`
	Timing   vdTiming   `json:"timing"`
	Note     string     `json:"note,omitempty"`
	// Notice says what the compare did differently from what was asked:
	// "main's dev server isn't running; compared with the turn-start
	// baseline instead".
	Notice string `json:"notice,omitempty"`
}

type vdSide struct {
	Kind   string `json:"kind"` // worktree, main, baseline
	Label  string `json:"label"`
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
	Dirty  int    `json:"dirty,omitempty"` // files changed and not committed
	Taken  string `json:"taken,omitempty"` // a baseline's time
}

type vdSettings struct {
	Sizes         []int    `json:"sizes"`
	Scale         float64  `json:"scale"`
	Threshold     float64  `json:"threshold"`
	Unchanged     float64  `json:"unchanged_below"`
	MaxHeight     int      `json:"max_height"`
	ColorScheme   string   `json:"color_scheme"`  // light, dark or both
	ColorSchemes  []string `json:"color_schemes"` // what was shot: [light], [dark], [light, dark]
	ReducedMotion bool     `json:"reduced_motion"`
	Mask          []string `json:"mask,omitempty"`
	Chromium      string   `json:"chromium,omitempty"`
	// Login is the email both sides were logged in as (--as).
	Login string `json:"login,omitempty"`
}

type vdSummary struct {
	Shots     int     `json:"shots"`
	Changed   int     `json:"changed"`
	Unchanged int     `json:"unchanged"`
	New       int     `json:"new"`
	Removed   int     `json:"removed"`
	Errors    int     `json:"errors"`
	Regions   int     `json:"regions"`
	Layout    int     `json:"layout"` // shots with a layout check that failed
	MaxPct    float64 `json:"max_pct"`
	Text      string  `json:"text"` // the headline
}

type vdPage struct {
	Path  string   `json:"path"`
	Title string   `json:"title,omitempty"`
	Shots []vdShot `json:"shots"`
	// MaxPct orders pages: the most changed first (errors 1000, new or
	// removed 500).
	MaxPct float64 `json:"max_pct"`
}

type vdShot struct {
	Size     int      `json:"size"`
	Scheme   string   `json:"scheme"` // light or dark
	Viewport [2]int   `json:"viewport"`
	Verdict  string   `json:"verdict"` // changed, unchanged, new, removed, error
	Why      string   `json:"why,omitempty"`
	Before   *vdImage `json:"before,omitempty"`
	After    *vdImage `json:"after,omitempty"`
	Heat     string   `json:"heat,omitempty"`
	Changed  int      `json:"changed_px"`
	Pct      float64  `json:"changed_pct"`
	AA       int      `json:"aa_px,omitempty"`
	Extra    int      `json:"extra_px,omitempty"`
	// Regions keeps the largest 12; RegionsTotal counts every one.
	Regions      []DiffRegion `json:"regions,omitempty"`
	RegionsTotal int          `json:"regions_total,omitempty"`
	// Checks are layout problems found besides the pixels: "overflow_x:N"
	// (the page scrolls sideways by N px now and did not before).
	Checks []string `json:"checks,omitempty"`
	Crops  []string `json:"crops,omitempty"` // before|after of the largest regions
	Masks  [][4]int `json:"masks,omitempty"`
	// Shift: from after-row y down, the page only moved by dy.
	Shift  *DiffShift `json:"shift,omitempty"`
	DiffMS int        `json:"diff_ms"`
}

type vdImage struct {
	Img    string   `json:"img,omitempty"`
	W      int      `json:"w,omitempty"`
	H      int      `json:"h,omitempty"`
	Status int      `json:"status,omitempty"`
	Errors []string `json:"errors,omitempty"` // the page's own errors while it loaded
	MS     int      `json:"ms,omitempty"`     // load and shot
	Cut    bool     `json:"cut,omitempty"`    // taller than max_height
	// Overflow is how many CSS pixels the page is wider than the viewport.
	Overflow int `json:"overflow_x,omitempty"`
}

type vdTiming struct {
	TotalMS  int `json:"total_ms"`
	StartMS  int `json:"browser_start_ms"`
	ShootMS  int `json:"shoot_ms"`
	DiffMS   int `json:"diff_ms"`
	WriteMS  int `json:"write_ms"`
	Bytes    int `json:"bytes"`     // this version's new images
	AllBytes int `json:"all_bytes"` // the visual diff's images on disk
}

var (
	vdVerdicts = []string{"changed", "unchanged", "new", "removed", "error"}
	vdImgRe    = regexp.MustCompile(`^[0-9a-f]{16}\.png$`)
)

// maxVisualDiff bounds a manifest: 50 pages × 6 sizes × 2 schemes fits
// with room.
const maxVisualDiff = 4 << 20

// ValidateVisualDiff checks a berth.visualdiff/v1 manifest: its schema,
// that every shot is a known verdict at a sane size, that every image it
// names is a content-hash name (nothing else is ever served), and that
// regions lie inside their shot.
func ValidateVisualDiff(raw []byte) error {
	if len(raw) > maxVisualDiff {
		return badRequest("a visual diff's manifest may be %d KB; this is %d KB", maxVisualDiff>>10, len(raw)>>10)
	}
	var vd VisualDiff
	if err := json.Unmarshal(raw, &vd); err != nil {
		return badRequest("not a visual diff: %v", err)
	}
	if vd.Schema != VisualDiffSchema {
		return badRequest("a visual diff's $schema is %q, not %q", VisualDiffSchema, vd.Schema)
	}
	if strings.TrimSpace(vd.Title) == "" {
		return badRequest("a visual diff needs a title")
	}
	if len(vd.Pages) == 0 {
		return badRequest("a visual diff has at least one page")
	}
	img := func(where, name string) error {
		if name != "" && !vdImgRe.MatchString(name) {
			return badRequest("%s: %q isn't an image this box stored (16 hex digits, .png)", where, name)
		}
		return nil
	}
	for _, p := range vd.Pages {
		if !strings.HasPrefix(p.Path, "/") {
			return badRequest("page %q: a path starts with /", p.Path)
		}
		if len(p.Shots) == 0 {
			return badRequest("page %s has no shots", p.Path)
		}
		for _, s := range p.Shots {
			where := fmt.Sprintf("%s at %d", p.Path, s.Size)
			if s.Size < minShotSize || s.Size > maxShotSize {
				return badRequest("%s: a size is %d to %d pixels wide", where, minShotSize, maxShotSize)
			}
			if !slices.Contains(vdVerdicts, s.Verdict) {
				return badRequest("%s: verdict %q isn't one of %s", where, s.Verdict, strings.Join(vdVerdicts, ", "))
			}
			if s.Scheme != "" && s.Scheme != "light" && s.Scheme != "dark" {
				return badRequest("%s: scheme %q isn't light or dark", where, s.Scheme)
			}
			if s.Pct < 0 || s.Pct > 100 {
				return badRequest("%s: changed_pct %v isn't 0 to 100", where, s.Pct)
			}
			for _, side := range []*vdImage{s.Before, s.After} {
				if side != nil {
					if err := img(where, side.Img); err != nil {
						return err
					}
				}
			}
			for _, n := range append([]string{s.Heat}, s.Crops...) {
				if err := img(where, n); err != nil {
					return err
				}
			}
			if s.Verdict == "changed" && (s.After == nil || s.After.Img == "" || s.Before == nil || s.Before.Img == "") {
				return badRequest("%s: a changed shot has a before and an after image", where)
			}
			if s.After != nil && s.After.Img != "" {
				bounds := image.Rect(0, 0, max(s.After.W, s.Before.wOr0()), max(s.After.H, s.Before.hOr0()))
				for _, r := range s.Regions {
					if r.W <= 0 || r.H <= 0 || !image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H).In(bounds) {
						return badRequest("%s: region %d,%d %dx%d lies outside the shot", where, r.X, r.Y, r.W, r.H)
					}
				}
			}
		}
	}
	return nil
}

func (i *vdImage) wOr0() int {
	if i == nil {
		return 0
	}
	return i.W
}

func (i *vdImage) hOr0() int {
	if i == nil {
		return 0
	}
	return i.H
}

// Images are every image a manifest names.
func (vd VisualDiff) Images() []string {
	var out []string
	for _, p := range vd.Pages {
		for _, s := range p.Shots {
			for _, side := range []*vdImage{s.Before, s.After} {
				if side != nil && side.Img != "" {
					out = append(out, side.Img)
				}
			}
			if s.Heat != "" {
				out = append(out, s.Heat)
			}
			out = append(out, s.Crops...)
		}
	}
	return out
}

// rank orders shots and pages: errors and new or removed pages first, then
// by how much changed, and a sideways scroll above any percentage.
func rank(s vdShot) float64 {
	switch s.Verdict {
	case "error":
		return 1000
	case "new", "removed":
		return 500
	}
	if len(s.Checks) > 0 {
		return 100 + s.Pct
	}
	return s.Pct
}

func summarize(pages []vdPage) vdSummary {
	var s vdSummary
	worst, worstSize, worstScheme := "", 0, ""
	changedPages := 0
	schemes, sizes := map[string]bool{}, map[int]bool{}
	for _, p := range pages {
		pc := false
		for _, sh := range p.Shots {
			s.Shots++
			schemes[sh.Scheme] = true
			sizes[sh.Size] = true
			switch sh.Verdict {
			case "changed":
				s.Changed++
				s.Regions += sh.RegionsTotal
				if len(sh.Checks) > 0 {
					s.Layout++
				}
				pc = true
				if sh.Pct > s.MaxPct || worst == "" {
					s.MaxPct, worst, worstSize, worstScheme = sh.Pct, p.Path, sh.Size, sh.Scheme
				}
			case "unchanged":
				s.Unchanged++
			case "new":
				s.New++
			case "removed":
				s.Removed++
			case "error":
				s.Errors++
			}
		}
		if pc {
			changedPages++
		}
	}
	at := strconv.Itoa(worstSize)
	if len(schemes) > 1 {
		at += " " + worstScheme
	}
	var parts []string
	switch {
	case s.Changed == 0 && s.New == 0 && s.Removed == 0 && s.Errors == 0:
		what := fmt.Sprintf("No visual changes · %s × %s", count(len(pages), "page", "pages"), count(len(sizes), "size", "sizes"))
		if len(schemes) > 1 {
			what += " × 2 schemes"
		}
		parts = append(parts, what)
	case s.Changed > 0:
		parts = append(parts, fmt.Sprintf("%d of %d pages changed · most: %s at %s (%s)", changedPages, len(pages), worst, at, pctText(s.MaxPct)))
	}
	if s.Layout > 0 {
		parts = append(parts, count(s.Layout, "layout warning", "layout warnings"))
	}
	if s.New > 0 {
		parts = append(parts, count(s.New, "new shot", "new shots"))
	}
	if s.Removed > 0 {
		parts = append(parts, count(s.Removed, "page gone", "pages gone"))
	}
	if s.Errors > 0 {
		parts = append(parts, count(s.Errors, "error", "errors"))
	}
	s.Text = strings.Join(parts, " · ")
	return s
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func pctText(p float64) string {
	switch {
	case p >= 10:
		return fmt.Sprintf("%.0f%%", p)
	case p >= 1:
		return fmt.Sprintf("%.1f%%", p)
	default:
		return fmt.Sprintf("%.2f%%", p)
	}
}

// agentText is what the agent reads, about 450 tokens for a typical run:
// the headline, then a line per changed shot (worst first within a page)
// with its largest region named by the element under it, a ⚠ for a
// sideways scroll, and a before|after crop to look at; errors and new
// pages one line per page, unchanged pages one line.
func agentText(vd VisualDiff, a Artifact, imgDir string) string {
	id, n := a.ID, a.Latest().N
	var b strings.Builder
	schemes := ""
	if len(vd.Settings.ColorSchemes) > 1 {
		schemes = " × 2 schemes"
	} else if len(vd.Settings.ColorSchemes) == 1 && vd.Settings.ColorSchemes[0] != "light" {
		schemes = " (" + vd.Settings.ColorSchemes[0] + ")"
	}
	fmt.Fprintf(&b, "visual diff %s v%d: %s vs %s, %d pages × %d sizes%s in %.1fs\n", id, n, vd.Head.Label, vd.Base.Label, len(vd.Pages), len(vd.Settings.Sizes), schemes, float64(vd.Timing.TotalMS)/1000)
	if vd.Notice != "" {
		fmt.Fprintf(&b, "note: %s\n", vd.Notice)
	}
	if vd.Settings.Login != "" {
		fmt.Fprintf(&b, "logged in as %s\n", vd.Settings.Login)
	}
	fmt.Fprintf(&b, "%s\n", vd.Summary.Text)
	both := len(vd.Settings.ColorSchemes) > 1
	sizeName := func(s vdShot) string {
		if both && s.Scheme == "dark" {
			return strconv.Itoa(s.Size) + " dark"
		}
		return strconv.Itoa(s.Size)
	}
	pad := strings.Repeat(" ", 22)
	crops := false
	for _, p := range vd.Pages {
		var unchanged, lines []string
		var sizes [][]string
		var grouped []bool
		same := map[string]int{}
		for _, s := range p.Shots {
			switch s.Verdict {
			case "unchanged":
				unchanged = append(unchanged, sizeName(s))
				continue
			case "changed":
				l := fmt.Sprintf("  %-14s %5s  changed %6s  %s", p.Path, sizeName(s), pctText(s.Pct), count(s.RegionsTotal, "region", "regions"))
				if len(s.Regions) > 0 {
					r := s.Regions[0]
					l += fmt.Sprintf(", largest %dx%d at %d,%d", r.W, r.H, r.X, r.Y)
					if r.El != "" {
						l += " (" + r.El + ")"
					}
				}
				if s.Before != nil && s.After != nil && s.Before.H != s.After.H {
					l += fmt.Sprintf("; height %d → %d", s.Before.H, s.After.H)
				}
				if s.Shift != nil {
					l += fmt.Sprintf("; %dpx from y=%d only moved %+dpx", s.Shift.H, s.Shift.Y, s.Shift.DY)
				}
				for _, c := range s.Checks {
					if px, ok := strings.CutPrefix(c, "overflow_x:"); ok {
						l += "\n" + pad + "⚠ now scrolls sideways: " + px + "px wider than the screen"
					}
				}
				if len(s.Crops) > 0 && imgDir != "" {
					l += "\n" + pad + "look: " + s.Crops[0]
					crops = true
				}
				lines = append(lines, l)
				sizes = append(sizes, nil)
				grouped = append(grouped, false)
			case "new", "removed", "error":
				// The same verdict at several sizes is one line.
				k := s.Verdict + "\x00" + s.Why
				if i, ok := same[k]; ok {
					sizes[i] = append(sizes[i], sizeName(s))
					continue
				}
				l := fmt.Sprintf("%s: %s", strings.ToUpper(s.Verdict), s.Why)
				if s.After != nil && len(s.After.Errors) > 0 {
					l += " · page error: " + s.After.Errors[0]
				}
				same[k] = len(lines)
				lines = append(lines, l)
				sizes = append(sizes, []string{sizeName(s)})
				grouped = append(grouped, true)
			}
		}
		for i, g := range grouped {
			if g {
				lines[i] = fmt.Sprintf("  %-14s %5s  %s", p.Path, strings.Join(sizes[i], ","), lines[i])
			}
		}
		b.WriteString(strings.Join(lines, "\n"))
		if len(lines) > 0 {
			b.WriteString("\n")
		}
		if len(unchanged) == len(p.Shots) {
			fmt.Fprintf(&b, "  %-14s        unchanged at every size\n", p.Path)
		} else if len(unchanged) > 0 {
			fmt.Fprintf(&b, "  %-14s %5s  unchanged\n", p.Path, strings.Join(unchanged, ","))
		}
	}
	if crops {
		// One folder for every crop: paths of hex names cost an agent
		// tokens on every line.
		fmt.Fprintf(&b, "look: before|after crops of each shot's largest change, in %s/\n", imgDir)
	}
	// The line the chat turns into a card (transcript/localartifacts.go).
	fmt.Fprintf(&b, "Artifact %s v%d · visualdiff · %s\n", id, n, strings.NewReplacer("\n", " ", `"`, "'", `\`, "/").Replace(a.Title))
	b.WriteString("Shown in the user's Shipyard beside your chat; compare again after a fix to update it.")
	return b.String()
}

// nameRegion says which element a changed region is: the smallest element
// box (of the after page) that holds the region, with the nearest named
// ancestor around it ("button "Shop now" in section.hero"). Empty when
// nothing smaller than the whole page holds it.
func nameRegion(r DiffRegion, els []pageEl) string {
	if len(els) == 0 {
		return ""
	}
	// Regions are whole 8px cells, so a region overhangs its element by up
	// to a cell on each side.
	const tol = 8
	box := image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H)
	holds := func(e pageEl) bool {
		return e.R.Min.X-tol <= box.Min.X && e.R.Min.Y-tol <= box.Min.Y && e.R.Max.X+tol >= box.Max.X && e.R.Max.Y+tol >= box.Max.Y
	}
	best, area := -1, 0
	for i, e := range els {
		a := e.R.Dx() * e.R.Dy()
		if holds(e) && (best < 0 || a < area) {
			best, area = i, a
		}
	}
	named := func(name string) bool {
		tag, rest, _ := strings.Cut(name, " ")
		return rest != "" || strings.ContainsAny(tag, "#.[") || slices.Contains([]string{"header", "nav", "main", "section", "footer", "aside", "form", "dialog", "article", "table", "ul", "ol"}, tag)
	}
	parent := func(i int) int {
		if p := els[i].Parent; p >= 0 && p < len(els) && p != i {
			return p
		}
		return -1
	}
	// A bare div names its nearest named ancestor instead.
	up := func(i int) int {
		for i >= 0 && !named(els[i].Name) {
			i = parent(i)
		}
		return i
	}
	around := ""
	if best >= 0 && up(best) >= 0 {
		best = up(best)
	} else {
		best = -1
		// The region spans several elements: the one at its centre.
		c := image.Pt(r.X+r.W/2, r.Y+r.H/2)
		for i, e := range els {
			a := e.R.Dx() * e.R.Dy()
			if c.In(e.R) && a >= r.W*r.H/4 && (best < 0 || a < area) {
				best, area = i, a
			}
		}
		if best < 0 || up(best) < 0 {
			return ""
		}
		best, around = up(best), "around "
	}
	name := around + els[best].Name
	for p, hops := parent(best), 0; p >= 0 && hops < 8; p, hops = parent(p), hops+1 {
		if n := els[p].Name; named(n) && n != els[best].Name {
			name += " in " + n
			break
		}
	}
	return clip(name, 120)
}
