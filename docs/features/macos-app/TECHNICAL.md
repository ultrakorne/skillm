# macOS App — Technical

## Architecture

`macos/project.yml` is an XcodeGen spec (the generated `Skillm.xcodeproj` is not checked in;
run `xcodegen generate` in `macos/`, then build or test the `Skillm` scheme). It has three
targets: the `Skillm` app, the `SkillmKit` static framework it links, and `SkillmKitTests`. The
app's "Embed skillm CLI" build phase runs `macos/scripts/build-cli.sh` on every build, which
builds the Go CLI for each architecture Xcode builds, joins the slices with `lipo`, writes the
result to `Contents/Helpers/skillm` and signs it with the app's identity and the hardened runtime.

Layering is one-way: `SkillmClient` (process and decoding, in `SkillmKit`) → `AppModel` (the
`@Observable` state, the only caller of the client) → SwiftUI views, which never touch the client.
`SkillmClient.run`/`runEnvelope` decode one envelope; `stream` runs with `--events` and yields
each event, then the result. Every call launches a `ChildProcess` with the arguments as an array
(no shell), stdin on `/dev/null` and the environment from `ChildEnvironment`; the client's own
`--json`, `--events` and `--home` go ahead of any `--`, after which skillm reads only positionals.

## Where things live

| File | Role |
|------|------|
| `macos/project.yml` | Targets, macOS 14 deployment, `LSUIElement`, hardened runtime without sandbox, team `6LH2JMGD3J`, `SKILLM_VERSION` |
| `macos/scripts/build-cli.sh` | The build phase: universal, version-stamped, signed CLI into `Contents/Helpers` |
| `macos/Skillm/SkillmApp.swift` | The `MenuBarExtra` scene and the delegate that starts the model after launch |
| `macos/Skillm/AppModel.swift` | App state: locate the CLI, `connect`, check git; `CLIState` |
| `macos/Skillm/MenuContent.swift` | The menu: starting, the CLI problem and its fix, Quit |
| `macos/SkillmKit/SkillmClient.swift` | `connect`, `checkGit`, `run`, `runEnvelope`, `stream`, flag assembly, envelope decoding |
| `macos/SkillmKit/SkillmStream.swift` | The `AsyncSequence` `stream` returns, with its cancel semantics |
| `macos/SkillmKit/ChildProcess.swift` | One skillm process: live stdout, stderr tail, interrupt, exit wait; `Latch`, `LineSplitter` |
| `macos/SkillmKit/SkillmBinary.swift` | `SkillmBinary` (where the CLI is looked for), `ChildEnvironment` (PATH), `GitCheck` |
| `macos/SkillmKit/SkillmError.swift` | Every client failure, with the user-facing message and fix |
| `macos/SkillmKit/Protocol.swift` | Envelope, warning, error, event and stream-line types; the protocol coders; `ErrorCode` |
| `macos/SkillmKit/Models.swift` | Codable mirrors of every command's data and the Refresh cache |
| `macos/SkillmKitTests/FixtureTests.swift` | Decodes and re-encodes every golden fixture in `internal/protocol/testdata/` |
| `macos/SkillmKitTests/SkillmClientTests.swift` | The client against the fake: errors, PATH, arguments, cancel, SIGTERM, streams |
| `macos/SkillmKitTests/SetupTests.swift` | Binary lookup order, PATH extension, the git check |
| `macos/SkillmKitTests/BundledCLITests.swift` | The real bundled CLI (or `$SKILLM_TEST_CLI`) against a temporary Home; skipped when no app was built |
| `macos/TestSupport/fake-skillm` | A shell skillm that answers from the fixtures, with modes for bad output, hangs and signals |

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
reads what it left. `AsyncStream` iteration ends the moment its task is cancelled, so stdout is
read in a detached task and the exit is awaited through `Latch`, which ignores cancellation.
Breaking out of a stream early interrupts skillm without waiting.

### SIGTERM only after a long grace

skillm does not catch SIGTERM and dies mid-write (copies the Registry does not record), so
`interruptGrace` is 60 s: a guard against a hung child, never part of an ordinary cancel.

### Pipes are read with read(2) on their own threads

`FileHandle.read(upToCount:)` waits for the full count, which would hold an event stream back
until the command ends. Both pipes are drained to EOF even after a decode failure, so a full pipe
never blocks the child. stderr keeps its last 64 KiB, where a Go panic's cause is, and the exit
wait gives stderr's EOF at most 2 s, since a grandchild (git) may still hold the pipe.

### PATH and the git stub

`ChildEnvironment` puts `/opt/homebrew/bin` and `/usr/local/bin` ahead of PATH and appends
`/usr/bin`, so Homebrew's git wins over Apple's stub. skillm's own `git_missing` only checks
PATH, so `GitCheck` treats `/usr/bin/git` as missing when `xcode-select -p` fails; that probe
never shows Apple's install prompt.

### Where the CLI is looked for

`Contents/Helpers/skillm`, not `Contents/MacOS`, where the app's own executable is also named
`skillm`; any path under `Contents/` makes the CLI's Upgrade method bundled. A debug build tries
`$SKILLM_BIN` first, then the bundle, then `skillm` and `bin/skillm` at the repository it was
compiled from; a release build tries the bundle only.

### The bundled CLI's version comes from a build setting

`build-cli.sh` stamps `SKILLM_VERSION` as `.goreleaser.yaml` does. It defaults to `dev`, whose
Upgrade method is dev (no self-update check), so a Release build left at `dev` warns.
