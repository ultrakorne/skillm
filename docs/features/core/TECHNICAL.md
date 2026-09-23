# Core — Technical

## Architecture

Each command in `cmd/` is a cobra command that orchestrates the `internal/` packages directly.
A command that changes anything resolves Home, takes the Home lock, loads Config and the
Registry, fetches any content it needs into a temp dir outside Home, writes Canonical copies,
agent Links and Lockfile entries, saves the Registry (and Config, when `agent` toggles Enabled
flags or a first run seeds the defaults) and releases the lock on return. `list` and `check`
load the same files with no lock; `upgrade` never touches Home. Presentation, prompts and
progress live in `internal/ui` and are called from `cmd/` only.

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
| `internal/gitx/gitx.go` | Treeless git fetches and per-skill Revision lookup via the system `git` |
| `internal/lockfile/lockfile.go` | Reads and writes the vercel-compatible Lockfile |
| `cmd/lock_test.go` | Asserts every mutating command waits for a held Home lock |

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
