package trust

import (
	"strings"
	"testing"
)

func TestNameFromHostname(t *testing.T) {
	for in, want := range map[string]string{
		"Alexs-MacBook-Pro.local":      "alexs-macbook-pro",
		"alex's laptop":                "alex-s-laptop",
		"--weird--":                    "weird",
		"ünïcode-box":                  "n-code-box",
		"":                             "laptop",
		"...":                          "laptop",
		strings.Repeat("a", 80):        strings.Repeat("a", 63),
		strings.Repeat("a", 62) + "-b": strings.Repeat("a", 62),
		"devbox.europe-north1-a.c.sales-moitoring.internal": "devbox",
		"DevBox.example.com.":                               "devbox",
		".devbox":                                           "devbox",
		"my_box.lan":                                        "my-box",
		"-.example.com":                                     "laptop",
	} {
		if got := NameFromHostname(in, "laptop"); got != want {
			t.Errorf("NameFromHostname(%q) = %q, want %q", in, got, want)
		}
	}
}
