// Package source classifies an `add` argument as a git remote or a local path
// and discovers the Skills (directories containing SKILL.md) within a fetched
// or local tree.
//
// A Source is the location a skill is fetched from. The primary kind is a git
// repository — which may hold one or many skills, acting as a catalog — and the
// secondary kind is a local directory holding a single skill. Classify decides
// which kind an argument refers to; DiscoverSkills walks a materialized tree and
// reports every skill directory it contains.
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
// classified as Local. Anything else (a non-existent path, or a file) yields a
// descriptive error so the caller can surface it to the user.
//
// Git detection is checked first and deliberately does not touch the filesystem:
// a string that looks like a remote is treated as one even if a same-named
// directory happens to exist locally.
func Classify(arg string) (Kind, error) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return 0, fmt.Errorf("empty source: provide a git URL or a local path")
	}

	if looksLikeGitRemote(trimmed) {
		return Git, nil
	}

	info, err := os.Stat(trimmed)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("source %q is neither a git URL nor an existing local directory", arg)
		}
		return 0, fmt.Errorf("inspect source %q: %w", arg, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("source %q is a file, not a skill directory or git URL", arg)
	}
	return Local, nil
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

// DiscoverSkills walks rootDir and returns one Found per directory that directly
// contains a SKILL.md file.
//
// rootDir itself counts: if rootDir/SKILL.md exists it is reported. Once a skill
// directory is found, its subtree is not descended into — a skill is one
// directory and nested SKILL.md files (e.g. supporting examples) are not treated
// as separate skills. The ".git" directory is skipped. Results are returned in
// lexical walk order.
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
	return found, nil
}
