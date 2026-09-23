package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/state"
)

// Source identity: deciding whether a skill being installed comes from the
// same Source as a registry entry of the same id, and the entry to record when
// it does. Plus RefetchSkill, the shared "get a git skill's current content"
// primitive.

// SrcIdentity is the Source identity of the current fetch, compared against a
// registry entry to decide whether a same-named skill came from here.
type SrcIdentity struct {
	Kind   string // state.KindGit / state.KindLocal
	Source string // git URL, or local source directory
	Path   string // git subpath within the repo ("" for local / repo root)
	// Base is the directory a relative local path is resolved against. Current
	// installs record absolute paths, but a legacy entry may hold one relative
	// to wherever it was installed from. With Base empty, relative paths are
	// compared as written.
	Base string
}

// Matches reports whether registry entry e was sourced from the same place as
// this fetch: for git the same repo and subpath (the remote compared leniently,
// so a spelling difference is not a different source); for local the same source
// directory (compared by cleaned path, a relative one resolved against Base, so
// ./foo and /abs/foo agree).
func (s SrcIdentity) Matches(e state.SkillEntry) bool {
	if e.Kind != s.Kind {
		return false
	}
	switch s.Kind {
	case state.KindGit:
		// One repo has many spellings — a trailing slash, a ".git" suffix, the
		// scp-like form — and none of them make it a different repo. Fetches
		// record a canonical URL (see CanonicalRemote), but entries written
		// before that still hold whatever was typed, so normalize both sides.
		return NormalizeRemote(e.Source) == NormalizeRemote(s.Source) && e.Path == s.Path
	case state.KindLocal:
		return ResolvePath(e.Source, s.Base) == ResolvePath(s.Source, s.Base)
	default:
		return false
	}
}

// CanonicalRemote returns a git remote URL in the form skillm records: trailing
// slashes removed, since they are not part of the repo's identity. It is
// deliberately gentler than NormalizeRemote — the result is stored and handed
// back to git, so the scheme and any ".git" suffix must survive.
func CanonicalRemote(u string) string {
	return strings.TrimRight(u, "/")
}

// RegistryCollision reports a *SourceCollisionError only when id is already
// registered from a source OTHER than ident — a same-id-different-source clash
// the user resolves by installing under another id. A same-source id is fine
// (its freshly fetched content is installed and its revision refreshed) and an
// unregistered id is fresh.
func RegistryCollision(st *state.State, id string, ident SrcIdentity) error {
	e, ok := st.Get(id)
	if !ok {
		return nil
	}
	if ident.Matches(e) {
		return nil
	}
	return &SourceCollisionError{ID: id}
}

// MergeEntry produces the entry to record for a chosen skill. For a genuinely
// new id it is fresh as-is. For an id already registered from the same source
// (guaranteed by RegistryCollision) it preserves the existing install markers
// (VendoredAt/Global) and original InstalledAt, overwriting only the source and
// revision fields — so re-installing a source refreshes the pin without
// forgetting where the skill is already installed.
func MergeEntry(st *state.State, id string, fresh state.SkillEntry) state.SkillEntry {
	existing, ok := st.Get(id)
	if !ok {
		return fresh
	}
	// The same repo typed another way is the same source (see Matches), so a
	// re-install can reach here with a different spelling of the recorded
	// remote. Keep the one on record: its spelling is a deliberate choice — an
	// HTTPS remote so a keyless CI can update — and update/list clone from it,
	// so a local `install git@...` must not silently repoint it to SSH. Only a
	// genuinely different source replaces it.
	sameRemote := existing.Kind == state.KindGit && fresh.Kind == state.KindGit &&
		NormalizeRemote(existing.Source) == NormalizeRemote(fresh.Source)
	existing.Kind = fresh.Kind
	if !sameRemote {
		existing.Source = fresh.Source
	}
	existing.Path = fresh.Path
	existing.Ref = fresh.Ref
	existing.Revision = fresh.Revision
	return existing
}

// ChosenID returns the Skill ID to install a discovered skill under: its own
// id, or the As override when one was given (validated single by
// CheckAsSingle).
func ChosenID(id, as string) string {
	if as != "" {
		return as
	}
	return id
}

// CheckAsSingle enforces that an As override is only used when exactly one
// skill was selected, since it renames a single skill. It returns ErrAsMultiple
// otherwise.
func CheckAsSingle(as string, selected int) error {
	if as != "" && selected > 1 {
		return ErrAsMultiple
	}
	return nil
}

// RepoRelSubpath returns dir expressed relative to repoDir, using forward
// slashes — the form SubtreeSHA/MaterializeSubdir expect. The repo root yields
// "".
func RepoRelSubpath(repoDir, dir string) string {
	rel, err := filepath.Rel(repoDir, dir)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ResolvePath returns p cleaned, and joined onto base first when p is
// relative and base is set. It is how core makes a path absolute without
// reading the process's working directory: callers pass Options.Cwd as base.
// A relative p with no base stays relative.
func ResolvePath(p, base string) string {
	if base != "" && !filepath.IsAbs(p) {
		return filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// pathCaseInsensitiveHosts are the hosts whose repo paths are case-insensitive,
// so a case difference there is still one repo. Everywhere else — GitLab
// self-managed, Gitea, plain git-over-ssh, a case-sensitive filesystem — path
// case is significant and two spellings are two repos. A host missing from this
// list only ever errs toward "different source", which the user resolves with
// --as; the reverse would silently install one repo over another.
var pathCaseInsensitiveHosts = map[string]bool{
	"github.com":    true,
	"gitlab.com":    true,
	"bitbucket.org": true,
}

// NormalizeRemote reduces a git remote URL to a comparable form: scheme and
// trailing ".git"/slashes stripped, the scp-like "git@host:path" form folded to
// "host/path", and the host lowercased (DNS is case-insensitive). The path is
// folded only for the hosts that treat it that way — see
// pathCaseInsensitiveHosts.
//
// It under-normalizes in three confirmed cases (embedded credentials, an ssh://
// port, an scp-like form with a non-"git@" user), each recorded in
// docs/known-issues.md.
func NormalizeRemote(u string) string {
	s := strings.TrimSpace(u)
	for _, p := range []string{"https://", "http://", "ssh://"} {
		if rest, ok := cutPrefixFold(s, p); ok {
			s = rest
			break
		}
	}
	if rest, ok := cutPrefixFold(s, "git@"); ok {
		s = strings.Replace(rest, ":", "/", 1)
	}
	s = trimSuffixFold(strings.TrimRight(s, "/"), ".git")
	host, path, ok := strings.Cut(s, "/")
	host = strings.ToLower(host)
	if !ok {
		return host
	}
	if pathCaseInsensitiveHosts[host] {
		path = strings.ToLower(path)
	}
	return host + "/" + path
}

// cutPrefixFold is strings.CutPrefix with a case-insensitive match, for the
// parts of a remote URL that carry no case significance (the scheme, "git@").
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// trimSuffixFold is strings.TrimSuffix with a case-insensitive match.
func trimSuffixFold(s, suffix string) string {
	if len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// RefetchSkill treeless-clones a git skill's recorded source at its pinned ref,
// materializes the skill's subdir into a fresh temp dir, and returns the staged
// content dir, the current upstream revision of that subdir, and a cleanup func
// the caller must call once it has copied the content. It is the shared "get a
// git skill's current content without a Home library" primitive used by install
// (adding a scope by id) and import (restoring a missing copy). Git-kind only.
func RefetchSkill(ctx context.Context, e state.SkillEntry) (dir, rev string, cleanup func(), err error) {
	cleanup = func() {}
	tmp, err := os.MkdirTemp("", "skillm-refetch-")
	if err != nil {
		return "", "", cleanup, fmt.Errorf("create temp dir: %w", err)
	}
	fail := func(e error) (string, string, func(), error) {
		os.RemoveAll(tmp)
		return "", "", func() {}, e
	}

	repoDir := filepath.Join(tmp, "repo")
	if err := gitx.TreelessClone(ctx, e.Source, e.Ref, repoDir); err != nil {
		return fail(err)
	}

	ref := e.Ref
	if ref == "" {
		def, derr := gitx.DefaultRef(ctx, repoDir)
		if derr != nil {
			return fail(derr)
		}
		ref = def
	}

	rev, err = gitx.SubtreeSHA(ctx, repoDir, ref, e.Path)
	if err != nil {
		return fail(fmt.Errorf("the skill's subdirectory %q is no longer present upstream: %w", e.Path, err))
	}

	staged := filepath.Join(tmp, "staged")
	if err := gitx.MaterializeSubdir(ctx, repoDir, e.Path, staged); err != nil {
		return fail(err)
	}
	return staged, rev, func() { os.RemoveAll(tmp) }, nil
}
