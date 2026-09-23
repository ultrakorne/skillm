# Core — Install primitives

## Overview

The operations every changing command shares to write an install live in `internal/core`:
writing, refreshing and removing a Canonical copy and its agent Links
(`internal/core/vendor.go`), keeping a Local install root's `skills-lock.json` entry in step
(`internal/core/locksync.go`), and deciding Source identity (`internal/core/source.go`). They
are presentation-free: each reports what it did as a log Event with a stable `Code` and a
human `Text`, and returns its failures. Their callers, core's
`InstallSkills`, `Update`, `Import`, `Uninstall` and `SetAgents`, keep the loops and run under
the Home lock. The prompts stay in `cmd`, which passes `termLog` or `termReporter`
(`cmd/reporter.go`) to print each Text through the `ui` helper for its level the moment it is
reported. A Text never names a CLI flag or command: `termLog` appends the advice (`--force`,
`skillm install`, `skillm uninstall`) for the codes it resolves.

## Noteworthy

### A refusal and a failure carry different codes

`link_refused`/`unlink_refused` mean a foreign entry is in the way and is left alone, which
`--force` can take over for a link; `link_failed`/`unlink_failed` are I/O failures that force
cannot fix. The split rests on `errors.Is(err, linker.ErrNotManaged)`: every refusal in
`internal/linker/linker.go` wraps it, and `Unlink`'s refusals keep their original sentence, so a
caller keyed on `Code` can offer "take over" only where it works while the CLI text stays the same.

### An interactive "yes" never forces agent Links

`VendorOne` takes `force` (overwrite a foreign entry at the canonical slot) apart from
`forceLinks` (replace a foreign entry at an agent's link path). The overwrite prompt lists only
canonical-slot conflicts, so answering it must not delete entries it never showed; only an
explicit `--force` sets `forceLinks`. A Link refusal is reported, never fatal: the copy is the
recorded unit and Links are re-derived from disk. Likewise `VendorRemove` returns an unlink
failure apart from a failure to remove the copy, and only the latter stops the removal.

### A recorded install's slot is skillm's own

A directory at the canonical slot counts as skillm's copy only when the Registry records that
install. Otherwise it is a conflict that needs force, and a removal deletes a copy only when the
caller says the install is recorded. A legacy skillm symlink into Home at the slot is converted
to a copy, never treated as a conflict.

### Lockfile writes are best-effort, not silent

A failed `skills-lock.json` write never fails the install that produced the files, matching
vercel's CLI; it is reported as `lockfile_not_updated` and returned, so a result can carry it
as a partial failure, and the next sync repairs it. A Lockfile with a newer schema version is
left untouched, and entry keys skillm does not model are preserved.

### A refresh compares content before writing

`RefreshCopy` rewrites a copy unconditionally only for a git skill whose Revision just
advanced; otherwise it writes only when the content differs from its source. Comparing content
repairs an install reverted or hand-edited behind the Registry's back; comparing first keeps a
correct copy from producing git churn. A failed write leaves the install recorded, so the next
Update retries.

### The primitives assume the Home lock is held

None of them takes the Home lock: each writes under the calling command's lock, which is also
what makes the staged-copy sweep in `store.ReplaceDir` safe.

## Integration

`internal/core/vendor_test.go` and `internal/core/source_test.go` pin the events, codes and
returned failures; `internal/linker/linker_test.go` pins the `ErrNotManaged` wrap; the `cmd`
command tests (`cmd/install_test.go`, `cmd/update_test.go`, `cmd/import_test.go`,
`cmd/integration_test.go`) pin the printed lines.
