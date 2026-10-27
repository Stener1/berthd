// Package version says which release a berth binary is. Release builds stamp
// Version with -ldflags "-X github.com/cosscom/shipyard/internal/version.Version=v1.2.3";
// anything built from a checkout is "dev".
//
// The build ID is the other half: a digest of the binary's own bytes. Two
// builds of one release are the same release, but only matching build IDs
// mean the same bytes, which is what `berth upgrade` compares.
package version

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Version is set at link time by release builds.
var Version = "dev"

// BuildID identifies a build by its bytes.
func BuildID(binary []byte) string {
	sum := sha256.Sum256(binary)
	return hex.EncodeToString(sum[:6])
}

// Line describes the running binary in one line, such as
// "berthd v0.1.0 (build 3f9a1c0b2d4e, linux/amd64)".
func Line(program string) string {
	build := "unknown"
	if exe, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(exe); err == nil {
			build = BuildID(b)
		}
	}
	return fmt.Sprintf("%s %s (build %s, %s/%s)", program, Version, build, runtime.GOOS, runtime.GOARCH)
}

// Compare orders two releases ("v0.3.22" against "v0.3.11"): -1, 0 or 1, as
// a is older, the same or newer. ok is false unless both are releases: a
// "dev" build, or a box too old to say its version, can't be placed.
func Compare(a, b string) (c int, ok bool) {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// parse reads "vX.Y.Z" (or "X.Y.Z").
func parse(v string) ([3]int, bool) {
	var p [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return p, false
	}
	for i, s := range parts {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return p, false
		}
		p[i] = n
	}
	return p, true
}
