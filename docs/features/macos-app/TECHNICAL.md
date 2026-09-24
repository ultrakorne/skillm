# macOS App — Technical

## Architecture

`macos/project.yml` is an XcodeGen spec (the generated `Skillm.xcodeproj` is not checked in;
run `xcodegen generate` in `macos/`, then build or test the `Skillm` scheme). It has three
targets: the `Skillm` app, the `SkillmKit` static framework it links, and `SkillmKitTests`. The
app carries no CLI, only the repository's `install.sh` as a resource for Install skillm CLI.

Layering is one-way: `SkillmClient` (process and decoding, [client.md](client.md)) → `AppModel`
(the `@MainActor` `@Observable` state in `SkillmKit`, the client's only caller) → the SwiftUI
views in `macos/Skillm/`. The model holds the CLI's state (`cli`: starting, ready, missing, too
old, too new, failed), runs at most one command (`operation`, published as `activity`) and, apart,
the launch checks and the work on the CLI itself (`cliTask`); `RefreshScheduler` ticks it, and
`AppDelegate` owns it and holds a quit until it has shut down; the windows' reads run beside the
one command ([windows.md](windows.md)). `StatusIcon` draws the
status item, and `StatusSummary` and `UpdateProgress` word the menu: pure functions of decoded
data the tests check without a process. `SparkleUpdater` updates the app ([updates.md](updates.md)).

## Where things live

| File | Role |
|------|------|
| `macos/project.yml` | Targets, macOS 14 deployment, `LSUIElement`, hardened runtime without sandbox, team `6LH2JMGD3J`, `install.sh`, Sparkle |
| `macos/Skillm/SkillmApp.swift` | The `MenuBarExtra`, window and `Settings` scenes, the status item's label, and the delegate |
| `macos/Skillm/MenuContent.swift` | The menu: its one status line, the commands (with the update count), both upgrades, the CLI's problem and fix, Stop, Quit |
| `macos/SkillmKit/AppModel.swift` | App state and the one command: the CLI's state and its fixes, status, refresh, update, settings, the windows' reads and changes, shutdown, the updater's relaunch |
| `macos/SkillmKit/CommandLineTool.swift` | Runs `install.sh` and a plain `skillm upgrade`, outside the protocol |
| `macos/SkillmKit/RefreshScheduler.swift` | The launch, hourly and wake ticks |
| `macos/SkillmKit/StatusIcon.swift` | The glyph, as a template, and the badged glyph with its red dot |
| `macos/SkillmKit/StatusSummary.swift` | The menu's status line for the Refresh cache and the notice for an update's outcome |
| `macos/SkillmKit/UpdateProgress.swift` | "Updating skills… n of m", folded from `update`'s events |
| `macos/SkillmKitTests/AppModelTests.swift` | The model against the fake: launch, ticks, notices, update, stop, shutdown, the toggle |
| `macos/SkillmKitTests/CLIStateTests.swift` | The fake found by the real lookup in a temporary folder: missing, too old, too new, installed or changed in a terminal, both upgrades |
| `macos/SkillmKitTests/RefreshSchedulerTests.swift` | The scheduler with a hand-driven sleep: hourly ticks, wakes, stop |
| `macos/SkillmKitTests/StatusIconTests.swift` | Template or not, and the red dot drawn under both appearances |
| `macos/SkillmKitTests/StatusSummaryTests.swift` | The words for each cache and update outcome, and the progress count |
| `macos/SkillmKitTests/RealCLITests.swift` | A CLI built from the checkout (or `$SKILLM_TEST_CLI`) against a temporary Home, and the built app's Sparkle key and missing CLI |
| `macos/TestSupport/fake-skillm` | A shell skillm that answers from the fixtures; `FAKE_SKILLM_*` variables pick a mode, fail or hang one command, and log each run |

## Noteworthy

### One command at a time; a busy tick is dropped

A command starts only when none runs, and a scheduled tick that finds one running is dropped,
not queued: the next tick or wake asks again, and skillm judges Due anyway. The status re-read
after an update runs in a task of its own, so it still runs after a Stop (skillm has exited).

### Every tick asks, whatever the setting

A tick runs `config get`, then `refresh --if-due`, even with Auto check off (the setting is part
of Due): so the toggle follows a terminal `config set`, and a failed launch read is retried.

### The hourly wait counts only awake time

`RefreshScheduler` sleeps on the suspending clock; a wake cancels the hourly wait, ticks after
`wakeDelay` (30 s), then starts it over, so no tick fires at the wake itself, before the network.

### The badged icon is not a template

A template is drawn in one colour, so the red dot needs a non-template image: its glyph is tinted
with `labelColor`, `cacheMode` `.never`, so it follows the menu bar's appearance at every draw.

### A quit waits for skillm, and starts from the run loop

`applicationShouldTerminate` answers `.terminateLater` while a command runs (`.terminateCancel`
would cancel a logout or restart) and replies from a MainActor task once `shutdown` has
interrupted skillm and seen it exit. GCD does not drain the main queue re-entrantly, so a
`terminate` from a main-queue job would wait for that reply forever: every quit the app starts
goes through `AppDelegate.quit()`, which terminates from the run loop. `install.sh` and a plain
`skillm upgrade` have no cancel path, so `shutdown` waits for them, two minutes at most.

### A notice knows who caused it

A failure from the launch read or a scheduled tick is a background notice, cleared by the next
tick or `status` read that succeeds; a menu command's notice stays until the next menu command.
A failed re-read after an update keeps the update's outcome on screen.

### The menu's words follow the CLI's

An update reads "up to date" only when it did no work at all, as the terminal's "Everything is up
to date." does: a repaired copy, a removed missing install or an imported skill is work. A cache
row `error` or `untracked` is a problem line, never counted as current.

### The CLI is watched through its file

While the CLI is ready, every tick and the end of every command compare its file (resolved
through symlinks: path, inode, size, modification time) with the one the launch checks found; a
change runs the launch checks again. A terminal upgrade, a Homebrew relink or a removal thus
turns the menu into too new, too old or missing without a relaunch, at the cost of a `stat`.
