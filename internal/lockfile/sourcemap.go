package lockfile

import (
	"fmt"
	"net/url"
	"strings"
)

// This file maps between skillm's source notion (a git URL or local path, as
// stored in state.toml) and the lock entry's source/sourceUrl/sourceType
// triple, in both directions. The quirks mirror vercel's getLockSource /
// buildLocalUpdateSource: GitHub HTTPS sources are normalized to "owner/repo"
// shorthand, SSH URLs are preserved verbatim (shorthand would break private
// repos cloned over SSH), and non-GitHub hosts keep the full URL with
// sourceUrl set so restores never mis-resolve to github.com.

// GitSourceFields derives a lock entry's source identity from a git remote
// URL as skillm recorded it.
func GitSourceFields(gitURL string) (source, sourceURL, sourceType string) {
	host := gitHost(gitURL)
	isSSH := strings.HasPrefix(gitURL, "git@") || strings.HasPrefix(gitURL, "ssh://")

	switch {
	case host == "github.com" && isSSH:
		return gitURL, "", SourceGitHub
	case host == "github.com":
		if or := ownerRepo(gitURL); or != "" {
			return or, "", SourceGitHub
		}
		return gitURL, "", SourceGitHub
	case strings.Contains(host, "gitlab"):
		return gitURL, gitURL, SourceGitLab
	default:
		return gitURL, gitURL, SourceGit
	}
}

// CloneURL resolves the git URL to fetch this entry's source from — the
// import direction. It errors for entries that do not describe a git remote
// (local paths, node_modules, registry skills) or whose remote cannot be
// reconstructed.
func (e *Entry) CloneURL() (string, error) {
	switch e.SourceType {
	case SourceGitHub:
		s := e.Source
		switch {
		case strings.HasPrefix(s, "git@"), strings.HasPrefix(s, "ssh://"),
			strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
			return s, nil
		case strings.Count(s, "/") == 1 && !strings.Contains(s, " "):
			return "https://github.com/" + s + ".git", nil
		default:
			return "", fmt.Errorf("unrecognized github source %q", s)
		}
	case SourceGitLab, SourceGit:
		for _, s := range []string{e.SourceURL, e.Source} {
			if isGitRemote(s) {
				return s, nil
			}
		}
		return "", fmt.Errorf("no usable remote URL (source %q)", e.Source)
	default:
		return "", fmt.Errorf("source type %q is not a git remote", e.SourceType)
	}
}

// isGitRemote reports whether s looks like a URL git can clone: HTTP(S), SSH
// (URL or scp-like), or a file:// URL (skillm records those for local git
// remotes, e.g. in tests or offline mirrors).
func isGitRemote(s string) bool {
	for _, p := range []string{"git@", "ssh://", "http://", "https://", "file://"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// gitHost extracts the hostname from an HTTP(S), ssh:// or scp-like
// (git@host:path) git URL, lowercased. Unparseable input yields "".
func gitHost(gitURL string) string {
	if strings.HasPrefix(gitURL, "git@") {
		rest := strings.TrimPrefix(gitURL, "git@")
		if i := strings.IndexAny(rest, ":/"); i > 0 {
			return strings.ToLower(rest[:i])
		}
		return ""
	}
	u, err := url.Parse(gitURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// ownerRepo extracts "owner/repo" from a github.com HTTP(S) URL, stripping a
// trailing ".git". It returns "" when the path does not have exactly an owner
// and a repo segment.
func ownerRepo(gitURL string) string {
	u, err := url.Parse(gitURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
}
