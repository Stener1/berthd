package database

import (
	"net"
	"strings"
)

// Source is the name a worktree's databases are listed under:
// "<project>/<worktree>", or the project alone for its main checkout. It is
// the rule Shipyard's other helpers name a worktree by (Leadline's
// Worktree.Service), written again here so that this helper depends on none
// of them; a test holds the two to the same answers.
//
// A worktree's commands ("web", "worker") are not part of the name: their
// databases are the worktree's.
func Source(project, worktree string) string {
	project, worktree = namePart(project), namePart(worktree)
	switch {
	case project == "":
		return worktree
	case worktree == "" || worktree == project:
		return project
	}
	return project + "/" + worktree
}

// namePart makes a name safe to be one part of a source: no slash and no
// colon, which are the joins, and no white space or control characters.
func namePart(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == ':' || r == '\\' || r <= 0x20 || r == 0x7f:
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.ToValidUTF8(out[:60], "")
	}
	return out
}

// LocalHost reports whether a host names this machine: localhost, a name
// under it, a loopback address, or "any address" (which is where a server
// listens, and is dialled as loopback).
func LocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}
