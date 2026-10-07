package trust

import "strings"

// NameFromHostname turns a hostname into a peer label: its first DNS label,
// e.g. "Alexs-MacBook-Pro.local" becomes "alexs-macbook-pro" and
// "devbox.europe-north1-a.c.project.internal" becomes "devbox". A box's name
// is one label of its URLs (<worktree>.<location>.<box>.localhost), so it
// can hold no dots. Two boxes whose names share a first label get the same
// name; a laptop pairs the second under a free one (AddWithFreeName), and
// its proxy accepts a box's own name only when one paired box claims it.
func NameFromHostname(hostname, fallback string) string {
	name, _, _ := strings.Cut(strings.Trim(strings.ToLower(hostname), "."), ".")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name = strings.Trim(b.String(), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	if !ValidName(name) {
		return fallback
	}
	return name
}
