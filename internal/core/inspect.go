package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/skill"
	"github.com/ultrakorne/skillm/internal/source"
	"github.com/ultrakorne/skillm/internal/state"
)

// Inspection is a Source read once: a git repository cloned (treelessly) and
// pinned to one commit, or a local directory, plus the skills found in it.
// It is the first half of an install from a Source: a caller inspects, picks
// which skills to install (it may prompt; core never does), then hands the
// Inspection to InstallSkills, which installs exactly the inspected content.
// Close removes the clone.
type Inspection struct {
	// Source is the Source as skillm records it: the canonical git remote
	// (a GitHub owner/repo shorthand expanded), or the absolute directory of a
	// local Source.
	Source string
	// Kind is state.KindGit or state.KindLocal.
	Kind string
	// Ref is the git ref recorded for update checks: the one asked for, or the
	// repository's default branch. Empty for a local Source.
	Ref string
	// Commit is the full SHA of the git commit that was inspected, which is
	// what an install from this Inspection copies. Empty for a local Source.
	Commit string
	// Skills are the skills found, in discovery order.
	Skills []InspectedSkill

	// root is the clone's worktree (git) or the local Source directory.
	root string
	// tmp holds the clone and every staging dir; Close removes it.
	tmp string
}

// InspectedSkill is one skill found by Inspect.
type InspectedSkill struct {
	// ID is the skill's own Skill ID (its directory name).
	ID string
	// Name and Description come from the skill's SKILL.md frontmatter.
	Name        string
	Description string
	// Path locates the skill in its Source: the repo-relative subdirectory
	// with forward slashes ("" for the repo root) for git, the absolute
	// directory for a local Source.
	Path string
}

// Close removes the clone behind the Inspection, including any content staged
// from it. It is safe to call more than once, and on a nil Inspection.
func (i *Inspection) Close() error {
	if i == nil || i.tmp == "" {
		return nil
	}
	err := os.RemoveAll(i.tmp)
	i.tmp = ""
	return err
}

// Select returns the inspected skills named by ids, in discovery order. Every
// id must be one of the inspected skills; otherwise nothing is returned and the
// error names each unknown id alongside the ids that are available.
func (i *Inspection) Select(ids []string) ([]InspectedSkill, error) {
	have := make(map[string]bool, len(i.Skills))
	for _, s := range i.Skills {
		have[s.ID] = true
	}
	want := make(map[string]bool, len(ids))
	var missing []string
	for _, id := range ids {
		if !have[id] {
			missing = append(missing, id)
		}
		want[id] = true
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("not found in source: %s; available: %s", strings.Join(missing, ", "), i.skillIDs())
	}
	var out []InspectedSkill
	for _, s := range i.Skills {
		if want[s.ID] {
			out = append(out, s)
		}
	}
	return out, nil
}

// skillIDs joins the inspected skill ids for error messages.
func (i *Inspection) skillIDs() string {
	ids := make([]string, 0, len(i.Skills))
	for _, s := range i.Skills {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, ", ")
}

// Inspect reads the Source src and discovers the skills it holds, prompting
// for nothing and writing nothing outside a temp dir. src is a git remote (a
// URL or a GitHub owner/repo shorthand) or a local directory; a relative
// local path is resolved against opts.Cwd, so the recorded Source is absolute.
// For git, ref pins a branch, tag or commit (default: the default branch) and
// the clone is pinned to the commit it resolves to. ref is ignored for a local
// Source. The caller must Close the returned Inspection.
func Inspect(ctx context.Context, opts Options, src, ref string) (*Inspection, error) {
	kind, err := source.ClassifyAt(src, opts.Cwd)
	if err != nil {
		return nil, err
	}
	switch kind {
	case source.Git:
		// Record the resolved remote, so a GitHub "owner/repo" shorthand is
		// the same Source as its full HTTPS URL, and canonicalize it before
		// anything records or compares it (a trailing slash is not part of a
		// repo's identity).
		return inspectGit(ctx, CanonicalRemote(source.GitRemote(src)), ref)
	case source.Local:
		p := strings.TrimSpace(src)
		if !filepath.IsAbs(p) && opts.Cwd == "" {
			return nil, fmt.Errorf("local source %q is relative, and no working directory was given to resolve it against", src)
		}
		return inspectLocal(ResolvePath(p, opts.Cwd), src)
	default:
		return nil, fmt.Errorf("unsupported source kind %s", kind)
	}
}

// inspectGit treeless-clones url into a temp dir, pins the commit and default
// ref, and discovers the skills in the clone.
func inspectGit(ctx context.Context, url, ref string) (*Inspection, error) {
	tmp, err := os.MkdirTemp("", "skillm-clone-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	insp := &Inspection{Source: url, Kind: state.KindGit, tmp: tmp}
	fail := func(e error) (*Inspection, error) {
		insp.Close()
		return nil, e
	}

	// git clone wants to create the destination itself; give it a
	// non-existent subpath of the temp dir.
	repoDir := filepath.Join(tmp, "repo")
	insp.root = repoDir
	if err := gitx.TreelessClone(ctx, url, ref, repoDir); err != nil {
		return fail(err)
	}

	// Record the ref asked for, or the default branch, so check/update know
	// what to fetch later.
	insp.Ref = ref
	if insp.Ref == "" {
		def, err := gitx.DefaultRef(ctx, repoDir)
		if err != nil {
			return fail(fmt.Errorf("resolve default branch of %s: %w", url, err))
		}
		insp.Ref = def
	}
	if insp.Commit, err = gitx.HeadCommit(ctx, repoDir); err != nil {
		return fail(err)
	}

	found, err := source.DiscoverSkills(repoDir)
	if err != nil {
		return fail(err)
	}
	if len(found) == 0 {
		return fail(fmt.Errorf("no skills found in %s: expected at least one directory containing %s", url, skill.SkillFile))
	}
	for _, f := range found {
		insp.Skills = append(insp.Skills, inspected(f, RepoRelSubpath(repoDir, f.Dir)))
	}
	return insp, nil
}

// inspectLocal discovers the skills under the absolute directory dir. display
// is the Source as the caller spelled it, for the error message.
func inspectLocal(dir, display string) (*Inspection, error) {
	found, err := source.DiscoverSkills(dir)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no skills found in %s: expected a directory containing %s", display, skill.SkillFile)
	}
	insp := &Inspection{Source: dir, Kind: state.KindLocal, root: dir}
	for _, f := range found {
		insp.Skills = append(insp.Skills, inspected(f, f.Dir))
	}
	return insp, nil
}

// inspected builds the InspectedSkill for a discovered skill at path.
func inspected(f source.Found, path string) InspectedSkill {
	s := InspectedSkill{ID: f.Id, Path: path}
	if f.Skill != nil {
		s.Name, s.Description = f.Skill.Name, f.Skill.Description
	}
	return s
}

// stage returns the directory holding s's content and, for git, its Revision
// at the inspected commit. A local skill is read in place; a git skill is
// materialized into a fresh staging dir inside the Inspection's temp dir. id
// is the Skill ID it is being installed under, for error messages.
func (i *Inspection) stage(ctx context.Context, s InspectedSkill, id string) (dir, rev string, err error) {
	if i.Kind == state.KindLocal {
		return s.Path, "", nil
	}
	if i.tmp == "" {
		return "", "", errors.New("inspection is closed")
	}
	// Read the revision at the inspected commit before materializing, so the
	// recorded revision is exactly what was copied.
	rev, err = gitx.SubtreeSHA(ctx, i.root, i.Commit, s.Path)
	if err != nil {
		return "", "", fmt.Errorf("read revision of %q: %w", id, err)
	}
	dir, err = os.MkdirTemp(i.tmp, "stage-")
	if err != nil {
		return "", "", fmt.Errorf("create staging dir: %w", err)
	}
	if err := gitx.MaterializeSubdir(ctx, i.root, s.Path, dir); err != nil {
		return "", "", fmt.Errorf("materialize skill %q: %w", id, err)
	}
	return dir, rev, nil
}
