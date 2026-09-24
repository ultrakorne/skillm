# macOS App — The Client

## Overview

`SkillmClient` in `SkillmKit` is how the app runs skillm: one `skillm --json` process per call,
decoded into the Swift mirrors of the protocol. It is documented apart because its cancel and
pipe handling carry invariants that matter only when the client itself changes.

`run`/`runEnvelope` decode one envelope; `stream` runs with `--events` and yields each event,
then the result. Every call launches a `ChildProcess` with the arguments as an array (no shell),
stdin on `/dev/null` and the environment from `ChildEnvironment`; the client's own `--json`,
`--events` and `--home` go ahead of any `--`, after which skillm reads only positionals.

| File | Role |
|------|------|
| `macos/SkillmKit/SkillmClient.swift` | `connect`, `checkGit`, `run`, `runEnvelope`, `stream`, flag assembly, envelope decoding |
| `macos/SkillmKit/SkillmStream.swift` | The `AsyncSequence` `stream` returns, with its cancel semantics |
| `macos/SkillmKit/ChildProcess.swift` | One skillm process: live stdout, stderr tail, interrupt, exit wait; `Latch`, `LineSplitter` |
| `macos/SkillmKit/SkillmBinary.swift` | `SkillmBinary` and `LoginShell` (where the CLI is looked for), `CLIFileIdentity`, `ChildEnvironment` (PATH), `GitCheck` |
| `macos/SkillmKit/SkillmError.swift` | Every client failure, with the user-facing message and fix |
| `macos/SkillmKit/Protocol.swift` | Envelope, warning, error, event and stream-line types; the protocol coders; `ErrorCode` |
| `macos/SkillmKit/Models.swift` | Codable mirrors of every command's data and the Refresh cache |
| `macos/SkillmKitTests/FixtureTests.swift` | Decodes and re-encodes every golden fixture in `internal/protocol/testdata/` |
| `macos/SkillmKitTests/SkillmClientTests.swift` | The client against the fake: errors, PATH, arguments, cancel, SIGTERM, streams |
| `macos/SkillmKitTests/SetupTests.swift` | Binary lookup order, the login shell against fake shells, PATH extension, the git check |

## Noteworthy

### Versions and codes mirrored from Go change together

`SkillmClient.supportedAPIVersions` must list `APIVersion` from `internal/protocol/protocol.go`,
and `protocolSchemaVersion` must equal its `SchemaVersion`; `ErrorCode` mirrors the constants in
`internal/protocol/errors.go`. A new fixture file directly in `internal/protocol/testdata/` needs
its data type in `FixtureTests.checks`, which refuses an unlisted file. Code, status and event
values are open (`ProtocolValue`), so an unknown value decodes instead of failing.

### A call returns only once skillm has exited

Cancelling the caller sends SIGINT, and `run` throws `CancellationError`, `runEnvelope` returns
the `cancelled` envelope with its warnings, and `stream` keeps delivering, then throws; each only
after the exit and stderr's EOF, so skillm no longer holds the Home lock and a follow-up `status`
reads what it left ([FLOW.mermaid](FLOW.mermaid)). `AsyncStream` iteration ends the moment its
task is cancelled, so stdout is read in a detached task and the exit is awaited through `Latch`,
which ignores cancellation. Breaking out of a stream early interrupts skillm without waiting.

### SIGTERM only after a long grace

skillm does not catch SIGTERM and dies mid-write (copies the Registry does not record), so
`interruptGrace` is 60 s: a guard against a hung child, never part of an ordinary cancel.

### Pipes are read with read(2) on their own threads

`FileHandle.read(upToCount:)` waits for the full count, which would hold an event stream back
until the command ends. Both pipes are drained to EOF even after a decode failure, so a full pipe
never blocks the child. stderr keeps its last 64 KiB, where a Go panic's cause is, and the exit
wait gives stderr's EOF at most 2 s, since a grandchild (git) may still hold the pipe.

### Where the CLI is looked for

The app carries no CLI. It looks at `$SKILLM_BIN` (debug builds only), then where `install.sh`
and Homebrew put skillm (`/usr/local/bin`, `~/.local/bin`, `/opt/homebrew/bin`), then asks the
user's login shell (`$SHELL -l -i -c 'command -v skillm'`), since a GUI app's PATH lacks what the
startup files add; the last absolute path the shell prints wins. Only the shell's exit races the
5 s timeout (then SIGKILL: an interactive shell ignores SIGINT and SIGTERM); its stdout, which a
startup file may close early or leave open in a background process, never ends the wait.

### Connecting refuses both directions

`connect` (`version --json`) maps a skillm from before the JSON API (a usage error, no output)
to API version 0, too old, and a document with a newer `schema_version` to too new, as it does
an `api_version` outside `supportedAPIVersions`; the model turns each into the menu's fix.

### PATH and the git stub

`ChildEnvironment` puts `/opt/homebrew/bin` and `/usr/local/bin` ahead of PATH and appends
`/usr/bin`, so Homebrew's git wins over Apple's stub. skillm's own `git_missing` only checks
PATH, so `GitCheck` treats `/usr/bin/git` as missing when `xcode-select -p` fails; that probe
never shows Apple's install prompt.

## Integration

`AppModel` is the client's only caller ([TECHNICAL.md](TECHNICAL.md)); views never touch it. The
tests run it against `macos/TestSupport/fake-skillm`, whose `FAKE_SKILLM_*` variables pick a mode.
