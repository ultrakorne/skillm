package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newImportCmd())
}

func newImportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "import [dir]",
		Short: "Adopt a project's skills-lock.json into skillm's tracking",
		Long: "import reads the skills-lock.json at the given directory (default: the " +
			"current one) — typically written by a teammate with skillm or with vercel's " +
			"`npx skills` CLI — and brings every entry under skillm management: each git " +
			"skill's source is fetched at the locked ref (recording its revision for " +
			"`check`/`update`), the directory is recorded as a Local install root, a missing " +
			"canonical copy in .agents/skills is written from the fetched content (or " +
			"restored from an existing install), and missing agent links are created for the " +
			"enabled agents. Entries already managed here " +
			"are simply adopted; entries that do not describe a git remote (local paths, " +
			"node_modules, registry skills) are reported and skipped. `skillm update` also " +
			"runs this adoption automatically across every tracked project, so a teammate's " +
			"additions join your machine-wide updates.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runImport(cmd.Context(), dir)
		},
	}
	return c
}

func runImport(ctx context.Context, dir string) error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}
	if err := store.EnsureHome(home); err != nil {
		return err
	}
	if err := config.EnsureExists(home); err != nil {
		return err
	}

	root, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", dir, err)
	}

	lf, err := lockfile.Load(root)
	if err != nil {
		return err
	}
	if len(lf.Skills) == 0 {
		ui.Warnf("no %s (or no entries) in %s; nothing to import", lockfile.FileName, root)
		return nil
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	st, err := state.Load(home)
	if err != nil {
		return err
	}

	changed, imported := importLockEntries(ctx, home, st, cfg.EnabledAgents(), root, lf)
	if changed {
		if err := state.Save(home, st); err != nil {
			return fmt.Errorf("save registry: %w", err)
		}
	}
	if imported == 0 {
		ui.Successf("nothing new to import from %s", root)
	}
	return ctx.Err()
}

// importLockEntries brings every entry of root's lockfile under skillm
// management: unknown skills are fetched into Home from their locked source
// (one clone per source repo), known skills are adopted, and in both cases
// root is recorded as a Local install root, a missing canonical copy is
// restored from Home, and missing agent links are created. Failures are
// per-entry warnings, never fatal — one broken entry must not block the rest.
// It mutates st in place and reports whether it changed (the caller persists)
// and how many entries were imported or adopted.
func importLockEntries(ctx context.Context, home string, st *state.State, agents []agentdir.Agent, root string, lf *lockfile.File) (changed bool, imported int) {
	// Deterministic order for output and for first-wins conflicts.
	names := make([]string, 0, len(lf.Skills))
	for name := range lf.Skills {
		names = append(names, name)
	}
	sort.Strings(names)

	// Group the unknown entries by (clone URL, ref) so each source repo is
	// cloned once, however many skills it provides.
	type fetchItem struct {
		name   string
		entry  *lockfile.Entry
		subdir string
	}
	type fetchGroup struct {
		url, ref string
		items    []fetchItem
	}
	var groups []fetchGroup
	groupIdx := map[string]int{}

	for _, name := range names {
		entry := lf.Skills[name]
		if strings.ContainsAny(name, "/\\") || name == "" || name == "." || name == ".." {
			ui.Warnf("skipping %q: not a usable skill id", name)
			continue
		}

		if existing, ok := st.Get(name); ok {
			if !lockEntryMatches(existing, entry) {
				ui.Warnf("skipping %s: already in Home from a different source (%s)", name, existing.Source)
				continue
			}
			if landLocalInstall(ctx, home, st, agents, existing, root, "") {
				changed = true
				imported++
				ui.Successf("adopted %s (%s)", name, root)
			}
			continue
		}

		url, err := entry.CloneURL()
		if err != nil {
			ui.Warnf("skipping %s: %v", name, err)
			continue
		}
		subdir, ok := entry.SubdirOf()
		if !ok {
			ui.Warnf("skipping %s: lock entry has no usable skillPath", name)
			continue
		}
		key := url + "\x00" + entry.Ref
		gi, ok := groupIdx[key]
		if !ok {
			gi = len(groups)
			groupIdx[key] = gi
			groups = append(groups, fetchGroup{url: url, ref: entry.Ref})
		}
		groups[gi].items = append(groups[gi].items, fetchItem{name: name, entry: entry, subdir: subdir})
	}

	for _, g := range groups {
		if ctx.Err() != nil {
			return changed, imported
		}
		tmp, err := os.MkdirTemp("", "skillm-import-clone-")
		if err != nil {
			ui.Warnf("import from %s: %v", g.url, err)
			continue
		}
		repoDir := filepath.Join(tmp, "repo")
		if err := gitx.TreelessClone(ctx, g.url, g.ref, repoDir); err != nil {
			ui.Warnf("import from %s: %v", g.url, err)
			os.RemoveAll(tmp)
			continue
		}

		pinnedRef := g.ref
		if pinnedRef == "" {
			if def, err := gitx.DefaultRef(ctx, repoDir); err == nil {
				pinnedRef = def
			}
		}

		for _, it := range g.items {
			rev, err := gitx.SubtreeSHA(ctx, repoDir, pinnedRef, it.subdir)
			if err != nil {
				ui.Warnf("skipping %s: %v", it.name, err)
				continue
			}
			stage, err := os.MkdirTemp(tmp, "stage-")
			if err != nil {
				ui.Warnf("skipping %s: %v", it.name, err)
				continue
			}
			if err := gitx.MaterializeSubdir(ctx, repoDir, it.subdir, stage); err != nil {
				ui.Warnf("skipping %s: %v", it.name, err)
				continue
			}

			entry := state.SkillEntry{
				ID:          it.name,
				Kind:        state.KindGit,
				Source:      g.url,
				Path:        it.subdir,
				Ref:         pinnedRef,
				Revision:    rev,
				InstalledAt: time.Now().UTC(),
			}
			st.Upsert(entry)
			// Write the project's canonical copy straight from the materialized
			// subdir (stage) — there is no Home library to restore it from.
			landLocalInstall(ctx, home, st, agents, entry, root, stage)
			changed = true
			imported++
			ui.Successf("imported %s from %s (%s)", it.name, g.url, root)
		}
		os.RemoveAll(tmp)
	}
	return changed, imported
}

// autoImportTrackedRoots runs the lockfile adoption of importLockEntries over
// every tracked project root — the machine-wide sweep an all-skills `skillm
// update` starts with, so skills a teammate added with `npx skills` (or
// another machine's skillm) join the update. Roots without a readable lockfile
// are skipped quietly; adoption is idempotent, so already-converged roots
// produce no output. It mutates st in place and reports whether it changed
// (the caller persists).
func autoImportTrackedRoots(ctx context.Context, home string, st *state.State, agents []agentdir.Agent) bool {
	roots := map[string]bool{}
	for _, r := range st.LocalRoots {
		roots[r] = true
	}
	for _, e := range st.Skills {
		for _, r := range e.VendoredAt {
			roots[r] = true
		}
	}
	sorted := make([]string, 0, len(roots))
	for r := range roots {
		sorted = append(sorted, r)
	}
	sort.Strings(sorted)

	changed := false
	for _, root := range sorted {
		if ctx.Err() != nil {
			return changed
		}
		lf, err := lockfile.Load(root)
		if err != nil {
			ui.Warnf("skipping %s: %v", root, err)
			continue
		}
		if len(lf.Skills) == 0 {
			continue
		}
		if ch, _ := importLockEntries(ctx, home, st, agents, root, lf); ch {
			changed = true
		}
	}
	return changed
}

// landLocalInstall records root as a Local install root for skill e and
// completes whatever the install on disk is missing: an absent canonical copy
// is written (with a fresh lockfile hash) and missing agent links are created.
// prefer, when a readable directory, is the content source for a missing copy
// (a freshly materialized clone); otherwise the copy is restored from an
// existing install of the skill or re-fetched (see resolveCopySource). An
// existing copy is left untouched — reconciling its content with upstream is
// `skillm update`'s job. It reports whether st changed.
func landLocalInstall(ctx context.Context, home string, st *state.State, agents []agentdir.Agent, e state.SkillEntry, root, prefer string) bool {
	changed := st.AddVendoredRoot(e.ID, root)
	if st.AddLocalRoot(root) {
		changed = true
	}

	localAgents, _ := splitLocalAliased(agents, root)
	if !localCopyExists(home, e.ID, root) {
		src, cleanup, err := resolveCopySource(ctx, home, st, e, prefer)
		if err != nil {
			ui.Warnf("restore copy of %s in %s: %v", e.ID, root, err)
			return changed
		}
		if cleanup != nil {
			defer cleanup()
		}
		if err := store.ReplaceDir(src, agentdir.CanonicalSkillDir(root, e.ID)); err != nil {
			ui.Warnf("restore copy of %s in %s: %v", e.ID, root, err)
			return changed
		}
		upsertLockEntry(e, root)
	}
	linkVendorAgents(home, e.ID, localAgents, agentdir.Local, root, scopeLabel(agentdir.Local, root, ""))
	return changed
}

// resolveCopySource returns a readable directory holding skill e's content, to
// write a missing install copy from. In order: the caller's preferred dir when
// it exists (a freshly materialized clone); the canonical global copy; any
// other project's canonical copy; a local-path skill's recorded source; and
// finally a fresh git re-fetch. It returns the dir and, when a temp was created
// (the re-fetch), a cleanup func the caller must call after copying.
func resolveCopySource(ctx context.Context, home string, st *state.State, e state.SkillEntry, prefer string) (dir string, cleanup func(), err error) {
	if prefer != "" && dirExists(prefer) {
		return prefer, nil, nil
	}
	if e.Global && vendorCopyExists(home, e.ID, agentdir.Global, "") {
		return agentdir.CanonicalSkillDirAt(agentdir.Global, "", e.ID), nil, nil
	}
	for _, r := range st.VendoredRoots(e.ID) {
		if localCopyExists(home, e.ID, r) {
			return agentdir.CanonicalSkillDir(r, e.ID), nil, nil
		}
	}
	if e.Kind == state.KindLocal {
		if dirExists(e.Source) {
			return e.Source, nil, nil
		}
		return "", nil, fmt.Errorf("no copy of local skill %q remains and its source %s is gone", e.ID, e.Source)
	}
	d, _, clean, err := refetchSkill(ctx, e)
	if err != nil {
		return "", nil, err
	}
	return d, clean, nil
}

// lockEntryMatches reports whether a lockfile entry describes the same source
// as an existing registry entry — same remote (compared leniently: scheme and
// a trailing ".git" ignored) and same subdirectory — so import can tell
// "already managed" from a genuine name collision.
func lockEntryMatches(existing state.SkillEntry, entry *lockfile.Entry) bool {
	if existing.Kind != state.KindGit {
		// Local-kind skills carry no remote; match on the recorded source path.
		return entry.SourceType == lockfile.SourceLocal && entry.Source == existing.Source
	}
	url, err := entry.CloneURL()
	if err != nil {
		return false
	}
	subdir, ok := entry.SubdirOf()
	if !ok || subdir != existing.Path {
		return false
	}
	return normalizeRemote(url) == normalizeRemote(existing.Source)
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

// normalizeRemote reduces a git remote URL to a comparable form: scheme and
// trailing ".git"/slashes stripped, the scp-like "git@host:path" form folded to
// "host/path", and the host lowercased (DNS is case-insensitive). The path is
// folded only for the hosts that treat it that way — see
// pathCaseInsensitiveHosts.
//
// It under-normalizes in three confirmed cases (embedded credentials, an ssh://
// port, an scp-like form with a non-"git@" user), each recorded in
// docs/known-issues.md.
func normalizeRemote(u string) string {
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
