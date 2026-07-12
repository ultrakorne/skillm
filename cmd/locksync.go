package cmd

import (
	"path"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/ui"
)

// skills-lock.json synchronization. Every Local install root gets a
// vercel-compatible lockfile entry per skill, written next to the canonical
// copies, so teammates can restore or update the repo's skills with `npx
// skills` while skillm keeps its own tracking in state.toml. Lockfile writes
// are best-effort by design (matching vercel's): a failure to write the lock
// never fails the install that produced the files — it is reported and the
// next sync repairs it.

// upsertLockEntry writes (or refreshes) skill e's entry in base's
// skills-lock.json, hashing the canonical copy on disk. Unknown keys of an
// existing entry (another tool's fields, e.g. "subagents") are preserved. A
// lockfile with a newer schema version is left untouched with a warning.
func upsertLockEntry(e state.SkillEntry, base string) {
	f, err := lockfile.Load(base)
	if err != nil {
		ui.Warnf("skills-lock.json not updated: %v", err)
		return
	}
	if !f.Editable() {
		ui.Warnf("skills-lock.json at %s uses a newer schema; entry for %s not written", base, e.ID)
		return
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
		ui.Warnf("skills-lock.json not updated: %v", err)
		return
	}
	entry.ComputedHash = hash

	if prev := f.Skills[e.ID]; prev != nil {
		entry.Extra = prev.Extra // keep fields other tools wrote
	}
	f.Skills[e.ID] = entry

	if err := lockfile.Save(base, f); err != nil {
		ui.Warnf("%v", err)
	}
}

// removeLockEntry drops skill id from base's skills-lock.json, removing the
// file when it becomes empty. Entries other tools wrote stay put; a newer
// schema version is left untouched with a warning.
func removeLockEntry(id, base string) {
	f, err := lockfile.Load(base)
	if err != nil {
		ui.Warnf("skills-lock.json not updated: %v", err)
		return
	}
	if _, ok := f.Skills[id]; !ok {
		return
	}
	if !f.Editable() {
		ui.Warnf("skills-lock.json at %s uses a newer schema; entry for %s not removed", base, id)
		return
	}
	delete(f.Skills, id)
	if err := lockfile.Save(base, f); err != nil {
		ui.Warnf("%v", err)
	}
}
