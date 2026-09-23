# JSON API — Technical

## Architecture

`internal/protocol` is the JSON protocol: the envelope and event-line types, the `Writer`, the
error-to-code mapping, and one data type per command converted from core's typed results. It
sits between `cmd` and `internal/core`: it imports core, never `cmd`, the terminal UI or cobra,
and `internal/protocol/arch_test.go` enforces that.

`--json` and `--events` are global flags on the root command. A command opts into JSON mode with
the `skillm:json` annotation; the root's `PersistentPreRunE` refuses `--json` on any other
command and `--events` without `--json`, and in JSON mode makes git non-interactive before the
git check runs. A JSON-mode command builds its core call as usual but passes the run's single
`protocol.Writer` (`jsonOut()` in `cmd/json.go`) as its `core.Reporter`, then ends with the
Writer's `Result`. Errors take the other path: `cmd.Execute` installs a fang error handler that,
in JSON mode, writes the error envelope through the same Writer's `Fail` instead of fang's
styled stderr message; `main.go` exits non-zero either way.

## Where things live

| File | Role |
|------|------|
| `internal/protocol/protocol.go` | `SchemaVersion`, `APIVersion`, the `Envelope`, `Warning` and `Error` types |
| `internal/protocol/writer.go` | `EventLine` and the `Writer`: the `core.Reporter` that collects warnings, streams events and writes the final document |
| `internal/protocol/errors.go` | The error code constants and `ErrorFrom`, mapping every typed core and store error to its code and fields |
| `internal/protocol/data.go` | `VersionData`, `ListData`, `CheckData` and their conversions from core's results; `Time` |
| `internal/protocol/testdata/` | Golden fixtures: the cross-language contract a GUI's tests decode |
| `internal/protocol/golden_test.go` | Writes each fixture through the `Writer` and decodes every fixture strictly back into the Go types |
| `cmd/json.go` | The flags, the annotation, JSON-mode detection, `Execute` with its error handler, quiet git, `capabilities` |
| `cmd/version.go` | `skillm version` (plain line, or `VersionData` with `--json`) |
| `cmd/list.go`, `cmd/check.go` | Their `--json` branches over `core.List` / `core.Check` |
| `cmd/lock.go` | `lockHome`: the waiting notice as a `lock_wait` info event in JSON mode |
| `cmd/json_test.go` | Runs the built binary with `--json`, as a GUI does, and decodes stdout; stderr must stay empty |

## Noteworthy

### The fixtures are the contract, shared with the GUI

A change to a protocol type changes a fixture, and every GUI decodes the same files, so a
field is added or changed in the Go type, the fixture (regenerate with `SKILLM_UPDATE_GOLDEN=1`)
and each GUI's decoder together. `TestFixturesDecode` refuses unknown fields, so a fixture can
never drift from the Go structs. Error codes are never renamed or reused.

### JSON mode is known before the command line parses

An error cobra hits before it has parsed `--json` (an unknown flag in front of it) must still be
an envelope, so until `PersistentPreRunE` marks the flags parsed, `jsonMode` scans the raw
arguments the way pflag would (the last `--json`/`--json=<bool>` before `--` wins). After a
successful parse only the parsed value counts.

### cobra's own answers are refused up front

`--help`/`-h`, `--version` and a bare `skillm --json` are answered by cobra before any hook runs,
with terminal text and exit 0. `Execute` refuses them with `json_unsupported` before fang
starts. Usage errors are recognised by cobra's message prefixes (`isUsageError`), because cobra
has no typed usage errors; a cobra upgrade that rewords them turns `usage` into `error`.

### One final document, whichever path ends the run

The `Writer`'s first `Result` or `Fail` wins and every later call or event is dropped, so a
command that fails after writing its result (a closed stdout pipe) never writes a second
envelope. A `*protocol.Error` returned from `cmd` passes through `ErrorFrom` untouched, which is
how `cmd` sets an exact code such as `git_missing` or `usage`.

### Warnings are log events only; event text is core's own

Only `warn`/`error` log events become envelope warnings; a check's `update_available` row is an
`item_done` event, not a warning. JSON output carries core's Event text unchanged, so a check
`error` row names its real cause where the terminal shows the historic "untracked" line
([check-and-list.md](../core/check-and-list.md)). `lock_wait` is `info`, so it streams with
`--events` but is never a warning.

### Git never prompts in JSON mode

`quietGit` sets `GIT_TERMINAL_PROMPT=0` and, unless the user set `GIT_SSH_COMMAND` or
`GIT_SSH`, runs ssh with `BatchMode=yes`, in this process's environment so every git child
inherits it. A remote that now wants credentials fails fast as a check `error` row.

### Stream field presence

`index`, `done` and `total` are emitted exactly when the event type carries them (so item 0 and
a zero count appear), and `data` is `null` on error while `warnings` is always a list.
