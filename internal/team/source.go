package team

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Source is where a team setup is read from: an org's own .berth
// repository, or a link to any repository, branch and folder holding a
// team.json, so a team setup can be tried before the org publishes it.
type Source struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	// Ref is the branch, tag or commit; empty is the default branch.
	Ref string `json:"ref,omitempty"`
	// Path is the folder inside the repository that holds team.json.
	Path string `json:"path,omitempty"`
	// Link is set for a source given as a link rather than an org name.
	Link bool `json:"link"`
}

var (
	linkTree  = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9._-]+?)(?:\.git)?/tree/([^/]+)(?:/(.*?))?/?$`)
	linkRepo  = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9._-]+?)(?:\.git)?/?(?:@([A-Za-z0-9._/-]+))?$`)
	shortRepo = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9._-]+?)(?:@([A-Za-z0-9._/-]+))?$`)
)

// ParseSource reads what someone typed or a link carried: an org name
// ("calcom"), a repository link ("github.com/o/r", "github.com/o/r@ref",
// "o/r@ref"), a folder link ("github.com/o/r/tree/<ref>/<path>"), or
// berth://team?org=… / berth://team?src=….
func ParseSource(s string) (Source, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(strings.ToLower(s), "berth://team") {
		u, err := url.Parse(s)
		if err != nil {
			return Source{}, fmt.Errorf("%q is not a team link", s)
		}
		if org := u.Query().Get("org"); org != "" {
			return ParseSource(org)
		}
		if src := u.Query().Get("src"); src != "" {
			return ParseSource(src)
		}
		return Source{}, fmt.Errorf("%q names no org or src", s)
	}
	if ValidOrg(s) {
		return Source{Owner: s, Repo: Repo}, nil
	}
	var m []string
	src := Source{Link: true}
	switch {
	case linkTree.MatchString(s):
		m = linkTree.FindStringSubmatch(s)
		src.Owner, src.Repo, src.Ref = m[1], m[2], m[3]
		if m[4] != "" {
			p, err := InsidePath(m[4])
			if err != nil {
				return Source{}, fmt.Errorf("%q: %v", s, err)
			}
			src.Path = p
		}
	case linkRepo.MatchString(s):
		m = linkRepo.FindStringSubmatch(s)
		src.Owner, src.Repo, src.Ref = m[1], m[2], m[3]
	case shortRepo.MatchString(s):
		m = shortRepo.FindStringSubmatch(s)
		src.Owner, src.Repo, src.Ref = m[1], m[2], m[3]
	default:
		return Source{}, fmt.Errorf("%q is neither a GitHub org nor a link to a team setup (github.com/owner/repo, …@ref, or …/tree/<ref>/<folder>)", s)
	}
	if src.Ref != "" && (strings.HasPrefix(src.Ref, "-") || strings.Contains(src.Ref, "..")) {
		return Source{}, fmt.Errorf("%q is not a ref", src.Ref)
	}
	// The org's own .berth, linked to plainly, is the org's.
	if src.Repo == Repo && src.Ref == "" && src.Path == "" {
		return Source{Owner: src.Owner, Repo: Repo}, nil
	}
	return src, nil
}

// Slug is "owner/repo".
func (s Source) Slug() string { return s.Owner + "/" + s.Repo }

// String writes the source back: the org's name, or a link.
func (s Source) String() string {
	if !s.Link {
		return s.Owner
	}
	out := "github.com/" + s.Slug()
	switch {
	case s.Path != "":
		ref := s.Ref
		if ref == "" {
			ref = "HEAD"
		}
		out += "/tree/" + ref + "/" + s.Path
	case s.Ref != "":
		out += "@" + s.Ref
	}
	return out
}

// HTMLURL is the source on github.com.
func (s Source) HTMLURL() string {
	u := "https://github.com/" + s.Slug()
	if s.Ref != "" || s.Path != "" {
		ref := s.Ref
		if ref == "" {
			ref = "HEAD"
		}
		u += "/tree/" + ref
		if s.Path != "" {
			u += "/" + s.Path
		}
	}
	return u
}

var unsafeKey = regexp.MustCompile(`[^a-z0-9._-]+`)

// Key names the source in file names: the org, or the link made safe.
func (s Source) Key() string {
	return strings.Trim(unsafeKey.ReplaceAllString(strings.ToLower(s.String()), "_"), "_.")
}

// File is the path of a file of the team setup inside the repository.
func (s Source) File(rel string) string {
	if s.Path == "" {
		return rel
	}
	return s.Path + "/" + rel
}
