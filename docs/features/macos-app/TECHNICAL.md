# macOS App — Technical

## Architecture

`macos/project.yml` is an XcodeGen spec (the generated `Skillm.xcodeproj` is not checked in;
run `xcodegen generate` in `macos/`, then build or test the `Skillm` scheme). It has three
targets: the `Skillm` app, the `SkillmKit` static framework it links, and `SkillmKitTests`. The
app's "Embed skillm CLI" build phase runs `macos/scripts/build-cli.sh` on every build, which
builds the Go CLI for each architecture Xcode builds, joins the slices with `lipo`, writes the
result to `Contents/Helpers/skillm` and signs it with the app's identity and the hardened runtime.

Layering is one-way: `SkillmClient` (process and decoding, [client.md](client.md)) → `AppModel`
(the `@MainActor` `@Observable` state in `SkillmKit`, the client's only caller) → the SwiftUI
views in `macos/Skillm/`, which read the model and call its methods. The model runs at most one
command, its `operation`, and publishes it as `activity`; `RefreshScheduler` calls its scheduled
refresh, and `AppDelegate` owns the model, starts it after launch and holds a quit until it has
shut down; the windows' reads run beside it ([windows.md](windows.md)). `StatusIcon` draws the
status item, and `StatusSummary` and `UpdateProgress` word the menu: pure functions of decoded
data the tests check without a process. `SparkleUpdater` updates the app ([updates.md](updates.md)).

## Where things live

| File | Role |
|------|------|
| `macos/project.yml` | Targets, macOS 14 deployment, `LSUIElement`, hardened runtime without sandbox, team `6LH2JMGD3J`, `SKILLM_VERSION`, Sparkle |
| `macos/scripts/build-cli.sh` | The build phase: universal, version-stamped, signed CLI into `Contents/Helpers` |
| `macos/Skillm/SkillmApp.swift` | The `MenuBarExtra`, window and `Settings` scenes, the status item's label, and the delegate |
| `macos/Skillm/MenuContent.swift` | The menu: its one status line, the commands (with the update count), the windows, Stop, Quit |
| `macos/SkillmKit/AppModel.swift` | App state and the one command: launch, status, refresh, update, settings, the windows' reads and changes, shutdown, the updater's relaunch |
| `macos/SkillmKit/RefreshScheduler.swift` | The launch, hourly and wake ticks |
| `macos/SkillmKit/StatusIcon.swift` | The glyph, as a template, and the badged glyph with its red dot |
| `macos/SkillmKit/StatusSummary.swift` | The menu's status line for the Refresh cache and the notice for an update's outcome |
| `macos/SkillmKit/UpdateProgress.swift` | "Updating skills… n of m", folded from `update`'s events |
| `macos/SkillmKitTests/AppModelTests.swift` | The model against the fake: launch, ticks, notices, update, stop, shutdown, the toggle |
| `macos/SkillmKitTests/RefreshSchedulerTests.swift` | The scheduler with a hand-driven sleep: hourly ticks, wakes, stop |
| `macos/SkillmKitTests/StatusIconTests.swift` | Template or not, and the red dot drawn under both appearances |
| `macos/SkillmKitTests/StatusSummaryTests.swift` | The words for each cache and update outcome, and the progress count |
| `macos/SkillmKitTests/BundledCLITests.swift` | The real bundled CLI (or `$SKILLM_TEST_CLI`) against a temporary Home; skipped when no app was built |
| `macos/TestSupport/fake-skillm` | A shell skillm that answers from the fixtures; `FAKE_SKILLM_*` variables pick a mode, fail or hang one command, and log each run |

## Noteworthy

### One command at a time; a busy tick is dropped

A command starts only when none runs, and a scheduled tick that finds one running is dropped,
not queued: the next tick or wake asks again, and skillm judges Due anyway. The status re-read
after an update runs in a task of its own, so it still runs after a Stop (skillm has exited).

### Every tick asks, whatever the setting

A tick runs `config get`, then `refresh --if-due`, even with Auto check skill updates off: the setting is
part of Due, so skillm does nothing. Reading the settings on each tick is what makes the toggle
follow a `config set` made in a terminal, and retries a settings read that failed at launch.

### The hourly wait counts only awake time

`RefreshScheduler` sleeps on the suspending clock, and a wake cancels the hourly wait, ticks after
`wakeDelay` (30 s), then starts the wait over: a deadline that passed during sleep never ticks at
the wake itself, before the network is back. The sleep is injected so tests drive it by hand.

### The badged icon is not a template

A template image is drawn in one colour, so the red dot needs a non-template image. Its glyph is
tinted with `labelColor` and `cacheMode` is `.never`, so the colour resolves against the menu
bar's appearance at every draw; the plain glyph stays a template.

### A quit waits for skillm, and starts from the run loop

`applicationShouldTerminate` answers `.terminateLater` while a command runs (`.terminateCancel`
would cancel a logout or restart) and replies from a MainActor task once `shutdown` has
interrupted skillm and seen it exit. GCD does not drain the main queue re-entrantly, so a
`terminate` from a main-queue job would wait for that reply forever: every quit the app starts
goes through `AppDelegate.quit()`, which terminates from the run loop.

### A notice knows who caused it

A failure from the launch read or a scheduled tick is a background notice, cleared by the next
tick or `status` read that succeeds; a menu command's notice stays until the next menu command.
A failed re-read after an update keeps the update's outcome on screen.

### The menu's words follow the CLI's

An update reads "up to date" only when it did no work at all, as the terminal's "Everything is up
to date." does: a repaired copy, a removed missing install or an imported skill is work. A cache
row `error` or `untracked` is a problem line, never counted as current.

### The bundled CLI's version comes from a build setting

`build-cli.sh` stamps `SKILLM_VERSION` as `.goreleaser.yaml` does; `release.sh` sets it and
`MARKETING_VERSION` from the tag ([release.md](release.md)); a Release build at `dev` warns.
