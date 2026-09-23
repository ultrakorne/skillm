# Core — Technical

## Architecture

Each command in `cmd/` is a cobra command. `install`, `update`, `uninstall`, `import` and
`agent` still orchestrate the `internal/` packages directly: a command that changes anything
resolves Home, takes the Home lock, loads Config and the Registry, fetches any content it needs
into a temp dir outside Home, writes Canonical copies, agent Links and Lockfile entries, saves
the Registry (and Config, when `agent` toggles Enabled flags or a first run seeds the defaults)
and releases the lock on return. `upgrade` never touches Home.

`check` and `list` instead call the presentation-free `internal/core` package. `core.Check` and
`core.List` take a `core.Options{Home, Cwd, Force, Yes}` (built by `coreOptions` in
`cmd/reporter.go`), load Config and the Registry with no lock, and return a typed result; core
reports progress by calling a `Reporter`'s `Event` (`internal/core/events.go`), and `cmd`'s
`termReporter` turns those Events into the terminal checklist (`internal/ui/checklist.go`) and
the `ui` print helpers. `update`'s per-skill loop still lives in `cmd/update.go`, but now drives
its work through `runChecklist` (`cmd/reporter.go`), which fans work out over `core.FanOut` and
renders it through the same `ui.Checklist`. Prompts and every other presentation concern stay in
`cmd`; `internal/core` calls neither `internal/ui` nor cobra/bubbletea/huh/lipgloss, and never
reads `os.Getwd`/`os.Stdout`/`os.Stderr`/`os.Stdin` — `internal/core/arch_test.go` enforces both
by inspecting `go list -deps` and core's own source.

Home holds three files: `config.toml` (Config), `state.toml` (Registry) and `.lock`. Both TOML
files are replaced whole on every save through one atomic-write primitive; the lock file holds
nothing but the current holder's description.

## Where things live

| File | Role |
|------|------|
| `cmd/root.go` | Root command and the global flags (`--home`, `--force`, `--yes`) |
| `cmd/lock.go` | Takes the Home lock for a command and prints the "waiting for …" notice |
| `cmd/fetch.go` | Shared fetch → discover → select → stage pipeline for a Source |
| `cmd/localinstall.go` | Canonical-copy and Link orchestration shared by `install`, `update`, `uninstall`, `list` and `agent` |
| `cmd/locksync.go` | Best-effort `skills-lock.json` sync for every Local install root |
| `internal/store/store.go` | Home resolution (`--home`, `$SKILLM_HOME`, `~/.skillm`) and the directory-copy primitives |
| `internal/store/atomic.go` | Atomic file replace used by every Config and Registry save |
| `internal/store/lock.go` | The cross-process Home lock: wait, timeout, holder description |
| `internal/store/lock_unix.go`, `internal/store/lock_windows.go` | `flock` and `LockFileEx` behind the lock |
| `internal/store/rename_windows.go` | Replacing rename that retries while a reader holds the file open |
| `internal/config/config.go` | Load and save `config.toml` (Agent definitions, Enabled flags) |
| `internal/state/state.go` | Load and save `state.toml` (the Registry) |
| `internal/linker/linker.go` | Creates, removes and discovers skillm-owned agent Links |
| `internal/gitx/gitx.go` | Treeless git fetches and per-skill Revision lookup via the system `git`; a missing subtree returns a typed `*gitx.NotFoundError` |
| `internal/lockfile/lockfile.go` | Reads and writes the vercel-compatible Lockfile |
| `cmd/lock_test.go` | Asserts every mutating command waits for a held Home lock |
| `internal/core/check.go`, `internal/core/list.go`, `internal/core/scan.go` | `core.Check` (per-skill upstream status) and `core.List` (installs read live from disk), the operations behind `check`/`list` |
| `internal/core/events.go` | The `Reporter`/`Event` model every core operation reports progress through |
| `internal/core/errors.go` | Typed errors (`ForeignFilesError`, `ErrNeedsConfirm`) core returns in place of prompting |
| `internal/core/options.go` | `Options{Home, Cwd, Force, Yes}`, the parameters every core operation takes instead of a cwd read or a cmd flag global |
| `internal/core/pool.go` | `FanOut`, the bounded concurrent fan-out core work runs under (moved from `internal/ui`) |
| `internal/core/arch_test.go` | Fails the build if `internal/core` imports a presentation/CLI package or reads a process global |
| `cmd/reporter.go` | `coreOptions`, `termReporter` (core Events → the checklist and `ui` prints) and `runChecklist` (`update`'s fan-out bridge) |
| `internal/ui/checklist.go` | `Checklist`: renders one row per label for work the caller runs, resolved via `Done`/`Wait` |
| `cmd/golden_test.go`, `cmd/testdata/golden/*.txt` | Pins `check`/`list` plain-mode output byte-for-byte across every status kind |

## Noteworthy

### The Home lock spans the whole command

`install`, `update`, `uninstall`, `import` and `agent` take the lock before loading anything
and hold it until they return, so a load → mutate → save cycle never interleaves with another
skillm process. That includes network fetches and interactive prompts: a second process waits
up to 30 seconds, then fails naming the holder (pid and command). Ctrl-C ends the wait at once.

### The lock file is never deleted

Release truncates `.lock` and unlocks it but leaves the file in place. The lock is advisory on
an open file, so deleting and recreating it would let two processes each lock a different
inode. The lock is also per Home: runs against two different Homes do not exclude each other.

### Reads take no lock because saves are atomic

A save writes a hidden temp file beside the target, fsyncs it and renames it over the target,
so a lock-free `list` or `check` sees the old file or the new one, never a partial one. The
save follows a symlinked `config.toml`/`state.toml` and replaces the link's target, keeps an
existing file's mode, and gives a new file `0644` filtered by the umask. On Windows the rename
retries for up to half a second, because a lock-free reader holding the file open blocks it.

### Canonical copies are staged, then swapped

Writing a Canonical copy stages the full copy in a uniquely named hidden sibling
(`.<id>.skillm-tmp-*`), removes the old copy and renames the stage into place. A failed copy
leaves the old one intact, and a brief window without the directory remains between remove
and rename. Each write first sweeps stages a killed run left for that skill; this is safe only
because the caller holds the Home lock, so no live run owns them.

### `check`'s "untracked" and "error" are distinct in `core`, identical on screen

`core.Check` gives an upstream lookup that fails outright its own `error` status, kept apart
from `untracked` (a `*gitx.NotFoundError`: the repo was read but the skill's subdir is gone).
`cmd` still renders both as "untracked" so `check`'s output stays byte-identical to before the
split — only a caller reading the structured `Status` sees the two apart.

### A checklist's `OnAbort` fires on any abnormal end, not just a user quit

Because core, not the checklist, now owns the work, `ChecklistOptions.OnAbort` must cancel it
when the live view ends any way other than every row resolving — a user quit, a renderer error,
or a final model of the wrong type — or the caller's workers would run to completion unobserved
while the checklist silently dropped further `Done` calls.
