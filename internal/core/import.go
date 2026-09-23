package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// Event codes Import and AutoImportTrackedRoots report with, beyond the vendor
// primitives' own.
const (
	// CodeImported: a lockfile entry skillm did not manage was fetched and
	// installed at the project.
	CodeImported = "imported"
	// CodeAdopted: an entry for a skill skillm already manages was recorded
	// at the project (and its missing copy or links completed).
	CodeAdopted = "adopted"
	// CodeImportSkipped: an entry could not be imported (an unusable id, not
	// a git remote, no usable skill path, or a failed fetch).
	CodeImportSkipped = "import_skipped"
	// CodeImportCollision: the entry's id is already installed from a
	// different Source; it is skipped, never overwritten.
	CodeImportCollision = "import_collision"
	// CodeRestoreFailed: a missing canonical copy could not be written.
	CodeRestoreFailed = "restore_failed"
	// CodeLockfileUnreadable: a tracked project's skills-lock.json could not
	// be read, so the sweep skipped that project.
	CodeLockfileUnreadable = "lockfile_unreadable"
)

// ImportOutcome is what an import did with one lockfile entry.
type ImportOutcome string

const (
	// ImportImported: the skill was fetched from the entry's source and
	// installed at the project.
	ImportImported ImportOutcome = "imported"
	// ImportAdopted: an already-managed skill was recorded at the project.
	ImportAdopted ImportOutcome = "adopted"
	// ImportSkipped: the entry was left alone (Err says why).
	ImportSkipped ImportOutcome = "skipped"
)

// ImportedSkill is one lockfile entry an import acted on. An entry that was
// already fully adopted (nothing to do) is not listed.
type ImportedSkill struct {
	ID      string
	Root    string
	Outcome ImportOutcome
	// Err is why a skipped entry was skipped.
	Err error
}

// ImportResult is Import's outcome for one project.
type ImportResult struct {
	// Root is the absolute project directory whose lockfile was read.
	Root string
	// Entries is the number of entries in its skills-lock.json (0 when there
	// is none).
	Entries int
	// Skills lists the entries imported, adopted or skipped, in the order
	// they were reported.
	Skills []ImportedSkill
}

// ImportedAny reports whether any entry was imported or adopted.
func (r ImportResult) ImportedAny() bool {
	for _, s := range r.Skills {
		if s.Outcome != ImportSkipped {
			return true
		}
	}
	return false
}

// Import adopts the skills-lock.json at dir (resolved against opts.Cwd) into
// skillm's tracking: every git entry skillm does not manage yet is fetched at
// its locked ref and installed at the project (a missing canonical copy
// written, missing agent links created), and every entry for a skill it
// already manages from the same Source is recorded at the project. Entries
// that are not git remotes, and ids installed from a different Source, are
// reported and skipped. An existing copy is never rewritten: reconciling
// content with upstream is Update's job.
//
// The fetches run first, without Home's lock; the writes then run under it
// (opts.Lock) against a fresh read of config, the Registry and the lockfile.
// A lockfile with no entries (or none at all) is not an error: the result's
// Entries is 0 and nothing is locked or written. A dir that is missing or not
// a directory is a *ProjectDirError.
func Import(ctx context.Context, opts Options, rep Reporter, dir string) (ImportResult, error) {
	rep = nopIfNil(rep)
	if !filepath.IsAbs(dir) && opts.Cwd == "" {
		return ImportResult{}, fmt.Errorf("import needs an absolute project directory, got %q", dir)
	}
	root := ResolvePath(dir, opts.Cwd)
	res := ImportResult{Root: root}
	// A missing lockfile is "nothing to import", but a missing directory is
	// a stale or mistyped path, not an empty project.
	if fi, err := os.Stat(root); err != nil {
		return res, &ProjectDirError{Path: root, Err: err}
	} else if !fi.IsDir() {
		return res, &ProjectDirError{Path: root, Err: errors.New("not a directory")}
	}
	if err := store.EnsureHome(opts.Home); err != nil {
		return res, err
	}

	lf, err := lockfile.Load(root)
	if err != nil {
		return res, err
	}
	res.Entries = len(lf.Skills)
	if len(lf.Skills) == 0 {
		return res, nil
	}

	// Fetch what the Registry, as it is now, does not manage yet.
	snap, err := state.Load(opts.Home)
	if err != nil {
		return res, err
	}
	f := newImportFetcher()
	defer f.close()
	f.prefetch(ctx, snap, root, lf)
	if err := ctx.Err(); err != nil {
		return res, err
	}

	unlock, err := opts.lock(ctx)
	if err != nil {
		return res, err
	}
	defer unlock()
	if err := config.EnsureExists(opts.Home); err != nil {
		return res, err
	}
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return res, err
	}
	st, err := state.Load(opts.Home)
	if err != nil {
		return res, err
	}
	// Re-read the lockfile too: another skillm process may have changed it
	// while the fetches ran.
	if lf, err = lockfile.Load(root); err != nil {
		return res, err
	}
	res.Entries = len(lf.Skills)

	changed, skills := importLockEntries(ctx, opts, rep, st, cfg.EnabledAgents(), root, lf, f)
	res.Skills = skills
	if changed {
		if err := state.Save(opts.Home, st); err != nil {
			return res, fmt.Errorf("save registry: %w", err)
		}
	}
	return res, ctx.Err()
}

// AutoImportTrackedRoots runs Import's adoption over every tracked project
// root: the machine-wide sweep an all-skills Update starts with, so skills a
// teammate added to a project's skills-lock.json (with `npx skills` or
// another machine's skillm) join the update. A root without a lockfile is
// skipped quietly, one whose lockfile cannot be read with a warning. Adoption
// is idempotent, so already-converged roots produce no output.
//
// Like Import it fetches without Home's lock, then takes it (opts.Lock) to
// re-read Home and write. It saves the Registry itself when it changed.
func AutoImportTrackedRoots(ctx context.Context, opts Options, rep Reporter) ([]ImportedSkill, error) {
	rep = nopIfNil(rep)
	snap, err := state.Load(opts.Home)
	if err != nil {
		return nil, err
	}
	f := newImportFetcher()
	defer f.close()
	for _, root := range trackedRoots(snap) {
		if ctx.Err() != nil {
			break
		}
		if lf, err := lockfile.Load(root); err == nil {
			f.prefetch(ctx, snap, root, lf)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	unlock, err := opts.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return nil, err
	}
	st, err := state.Load(opts.Home)
	if err != nil {
		return nil, err
	}

	var all []ImportedSkill
	changed := false
	for _, root := range trackedRoots(st) {
		if ctx.Err() != nil {
			break
		}
		lf, err := lockfile.Load(root)
		if err != nil {
			rep.Event(logEvent(LevelWarn, "", CodeLockfileUnreadable, fmt.Sprintf("skipping %s: %v", root, err)))
			continue
		}
		if len(lf.Skills) == 0 {
			continue
		}
		ch, skills := importLockEntries(ctx, opts, rep, st, cfg.EnabledAgents(), root, lf, f)
		all = append(all, skills...)
		if ch {
			changed = true
		}
	}
	if changed {
		if err := state.Save(opts.Home, st); err != nil {
			return all, fmt.Errorf("save registry: %w", err)
		}
	}
	return all, nil
}

// trackedRoots returns every project root st tracks — the recorded local roots
// and every skill's vendored roots — sorted.
func trackedRoots(st *state.State) []string {
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
	return sorted
}

// importStep is what importing one lockfile entry takes, given the Registry.
type importStep int

const (
	// stepSkip: the entry cannot be imported (importDecision.text says why).
	stepSkip importStep = iota
	// stepCollision: the id is installed from a different Source.
	stepCollision
	// stepAdopt: the skill is already managed from this Source.
	stepAdopt
	// stepFetch: the skill is new; its source must be fetched.
	stepFetch
)

// importDecision is how one lockfile entry is imported.
type importDecision struct {
	step importStep
	// existing is the Registry entry (stepCollision, stepAdopt).
	existing state.SkillEntry
	// url and subdir locate a new skill (stepFetch).
	url, subdir string
	// text and err explain a stepSkip.
	text string
	err  error
}

// decideImport classifies lockfile entry name against st. It changes nothing,
// so the unlocked prefetch and the locked write phase decide alike.
func decideImport(st *state.State, root, name string, entry *lockfile.Entry) importDecision {
	if strings.ContainsAny(name, "/\\") || name == "" || name == "." || name == ".." {
		err := errors.New("not a usable skill id")
		return importDecision{step: stepSkip, err: err, text: fmt.Sprintf("skipping %q: %v", name, err)}
	}
	if existing, ok := st.Get(name); ok {
		if !lockEntryMatches(existing, entry, root) {
			return importDecision{step: stepCollision, existing: existing}
		}
		return importDecision{step: stepAdopt, existing: existing}
	}
	url, err := entry.CloneURL()
	if err != nil {
		return importDecision{step: stepSkip, err: err, text: fmt.Sprintf("skipping %s: %v", name, err)}
	}
	subdir, ok := entry.SubdirOf()
	if !ok {
		err := errors.New("lock entry has no usable skillPath")
		return importDecision{step: stepSkip, err: err, text: fmt.Sprintf("skipping %s: %v", name, err)}
	}
	return importDecision{step: stepFetch, url: url, subdir: subdir}
}

// sortedEntryNames returns lf's entry names sorted, for a deterministic order
// of output and of first-wins conflicts.
func sortedEntryNames(lf *lockfile.File) []string {
	names := make([]string, 0, len(lf.Skills))
	for name := range lf.Skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// importLockEntries brings every entry of root's lockfile under skillm
// management: unknown skills are fetched from their locked source (one clone
// per source repo, reused from f when the prefetch already made it), known
// skills are adopted, and in both cases root is recorded as a Local install
// root, a missing canonical copy is written, and missing agent links are
// created. Failures are per-entry warnings, never fatal: one broken entry
// must not block the rest. It mutates st in place and reports whether it
// changed (the caller persists) and what it did per entry. The caller holds
// Home's lock.
func importLockEntries(ctx context.Context, opts Options, rep Reporter, st *state.State, agents []agentdir.Agent, root string, lf *lockfile.File, f *importFetcher) (changed bool, skills []ImportedSkill) {
	skipped := func(id string, err error) {
		skills = append(skills, ImportedSkill{ID: id, Root: root, Outcome: ImportSkipped, Err: err})
	}

	// Group the unknown entries by (clone URL, ref) so each source repo is
	// cloned once, however many skills it provides.
	type fetchItem struct {
		name   string
		subdir string
	}
	type fetchGroup struct {
		url, ref string
		items    []fetchItem
	}
	var groups []fetchGroup
	groupIdx := map[string]int{}

	for _, name := range sortedEntryNames(lf) {
		entry := lf.Skills[name]
		d := decideImport(st, root, name, entry)
		switch d.step {
		case stepSkip:
			rep.Event(logEvent(LevelWarn, name, CodeImportSkipped, d.text))
			skipped(name, d.err)
		case stepCollision:
			err := fmt.Errorf("already in Home from a different source (%s)", d.existing.Source)
			rep.Event(logEvent(LevelWarn, name, CodeImportCollision, fmt.Sprintf("skipping %s: %v", name, err)))
			skipped(name, err)
		case stepAdopt:
			if landLocalInstall(ctx, opts, rep, st, agents, d.existing, root, "") {
				changed = true
				skills = append(skills, ImportedSkill{ID: name, Root: root, Outcome: ImportAdopted})
				rep.Event(logEvent(LevelSuccess, name, CodeAdopted, fmt.Sprintf("adopted %s (%s)", name, root)))
			}
		case stepFetch:
			key := d.url + "\x00" + entry.Ref
			gi, ok := groupIdx[key]
			if !ok {
				gi = len(groups)
				groupIdx[key] = gi
				groups = append(groups, fetchGroup{url: d.url, ref: entry.Ref})
			}
			groups[gi].items = append(groups[gi].items, fetchItem{name: name, subdir: d.subdir})
		}
	}

	for _, g := range groups {
		if ctx.Err() != nil {
			return changed, skills
		}
		c := f.clone(ctx, g.url, g.ref)
		if c.err != nil {
			rep.Event(logEvent(LevelWarn, "", CodeImportSkipped, fmt.Sprintf("import from %s: %v", g.url, c.err)))
			for _, it := range g.items {
				skipped(it.name, c.err)
			}
			continue
		}
		for _, it := range g.items {
			s := f.stage(ctx, c, it.subdir)
			if s.err != nil {
				rep.Event(logEvent(LevelWarn, it.name, CodeImportSkipped, fmt.Sprintf("skipping %s: %v", it.name, s.err)))
				skipped(it.name, s.err)
				continue
			}
			entry := state.SkillEntry{
				ID:          it.name,
				Kind:        state.KindGit,
				Source:      g.url,
				Path:        it.subdir,
				Ref:         c.ref,
				Revision:    s.rev,
				InstalledAt: time.Now().UTC(),
			}
			st.Upsert(entry)
			// Write the project's canonical copy straight from the
			// materialized subdir: there is no Home library to restore it from.
			landLocalInstall(ctx, opts, rep, st, agents, entry, root, s.dir)
			changed = true
			skills = append(skills, ImportedSkill{ID: it.name, Root: root, Outcome: ImportImported})
			rep.Event(logEvent(LevelSuccess, it.name, CodeImported, fmt.Sprintf("imported %s from %s (%s)", it.name, g.url, root)))
		}
	}
	return changed, skills
}

// importFetcher clones import sources and stages their skills, remembering
// each result so the write phase reuses what the unlocked prefetch fetched.
// It is used from one goroutine at a time. close removes every temp dir.
type importFetcher struct {
	clones map[string]*importClone // by url + "\x00" + ref
	stages map[string]*importStage // by clone key + "\x00" + subdir
}

// importClone is one treeless clone of an import source.
type importClone struct {
	key  string
	tmp  string // temp dir holding the clone and its staged skills
	repo string
	// ref is the ref the skills are read at: the locked one, or the
	// clone's default branch when the entry pins none.
	ref string
	err error
}

// importStage is one skill materialized from an importClone.
type importStage struct {
	rev, dir string
	err      error
}

func newImportFetcher() *importFetcher {
	return &importFetcher{clones: map[string]*importClone{}, stages: map[string]*importStage{}}
}

// prefetch clones and stages every entry of root's lockfile that is new to st,
// without writing anything outside its temp dirs. It stops at cancellation.
func (f *importFetcher) prefetch(ctx context.Context, st *state.State, root string, lf *lockfile.File) {
	for _, name := range sortedEntryNames(lf) {
		entry := lf.Skills[name]
		d := decideImport(st, root, name, entry)
		if d.step != stepFetch {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if c := f.clone(ctx, d.url, entry.Ref); c.err == nil {
			f.stage(ctx, c, d.subdir)
		}
	}
}

// clone returns the treeless clone of url at ref, cloning it on first use.
// A failure caused by cancellation is not remembered.
func (f *importFetcher) clone(ctx context.Context, url, ref string) *importClone {
	key := url + "\x00" + ref
	if c, ok := f.clones[key]; ok {
		return c
	}
	c := &importClone{key: key, ref: ref}
	defer func() {
		if c.err == nil || ctx.Err() == nil {
			f.clones[key] = c
		} else if c.tmp != "" {
			os.RemoveAll(c.tmp)
		}
	}()
	tmp, err := os.MkdirTemp("", "skillm-import-clone-")
	if err != nil {
		c.err = err
		return c
	}
	c.tmp = tmp
	repo := filepath.Join(tmp, "repo")
	if err := gitx.TreelessClone(ctx, url, ref, repo); err != nil {
		c.err = err
		return c
	}
	c.repo = repo
	if c.ref == "" {
		if def, err := gitx.DefaultRef(ctx, repo); err == nil {
			c.ref = def
		}
	}
	return c
}

// stage returns skill subdir of clone c, materialized into a temp dir with its
// current Revision, staging it on first use. A failure caused by cancellation
// is not remembered.
func (f *importFetcher) stage(ctx context.Context, c *importClone, subdir string) *importStage {
	key := c.key + "\x00" + subdir
	if s, ok := f.stages[key]; ok {
		return s
	}
	s := &importStage{}
	defer func() {
		if s.err == nil || ctx.Err() == nil {
			f.stages[key] = s
		}
	}()
	rev, err := gitx.SubtreeSHA(ctx, c.repo, c.ref, subdir)
	if err != nil {
		s.err = err
		return s
	}
	dir, err := os.MkdirTemp(c.tmp, "stage-")
	if err != nil {
		s.err = err
		return s
	}
	if err := gitx.MaterializeSubdir(ctx, c.repo, subdir, dir); err != nil {
		s.err = err
		return s
	}
	s.rev, s.dir = rev, dir
	return s
}

// close removes every clone and staged skill.
func (f *importFetcher) close() {
	for _, c := range f.clones {
		if c.tmp != "" {
			os.RemoveAll(c.tmp)
		}
	}
	f.clones = map[string]*importClone{}
	f.stages = map[string]*importStage{}
}

// landLocalInstall records root as a Local install root for skill e and
// completes whatever the install on disk is missing: an absent canonical copy
// is written (with a fresh lockfile hash) and missing agent links are created
// (with opts.Force, taking over foreign entries at the link paths). prefer,
// when a readable directory, is the content source for a missing copy (a
// freshly materialized clone); otherwise the copy is restored from an existing
// install of the skill or re-fetched (see resolveCopySource). An existing copy
// is left untouched: reconciling its content with upstream is Update's job.
// It reports whether st changed.
func landLocalInstall(ctx context.Context, opts Options, rep Reporter, st *state.State, agents []agentdir.Agent, e state.SkillEntry, root, prefer string) bool {
	changed := st.AddVendoredRoot(e.ID, root)
	if st.AddLocalRoot(root) {
		changed = true
	}

	localAgents, _ := SplitLocalAliased(agents, root)
	if !LocalCopyExists(opts.Home, e.ID, root) {
		src, cleanup, err := resolveCopySource(ctx, opts, st, e, prefer)
		if err != nil {
			rep.Event(logEvent(LevelWarn, e.ID, CodeRestoreFailed, fmt.Sprintf("restore copy of %s in %s: %v", e.ID, root, err)))
			return changed
		}
		if cleanup != nil {
			defer cleanup()
		}
		if err := store.ReplaceDir(src, agentdir.CanonicalSkillDir(root, e.ID)); err != nil {
			rep.Event(logEvent(LevelWarn, e.ID, CodeRestoreFailed, fmt.Sprintf("restore copy of %s in %s: %v", e.ID, root, err)))
			return changed
		}
		_ = UpsertLockEntry(rep, e, root)
	}
	LinkVendorAgents(rep, opts.Home, e.ID, localAgents, agentdir.Local, root, ScopeLabel(agentdir.Local, root, ""), opts.Force)
	return changed
}

// resolveCopySource returns a readable directory holding skill e's content, to
// write a missing install copy from. In order: the caller's preferred dir when
// it exists (a freshly materialized clone); the canonical global copy; any
// other project's canonical copy; a local-path skill's recorded source
// (resolved against opts.Cwd when it is an older relative one); and finally a
// fresh git re-fetch. It returns the dir and, when a temp was created (the
// re-fetch), a cleanup func the caller must call after copying.
func resolveCopySource(ctx context.Context, opts Options, st *state.State, e state.SkillEntry, prefer string) (dir string, cleanup func(), err error) {
	if prefer != "" && isDir(prefer) {
		return prefer, nil, nil
	}
	if e.Global && CopyExists(opts.Home, e.ID, agentdir.Global, "") {
		return agentdir.CanonicalSkillDirAt(agentdir.Global, "", e.ID), nil, nil
	}
	for _, r := range st.VendoredRoots(e.ID) {
		if LocalCopyExists(opts.Home, e.ID, r) {
			return agentdir.CanonicalSkillDir(r, e.ID), nil, nil
		}
	}
	if e.Kind == state.KindLocal {
		if src, ok := localSource(e, opts.Cwd); ok {
			return src, nil, nil
		}
		return "", nil, fmt.Errorf("no copy of local skill %q remains and its source %s is gone", e.ID, e.Source)
	}
	d, _, clean, err := RefetchSkill(ctx, e)
	if err != nil {
		return "", nil, err
	}
	return d, clean, nil
}

// localSource returns local-path skill e's recorded source directory, and
// whether it still exists. An older relative Source is resolved against cwd;
// with no cwd it counts as gone rather than being looked up in the process's
// working directory.
func localSource(e state.SkillEntry, cwd string) (string, bool) {
	if !filepath.IsAbs(e.Source) && cwd == "" {
		return e.Source, false
	}
	src := ResolvePath(e.Source, cwd)
	return src, isDir(src)
}

// lockEntryMatches reports whether a lockfile entry describes the same source
// as an existing registry entry — same remote (compared leniently: scheme and
// a trailing ".git" ignored) and same subdirectory — so import can tell
// "already managed" from a genuine name collision.
func lockEntryMatches(existing state.SkillEntry, entry *lockfile.Entry, root string) bool {
	if existing.Kind != state.KindGit {
		// Local-kind skills carry no remote; match on the recorded source
		// path. Older installs wrote it relative (to the project root they ran
		// in) while the Registry now records it absolute, so resolve both
		// against the lockfile's root before comparing.
		return entry.SourceType == lockfile.SourceLocal &&
			ResolvePath(entry.Source, root) == ResolvePath(existing.Source, root)
	}
	url, err := entry.CloneURL()
	if err != nil {
		return false
	}
	subdir, ok := entry.SubdirOf()
	if !ok || subdir != existing.Path {
		return false
	}
	return NormalizeRemote(url) == NormalizeRemote(existing.Source)
}
