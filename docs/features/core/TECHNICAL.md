# Core — Technical

## Architecture

Each command in `cmd/` is a cobra command over core operations: `install` over `core.Inspect`
and `core.InstallSkills`, `source inspect` over `core.Inspect`, `update`, `import` and
`uninstall` over their namesakes, `agent` (and `agent ls`/`set`) over `core.Agents` and
`core.SetAgents`, and `config get`/`set` over `internal/config` ([settings.md](settings.md)).
Every question (pickers, scope, overwrite, confirmations) and every fetch runs with Home unlocked;
the write phase then takes the Home lock and re-reads Config and the Registry under it. `update`
and `import` take it through the `Options.Lock` hook after their fetches; the others take it in
`cmd` around the core call. Core writes through its install primitives (Canonical copies, agent
Links, Lockfile entries, Source identity; see [install-primitives.md](install-primitives.md)).
`upgrade` runs over `core.CheckSelf`/`core.UpgradeSelf`; in Home it touches only the Refresh cache.

`check` and `list` call `core.Check` and `core.List`, which read Home with no lock. Every core
operation takes a `core.Options` and reports through a `Reporter`'s `Event`: `cmd`'s
`termReporter` turns Events into the terminal checklist (`check`, and `update`'s fetches with a
progress bar) and the `ui` print helpers, and `termLog` prints log Events; with `--json` the
Reporter is the protocol writer ([json-api](../json-api/TECHNICAL.md)). Prompts and every other
presentation concern stay in `cmd`: `internal/core` imports neither `internal/ui` nor
cobra/bubbletea/huh/lipgloss, never touches the standard streams, and never reads the process
working directory (`os.Getwd` or `filepath.Abs`); it resolves relative paths against
`Options.Cwd`. `internal/core/arch_test.go` enforces all of it.

Home holds `config.toml` (Config), `state.toml` (Registry), `.lock` and, once a refresh ran,
`status.json` (the Refresh cache, [refresh-status](../refresh-status/TECHNICAL.md)). Each data file
is replaced whole on every save through one atomic-write primitive; the lock file holds nothing
but the current holder's description.

## Where things live

| File | Role |
|------|------|
| `cmd/root.go`, `cmd/lock.go` | Root command and global flags; taking the Home lock with its "waiting for …" notice |
| `cmd/install.go` | `install`: source or id mode, the scope question, the overwrite retry, flag advice for core's typed errors |
| `cmd/fetch.go`, `cmd/source.go` | The skill picker over an Inspection; `source inspect`'s listing |
| `cmd/reporter.go` | `coreOptions`, `termReporter` (core Events → checklist and prints), `termLog` with its flag advice |
| `cmd/update.go`, `cmd/import.go` | Build `core.Options` with the lock hook, render Events, print the summary line |
| `cmd/uninstall.go`, `cmd/agent.go` | The skill and agent pickers (and `agent ls`/`set`) and confirmations, then the core call under the Home lock |
| `cmd/check.go` | `check` over `core.Check`, with `checkReporter` restoring the CLI's "untracked" line |
| `cmd/upgrade.go` | `upgrade`'s confirmation and output over `core.SelfMethod`/`CheckSelf`/`UpgradeSelf` |
| `internal/store/store.go` | Home resolution (`--home`, `$SKILLM_HOME`, `~/.skillm`) and the directory-copy primitives |
| `internal/store/atomic.go` | Atomic file replace used by every Config and Registry save |
| `internal/store/lock.go` | The cross-process Home lock (`flock`/`LockFileEx` in its `_unix`/`_windows` files) |
| `internal/store/rename_windows.go` | Replacing rename that retries while a reader holds the file open |
| `internal/config/config.go`, `internal/state/state.go` | Load and save `config.toml` and `state.toml`; `internal/config/settings.go` holds the `[refresh]` settings |
| `internal/linker/linker.go` | Creates, removes and discovers skillm-owned agent Links |
| `internal/gitx/gitx.go` | Treeless git fetches and Revision lookup via the system `git`; a missing subtree is a `*gitx.NotFoundError` |
| `internal/lockfile/lockfile.go` | Reads and writes the vercel-compatible Lockfile |
| `internal/core/check.go`, `internal/core/list.go`, `internal/core/scan.go` | `core.Check` (per-skill upstream status) and `core.List` (installs read live from disk) |
| `internal/core/events.go`, `internal/core/errors.go`, `internal/core/options.go` | The `Reporter`/`Event` model, typed errors returned in place of prompts, and `Options` |
| `internal/core/pool.go` | `FanOut`, the bounded concurrent fan-out core work runs under |
| `internal/core/vendor.go` | Writes, refreshes and removes a Canonical copy and its agent Links; the log Event codes |
| `internal/core/locksync.go` | Upserts and removes a skill's `skills-lock.json` entry at a Local install root |
| `internal/core/inspect.go`, `internal/core/install.go` | `Inspect` (a Source read once, pinned to one commit) and `InstallSkills` with its selection checks |
| `internal/core/update.go`, `internal/core/import.go` | `Update` with its per-skill outcomes; `Import` and the all-skills adoption sweep |
| `internal/core/uninstall.go`, `internal/core/agents.go`, `internal/core/roots.go` | `Uninstall`; `Agents` and `SetAgents` with their link sweeps; pruning tracked roots and vanished installs |
| `internal/core/source.go` | Source identity (same-source refresh or collision), the entry to record, git re-fetch, `ResolvePath` |
| `internal/core/selfstatus.go`, `internal/selfupdate/` | The Upgrade method and `SelfStatus`; release lookup, checksum, swap and the app-bundle guard |
| `internal/ui/checklist.go` | `Checklist`: one row per label for work the caller runs, resolved via `Done`/`Wait` |

## Noteworthy

### The Home lock spans every read → decide → write, and its file is never deleted

Every changing command asks and fetches first, then holds the lock around a write phase that
re-reads Home, so a load → mutate → save cycle never interleaves with another skillm process and
an open prompt never blocks one. A waiter gives up after 30 seconds, naming the holder. Release
truncates `.lock` but leaves it in place: the lock is advisory on an open file, so recreating it
would let two processes each lock a different inode. The lock is per Home.

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

Each operation's results, refusals and cancellation rules have a page listed in [INDEX.md](INDEX.md).
