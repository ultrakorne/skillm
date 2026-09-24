# Core — Update and import

## Overview

`update` runs over `core.Update` (`internal/core/update.go`), and `import` over `core.Import`
(`internal/core/import.go`), which also holds `core.AutoImportTrackedRoots`, the adoption
sweep an all-skills Update starts with. Each fetches first with Home unlocked, then takes the
Home lock for its write phase and re-reads Config, the Registry and the Lockfile under it. Each
returns what it did per skill or Lockfile entry: an `UpdatedSkill` with its `UpdateOutcome`, or
an `ImportedSkill`. `cmd/update.go` and `cmd/import.go` only build `core.Options`, render the
Events and print the closing summary line.

## Noteworthy

### Core locks Home only through `Options.Lock`, and only around writes

`Options.Lock` is the caller's hook. Update, Import and the adoption sweep call it after their
fetches, so a slow network never holds Home. A nil hook means the caller already holds the
lock. If the context is cancelled before the write phase, nothing is written. The exception is
an all-skills Update: its adoption sweep runs first, under its own lock and save, so imports it
made may already be saved.

### A fetch is judged against the skill's current entry, never rolled back over it

Another process may change a skill while its update fetches. If the skill was uninstalled
meanwhile, the update of it is up to date. If another process moved it to the Revision fetched
here, the update only checks it for drift. If it was reinstalled from another Source, or moved
to a *different* Revision, the skill fails with "run update again" (`update_skipped`). Which of
the two Revisions is newer upstream is unknown, so writing the fetched tree could roll back
what the other process wrote.

### Every fetch is staged, advanced or not

Installs are compared by content against the staged upstream tree. That comparison is the only
way to find a drifted copy: a `git checkout` that reverts a committed `.agents/skills` copy
leaves the Registry's Revision ahead of the copy. A staging failure on a skill whose Revision
did not advance is `drift_check_skipped`, not a failure: the skill is up to date and the next
update retries the check.

### Outcome, Advanced and Warnings answer different questions

`Outcome` is where a skill ended: `pruned` overrides `updated` when its last install had
vanished. `Advanced` records that its Revision advanced even when it was then pruned.
`UpdatedAny` counts `Advanced`, so the CLI's "Everything is up to date." line never hides an
advance. `Warnings` collects the failed copy and `skills-lock.json` writes for installs that
stay recorded. These writes do not change `Outcome`: the skill is a partial success, and the
next update retries the stale installs because their content no longer matches upstream.

### One save after every skill; failures come back afterwards

Update saves the Registry once, after every skill is written, so a failure part-way keeps the
Revisions already advanced. Failed skills come back as `*UpdateFailedError`, with their ids,
after the others are written; each is also a log Event naming it (`update_failed` for a failed
fetch, `update_skipped` for one changed meanwhile), which the terminal drops for a fetch whose
row already shows the error. Only enabled agents get their links remade: a disabled agent's
links stay gone. With `Force`, every surviving install is relinked.

### Import refuses a missing directory, not a missing Lockfile

A directory without `skills-lock.json` imports nothing and succeeds with 0 entries. A
directory that is missing or is a file is a `*ProjectDirError`, because a stale or mistyped path
must not read as an empty project.

### Import decides twice and fetches once

The unlocked prefetch and the locked write phase classify each entry with the same rule
against the Registry they read. A skill installed meanwhile from the same Source is therefore
adopted, not fetched. The write phase reuses the prefetched clones (one per source URL and ref)
and never caches a failure caused by cancellation. Import never rewrites an existing copy:
reconciling content with upstream is Update's job.

### The live view ends before the lock and before any log line

`cmd/update.go`'s lock hook waits for the checklist to end first, because taking the lock can
print "waiting for …". `termReporter` (`cmd/reporter.go`) ends an open checklist before it
prints a log Event. Core reports a batch's log Events only once its last item is done.

## Integration

`internal/core/update_test.go` covers the fetch-before-lock order, the judgement against the
current entry, cancellation, `Advanced` on a pruned skill and the collected warnings.
`internal/core/import_test.go` covers the prefetch, adoption of a skill installed meanwhile and
local-source matching. `cmd/update_test.go`, `cmd/import_test.go` and `cmd/lock_test.go` cover
the printed lines and the wait for a held lock.
