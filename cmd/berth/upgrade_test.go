package main

import "testing"

// A laptop on an older release never takes a newer box for outdated, nor
// puts its older daemon on it (the app's Update all asks this).
func TestABoxOnANewerReleaseIsNotOutdated(t *testing.T) {
	for _, c := range []struct {
		box, mine string
		newer     bool
	}{
		{"v0.3.22", "v0.3.11", true},
		{"v0.3.11", "v0.3.22", false},
		{"v0.3.22", "v0.3.22", false},
		// Unplaced: told apart by build alone, as before.
		{"", "v0.3.22", false},
		{"dev", "v0.3.11", false},
		{"v0.3.22", "dev", false},
	} {
		if got := boxIsNewer(c.box, c.mine); got != c.newer {
			t.Errorf("boxIsNewer(%q, %q) = %v, want %v", c.box, c.mine, got, c.newer)
		}
	}
}
