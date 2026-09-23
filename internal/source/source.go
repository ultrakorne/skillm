// Package source classifies an `add` argument as a git remote or a local path
// and discovers the Skills (directories containing SKILL.md) within a fetched
// or local tree.
//
// A Source is the location a skill is fetched from. The primary kind is a git
// repository — which may hold one or many skills, acting as a catalog, and may be
// named by URL or by GitHub "owner/repo" shorthand — and the secondary kind is a
// local directory holding a single skill. Classify decides which kind an argument
// refers to and GitRemote resolves a git one to the URL to clone; DiscoverSkills
// walks a materialized tree and reports every skill directory it contains.
package source

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ultrakorne/skillm/internal/skill"
)

// Kind distinguishes the two supported Source kinds.
type Kind int

const (
	// Git is a remote git repository (a catalog of one or more skills).
	Git Kind = iota
	// Local is an existing local directory holding a skill.
	Local
)

// String returns the lowercase name of the kind ("git" / "local"). These match
// the SkillEntry.Kind values used in the registry.
func (k Kind) String() string {
	switch k {
	case Git:
		return "git"
	case Local:
		return "local"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Classify decides whether arg names a git remote or a local directory.
//
// It is recognised as Git when arg looks like a git remote URL — an http(s),
// git, ssh or file scheme, an scp-like "user@host:path" / "host:path" form, or a
// path ending in ".git". Otherwise, if arg is an existing local directory it is
// classified as Local. Failing both, a GitHub "owner/repo" shorthand is Git.
// Anything else (a non-existent path, or a file) yields a descriptive error so
// the caller can surface it to the user.
//
// The order matters twice over. An explicit remote is checked first and
// deliberately does not touch the filesystem: a string that looks like a remote
// is treated as one even if a same-named directory happens to exist locally. The
// shorthand, by contrast, is checked last — "owner/repo" is also a valid
// relative path, so an existing local directory of that name keeps winning, as
// it did before shorthands were understood.
func Classify(arg string) (Kind, error) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return 0, fmt.Errorf("empty source: provide a git URL, a GitHub owner/repo, or a local path")
	}

	if looksLikeGitRemote(trimmed) {
		return Git, nil
	}

	info, err := os.Stat(trimmed)
	switch {
	case err == nil && info.IsDir():
		return Local, nil
	case err == nil:
		return 0, fmt.Errorf("source %q is a file, not a skill directory or git URL", arg)
	case !os.IsNotExist(err):
		return 0, fmt.Errorf("inspect source %q: %w", arg, err)
	}

	// Nothing of that name on disk: a GitHub shorthand is a git remote.
	if _, ok := GitHubShorthand(trimmed); ok {
		return Git, nil
	}
	return 0, fmt.Errorf("source %q is neither a git URL (or GitHub owner/repo) nor an existing local directory", arg)
}

// GitRemote returns the remote URL to clone for an arg Classify reported as Git:
// a GitHub "owner/repo" shorthand expanded to its HTTPS clone URL, any other
// remote passed through as given (trimmed).
//
// Callers record the result as the skill's Source, so installing "owner/repo"
// and installing "https://github.com/owner/repo" are the same Source — the same
// skill, checked and updated against the same remote.
func GitRemote(arg string) string {
	trimmed := strings.TrimSpace(arg)
	if url, ok := GitHubShorthand(trimmed); ok {
		return url
	}
	return trimmed
}

// GitHubShorthand reports whether s is a GitHub "owner/repo" shorthand — the
// form `npx skills add owner/repo` takes, and the form a lockfile records for a
// GitHub HTTPS source — and returns the HTTPS clone URL it expands to.
//
// The recognised shape is exactly two segments split by a single "/", each made
// only of letters, digits, "-", "_" or "." and neither being "." or ".."; the
// repo segment may carry a trailing ".git". Requiring that alphabet is what
// keeps the shorthand from swallowing other source shapes: a scheme's ":", a
// deeper path ("owner/repo/sub" — not a repo), a backslash, or whitespace all
// disqualify it.
func GitHubShorthand(s string) (cloneURL string, ok bool) {
	owner, repo, found := strings.Cut(strings.TrimSpace(s), "/")
	if !found {
		return "", false
	}
	repo = strings.TrimSuffix(repo, ".git")
	if !isShorthandSegment(owner) || !isShorthandSegment(repo) {
		return "", false
	}
	return "https://github.com/" + owner + "/" + repo + ".git", true
}

// isShorthandSegment reports whether seg is usable as the owner or the repo half
// of a GitHub shorthand (see GitHubShorthand for the alphabet and why it is that
// narrow).
func isShorthandSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." {
		return false
	}
	for _, r := range seg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// LooksLikeSource reports whether arg has the SHAPE of a Source — a git remote,
// or an explicitly path-shaped local path — as opposed to a bare in-Home Skill
// ID. It is purely lexical and never touches the filesystem: a bare name (a
// single segment with no path separator, no scheme, and no leading "~") is
// treated as an in-Home id even when a same-named directory exists in the
// current directory. To install a local directory you must path-qualify it
// (e.g. "./my-skill").
//
// Callers route on this to choose source mode vs in-Home-id mode, and only call
// Classify — which does stat the filesystem — once they have decided it is a
// Source. The recognised Source shapes are:
//   - a git remote (see looksLikeGitRemote): a scheme, scp-like syntax, or ".git";
//   - a "~"-prefixed (home-relative) path;
//   - anything containing a path separator ("/" or "\\"): "./x", "../x",
//     "/abs/x", "a/b", "dir\\x" — which also covers the GitHub "owner/repo"
//     shorthand, left for Classify to tell apart from a relative path.
func LooksLikeSource(arg string) bool {
	s := strings.TrimSpace(arg)
	if s == "" {
		return false
	}
	if looksLikeGitRemote(s) {
		return true
	}
	if strings.HasPrefix(s, "~") {
		return true
	}
	return strings.ContainsAny(s, `/\`)
}

// looksLikeGitRemote reports whether s has the shape of a git remote URL.
//
// Recognised forms:
//   - explicit schemes: https://, http://, ssh://, git://, git+ssh://, file://
//   - scp-like syntax:  git@host:path  or  host:path  (a colon before the first
//     slash, with a non-empty host that contains a "." or is "git@...")
//   - any URL/path ending in ".git"
func looksLikeGitRemote(s string) bool {
	lower := strings.ToLower(s)

	for _, scheme := range []string{
		"https://", "http://", "ssh://", "git://", "git+ssh://", "file://",
	} {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}

	// Explicit ".git" suffix is an unambiguous git marker (covers both URLs
	// and scp-like remotes such as host:repo.git).
	if strings.HasSuffix(lower, ".git") {
		return true
	}

	// scp-like syntax: "user@host:path" or "host:path". The colon must come
	// before any slash (otherwise it is a URL we'd have matched above, or a
	// local path like "./a:b/c"), and there must be a real host component.
	if isScpLike(s) {
		return true
	}

	return false
}

// isScpLike reports whether s matches git's scp-like remote syntax
// ("[user@]host:path"), distinguishing it from a local path that merely
// contains a colon.
func isScpLike(s string) bool {
	// A leading slash, "./", "../" or "~" is a filesystem path, never scp-like.
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~") {
		return false
	}

	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return false
	}

	// A slash before the colon means the colon lives inside a path segment, not
	// as the scp host/path separator.
	if slash := strings.IndexByte(s, '/'); slash >= 0 && slash < colon {
		return false
	}

	host := s[:colon]
	// Explicit "user@host" form is unambiguously a remote.
	if at := strings.IndexByte(host, '@'); at >= 0 {
		return at+1 < len(host) // require a non-empty host after '@'
	}

	// Bare "host:path" — only treat as a remote when the host looks like a
	// hostname (contains a dot, e.g. github.com:owner/repo). A plain
	// "name:something" with no dot is ambiguous and left to Local handling.
	return strings.Contains(host, ".")
}

// Found is a single skill discovered inside a tree by DiscoverSkills.
type Found struct {
	// Id is the skill's directory base name — its candidate Skill ID.
	Id string
	// Dir is the absolute-or-relative directory containing the skill's
	// SKILL.md (as walked from the supplied rootDir).
	Dir string
	// Skill is the parsed skill (via skill.Load).
	Skill *skill.Skill
}

// DiscoverSkills walks rootDir and returns one Found per skill ID, each from a
// directory that directly contains a SKILL.md file.
//
// rootDir itself counts: if rootDir/SKILL.md exists it is reported. Once a skill
// directory is found, its subtree is not descended into — a skill is one
// directory and nested SKILL.md files (e.g. supporting examples) are not treated
// as separate skills. The ".git" directory is skipped.
//
// Repos that ship one skill to many agents commit a copy per agent folder
// (.claude/skills/x, .cursor/skills/x, plugin/skills/x, ...). Those copies share
// an ID and would install over each other, so only one directory per ID is
// kept: see preferFound. Results are returned in lexical walk order.
func DiscoverSkills(rootDir string) ([]Found, error) {
	info, err := os.Stat(rootDir)
	if err != nil {
		return nil, fmt.Errorf("discover skills in %q: %w", rootDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("discover skills: %q is not a directory", rootDir)
	}

	var found []Found
	walkErr := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" && path != rootDir {
			return fs.SkipDir
		}

		skillPath := filepath.Join(path, skill.SkillFile)
		fi, statErr := os.Stat(skillPath)
		if statErr != nil || fi.IsDir() {
			return nil // no SKILL.md here; keep descending
		}

		sk, loadErr := skill.Load(path)
		if loadErr != nil {
			return fmt.Errorf("load skill in %q: %w", path, loadErr)
		}
		found = append(found, Found{Id: sk.ID, Dir: path, Skill: sk})

		// One directory == one skill: do not recurse into a found skill dir.
		return fs.SkipDir
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return dedupeByID(rootDir, found), nil
}

// dedupeByID keeps one Found per ID — the one preferFound ranks best — at the
// position of that ID's first occurrence, so the result stays in walk order.
func dedupeByID(rootDir string, found []Found) []Found {
	best := make(map[string]int, len(found)) // id -> index into out
	out := make([]Found, 0, len(found))
	for _, f := range found {
		i, seen := best[f.Id]
		if !seen {
			best[f.Id] = len(out)
			out = append(out, f)
			continue
		}
		if preferFound(rootDir, f, out[i]) {
			out[i] = f
		}
	}
	return out
}

// preferFound reports whether a should be kept over b, two directories holding
// the same skill ID. The conventional homes win: a top-level skills/<id>, then
// the cross-agent .agents/skills/<id>; otherwise the shallower directory wins,
// and on a tie the earlier one in walk order (b) is kept.
func preferFound(rootDir string, a, b Found) bool {
	ra, rb := foundRank(rootDir, a.Dir), foundRank(rootDir, b.Dir)
	if ra != rb {
		return ra < rb
	}
	return foundDepth(rootDir, a.Dir) < foundDepth(rootDir, b.Dir)
}

// foundRank orders skill directories by how conventional their location is:
// 0 for skills/<id>, 1 for .agents/skills/<id>, 2 for anywhere else.
func foundRank(rootDir, dir string) int {
	rel, err := filepath.Rel(rootDir, filepath.Dir(dir))
	if err != nil {
		return 2
	}
	switch filepath.ToSlash(rel) {
	case "skills":
		return 0
	case ".agents/skills":
		return 1
	}
	return 2
}

// foundDepth is the number of path elements between rootDir and dir.
func foundDepth(rootDir, dir string) int {
	rel, err := filepath.Rel(rootDir, dir)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}
