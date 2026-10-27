package agent

import "testing"

func TestKitLinksWithoutASchemeAreWebLinks(t *testing.T) {
	cases := map[string]string{
		"github.com/acme/berth-kit-web":         "https://github.com/acme/berth-kit-web",
		"gist.github.com/me/abc123":                       "https://gist.github.com/me/abc123",
		"https://github.com/acme/berth-kit-web": "https://github.com/acme/berth-kit-web",
		"/Users/me/kits/cal":                              "/Users/me/kits/cal",
		"~/kits/cal":                                      "~/kits/cal",
		"git@github.com:me/kit.git":                       "git@github.com:me/kit.git",
	}
	for in, want := range cases {
		if got := withScheme(in); got != want {
			t.Errorf("withScheme(%q) = %q, want %q", in, got, want)
		}
	}
}
