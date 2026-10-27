package box

import (
	"bufio"
	"context"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// forge is where a repository's origin lives, as a pr step sees it.
type forge struct {
	// Host is origin's host ("gitea.acme.dev"), or "" when origin is a
	// folder or there is no origin.
	Host string
	// Local is set when origin is a folder on this box.
	Local bool
	// GitHub is set when gh can open a pull request there: github.com, or
	// a host gh is signed in to (GitHub Enterprise).
	GitHub bool
}

// originForge reads where the checkout at dir pushes to and whether gh
// can open a pull request there. Nothing is fetched or pushed.
func originForge(ctx context.Context, dir string) forge {
	remote := remoteURL(ctx, dir)
	if remote == "" {
		// No origin: the push says so.
		return forge{GitHub: true}
	}
	host, ssh := remoteHost(remote)
	if host == "" {
		return forge{Local: true}
	}
	if ssh {
		// An SSH alias (Host work in ~/.ssh/config) names its real host,
		// as gh reads it.
		host = sshHostname(ctx, host)
	}
	f := forge{Host: host}
	f.GitHub = isGitHubHost(host) || ghSignedIn(ctx, dir, host)
	return f
}

// remoteHost is a remote URL's host, lower-cased, and whether it is
// reached over SSH; "" for a path.
func remoteHost(remote string) (string, bool) {
	if m := scpLike.FindStringSubmatch(remote); m != nil {
		return strings.ToLower(m[1]), true
	}
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" || u.Scheme == "file" {
		return "", false
	}
	return strings.ToLower(u.Hostname()), u.Scheme == "ssh" || strings.HasPrefix(u.Scheme, "git+ssh")
}

func isGitHubHost(host string) bool {
	return host == "github.com" || host == "www.github.com" || host == "ssh.github.com" || strings.HasPrefix(host, "github.com-")
}

// sshHostname is what ssh would connect to for alias, from ssh -G (which
// reads the config and connects to nothing).
func sshHostname(ctx context.Context, alias string) string {
	bin, err := exec.LookPath("ssh")
	if err != nil {
		return alias
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-G", "--", alias).Output()
	if err != nil {
		return alias
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), " "); ok && k == "hostname" && v != "" {
			return strings.ToLower(strings.TrimSpace(v))
		}
	}
	return alias
}

// ghSignedIn says whether gh has an account on host, the way gh itself
// would find a GitHub Enterprise remote.
func ghSignedIn(ctx context.Context, dir, host string) bool {
	bin, err := toolPath("gh")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status", "--hostname", host)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "NO_COLOR=1")
	return cmd.Run() == nil
}

var (
	remoteLink = regexp.MustCompile(`https?://[^\s'"<>]+`)
	userinfo   = regexp.MustCompile(`(\b[a-z][a-z0-9+.-]*://)[^/\s@'"]+@`)
)

// noUserinfo takes any user and password out of the URLs in a tool's
// output, so a token in a remote URL is never kept in a run.
func noUserinfo(s string) string { return userinfo.ReplaceAllString(s, "$1") }

// pushLink is the link a forge prints on push to open a pull or merge
// request for the branch (Gitea, Forgejo, GitLab and Bitbucket all do),
// without any credentials in it; "" when it printed none.
func pushLink(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "remote:") {
			continue
		}
		m := remoteLink.FindString(l)
		if m == "" {
			continue
		}
		u, err := url.Parse(m)
		if err != nil || u.Host == "" {
			continue
		}
		u.User = nil
		return u.String()
	}
	return ""
}

// noPRText says why a pr step opened nothing, and where the branch is.
func noPRText(f forge, branch, link string) string {
	var b strings.Builder
	if f.Local {
		b.WriteString("This repository's origin is a folder on this box, not GitHub, so Shipyard didn't open a pull request. ")
	} else {
		b.WriteString("This repository is on " + f.Host + ", not GitHub, so Shipyard didn't open a pull request. ")
	}
	b.WriteString("The branch " + branch + " is pushed to origin")
	if link != "" {
		b.WriteString("; open one there: " + link)
	} else if !f.Local {
		b.WriteString(", ready for a pull request on " + f.Host)
	}
	b.WriteString(".")
	if !f.Local {
		b.WriteString(" (If " + f.Host + " is GitHub Enterprise, run \"gh auth login --hostname " + f.Host + "\" on this box.)")
	}
	return b.String()
}
