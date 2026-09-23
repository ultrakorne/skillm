# Plan — macOS menu bar app (SwiftUI over `skillm --json`)

Status: **in progress** · Branch: `macos-toolbar` · Delete this file (or fold what survives
into `docs/features/`) once the last step lands.

## Goal

A native macOS menu bar app for skillm, and later a Linux Quickshell widget (Omarchy), that
share **all** behaviour. The Go CLI is the single core; every GUI is a thin view that runs
`skillm … --json` and renders the result. A feature is written once in Go, exposed once in
the JSON protocol, and each GUI only adds a control for it.

### The app

Status item (top-right) with a red dot when anything has an update. Menu:

- **Refresh**: check now
- **Auto refresh** ✓: toggle the daily check
- **Update all skills**: shows progress, then clears the dot
- **Upgrade and restart**: shown only when skillm itself has an update
- **View skills…**: window listing installed skills and where each is installed (like `skillm list`)
- **Add skill…**: window: repo URL → pick skills → pick scope (global or a project folder) → install
- **Settings…**: auto refresh on/off and interval, **Start at login** checkbox, enabled agents
- **Quit**

## Decision (why this shape)

We evaluated four options with Codex (gpt-6-astra) and Fable 5.1, and both independently chose
the same one:

| Option | Verdict |
|---|---|
| **SwiftUI `MenuBarExtra` + `skillm --json` subprocess** | **Chosen.** Keeps `CGO_ENABLED=0` and the release pipeline. The binary is already the tested surface. Quickshell's `Process` + `JSON.parse` reuses the exact contract. |
| Go daemon (`skillm serve`) over a Unix socket | Rejected for now: its lifecycle, socket security and version skew are too much cost for one check a day. Add it later only if live subscriptions are ever needed. |
| Go as a c-archive linked into Swift | Rejected: needs cgo and a macOS CI runner, and gives Quickshell nothing. `selfupdate` would also try to replace the host app. |
| Pure-Go GUI (Wails, Fyne, systray, DarwinKit) | Rejected: less native (or cgo + Objective-C ownership), and no path to share UI with QML. |

Consequences:
1. `cmd/` must become a thin terminal front-end over a presentation-free Go core. Today the
   orchestration lives in `cmd/` and is tied to the terminal: `ui.*` printing is the only
   event channel, prompts sit inside pipelines, the `flagForce`/`flagYes`/`flagHome` globals
   are read deep in the logic, and commands assume `os.Getwd()`.
2. The CLI and the GUI can mutate Home at the same time, so persistence needs a lock and
   atomic writes.
3. The `.app` bundles a version-matched `skillm` binary. Upgrades go through Sparkle, which
   replaces the whole bundle. `skillm upgrade` must refuse to swap a binary inside a `.app`,
   because that breaks the code signature.

## How to run each step (context-reset protocol)

Every step is sized for **one fresh session**. Each session:

1. **Start fresh.** Prompt: *"Implement step `<ID>` of `docs/plans/macos-menubar.md`."* Read
   this plan, `docs/INDEX.md`, `docs/CONTEXT.md`, and the **Step log** at the bottom for
   notes from earlier steps. Only read the code that the step's scope names.
2. **Record the base.** Run `git rev-parse HEAD` and put the SHA in the step log row before
   changing anything.
3. **Implement only the step's scope.** If something outside the scope needs changing, note
   it in the step log for a later step instead of doing it now.
4. **Check the step's gate.** Every Go step must pass:
   `gofmt -l .` (prints nothing), `go vet ./...`, `go build ./...`, and
   `go test ./...`, plus the step's own acceptance list. Refactor steps (A*) are
   behaviour-preserving: `cmd/integration_test.go` passes **unchanged**. In-process tests
   that move along with the code are the only tests allowed to change.
5. **Commit** as `<ID>: <summary>`. One or more commits per step is fine.
6. **Multi-review.** Run `/multi-review review <base-sha>..HEAD — step <ID> of
   docs/plans/macos-menubar.md; check it against that step's scope and acceptance`. Apply
   the confirmed fixes, rerun the gate, and commit as `<ID>: review fixes`.
7. **Docs.** Use the `project-documentation` skill to bring `docs/` in line with what the
   step changed. For A* steps this is mostly the glossary plus
   `docs/features/core/TECHNICAL.md`; the protocol docs start at B1.
8. **Hand off.** Fill in the step log row: status, final SHA, deviations from the plan, and
   notes for the next step. End the session.

Rules that hold across steps:

- `internal/core` never imports `internal/ui`, cobra, huh or bubbletea, and never reads
  `os.Getwd()`, `os.Stdout` or the cmd flag globals. From A2 on, a test enforces this.
- Prompts live only in `cmd/`. Core returns typed errors, and cmd decides whether to prompt.
- In JSON mode (B1+), stdout carries only protocol output. skillm never prompts and never
  draws TTY UI in that mode.

---

## Phase A — Go core extraction (no behaviour change)

Target shape:

```
internal/core/            presentation-free operations
  options.go              Options{Home, Cwd string; Force, Yes bool}
  events.go               Reporter interface + Event types + NopReporter
  errors.go               typed errors (ErrForeignFiles{Paths}, ErrNeedsConfirm, …)
  pool.go                 bounded fan-out (moved from ui.fanOut)
  check.go list.go install.go inspect.go update.go import.go uninstall.go agents.go
  vendor.go locksync.go   (moved from cmd/localinstall.go, cmd/locksync.go)
cmd/                      cobra wiring, flag parsing, prompts, a terminal Reporter, rendering
```

Event model (fix this in A2 and extend it later):

```go
type Reporter interface{ Event(Event) }
type Event struct {
    Type   EventType // Log | ItemStart | ItemDone | Progress
    Level  Level     // Info | Success | Warn | Error
    Index  int       // item index for ItemStart/ItemDone
    Skill  string    // skill id, when the event concerns one
    Code   string    // stable machine code, e.g. "update_available", "link_skipped"
    Text   string    // human sentence (what the CLI prints today)
    Done, Total int  // Progress
}
```

Core runs the concurrency and emits `ItemStart`/`ItemDone`. The terminal Reporter in `cmd/`
drives the existing bubbletea checklist from those events. `ui.RunChecks` and
`ui.RunChecklistProgress` therefore become event-driven renderers (`ui.NewChecklist(labels)`
returns a sink plus a `Wait()`), and they stop owning the work.

### A1 — Safe persistence and a cross-process lock
Scope: `internal/state`, `internal/config`, `internal/store`, the mutating commands in `cmd/`.
- `state.Save` and `config.Save` write to a temp file in the same dir, `fsync`, then `rename`.
- `store.ReplaceDir` uses a unique staging name (`os.MkdirTemp` next to dst) instead of the
  fixed `dst + ".skillm-tmp"`.
- New `store.Lock(home) (unlock func(), err error)`. It is an exclusive advisory lock on
  `<home>/.lock`: `flock` on unix and `LockFileEx` on windows (build tags;
  `golang.org/x/sys` is already a dependency). It blocks with a timeout, and the error names
  the holding operation if that's easy.
- Take the lock in every command that saves: `install`, `update`, `uninstall`, `import`,
  `agent`. The lock covers load → mutate → save. Read-only commands (`list`, `check`) take
  no lock.
Acceptance: unit tests for the atomic save (no partial file after a simulated failure) and
for lock contention (the second locker waits, then gets the lock or times out); the Windows
build compiles (`GOOS=windows go build ./...`).

### A2 — Core skeleton + read-only commands (`check`, `list`)
Scope: new `internal/core` (options, events, errors, pool), `internal/ui/checklist*.go`,
`cmd/check.go`, `cmd/list.go`.
- Move `fanOut` into `core/pool.go`. Make the ui checklist renderers event-driven, as
  described above.
- `core.Check(ctx, opts, rep) (CheckResult, error)` returns per-skill
  `{ID, Kind, Status: up_to_date|update_available|untracked|local|error, InstalledRev,
  UpstreamRev, Err}`. Stop collapsing lookup errors into "untracked" (`upstreamStatus`):
  return `error` with a message and let cmd keep today's wording.
- `core.List(opts) (ListResult, error)` returns structured installs per skill
  `{Scope: global|local, Root, Path, Agents[], Exists}` in place of the
  `linkedLabel` string. cmd rebuilds the same label from the struct.
- A cmd-side `termReporter` maps events to `ui.Successf/Warnf/...` and to the checklist.
- An architecture test (`internal/core/arch_test.go`) runs `go list -deps` and fails if
  core imports ui, cobra, huh, bubbletea or lipgloss.
Acceptance: `skillm check` and `skillm list` output are byte-identical on and off a TTY
(existing tests plus a golden test for the plain output).

### A3 — Move the install/vendor primitives
Scope: `cmd/localinstall.go`, `cmd/locksync.go`, and the non-interactive helpers in
`cmd/fetch.go` (`canonicalRemote`, `srcIdentity`, `registryCollision`, `mergeEntry`,
`chosenID`, `checkAsSingle`, `repoRelSubpath`, `refetchSkill`, `normalizeRemote` from
`import.go`). This is a pure move into core.
- Take `force`/`forceLinks` as parameters. Replace `ui.Warnf` with `rep.Event`.
  `upsertLockEntry`/`removeLockEntry` and `refreshCopy` currently only print failures; they
  now also return them, so results can carry partial failures.
- Leave call sites in cmd calling the core versions. Move the corresponding
  `cmd/localinstall_test.go` / `fetch_test.go` cases with the code.
Acceptance: the gate passes and no behaviour changes.

### A4 — Install: split inspect from install
Scope: `cmd/fetch.go` (`fetchToStage`, `fetchGitToStage`, `fetchLocalToStage`,
`selectFound`), `cmd/install.go`.
- `core.Inspect(ctx, src, ref) (*Inspection, error)`: clone or read, pin the commit, discover
  skills. `Inspection{Source, Kind, Ref, Commit, Skills[{ID, Name, Description, Path}]}` plus
  `Close()`. It prompts nothing.
- `core.Install(ctx, opts, rep, InstallRequest{Inspection | RegisteredIDs, IDs, As, Scope,
  Base}) (InstallResult, error)`. This covers both source mode and id mode
  (`resolveIDItems`, `idModeSource`). Installs use the inspected commit, not a re-resolved
  ref.
- Foreign files at the canonical slot → `*ErrForeignFiles{Paths}` before anything is written
  (atomic batch, as today). cmd catches it, runs `ui.Confirm` on a TTY, and retries with
  `Force`, which reproduces today's prompt exactly.
- `resolveInstallTarget` and `ui.SelectScope` stay in cmd. Core takes an explicit
  `Scope` + `Base` (the project dir), never a cwd.
Acceptance: every install path in `install_test.go`, `install_source_test.go` and the
integration tests passes unchanged, including the non-TTY refusal and `--force` overwrite.

### A5 — Update and import
Scope: `cmd/update.go`, `cmd/import.go`.
- `core.Update(ctx, opts, rep, UpdateRequest{ID}) (UpdateResult, error)`, where the result is
  per-skill `{ID, Outcome: updated|up_to_date|synced|pruned|failed|drift_check_skipped,
  Err}` plus `Synced bool`. This absorbs `runUpdate`, `updateOne`, `selectUpdateTargets`,
  `refreshVendoredCopies` and `classifyStagingErr`.
- `core.Import(ctx, opts, rep, dir)` and `core.AutoImportTrackedRoots`: `landLocalInstall`
  stops reading `flagForce`.
Acceptance: `update_test.go`, `import_test.go` and the integration tests pass; the summary
lines are unchanged.

### A6 — Uninstall and agents (non-interactive agent set)
Scope: `cmd/uninstall.go`, `cmd/agent.go`.
- `core.Uninstall(ctx, opts, rep, ids)`. The confirmation stays in cmd, and core returns
  `ErrNeedsConfirm` for the cases `uninstallOne` gates on `flagForce`.
- `core.SetAgents(ctx, opts, rep, enable, disable []string) (AgentsResult, error)` runs the
  reconciliation now in `runAgent` (`enableAgent`, `disableAgent`, `reconcileLocalRoots`,
  `reconcileVendoredRoots`). `core.Agents(opts)` lists all agents with their enabled state.
- The interactive `skillm agent` picker becomes: pick in cmd → `core.SetAgents`.
Acceptance: `agent_test.go` passes. `grep -n 'flagForce\|flagYes\|flagHome\|os.Getwd'
cmd/*.go` only hits `RunE`/option-building code.

### A7 — Self-update status and the bundle guard
Scope: `cmd/upgrade.go`, `internal/selfupdate`.
- `core.SelfStatus(ctx, version) (SelfStatus{Current, Latest, Available, Eligible,
  Method: binary|bundled|dev}, error)`.
- `Method = bundled` when `os.Executable()` resolves inside `*.app/Contents/`. In that case
  `skillm upgrade` refuses with "managed by the skillm app; use Upgrade in the menu".
Acceptance: tests for the three methods (inject the executable path).

**End of Phase A:** `cmd/` holds cobra wiring, prompts and rendering only.

---

## Phase B — JSON protocol

Contract, fixed in B1:

- A global `--json` flag. Output is one JSON document, or NDJSON when `--events` is also
  given (for long operations).
- Envelope: `{"schema_version":1,"data":…,"warnings":[…],"error":null|{code,message,skill_id?,path?,retryable}}`.
- Every event line has `{"schema_version":1,"type":"event",…Event fields}`, and the stream
  ends with one `{"type":"result",…envelope}` line.
- Exit code 0 on success, non-zero on error. On error the envelope still goes to stdout, and
  fang's styled error is suppressed in JSON mode.
- JSON uses `snake_case`, and times are RFC 3339 UTC.
- Golden fixtures live in `internal/protocol/testdata/*.json` and are the cross-language
  contract. The Swift tests in C1 decode these same files.

### B1 — Protocol plumbing + `version`, `list`, `check`
Scope: new `internal/protocol` (envelope, NDJSON reporter implementing `core.Reporter`,
error → code mapping), `cmd/root.go`, `cmd/list.go`, `cmd/check.go`, new
`cmd/version.go`.
- `skillm version --json` → `{version, api_version: 1, capabilities: [...]}`. The app uses
  it to refuse a mismatched CLI.
- `list --json` returns the structured installs from A2. `check --json` returns the A2 statuses.
- The `docs/features/json-api/` docs start here, with one section per command.
Acceptance: golden tests, plus integration tests that run the binary with `--json` and
decode the output.

### B2 — Mutating commands in JSON mode
Scope: `install`, `update`, `uninstall`, `import`, `upgrade`.
- In JSON mode, install needs explicit ids plus `--global` or `--project <path>` (add
  `--project` as the cwd-free spelling of `--local`). `ErrForeignFiles` →
  `error.code = "foreign_files"` with `paths`, and the caller retries with `--force`.
- `uninstall --json` needs `--yes`. `--events` streams item events for install and update.
- `upgrade --check --json` → the `SelfStatus` from A7.

### B3 — New commands: `source inspect`, `agent ls/set`, `config get/set`
- `skillm source inspect <src> [--ref R] --json` → the `Inspection` from A4. The app uses it
  for the Add Skill picker, then calls `install <src> <ids…> --ref <commit>`.
- `skillm agent ls --json`, and `skillm agent set --enable a --disable b`, which is also
  usable without JSON.
- `skillm config get/set --json` covers the GUI-relevant keys only. Add
  `[refresh] enabled = true, interval_hours = 24` to `config.Config`, with defaults when
  absent. `config.Save` already rewrites the file (as `skillm agent` does), so keep that
  documented.

### B4 — `refresh` / `status`: one badge policy for every GUI
- `skillm refresh [--if-due] --json` runs Check + SelfStatus and atomically writes
  `<home>/status.json`: `{checked_at, next_due_at, skills: [{id, status}], updates,
  self: SelfStatus, errors: [...], badge: bool}`. `--if-due` does nothing unless
  `now >= next_due_at` and refresh is enabled.
- `skillm status --json` reads the cache offline. It returns `stale: true` when it's older
  than the interval.
- Badge = `updates > 0 || self.available`. A failed check keeps its error status and never
  reports current.
- `update`, `install` and `upgrade` rewrite the affected entries in `status.json`, so the dot
  clears without another network pass.
Acceptance: tests for due-time logic with an injected clock, plus a golden fixture for the cache.

---

## Phase C — macOS app (`macos/`)

Tooling: an XcodeGen `macos/project.yml` is checked in and the generated `.xcodeproj` is
gitignored, so the project stays reviewable text. It targets **macOS 14+** (`MenuBarExtra`,
`@Observable`, `SettingsLink`, `SMAppService`), with `LSUIElement = YES`, hardened runtime
and no App Sandbox (skillm writes `~/.claude`, project dirs, and spawns git). The only Swift
dependency is Sparkle 2 (SPM).

Structure: `SkillmClient` (process + decoding) → `AppModel` (`@Observable` state) → views.
Views never call the client directly.

### C1 — Skeleton + `SkillmClient`
- Finds the binary at `Bundle.main.url(forAuxiliaryExecutable: "skillm")`. A debug build
  falls back to `$SKILLM_BIN` or the `go build` output. On launch it runs
  `version --json` and refuses mismatched `api_version`s.
- `run<T: Decodable>(args) async throws -> T` and `stream(args) -> AsyncThrowingStream<Event>`
  (NDJSON line reader). Arguments go as an array (no shell), cancellation sends SIGINT, and
  the environment gets PATH extended with `/opt/homebrew/bin:/usr/local/bin:/usr/bin`. A
  missing git is reported with its fix.
- Codable models mirror the protocol. **Tests decode `internal/protocol/testdata/*.json`**,
  and a fake-binary script returns the fixtures for client tests.
- A menu bar icon (SF Symbol, template) that shows only Quit.

### C2 — Menu, auto refresh, badge
- Menu items as in the Goal. The auto refresh toggle is written through `config set`.
- Scheduler: a `Timer` every hour plus `NSWorkspace.didWakeNotification` plus launch, each
  running `refresh --if-due`. Go decides whether a check is due. The Refresh item runs
  `refresh` without `--if-due`.
- Red dot: a composed non-template `NSImage` (template glyph + red circle) when
  `status.badge`. The template variant is used otherwise, so dark and light menu bars both
  work.
- "Update all skills" → `update --json --events`, with progress shown in the menu or a small
  HUD, then status re-read.
- Optional: a `UNUserNotificationCenter` notification deduplicated by revision/version.

### C3 — Windows: View skills, Add skill, Settings
- **View skills**: a `Table` over `list --json` (id, source, kind, where installed per scope
  and agents), with Reveal in Finder, per-row Update, and Uninstall with a confirm sheet.
- **Add skill**: repo field + optional ref → `source inspect` → checklist → scope (Global or
  a project folder via `NSOpenPanel`) → `install … --events`. `foreign_files` → a confirm
  sheet listing the paths → retry with `--force`.
- **Settings** (a `Settings` scene): auto refresh on/off + interval, enabled agents
  (`agent ls/set`), and "Install command-line tool" (a symlink to the bundled `skillm` in
  `/usr/local/bin` or `~/.local/bin`).
- **Start at login** checkbox: `SMAppService.mainApp.register()` / `unregister()`. It
  defaults to off. It is the one setting that is **not** stored in `config.toml`: the
  checkbox reads `SMAppService.mainApp.status` every time Settings opens, so it stays
  correct if the user removes the app in System Settings → General → Login Items. When the
  status is `.requiresApproval`, show a hint with a button that opens that pane
  (`SMAppService.openSystemSettingsLoginItems()`).

### C4 — Upgrade and restart (Sparkle)
- Sparkle 2 with an EdDSA-signed appcast served from GitHub Pages or the release. The
  "Upgrade and restart" item appears when Sparkle finds an update, or when
  `status.self.available` is set (both come from the same release).
- The bundled CLI is upgraded with the app, and `skillm upgrade` refuses inside the bundle (A7).

### C5 — Release pipeline
- A new `macos` job in `.github/workflows/release.yml` on `macos-latest`: build a universal
  `skillm` (`lipo` of arm64 + amd64, same ldflags) → `xcodegen` → `xcodebuild archive` →
  codesign the nested CLI, then the app (Developer ID, hardened runtime) → `notarytool
  submit --wait` → `stapler staple` → zip + sha256 → `generate_appcast` → upload to the
  same GitHub release.
- **User action needed:** an Apple Developer ID certificate + notarization API key as repo
  secrets, and a Sparkle EdDSA key pair.

---

## Phase D — Linux Quickshell (not on this branch)

It reuses everything from phases A–B unchanged: a `quickshell/` QML widget using `Process`
+ `JSON.parse` over the same commands, a systemd user timer running
`skillm refresh --if-due`, and `FileView` watching `~/.skillm/status.json` for the badge.
Self-upgrade uses `skillm upgrade` (method `binary`) or the distro package. Start at login
is the Hyprland `exec-once` / Quickshell config, not skillm's job. Nothing
Linux-specific should be needed in Go. If something is, that is a gap in phase B.

---

## Step log

| Step | Status | Base SHA | Final SHA | Deviations / notes for next step |
|---|---|---|---|---|
| A1 | todo | | | |
| A2 | todo | | | |
| A3 | todo | | | |
| A4 | todo | | | |
| A5 | todo | | | |
| A6 | todo | | | |
| A7 | todo | | | |
| B1 | todo | | | |
| B2 | todo | | | |
| B3 | todo | | | |
| B4 | todo | | | |
| C1 | todo | | | |
| C2 | todo | | | |
| C3 | todo | | | |
| C4 | todo | | | |
| C5 | todo | | | |
