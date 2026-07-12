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
			"skill's source is fetched into Home at the locked ref (recording its revision " +
			"for `check`/`update`), the directory is recorded as a Local install root, a " +
			"missing canonical copy in .agents/skills is restored from Home, and missing " +
			"agent links are created for the enabled agents. Entries already managed here " +
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
			if adoptLocalInstall(home, st, agents, existing, root) {
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
			if err := store.AddSkillDir(home, it.name, stage); err != nil {
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
			adoptLocalInstall(home, st, agents, entry, root)
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

// adoptLocalInstall records root as a Local install root for skill e and
// completes whatever the install on disk is missing: an absent canonical copy
// is restored from Home (with a fresh lockfile hash), and missing agent links
// are created. An existing copy is left untouched — reconciling its content
// with upstream is `skillm update`'s job. It reports whether st changed.
func adoptLocalInstall(home string, st *state.State, agents []agentdir.Agent, e state.SkillEntry, root string) bool {
	changed := st.AddVendoredRoot(e.ID, root)
	if st.AddLocalRoot(root) {
		changed = true
	}

	localAgents, _ := splitLocalAliased(agents, root)
	if !localCopyExists(home, e.ID, root) {
		if err := store.ReplaceDir(store.SkillDir(home, e.ID), agentdir.CanonicalSkillDir(root, e.ID)); err != nil {
			ui.Warnf("restore copy of %s in %s: %v", e.ID, root, err)
			return changed
		}
		upsertLockEntry(e, root)
	}
	linkVendorAgents(home, e.ID, localAgents, agentdir.Local, root, scopeLabel(agentdir.Local, root, ""))
	return changed
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

// normalizeRemote reduces a git remote URL to a comparable form: lowercase,
// scheme and trailing ".git"/slashes stripped, the scp-like "git@host:path"
// form folded to "host/path".
func normalizeRemote(u string) string {
	s := strings.ToLower(strings.TrimSpace(u))
	for _, p := range []string{"https://", "http://", "ssh://"} {
		s = strings.TrimPrefix(s, p)
	}
	if rest, ok := strings.CutPrefix(s, "git@"); ok {
		rest = strings.Replace(rest, ":", "/", 1)
		s = rest
	}
	s = strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
	return s
}
