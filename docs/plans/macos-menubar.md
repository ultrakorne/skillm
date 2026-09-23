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
- **User decision:** do it. Generate the EdDSA key pair with Sparkle's `generate_keys` (60s
  timeout; the private key stays in the login keychain). If it cannot run
  non-interactively, read a placeholder public key (`SUPublicEDKey`) from one config place
  and list the `generate_keys` command as a user action.

### C5 — Release pipeline
- A new `macos` job in `.github/workflows/release.yml` on `macos-latest`: build a universal
  `skillm` (`lipo` of arm64 + amd64, same ldflags) → `xcodegen` → `xcodebuild archive` →
  codesign the nested CLI, then the app (Developer ID, hardened runtime) → `notarytool
  submit --wait` → `stapler staple` → zip + sha256 → `generate_appcast` → upload to the
  same GitHub release.
- **User action needed:** an Apple Developer ID certificate + notarization API key as repo
  secrets, and a Sparkle EdDSA key pair.
- **User decisions:** the app is given to colleagues' Macs, so it is signed with the user's
  team, Starberry Games GmbH (team ID `6LH2JMGD3J`): put `DEVELOPMENT_TEAM: 6LH2JMGD3J` in
  `macos/project.yml` (automatic signing with `-allowProvisioningUpdates` works locally).
  Distribution needs a "Developer ID Application" certificate and notarization, and this
  Mac only has an "Apple Development" identity so far. C5 therefore ships **both** a local
  release script (sign with the Developer ID identity from the login keychain, notarize
  with a `notarytool` keychain profile, staple, zip, generate the Sparkle appcast) **and**
  the CI job above. Never invent or store credentials: list the missing ones (create the
  Developer ID certificate in Xcode, `xcrun notarytool store-credentials`, GitHub secrets)
  as user actions with exact commands. Agents may `brew install xcodegen` and other
  Homebrew dev tools the macOS steps need.

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
| A1 | done | 6642d52 | 3b719b4 (docs + this log in the next commit) | **Deviations:** `store.Lock(ctx, home, op, onWait)` instead of `Lock(home)`: ctx makes Ctrl-C/GUI cancel stop the wait, `op` (e.g. "skillm install") is the recorded holder (pid + op, never argv, so no credentials leak into `.lock`), `onWait` lets cmd print "waiting for …" (`cmd/lock.go` `lockHome`). Atomic writes are `store.WriteFileAtomic` (state/config import store); it follows symlinks, keeps an existing mode, umasks new files, retries the rename on Windows. `ReplaceDir` stages in a hidden `.<id>.skillm-tmp-*` holder and sweeps stale ones (safe only under the lock). Lock creates Home, so `uninstall`/`agent` on a fresh machine now create `~/.skillm/.lock`. `runAgent`/`runUninstall` now take a ctx.<br>**Notes for A2+:** the lock is held for the whole command, including prompts and network fetches; waiters time out after 30s. A4/A6 should prompt before locking, then lock → reload/revalidate → mutate → save; A5 should stage `update` fetches before locking and hold it for the write phase only. Revisit the 30s timeout for interactive callers. When code moves into core (A3–A6), keep every `ReplaceDir`/save under the lock and pass `op`/`onWait` from cmd (core must not print). B1: map `*store.LockTimeoutError` to a retryable error code, and route the "waiting for …" notice (stdout today via `ui.Warnf`) to an event or stderr in JSON mode. `docs/vercel-skills-comparison.md` still says Home holds only two files (history section, left as is). |
| A2 | done | 4844e64 | 6c7e2a0 (docs + this log in the next commit) | **Deviations:** `EventBatch` (with an `Items []string`) was added to the event model up front, not deferred to "extend it later" as the plan's sketch implied — the checklist needs every row's label before the first `ItemStart` so it can lay them all out at once, and both `Check` and `List`'s single caller (the checklist) needed it immediately. `core/errors.go` also defines `ForeignFilesError`/`ErrNeedsConfirm` now even though nothing in A2 raises them yet (A4/A6 are their first callers) — defined early so A3's moved code and A2's arch test agree on where typed errors live. Named `ForeignFilesError` (a struct type) rather than the plan's literal `ErrForeignFiles{Paths}` (which reads as a var of a struct type); A4 should treat this as the settled name. `core.SourceLabel(kind, source, path string) string` is a package-level function (not a method) so both `CheckedSkill` and `ListedSkill` can share it via a `SourceLabel()` method — reuse it rather than re-deriving a label. Multi-review (Claude + `codex exec review`, no Workflow tool available to this session so run manually — see the commit) found and fixed two real issues, both now covered by tests: (1) `ui.Checklist`'s `OnAbort` did not fire when the TUI renderer itself ended abnormally (an error, or an unexpected final-model type), only on a user quit — since `Checklist` no longer owns the work (core/cmd's `FanOut` does), that left outstanding work running unobserved with no way for the caller to know; fixed by firing `OnAbort` on any abnormal end, factored into a pure `shouldAbort` and unit-tested. (2) the new `cmd/golden_test.go` fixtures did not normalize Windows paths (a git Source is the `file://` URL git was given, forward-slashed, never the raw native path) — fixed with a `urlPath` match plus a `filepath.ToSlash` pass, both no-ops on POSIX. `core.Install` also has a `Recorded bool` beyond the plan's `{Scope, Root, Path, Agents[], Exists}`: it says the install is in the Registry (not only found on disk), and `List` uses it to decide what to include; B1 serializes it with the rest. A second multi-review round (commit `6c7e2a0`) fixed three more: (1) quitting the live `check`/`update` view could hang, because core now waits for its workers and a killed `git clone` over HTTP(S) leaves `git-remote-http` holding git's stderr; `gitx.runGit` now sets `cmd.WaitDelay` (2s), with a stalled-HTTP-server regression test (the optional process-group kill was not added: a new process group would stop git with SIGTTIN when it prompts for credentials on the tty). (2) a cancellation during `DefaultRef` showed as a failed row; `core.Check` now drops any `StatusError` row once ctx is done (`interrupted`), and `DefaultRef` wraps ctx's error. (3) `core.Check`'s `ItemDone` Text for code `error` now names the cause (`<id>: could not read upstream (<label>): <err>`); `cmd/check.go`'s `checkReporter` swaps in the historic untracked line (`core.UntrackedText`), so the CLI stays byte-identical (golden test).<br>**Notes for B1:** `Event.Text` for check code `error` is the honest cause line, so forward it as is; only the terminal renders it as "untracked". Code `untracked` still means the subdir is gone upstream.<br>**Notes for A3+:** `internal/core/arch_test.go` is live now — any new `internal/core` file must not import `internal/ui`/cobra/fang/huh/bubbletea/bubbles/lipgloss, and must not reference `os.Getwd`/`os.Stdout`/`os.Stderr`/`os.Stdin` (take `Options.Cwd` instead, report via `Reporter` instead of printing). A3's "pure move" should have its moved functions accept the Home lock as already held by the cmd caller — per A1's note, core must not try to lock/unlock Home itself. Reuse `core.FanOut`, `core.Options` and the `Reporter`/`Event` model already in place rather than introducing parallel versions; if A3's moved localinstall/locksync helpers gain a per-item loop worth a live view, wire it through `core.FanOut` + a `Reporter` (A2's pattern for `check`/`list`) rather than reviving the deleted `ui.RunChecklistProgress`, so `update`'s eventual A5 move (still on the `runChecklist` bridge in `cmd/reporter.go`) has one pattern to converge on, not two. `docs/features/core/TECHNICAL.md`'s file table will need the new `internal/core` files A3 adds. |
| A3 | done | ffedeff | d97b102 (docs + this log in the next commit) | **Deviations:** a pure move, CLI output byte-identical. `cmd/localinstall.go` → `internal/core/vendor.go` (`VendorOne`, `LinkVendorAgents`, `VendorRemove`, `RefreshCopy`, `VendorConflict`, `CopyExists`, …), `cmd/locksync.go` → `internal/core/locksync.go` (`UpsertLockEntry`/`RemoveLockEntry` now return their error), and the identity helpers from `fetch.go`/`import.go` → `internal/core/source.go` (`SrcIdentity.Matches`, `CanonicalRemote`, `NormalizeRemote`, `RegistryCollision`, `MergeEntry`, `ChosenID`, `CheckAsSingle`, `RepoRelSubpath`, `RefetchSkill`). cmd prints core's log Events through `termLog` (`cmd/reporter.go`); callers discard the newly returned errors with `_ =`, so behaviour is unchanged. `dedupeStrings` stays in cmd (`cmd/agent.go` uses it); cmd's test-only `servedAgents` was dropped. Beyond the plan, the review fix (`d97b102`) split the event codes: `link_refused`/`unlink_refused` now mean only "a foreign entry is in the way" (`errors.Is(err, linker.ErrNotManaged)`), and genuine I/O failures (EACCES, missing symlink privilege, …) are `link_failed`/`unlink_failed`. `linker.Unlink`'s two "refusing to remove" errors now wrap `ErrNotManaged` through a small error type whose text is unchanged. Docs: new `docs/features/core/install-primitives.md`, TECHNICAL.md file table, and `docs/known-issues.md` now point at the core names.<br>**Notes for A4:** (1) core still reads the process cwd implicitly: `sameLocalPath`/`absClean` (`internal/core/source.go`, used by `SrcIdentity.Matches`) and `absOr` (`internal/core/list.go`) call `filepath.Abs`, which `arch_test.go` cannot see. This is not a regression, and it is a real bug today: `skillm install ./skills/foo --local --yes` records `source = './skills/foo'`, and `skillm update` run from another directory then reports the source as gone. From the GUI (cwd `/`) it would give a false "different source" or a false same-source match. Fix in A4: `core.Inspect` makes a local `src` absolute against `Options.Cwd` before discovery, so `fnd.Dir` and the recorded Source are absolute. `sameLocalPath` then compares `filepath.Clean` forms, or resolves a relative stored Source against an explicit base, and treats legacy relative entries as unresolvable rather than cwd-relative. Replace `absOr` the same way, then add `filepath.Abs` to `forbiddenRefs` in `arch_test.go`. TECHNICAL.md currently documents this cwd dependency; update it when it is gone. (2) Core texts still name CLI flags: `CheckAsSingle` ("drop --as"), `differentSourceErr` ("pass `--as <name>`") and `LinkVendorAgents`' " (pass --force to take it over)" suffix. For A4/B1: move the `--force` suffix into cmd (`termLog` appends it for code `link_refused`), and turn the two `--as` errors into typed errors (e.g. `*SourceCollisionError{ID}` plus a sentinel for `--as` misuse), so cmd writes the flag advice and B1/the GUI map codes to their own wording. Until then, B1 must not forward these `Text`s to the GUI as they are. (3) `docs/known-issues.md` names core functions too, so keep it in step when A4/A5 rename or move them (not only TECHNICAL.md).<br>**Notes for B1:** key the GUI's "Force" offer on `link_refused` only; `link_failed`/`unlink_failed` need a different message.<br>**User actions (for C4/C5, recorded now so they are not lost):** this Mac has only an "Apple Development: Samir Hadi" identity. To distribute to colleagues: create a "Developer ID Application" certificate for team 6LH2JMGD3J (Xcode → Settings → Accounts → Manage Certificates → + → Developer ID Application; needs the Account Holder or Admin role), then `xcrun notarytool store-credentials skillm-notary --apple-id <apple-id> --team-id 6LH2JMGD3J` (with an app-specific password from appleid.apple.com) and add the GitHub secrets C5 lists. |
| A4 | done | bad9d7d | 30ed41b (docs + this log in the next commit) | **Deviations:** `cmd/fetch.go`'s staging and `cmd/install.go`'s install moved to `internal/core/inspect.go` (`Inspect`, `Inspection`, `InspectedSkill`, `Close`, `Select`) and `internal/core/install.go`. The install entry point is `core.InstallSkills(ctx, opts, rep, InstallRequest)`, because A2's `core.Install` type already has the plan's name. `Inspect` takes `Options` (`Inspect(ctx, opts, src, ref)`) for its `Cwd`. Added `ValidateSourceSelection` and `ValidateIDSelection` so cmd reports a bad selection (a collision, `--as` on several skills, an unknown id, a local skill whose source is gone) before the scope question. On a TTY, "yes" to the overwrite question retries with `Options.Yes`, not `Force`: the question lists only the canonical slots, so it must not take over agent link paths (`--force` still does); "no" retries with `SkipForeign`. Typed errors (`*SourceCollisionError`, `ErrAsMultiple`, `*LocalScopeAliasedError`) replace core's flag advice; cmd adds it (`installError`, and `flagAdvice` in `cmd/reporter.go` for `link_refused`/`install_blocked`). Core no longer calls `filepath.Abs` (`arch_test.go` forbids it): paths resolve with `ResolvePath(p, Options.Cwd)`.<br>Behaviour changes, all deliberate: (1) a local Source is recorded **absolute** in the Registry and in the committed `skills-lock.json`, where the old binary wrote the argument as typed (`./skills/foo`). This matches vercel's `npx skills`, which `path.resolve()`s a local source and writes that as the lock's `source`. A test pins it. A relative git repository path (`./catalog.git`) is resolved against `Cwd` and recorded absolute as well. (2) `install` takes Home's lock only for the write phase: the clone, the pickers, the scope question and the overwrite question run unlocked. `InstallSkills` reloads config and the Registry and re-plans under the lock. The one in-process test that encoded "lock first" (`cmd/lock_test.go`, install case) now installs a real skill and still checks that it waits. (3) A symlinked local Source works with or without a trailing slash (before A4 only `link/` worked). (4) In id mode a git skill's re-fetch (and its errors) now happen after the scope and overwrite questions, inside `InstallSkills`. A local skill whose source is gone is still reported first. (5) On Windows a drive-less rooted path (`\skills\foo`) resolves to the base's drive root, as `filepath.Abs` did. (6) `lockEntryMatches` (import/update) resolves a local lockfile source against the lockfile's root, so an older relative entry still matches the now-absolute Registry Source.<br>Review round (`30ed41b`) fixed all 9 confirmed findings. One was only partly fixed: `SrcIdentity.Matches` still resolves an older relative local Source against the CLI's cwd rather than treating it as unresolvable. Refusing it would turn a reinstall from the same project, which is the only way such an entry gets rewritten to an absolute path, into a collision that needs an uninstall. The remaining risk: from another project, a same-named relative path can falsely match. With an empty `Cwd`, core now refuses relative paths (`Inspect`, id mode) instead of reading the process cwd.<br>**Notes for A5:** `update`/`import` still take the lock for the whole command; stage fetches before locking, as `install` now does (`cmd/install.go` `installLocked`). `RefetchSkill` and `idModeSource` are the shared re-fetch primitives. Keep `lockEntryMatches(existing, entry, root)` resolving local sources against `root` when it moves to core.<br>**Notes for A6:** use the same shape as install: prompt without the lock, then lock → `core.X` (reload/re-validate) → save, and release the lock around any retry question.<br>**Notes for B1/B3:** `InstallRequest.Commit` (full SHA or a prefix of 7+ characters) with `*CommitMismatchError` exists for B3's cross-process flow. The app should call `install <src> <ids…> --ref <branch> --commit <sha from source inspect>`, not `--ref <sha>`: `--ref <sha>` records the SHA as the tracked Ref, so `check`/`update` would never show an update. B3 adds the `--commit` flag and maps `CommitMismatchError` to "the source changed, re-inspect". Map `*ForeignFilesError` to the GUI's overwrite question: retry with `Yes`, not `Force`, or with `SkipForeign` on "no". `Inspect` with an empty `Cwd` refuses relative paths, so the GUI must send absolute ones.<br>**User actions:** none for A4. The Developer ID / notarization / Sparkle actions recorded under A3 still apply to C4/C5. |
| A5 | done | 5b6e77a | cf9c2f0 (docs + this log in the next commit) | **Deviations:** `cmd/update.go` and `cmd/import.go` moved to `internal/core/update.go` (`Update`, `UpdateRequest`, `UpdateResult`, `UpdatedSkill`, `UnknownSkillError`, `UpdateFailedError`, `refreshInstalls`, `fetchUpdates`) and `internal/core/import.go` (`Import`, `AutoImportTrackedRoots`, `ImportResult`, `ImportedSkill`, `importFetcher`, `lockEntryMatches`). `landLocalInstall` takes `Options` instead of reading `flagForce`. The old cmd `update_test.go`/`import_test.go` cases moved to core; `runChecklist` is gone (the `termReporter` ends an open checklist before printing a log). Beyond the plan: (1) `Options.Lock` is the caller's lock hook. `Update`, `Import` and `AutoImportTrackedRoots` fetch unlocked and call it around their write phases only, then re-read config, the Registry and the lockfile. `Update` judges each fetch against the skill's current entry: one another process moved to the fetched Revision is only drift-checked; one reinstalled from another source, or moved to a different Revision, fails with "run update again" (`update_skipped`) instead of being rolled back. `import` skips the lock when there is nothing to write (`cmd/lock_test.go` now gives it a lockfile entry). (2) `UpdatedSkill` also has `Revision`, `Advanced` (the Revision advanced, true even when the skill was then pruned; `UpdatedAny` counts it), `Pruned` (the forgotten installs) and `Warnings` (failed copy or `skills-lock.json` writes for installs that stay recorded). Behaviour change, deliberate: quitting the live `update` view (or cancelling ctx) before the write phase now writes nothing and prints "update interrupted; no skill was updated". Before A5, skills already fetched were still written and the command failed with "N skills failed to update". The all-skills adoption sweep runs before the fetches, so its imports may already be saved. A broken `config.toml` is now reported after the fetches, and `config.EnsureExists` runs only for a non-empty lockfile. Review round (`cf9c2f0`) fixed all 4 confirmed findings: the stale-fetch rollback above, "Everything is up to date." wrongly printed after a skill that advanced was pruned (`Advanced`, with a new binary-level `cmd/update_test.go`), write failures missing from the typed result (`Warnings`), and this log entry for the cancel change.<br>**Notes for A6:** reuse the `Options.Lock` shape where uninstall or agents do slow work before writing; otherwise the cmd caller holds the lock for the whole call (`Options.Lock` nil).<br>**Notes for B1:** map `UpdatedSkill.Outcome` directly. `updated`/`synced` with non-empty `Warnings` is a partial success: say which install stayed stale (the warning events carry `copy_failed`/`lockfile_not_updated`); the next update repairs it. `failed` with code `update_skipped` means "changed by another process, run update again", not a network error. Cancel before the write phase writes nothing, so the GUI can offer "Cancel" safely during fetches; once the lock is taken the writes run to the end. `UnknownSkillError` maps to "not installed". The fetch rows' `ItemDone` codes are judged against the Registry as read before the fetches; the final `Outcome` in the result is authoritative.<br>**User actions:** none for A5. The Developer ID, notarization and Sparkle actions recorded under A3 still apply to C4/C5. |
| A6 | done | 679b6088c9667a154636c7fdbe56342a720672bb | a07c9a5 (docs + this log in the next commit) | **Deviations:** `cmd/uninstall.go` and `cmd/agent.go`'s reconciliation moved to `internal/core/uninstall.go` (`Uninstall`, `UninstallRequest`, `UninstallResult`, `UninstalledSkill`, `UninstallRoots`, `CheckInstalled`, `NotInstalledError`, `UninstallBlockedError`, `UninstallScopeChangedError`) and `internal/core/agents.go` (`SetAgents`, `Agents`, `AgentInfo`, `AgentChange`, `AgentsResult`, `ErrNoAgentEnabled`, `UnknownAgentError`, `enableAgent`/`disableAgent`); `reconcileLocalRoots`/`reconcileVendoredRoots`/`localScanDirs` moved to `internal/core/roots.go`. The in-process agent/reconcile tests moved to core (`agents_test.go`, `setagents_test.go`, `roots_test.go`); `cmd/integration_test.go` is unchanged. The entry point is `core.Uninstall(ctx, opts, rep, UninstallRequest{IDs, CheckRoots, ConfirmedRoots})`, not the plan's `ids` argument. Core texts no longer name commands: `termLog` adds the `skillm install`/`skillm uninstall` advice by code (`flagAdvice`), and prints `copies_kept` as a dim hint (`hintCodes`). The plan's "core returns `ErrNeedsConfirm` for the cases gated on `flagForce`" was changed in review: a foreign entry in the way is `*UninstallBlockedError` matching the new **`ErrNeedsForce`** (it may come after partial progress, and `Yes` does not step past it; `Force` does). An I/O failure there is a plain error that `Force` still steps past but that matches no sentinel. `ErrNeedsConfirm` is now raised only by `*UninstallScopeChangedError`.<br>Behaviour changes, all deliberate: (1) `uninstall` and `agent` ask their pickers and confirmations **before** taking Home's lock; core re-reads config and the Registry under it (`cmd/lock_test.go` now gives both something to do). (2) The uninstall confirmation's project list is binding: cmd passes it as `ConfirmedRoots` with `CheckRoots`, and if another process added a Local install in a new project while the question was open, core refuses before removing anything, cmd releases the lock and asks again with the new list. `--yes`/`--force`/non-TTY runs pass no `CheckRoots` and clear every recorded project, as before. (3) Ctrl-C (or a cancelled ctx) now stops an uninstall batch between skills: the skills already done are saved, the rest stay installed, and the CLI prints `uninstall interrupted; N of M skills removed`. Before A6 the batch always ran to the end. (4) A skill removed by another process while the prompt was open fails the whole batch with `not installed: X; nothing to uninstall` (nothing removed).<br>Review round (`a07c9a5`) fixed all 4 confirmed findings: the unbound confirmation (2 above), the `ErrNeedsConfirm` contract (`ErrNeedsForce`, refusals only), warnings missing from the typed results (`VendorRemove` now returns its unlink failure, so a Global link left behind is in `UninstalledSkill.Warnings`; `AgentChange.Warnings` records `link_skipped`/`link_failed`/`unlink_refused`/`unlink_failed`/`scan_failed`), and the undocumented cancel change (3 above). The optional "skip ids that vanished during the prompt with a warning" was not done (4 above).<br>**Notes for A7:** A7's scope is independent of A6. Keep the pattern: questions unlocked, then lock → `core.X` re-reads → save.<br>**Notes for B1/B2:** map `*UninstallBlockedError` + `ErrNeedsForce` to "a foreign entry is in the way, retry with force"; an `*UninstallBlockedError` without it is an I/O failure (no force offer). `*UninstallScopeChangedError` (`ErrNeedsConfirm`) carries the full new `Roots`: show them and retry with them as `ConfirmedRoots`. B2 should expose `CheckRoots`/`ConfirmedRoots` as a flag for `uninstall --json` (it requires `--yes`, and the GUI does its own confirmation; build that confirmation from `core.UninstallRoots`). `UninstalledSkill.Warnings` / `AgentChange.Warnings` non-empty means a partial success: say which entries were left in place. `*NotInstalledError` maps to "not installed"; `ErrNoAgentEnabled` and `*UnknownAgentError` are validation errors, returned before anything is written. `core.Agents(opts)` is B3's `agent ls`.<br>**User actions:** none for A6. The Developer ID, notarization and Sparkle actions recorded under A3 still apply to C4/C5. |
| A7 | done | 899fc2d2054672a0537083b803f18995eec59d2d | bb8f6be (docs + this log in the next commit) | **Deviations:** the plan's `core.SelfStatus(ctx, version)` is `core.CheckSelf(ctx, version) (SelfStatus, error)`, because Go does not allow a function and a type with the same name. It is split into three calls in `internal/core/selfstatus.go`: `SelfMethod(version) (UpgradeMethod, exe)` judges `binary`/`bundled`/`dev` offline, `CheckSelf` adds the release lookup, and `UpgradeSelf(ctx, st)` installs only the release `CheckSelf` found (it is carried unexported). `SelfStatus` has an extra `Executable` field (the resolved path that was judged). Errors: `core.ErrManagedByApp` ("skillm is managed by the skillm app; use Upgrade in the menu"), `ErrSourceBuild`, `ErrNoUpgrade`. `internal/selfupdate/bundle.go` adds `InBundle(path)` (any `<name>.app/Contents` component, case-insensitive, either separator; lexical only), `Executable()` (symlinks resolved, so a CLI link into the app counts as bundled) and `ErrBundled`, which `Apply` returns before downloading anything; `UpgradeSelf` maps it to `ErrManagedByApp`. A non-release version is `dev` even inside a bundle, and `CheckSelf` then makes no network request (`Latest` empty). `skillm upgrade` in a bundle refuses before the network (same answer offline); `upgrade --check` there still looks the release up and says "Use Upgrade in the skillm app's menu". Output is unchanged for dev and binary installs.<br>Review round (`bb8f6be`) fixed the 1 confirmed finding: `cmd/upgrade_bundle_test.go`'s `runOffline` now drops every inherited `*_proxy` variable (any case) before pointing `HTTP(S)_PROXY` at a dead port, since Go reads the first non-empty of `NO_PROXY`/`no_proxy` and an inherited `no_proxy=*` let the test reach GitHub. Verified with `no_proxy='*'` set.<br>**Notes for B1/B2/B4:** `upgrade --check --json` is `core.CheckSelf`'s `SelfStatus`; map `ErrManagedByApp` to "use the app's Upgrade" (not an error the GUI should show as a failure), `ErrSourceBuild` to "dev build, nothing to upgrade". A bundled `CheckSelf` still needs this platform's archive and `checksums.txt` in the release (`selfupdate.Latest`); `.goreleaser.yaml` builds darwin, so this holds today, but keep it in mind for B4's badge. `InBundle` refuses inside *any* `.app` bundle on purpose.<br>**Notes for C4:** the app's bundled CLI should live under `skillm.app/Contents/` (e.g. `Contents/Helpers/skillm`) so the guard applies, and a CLI link the app installs should be a symlink into the bundle. The app upgrades itself and its CLI through Sparkle.<br>**User actions:** none for A7. The Developer ID, notarization and Sparkle actions recorded under A3 still apply to C4/C5. |
| B1 | done | 36e2d6b01da3e27cf2b5f449c203b4d18e9bd4e6 | be4d333 (docs + this log in the next commit) | **Deviations:** new `internal/protocol` (`protocol.go` envelope/`Warning`/`Error`, `writer.go` `Writer` = the run's `core.Reporter`, `errors.go` codes + `ErrorFrom`, `data.go` `VersionData`/`ListData`/`CheckData`), fixtures in `internal/protocol/testdata/`, and `cmd/json.go`. Beyond the plan's contract: (1) the error object also has `paths` (a list, e.g. `foreign_files`' entries, `needs_confirm`'s new roots); (2) a single document omits `type`, only the stream's last line has `"type":"result"`; (3) a command opts into JSON mode with the `skillm:json` annotation, and `--json` anywhere else fails with `json_unsupported` (so do `--help`/`-h`, `--version` and a bare `skillm --json`, which cobra would otherwise answer with terminal text and exit 0); `--events` without `--json` is a plain CLI usage error; (4) `skillm version` is a new command that skips the git check (plain: the same line as `--version`), and `capabilities` lists the annotated commands plus `events`; (5) A1's note done: `lockHome`'s waiting notice is an info event `lock_wait` in JSON mode, and `*store.LockTimeoutError` maps to retryable `home_locked` (with `path`); (6) every typed core/store error already has its code (B2 needs no new mapping for them).<br>Review round (`be4d333`) fixed all 4 confirmed findings: JSON mode now makes git non-interactive (`quietGit`: `GIT_TERMINAL_PROMPT=0`, and `GIT_SSH_COMMAND="ssh -o BatchMode=yes"` unless the user set `GIT_SSH_COMMAND`/`GIT_SSH`), with a binary test against a `git http-backend` server that starts asking for credentials (`TestJSONCheckNeverPrompts`; `runJSON` now drops the prompt variables from the inherited env); `--help`/`--version`/no-command are refused up front in `Execute`; after a successful parse only the parsed `--json`/`--events` count and the raw-argument scan follows pflag (last value wins); the `--events` error no longer starts with the flag (fang capitalized it to `--Events`). Dismissed: SIGTERM handling (SIGINT is C1's cancel signal; documented in `docs/features/json-api/DESIGN.md`).<br>**Notes for B2:** annotate each command with `annotationJSON`, pass `jsonOut()` as the Reporter and end with `jsonOut().Result(...)`; errors need nothing (the fang handler writes the envelope). Every prompt path must be unreachable under `flagJSON`: refuse with the typed error (`ErrNeedsConfirm`, `ForeignFilesError`, …) instead of asking; `uninstall --json` requires `--yes` and takes `CheckRoots`/`ConfirmedRoots` (A6 note). A new data type or code means a new fixture (`SKILLM_UPDATE_GOLDEN=1`) and the Go struct in one change; `TestFixturesDecode` rejects unknown fields. `upgrade --check --json` maps `ErrManagedByApp`/`ErrSourceBuild`/`ErrNoUpgrade` (codes already exist). Usage errors are recognised by cobra's message prefixes (`isUsageError`), so a cobra upgrade could turn `usage` into `error`. `list --json` forwards a git Source with any embedded token, like the terminal does (`docs/known-issues.md`).<br>**Notes for C1:** run `version --json` first (it works without git) and refuse an unknown `api_version`; decode the fixtures in `internal/protocol/testdata/` in the Swift tests; cancel with SIGINT (SIGTERM gives no result line). `quietGit` does not unset `GIT_ASKPASS`/`core.askPass`, so a user's askpass helper can still show its own dialog; a GUI-spawned child normally has none.<br>**User actions:** none for B1. The Developer ID, notarization and Sparkle actions recorded under A3 still apply to C4/C5. |
| B2 | done | fa662b90317a6b5dd638fb0409f1c813a011acfb | 8aee3df (docs + this log in the next commit) | **Deviations:** `install --json` needs ids (or `--all`) and one of `--global`/`--local`/`--project <dir>`; `--project` (an existing directory, resolved before any fetch) works in the terminal too, and with `--global` or an absolute `--project` no working directory is needed. Beyond the plan: (1) `foreign_files` can be answered three ways, not only `--force`: `--yes` (canonical slots only), `--force` (link paths too) or the new `--skip-foreign` (the terminal's "no"; skipped skills are `skipped` with an `install_blocked` warning); (2) `core.InstallSkills` now reports `batch`/`item_start`/`item_done`, so `install --events` streams like `update`; (3) `uninstall --json` takes `--confirmed-root <dir>` (repeatable, `--confirmed-root=` confirms none) for `CheckRoots`/`ConfirmedRoots`; (4) `upgrade --json` (not only `--check`) exists and refuses `source_build`/`managed_by_app` before the network; (5) `import --json` exists; (6) cobra's flag-group errors map to `usage`. New data types in `internal/protocol/data_mutating.go` with fixtures.<br>Review round (`8aee3df`) fixed all 4 confirmed findings: `--skip-foreign` with `--yes`/`--force` is now `usage` (it used to overwrite the files it should keep); `uninstall --json` skips ids already gone with a `not_installed` warning (`UninstallRequest.SkipMissing`), so a `needs_force`/`uninstall_failed`/`cancelled` run is retried with the same ids; `update` reports each failed fetch as an `update_failed` error log event with its `skill_id` (dropped on the terminal, whose row shows it), `UpdateFailedError` carries `IDs`, and the error's `skill_id` is set when one skill failed (`error_update_failed.json` now has two failures and their warnings); `import` of a missing or non-directory path is a `*core.ProjectDirError` (code `error` with `path`) in both modes, instead of 0 entries. Dismissed items stayed as they were (see the review).<br>**Notes for B3:** follow the same pattern (annotate with `annotationJSON`, pass `jsonOut()`, end with `Result`; refuse every question with `usageError` or a typed error; new data type = struct + fixture in one change). `agent set` should refuse `no_agent_enabled`/`unknown_agent` (codes exist). Flag checks against persistent flags (`--yes`, `--force`) must be done by hand; cobra's mutual exclusion cannot see them.<br>**Notes for C1:** a failed `update --json` has `data:null`: read failed ids from `warnings` (`update_failed`/`update_skipped`), and use `--events` for the others' outcomes. Retry an uninstall with the same ids and ignore `not_installed` warnings. `install` answers `foreign_files` by asking, then retrying with exactly one of `--yes`, `--force`, `--skip-foreign`. Pass absolute paths to `--project` and `import`.<br>**User actions:** none for B2. The Developer ID, notarization and Sparkle actions recorded under A3 still apply to C4/C5 (see the orchestrator's amended decisions: team 6LH2JMGD3J, a local release script plus the CI job in C5, Sparkle in C4). |
| B3 | done | fc8bb1a497df1948af3ba391c1d3803a8d42fd29 | 0ea80078c0cdadf4282c4f298a17ca188f23beff (this log in the next commit) | **Deviations:** (1) the Add Skill flow is `source inspect <src> [--ref R] --json`, then `install <src> <ids…> --ref <branch> --commit <sha>` (new `--commit` flag), not the plan's `--ref <commit>`: the branch stays recorded for updates, and a source that moved since inspection fails with `commit_mismatch` before any question or write (follows the A4 hand-off note). (2) `config get <key> --json` still returns every setting (the key only narrows terminal output). (3) JSON-mode `agent set` does not treat the working directory as a project; it reconciles the global folders and the recorded projects only. (4) A fresh `config.toml` now includes a `[refresh]` table (`enabled = true`, `interval_hours = 24`). `config set` takes Home's lock and rewrites the whole file. New codes `unknown_key`, `invalid_value`; JSON mode refuses every group command (`source`, `config`, `agent`, `completion`).<br>Review round (`0ea8007`) fixed all 4 confirmed findings: `[refresh]` is decoded leniently, so a wrong-typed hand edit (`interval_hours = "12"` or `1.5`, `enabled = "yes"`) reads as the default instead of failing every command; `--commit` must be 7 to 64 hex characters (`usage` before any clone; `core.checkCommit` returns a plain error for a malformed value, `core.IsCommitSHA`), so `commit_mismatch` only ever means "re-inspect"; terminal `agent set` refuses a change that leaves no agent enabled before its confirmation prompt; `refuseTerminalOnly` adds cobra's help and completion commands before resolving, so `completion --json` is refused.<br>**Notes for B4:** read the interval with `cfg.RefreshIntervalHours()` / `cfg.RefreshEnabled()` (they clamp and default); follow the same JSON pattern (annotation, `jsonOut()`, `Result`, fixture per data type). **Notes for C1:** on `commit_mismatch`, re-run `source inspect` and let the user re-pick; a `usage` error from `install --commit` is a bug in the caller, not a moved source.<br>**User actions:** none for B3. The Developer ID, notarization and Sparkle actions for C4/C5 still apply (team 6LH2JMGD3J). |
| B4 | done | eeac994b21e034b65f965a876494977cbdf4cdf0 | d1fefa54d1426bca33beb9f1ff34a27b93b9e02a (docs + this log in the next commit) | **Deviations:** new package `internal/status` owns `<home>/status.json` (format, atomic `Load`/`Save`, and the shared policies `Recount` (badge), `Due`, `DueAt`, `Stale`); `core.Refresh(ctx, opts, rep, RefreshRequest{Version, Now, IfDue})` and `core.ReadStatus` fill and read it; cmd `skillm refresh [--if-due]` and `skillm status` (both `--json`; `status` needs no git), protocol `StatusData` (the cache fields at the top level, including the cache's own `schema_version`, plus `stale`) and `RefreshData` (plus `refreshed`). Beyond the plan's shape: each skill row also has `installed_rev`/`upstream_rev`/`error`, `self` also has `error`, `errors` lists `{code, skill_id, message}` (code `error`/`untracked`/`self_check`), and the file has `schema_version: 1`. `--if-due` is due when no refresh ever ran, at `next_due_at`, at `checked_at` + the current interval (a shortened interval applies at once), or when `checked_at` is in the future; when every lookup failed `next_due_at` is one hour out (`status.RetryAfterFailure`). Refresh runs its lookups unlocked and writes under Home's lock, merged with a fresh Registry read. `uninstall` also rewrites the cache (the plan named update/install/upgrade); nothing is written by those commands when no refresh ever ran. The plan's "`refresh --if-due` does nothing unless `now >= next_due_at`" holds; the version trigger the first commit added (due when another skillm version wrote the cache) was removed in review.<br>Review round (`d1fefa5`) fixed all 5 confirmed code findings: (1) two skillm binaries sharing one Home (the app's bundled CLI and a terminal install) no longer make each other's `--if-due` due, and `status --json` re-judges `self` for the running binary whenever `current`, `method` or `executable` differ, not only the version; (2) an id-mode install that re-fetched from upstream writes the row `up_to_date` even when the Revision did not move; (3) a refresh that finishes after one that started later keeps the newer cache (`newerRefresh`; a cache dated beyond this refresh's run, from a clock that went back, is still replaced); (4) the refresh merge accepts a Check row only when the skill still tracks the checked Kind/Source/Path/Ref (`sameTracking`, via an unexported `CheckedSkill.entry`); (5) the cache fixture moved to `internal/protocol/testdata/cache/status.json`: every file directly in `testdata/` is an envelope, subdirectories hold other formats. The sixth (bundled `self.available` source) is a hand-off note below.<br>**Notes for C1:** decode every file directly in `internal/protocol/testdata/` as an envelope (`.json`) or an event stream (`.ndjson`); skip subdirectories. `testdata/status.json` is the `status --json` envelope; `testdata/cache/status.json` is the raw cache file. `status --json` is offline and cheap: call it on launch and after every command; `refreshed: false` from `refresh --if-due --json` means nothing was due (the data is the cache as it was). `stale: true` is informational; do not hide the badge for it.<br>**Notes for C2/C4/C5:** the menu-bar tick runs `refresh --if-due` (hourly, or on wake) and lets Go decide; the Refresh item runs `refresh` without it. A skill row `error`/`untracked` is a problem to show, never "current". For `method: bundled`, `self.available` means "a newer GitHub release exists" (it needs this platform's archive and `checksums.txt` in the release), not "Sparkle has an update". So either publish the appcast before or together with the GitHub release becoming "latest" (C5: the macOS job must finish before goreleaser marks the release latest, or upload the appcast first), or have C4 show "Upgrade and restart" for `bundled` only once Sparkle confirms the update. A release that lacks the darwin archive makes the self check fail (`self.error`, no dot). The app's bundled CLI must live under `skillm.app/Contents/` (A7) so `method` is `bundled`.<br>**User actions:** none for B4. The Developer ID, notarization and Sparkle actions for C4/C5 still apply (team 6LH2JMGD3J). |
| C1 | done | 35f25d307ae89c2957eddb92f3ba4d25e15dfedd | 9ea79fbae4e7b6ef719484f4620e7df1b7171cee (feature docs in `03eeb47`, this log in the next commit) | **Deviations:** (1) the bundled CLI lives at `skillm.app/Contents/Helpers/skillm`, not where `Bundle.main.url(forAuxiliaryExecutable:)` looks: the app's `PRODUCT_NAME` is `skillm`, so `Contents/MacOS/skillm` is the GUI itself, and any path under `Contents/` makes the CLI's method `bundled` (A7). A build phase (`macos/scripts/build-cli.sh`) builds it with Go for every `ARCHS`, joins the slices with `lipo` and signs it with the app's identity (hardened runtime; a secure timestamp for Release). A debug build looks at `$SKILLM_BIN` first, then the bundle, then the repository's `go build` output (`skillm`, `bin/skillm`). (2) `run<T: Codable & Sendable>` plus `runEnvelope` (the whole envelope, for callers that need a success's or a cancel's warnings) and `connect()` (`version --json`, refuses an `api_version` outside `supportedAPIVersions = [1]`). `stream(args)` returns a `SkillmStream<T>` of `StreamMessage<T>` (`.event`, then `.result(data, warnings)`), not `AsyncThrowingStream<Event>`: an `AsyncThrowingStream` ends the moment its consumer's task is cancelled, before skillm has exited. (3) Errors are one `SkillmError` enum (`binaryNotFound`, `launchFailed`, `incompatibleCLI`, `unsupportedSchema`, `malformedOutput` with stderr and exit status, `gitMissing`, `command(ProtocolError, warnings)`), `LocalizedError` with the fix as `recoverySuggestion`. (4) The git check goes beyond skillm's `git_missing`: `GitCheck` treats Apple's `/usr/bin/git` stub as missing when `xcode-select -p` fails. (5) The status item also shows the CLI problem (message and fix) above Quit when there is one.<br>Review round (`9ea79fb`) fixed all 6 confirmed findings: (1) a cancelled `run`/`runEnvelope` returned at once while skillm was still writing (and holding Home's lock); the output is now read in a detached task and the exit awaited through a latch that ignores cancellation, so a call returns, and a `SkillmStream` finishes with `CancellationError`, only once skillm has exited, and a cancelled stream still delivers the events skillm reports after SIGINT; `runEnvelope` returns the `cancelled` envelope with its warnings, `run` maps it to `CancellationError`. (2) SIGTERM followed SIGINT after 5 s, which skillm (it catches only SIGINT) would die of mid-write; `interruptGrace` is now 60 s, a guard against a hung child. (3) stderr is read to EOF (at most 2 s after the exit) before a failure is built, so its tail is never lost. (4) `/opt/homebrew/bin` and `/usr/local/bin` now go ahead of PATH (`/usr/bin` is appended when missing), so Homebrew's git wins over Apple's stub and the "brew install git" fix works. (5) The client's `--json`/`--events`/`--home` go before a `--` separator. (6) `build-cli.sh` prints a warning when a Release build bundles a CLI stamped `dev`.<br>**Notes for C2:** go through `AppModel` only; views never call `SkillmClient`. Calls and streams return only after skillm has exited, so after a cancel `status --json` reads the final state; show "Stopping…" while a cancelled call is still running (it can take as long as skillm needs to finish its current write, up to the 60 s grace). A cancelled `stream` loop ends by throwing `CancellationError`; breaking out of it early interrupts skillm without waiting. Run `status --json` on launch and after every command (B4), the menu-bar tick `refresh --if-due --json`, the Refresh item `refresh --json`. Tests use `macos/TestSupport/fake-skillm` (modes via `FAKE_SKILLM_MODE`; it records args, PATH, signals and an exit marker); add a mode for any new command the client tests need. Regenerate the project with `xcodegen generate` in `macos/` after adding a Swift file (the `.xcodeproj` is gitignored); build and test with `xcodebuild test -project Skillm.xcodeproj -scheme Skillm -destination platform=macOS -allowProvisioningUpdates`.<br>**Notes for C4/C5:** the bundled CLI's version is the `SKILLM_VERSION` build setting, independent of `MARKETING_VERSION` and `dev` by default in every configuration: a `dev` CLI reports `method: dev` (no `self.available`, and `upgrade` gives the source-build answer). The release script and CI job must set **both** `SKILLM_VERSION` and `MARKETING_VERSION` from the release tag (e.g. `xcodebuild archive … SKILLM_VERSION=0.5.0 MARKETING_VERSION=0.5.0`); do not default `SKILLM_VERSION` to `$(MARKETING_VERSION)`, since `0.0.0` is a release version to `IsReleaseVersion` and every local build would then see a newer release. A Go change outside C1 would make escalation cooperative: `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` in `main.go`; until then an app quit while a command runs should send SIGINT and wait, not SIGTERM. The app signs with team `6LH2JMGD3J` (automatic signing, `-allowProvisioningUpdates`).<br>**User actions:** none for C1. The Developer ID, notarization and Sparkle actions recorded under A3 still apply to C4/C5. |
| C2 | in progress | fa841175d4984089a0a529b971a03c739d7a4f40 | | |
| C3 | todo | | | |
| C4 | todo | | | |
| C5 | todo | | | |
