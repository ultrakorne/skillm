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
Writer's `Result`. A command's own flag checks return `usageError` (`cmd/json.go`), a
`*protocol.Error` with code `usage`, so the same refusal reads as a plain message in the
terminal. Errors take the other path: `cmd.Execute` installs a fang error handler that,
in JSON mode, writes the error envelope through the same Writer's `Fail` instead of fang's
styled stderr message; `main.go` exits non-zero either way.

## Where things live

| File | Role |
|------|------|
| `internal/protocol/protocol.go` | `SchemaVersion`, `APIVersion`, the `Envelope`, `Warning` and `Error` types |
| `internal/protocol/writer.go` | `EventLine` and the `Writer`: the `core.Reporter` that collects warnings, streams events and writes the final document |
| `internal/protocol/errors.go` | The error code constants and `ErrorFrom`, mapping every typed core and store error to its code and fields |
| `internal/protocol/data.go` | `VersionData`, `ListData`, `CheckData` and their conversions from core's results; `Time` |
| `internal/protocol/data_mutating.go` | The data of `install`, `update`, `import`, `uninstall` and `upgrade`, converted from core's typed results |
| `internal/protocol/data_settings.go` | The data of `source inspect`, `agent ls`/`set` and `config get`/`set` |
| `internal/protocol/data_status.go` | The data of `status` and `refresh`: the Refresh cache's fields plus `stale` and `refreshed` |
| `internal/protocol/testdata/` | Golden fixtures: the cross-language contract a GUI's tests decode |
| `internal/protocol/golden_test.go` | Writes each fixture through the `Writer` and decodes every fixture strictly back into the Go types |
| `cmd/json.go` | The flags, the annotation, JSON-mode detection, `Execute` with its error handler, quiet git, `capabilities` |
| `cmd/version.go` | `skillm version` (plain line, or `VersionData` with `--json`) |
| `cmd/list.go`, `cmd/check.go` | Their `--json` branches over `core.List` / `core.Check` |
| `cmd/install.go`, `cmd/update.go`, `cmd/import.go`, `cmd/uninstall.go`, `cmd/upgrade.go`, `cmd/agent.go` | Each command's JSON branch: the flags it requires in place of a question, then the core call with the Writer |
| `cmd/source.go`, `cmd/config.go` | `source inspect` over `core.Inspect`; `config get`/`set` over `internal/config` |
| `cmd/refresh.go` | `refresh` and `status` over `core.Refresh`/`core.ReadStatus` ([refresh-status](../refresh-status/TECHNICAL.md)) |
| `cmd/lock.go` | `lockHome`: the waiting notice as a `lock_wait` info event in JSON mode |
| `cmd/json_test.go`, `cmd/json_mutating_test.go`, `cmd/json_settings_test.go`, `cmd/json_status_test.go` | Run the built binary with `--json`, as a GUI does, and decode stdout; stderr must stay empty |

## Noteworthy

### The fixtures are the contract, shared with the GUI

A change to a protocol type changes a fixture, and every GUI decodes the same files, so a
field is added or changed in the Go type, the fixture (regenerate with `SKILLM_UPDATE_GOLDEN=1`)
and each GUI's decoder together. `TestFixturesDecode` refuses unknown fields, so a fixture can
never drift from the Go structs. Error codes are never renamed or reused. `TestFixturesDecode`
decodes every file directly in the fixture folder as an envelope or event stream, so other formats
sit in a subdirectory: `internal/protocol/testdata/cache/status.json` is the Refresh cache file.

### Capabilities are derived, their fixture is not

`capabilities` is built from the annotated commands' paths at run time, but `version.json` is
written from a hand-kept list in `internal/protocol/golden_test.go`: a new JSON command goes into
that list too, or the fixture a GUI tests against under-reports what the CLI offers.

### JSON mode is known before the command line parses

An error cobra hits before it has parsed `--json` (an unknown flag in front of it) must still be
an envelope, so until `PersistentPreRunE` marks the flags parsed, `jsonMode` scans the raw
arguments the way pflag would (the last `--json`/`--json=<bool>` before `--` wins). After a
successful parse only the parsed value counts.

### cobra's own answers are refused up front

`--help`/`-h`, `--version`, a bare `skillm --json` and a group command that is not runnable
(`source`, `config`, `completion`) are answered by cobra with help text and exit 0 before any
hook runs. `Execute` refuses them with `json_unsupported` before fang starts; it first adds
cobra's `help` and `completion` commands, which cobra otherwise adds only inside its own
`Execute`, so `completion --json` resolves to the group it is. Usage errors are recognised by
cobra's message prefixes (`isUsageError`), since cobra has no typed usage errors; the flag-group
messages are among them, and a cobra upgrade that rewords any of them turns `usage` into `error`.

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
`--events` but is never a warning. `index`, `done` and `total` are emitted exactly when the
event type carries them, so item 0 and a zero count appear.

### Git never prompts in JSON mode

`quietGit` sets `GIT_TERMINAL_PROMPT=0` and, unless the user set `GIT_SSH_COMMAND` or
`GIT_SSH`, runs ssh with `BatchMode=yes`, in this process's environment so every git child
inherits it. A remote that now wants credentials fails fast as a check `error` row.

### No prompt is reachable in JSON mode

Every question a changing command asks in the terminal is either replaced by a flag JSON mode
requires (scope, ids, `--yes`) or turned into the typed refusal core already returns
(`foreign_files`, `needs_confirm`); `cmd` checks `flagJSON` before each picker and confirmation.
`installError` returns core's error untouched in JSON mode, since its flag advice is terminal
wording and the code alone tells a GUI what to offer.

### A failed update reports twice, and the terminal drops one

Core reports a failed fetch both as its `item_done` row and, when the writes start, as an
`update_failed` error log event, because the envelope of a failed update carries no data and
its warnings are the only per-skill record a GUI without `--events` gets. The terminal wraps its
reporter in `dropCodes` (`cmd/reporter.go`) to drop that log line, since the row already shows
it.

### Uninstall retries are idempotent by design

In JSON mode `cmd` passes the ids through unchecked and sets `UninstallRequest.SkipMissing`, so
an id no longer installed becomes a `not_installed` warning instead of refusing the batch.
Without it, retrying a `needs_force`, `uninstall_failed` or `cancelled` run with the same ids
would fail on the skills the first attempt already removed.
