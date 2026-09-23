# Core — Technical

## Architecture

Each command in `cmd/` is a cobra command. `update`, `uninstall`, `import` and `agent` keep
their loops and prompts in `cmd` and write through core's install primitives (Canonical copies,
agent Links, Lockfile entries, Source identity; see [install-primitives.md](install-primitives.md)):
each resolves Home, takes the Home lock, loads Config and the Registry, fetches any content it
needs into a temp dir outside Home, writes copies, Links and Lockfile entries, saves the
Registry (and Config, when `agent` toggles Enabled flags or a first run seeds the defaults) and
releases the lock on return. `install` runs over `core.Inspect` and `core.InstallSkills`
instead: it inspects the Source and asks which skills and where with Home unlocked, then takes
the lock for each install attempt (see [inspect-and-install.md](inspect-and-install.md)).
`upgrade` never touches Home.

`check` and `list` call `core.Check` and `core.List`, which take a `core.Options`, load Config
and the Registry with no lock, and return a typed result. Core reports through a `Reporter`'s
`Event`: `cmd`'s `termReporter` turns Events into the terminal checklist and the `ui` print
helpers, and `termLog` prints the log Events of the install primitives. `update`'s per-skill
loop lives in `cmd/update.go` and drives its work through `runChecklist`, which fans out over
`core.FanOut` and renders through the same `ui.Checklist`. Prompts and every other presentation
concern stay in `cmd`: `internal/core` imports neither `internal/ui` nor cobra/bubbletea/huh/
lipgloss, never touches the standard streams, and never reads the process working directory
(`os.Getwd` or `filepath.Abs`); it resolves relative paths against `Options.Cwd`.
`internal/core/arch_test.go` enforces all of it.

Home holds three files: `config.toml` (Config), `state.toml` (Registry) and `.lock`. Both TOML
files are replaced whole on every save through one atomic-write primitive; the lock file holds
nothing but the current holder's description.

## Where things live

| File | Role |
|------|------|
| `cmd/root.go`, `cmd/lock.go` | Root command and global flags; taking the Home lock with its "waiting for …" notice |
| `cmd/install.go` | `install`: source or id mode, the scope question, the overwrite retry, flag advice for core's typed errors |
| `cmd/fetch.go` | The skill picker over an Inspection |
| `cmd/reporter.go` | `coreOptions`, `termReporter` (core Events → checklist and prints), `termLog` with its flag advice, `runChecklist` |
| `cmd/check.go` | `check` over `core.Check`, with `checkReporter` restoring the CLI's "untracked" line |
| `internal/store/store.go` | Home resolution (`--home`, `$SKILLM_HOME`, `~/.skillm`) and the directory-copy primitives |
| `internal/store/atomic.go` | Atomic file replace used by every Config and Registry save |
| `internal/store/lock.go` | The cross-process Home lock: wait, timeout, holder description |
| `internal/store/lock_unix.go`, `internal/store/lock_windows.go` | `flock` and `LockFileEx` behind the lock |
| `internal/store/rename_windows.go` | Replacing rename that retries while a reader holds the file open |
| `internal/config/config.go`, `internal/state/state.go` | Load and save `config.toml` and `state.toml` |
| `internal/linker/linker.go` | Creates, removes and discovers skillm-owned agent Links |
| `internal/gitx/gitx.go` | Treeless git fetches and Revision lookup via the system `git`; a missing subtree is a `*gitx.NotFoundError` |
| `internal/lockfile/lockfile.go` | Reads and writes the vercel-compatible Lockfile |
| `internal/core/check.go`, `internal/core/list.go`, `internal/core/scan.go` | `core.Check` (per-skill upstream status) and `core.List` (installs read live from disk) |
| `internal/core/events.go`, `internal/core/errors.go`, `internal/core/options.go` | The `Reporter`/`Event` model, typed errors returned in place of prompts, and `Options` |
| `internal/core/pool.go` | `FanOut`, the bounded concurrent fan-out core work runs under |
| `internal/core/vendor.go` | Writes, refreshes and removes a Canonical copy and its agent Links; the log Event codes |
| `internal/core/locksync.go` | Upserts and removes a skill's `skills-lock.json` entry at a Local install root |
| `internal/core/inspect.go`, `internal/core/install.go` | `Inspect` (a Source read once, pinned to one commit) and `InstallSkills` with its selection checks |
| `internal/core/source.go` | Source identity (same-source refresh or collision), the entry to record, git re-fetch, `ResolvePath` |
| `internal/ui/checklist.go` | `Checklist`: one row per label for work the caller runs, resolved via `Done`/`Wait` |

## Noteworthy

### The Home lock spans every read → decide → write, and its file is never deleted

`update`, `uninstall`, `import` and `agent` take the lock before loading anything and hold it
until they return, network fetches and prompts included, so a load → mutate → save cycle never
interleaves with another skillm process; `install` holds it only around each install attempt,
which reloads Home under it. A second process waits up to 30 seconds, then fails naming the holder.
Release truncates `.lock` but leaves it in place: the lock is advisory on an open file, so
recreating it would let two processes each lock a different inode. The lock is per Home.

### Reads take no lock because saves are atomic

A save writes a hidden temp file beside the target, fsyncs it and renames it over the target,
so a lock-free `list` or `check` sees the old file or the new one, never a partial one. The
save follows a symlinked `config.toml`/`state.toml`, keeps an existing file's mode, and gives a
new file `0644` filtered by the umask. On Windows the rename retries for up to half a second,
because a lock-free reader holding the file open blocks it.

### Canonical copies are staged, then swapped

A Canonical copy is staged in full in a hidden sibling (`.<id>.skillm-tmp-*`), then the old copy
is removed and the stage renamed into place, so a failed copy leaves the old one intact. Each
write first sweeps stages a killed run left for that skill, which is safe only because the
caller holds the Home lock.

### Sub-component rules live on topic pages

[check-and-list.md](check-and-list.md) holds the status, cancellation and install-listing rules
of `core.Check` and `core.List`; [install-primitives.md](install-primitives.md) the
refusal-versus-failure codes, the `force`/`forceLinks` split, best-effort Lockfile writes and
the recorded-copy invariant; [inspect-and-install.md](inspect-and-install.md) the pinned commit,
the batch checks, the lock hand-off and path resolution against `Options.Cwd`.
