# Core — Inspect and install

## Overview

`install` runs in two core calls. `core.Inspect` (`internal/core/inspect.go`) reads a Source
once, as a treeless clone pinned to one commit or a local directory, and returns an Inspection
listing its skills; it prompts for nothing and writes only to a temp dir. `core.InstallSkills`
(`internal/core/install.go`) installs a selection at an explicit Scope and Base, either from
that Inspection (source mode) or from registered Skill IDs (id mode). Between the two, `cmd`
runs the skill picker (`cmd/fetch.go`), the scope question and the overwrite question
(`cmd/install.go`). The split lets a GUI show what a Source holds before anything is installed:
`source inspect` (`cmd/source.go`) runs `core.Inspect` alone and prints or returns the Inspection.

## Noteworthy

### An install copies exactly the inspected commit

A git skill's Revision is read at the Inspection's commit before its content is materialized,
so the recorded Revision always matches what was copied. The recorded Ref stays the ref asked
for, or the default branch, so `update` keeps following it.

### `--commit` binds an install to another process's Inspection

`install --commit <sha>` sets `InstallRequest.Commit`, refusing a Source whose ref moved since
`source inspect` with a `*CommitMismatchError`; `cmd` checks it (`Inspection.CheckCommit`) right
after cloning, before the pickers, and `InstallSkills` again under the lock. The flag, not
`--ref <sha>`, is the way to pin: a SHA as the ref would be recorded, and `update` would never
see a newer commit. A value that is not 7 to 64 hex characters (`core.IsCommitSHA`) is a usage
error before any clone, and core reports it as a plain error rather than a mismatch, since no
inspection could ever match it and a GUI would re-inspect in a loop.

### The whole batch is checked before anything is written

An unknown id, a different-Source collision (`*SourceCollisionError`), an id override on
several skills (`ErrAsMultiple`), a commit mismatch and foreign entries at the canonical slots
(`*ForeignFilesError`) all stop the install first, so one bad skill never leaves half a batch.
`ValidateSourceSelection` and `ValidateIDSelection` run the same selection checks without
writing, so `cmd` reports them before asking where to install.

### Force, Yes and SkipForeign are three different answers

`Options.Force` overwrites foreign canonical slots and takes over foreign agent link paths.
`Options.Yes`, the terminal's "yes" to the overwrite question, overwrites the canonical slots
only, because the question never listed link paths. `InstallRequest.SkipForeign`, the "no",
installs the rest and reports each skipped skill as `install_blocked`; the CLI spells it
`--skip-foreign` and refuses it together with `--yes` or `--force`, which would overwrite what it
leaves alone. See [install-primitives.md](install-primitives.md) for the underlying
`force`/`forceLinks` split.

### Items are reported only once the batch is accepted

`InstallSkills` reports its `batch` (the final ids, after `--as`) only after every selection
check and the foreign-entry check pass, so a refused install streams no items. Each skill then
gets an `item_start` as its content is staged and one `item_done`: `installed`,
`install_blocked`, or `install_failed`, after which the install stops.

### `install` asks before it locks

The clone, the pickers and the scope question run without the Home lock, so a slow fetch or an
open prompt never blocks another skillm process. Each `InstallSkills` attempt takes the lock,
and it is released while the overwrite question is open. `InstallSkills` reloads Config and the
Registry and re-plans the selection under the lock, so a change made meanwhile is caught; the
retry after the question drops the `agent_skipped` notices already printed.

### Core never reads the process working directory

Every relative path is resolved against `Options.Cwd` through `core.ResolvePath`, and
`internal/core/arch_test.go` forbids `os.Getwd` and `filepath.Abs` in core. With no `Cwd`, a
relative Source (a local directory or a git repository path) is refused, and id mode refuses a
local skill whose recorded source is relative. A GUI runs with cwd `/`, where an implicit
lookup would silently resolve to the wrong place. `cmd` needs the working directory only for
`--local`, the scope question, a relative `--project` or a relative Source, so a run naming its
target absolutely (`--global`, or `--project` with an absolute path) works without one.
`--project` must name an existing directory, so a typo never starts a project somewhere new.

### A local Source is recorded absolute

A local Source is recorded as its absolute directory in the Registry and in `skills-lock.json`,
so `update` finds it from any directory. A legacy relative entry is resolved against the
caller's `Cwd` when identities are compared (`SrcIdentity.Base`): reinstalling from the project
it came from matches and rewrites it absolute, while from another project a same-named relative
path there can falsely match. `import` resolves a Lockfile's local source against that
Lockfile's root before comparing.

### Core texts name no CLI flag

Core returns typed errors and event codes; `installError` in `cmd/install.go` and `flagAdvice`
in `cmd/reporter.go` add the `--as`, `--force` and `--global` advice. A GUI maps the same codes
to its own wording.

### Path edge cases

Discovery follows a symlinked Source root, with or without a trailing separator, but not
symlinks below it. On Windows a path rooted at a separator with no drive resolves onto the
base's drive (`source.JoinPath` in `internal/source/source.go`), where `filepath.Abs` would put it.

## Integration

`internal/core/install_test.go` pins the typed errors, the Yes/Force split, the inspected and
expected commit (and a malformed one), relative-path resolution and the absolute local source in `skills-lock.json`;
`internal/core/resolve_windows_test.go` and `internal/source/joinpath_windows_test.go` the
Windows joins. The printed CLI behaviour is pinned by `cmd/install_test.go`,
`cmd/install_source_test.go`, `cmd/integration_test.go` and `cmd/import_test.go`;
`cmd/lock_test.go` checks that `install` still waits for a held Home lock before writing.
