package core

import (
	"fmt"
	"path"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/state"
)

// skills-lock.json synchronization. Every Local install root gets a
// vercel-compatible lockfile entry per skill, written next to the canonical
// copies, so teammates can restore or update the repo's skills with `npx
// skills` while skillm keeps its own tracking in state.toml. Lockfile writes
// are best-effort by design (matching vercel's): a failure to write the lock
// never fails the install that produced the files. It is reported as a
// warning Event and returned, so a result can carry it as a partial failure,
// and the next sync repairs it.

// UpsertLockEntry writes (or refreshes) skill e's entry in base's
// skills-lock.json, hashing the canonical copy on disk. Unknown keys of an
// existing entry (another tool's fields, e.g. "subagents") are preserved. A
// lockfile with a newer schema version is left untouched. Any failure is both
// reported and returned.
func UpsertLockEntry(rep Reporter, e state.SkillEntry, base string) error {
	fail := lockFailure(rep, e.ID)
	f, err := lockfile.Load(base)
	if err != nil {
		return fail(fmt.Errorf("skills-lock.json not updated: %w", err))
	}
	if !f.Editable() {
		return fail(fmt.Errorf("skills-lock.json at %s uses a newer schema; entry for %s not written", base, e.ID))
	}

	entry := &lockfile.Entry{}
	if e.Kind == state.KindGit {
		entry.Source, entry.SourceURL, entry.SourceType = lockfile.GitSourceFields(e.Source)
		entry.Ref = e.Ref
		entry.SkillPath = path.Join(e.Path, "SKILL.md")
	} else {
		entry.Source = e.Source
		entry.SourceType = lockfile.SourceLocal
	}

	hash, err := lockfile.ComputeDirHash(agentdir.CanonicalSkillDir(base, e.ID))
	if err != nil {
		return fail(fmt.Errorf("skills-lock.json not updated: %w", err))
	}
	entry.ComputedHash = hash

	if prev := f.Skills[e.ID]; prev != nil {
		entry.Extra = prev.Extra // keep fields other tools wrote
	}
	f.Skills[e.ID] = entry

	if err := lockfile.Save(base, f); err != nil {
		return fail(err)
	}
	return nil
}

// RemoveLockEntry drops skill id from base's skills-lock.json, removing the
// file when it becomes empty. Entries other tools wrote stay put; a newer
// schema version is left untouched. Any failure is both reported and
// returned.
func RemoveLockEntry(rep Reporter, id, base string) error {
	fail := lockFailure(rep, id)
	f, err := lockfile.Load(base)
	if err != nil {
		return fail(fmt.Errorf("skills-lock.json not updated: %w", err))
	}
	if _, ok := f.Skills[id]; !ok {
		return nil
	}
	if !f.Editable() {
		return fail(fmt.Errorf("skills-lock.json at %s uses a newer schema; entry for %s not removed", base, id))
	}
	delete(f.Skills, id)
	if err := lockfile.Save(base, f); err != nil {
		return fail(err)
	}
	return nil
}

// lockFailure returns a func that reports err as a warning about skill id and
// returns it.
func lockFailure(rep Reporter, id string) func(error) error {
	rep = nopIfNil(rep)
	return func(err error) error {
		rep.Event(logEvent(LevelWarn, id, CodeLockfileFailed, err.Error()))
		return err
	}
}
