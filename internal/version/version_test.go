package version

import (
	"strings"
	"testing"
)

func TestLineNamesTheReleaseAndTheBuild(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "v1.2.3"
	line := Line("berthd")
	if !strings.HasPrefix(line, "berthd v1.2.3 (build ") || strings.Contains(line, "build unknown") {
		t.Errorf("Line = %q", line)
	}
	if BuildID([]byte("a")) == BuildID([]byte("b")) || len(BuildID(nil)) != 12 {
		t.Error("build IDs must tell builds apart, in 12 hex digits")
	}
}

func TestReleasesAreOrderedAndDevBuildsAreNot(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.3.22", "v0.3.11", 1, true},
		{"v0.3.11", "v0.3.22", -1, true},
		{"v0.3.9", "v0.3.10", -1, true},
		{"v1.0.0", "v0.99.99", 1, true},
		{"0.3.22", "v0.3.22", 0, true},
		{"dev", "v0.3.22", 0, false},
		{"", "v0.3.22", 0, false},
		{"v0.3", "v0.3.22", 0, false},
	} {
		if got, ok := Compare(c.a, c.b); got != c.want || ok != c.ok {
			t.Errorf("Compare(%q, %q) = %d %v, want %d %v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}
