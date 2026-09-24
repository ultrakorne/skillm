# macOS App — Upgrade app and restart

## Overview

The app updates itself with Sparkle 2, which replaces the app bundle; the **Installed CLI** is
not part of it and upgrades on its own (`skillm upgrade`, the menu's Upgrade skillm CLI). Sparkle
never checks on its own schedule: the app asks it silently, and the menu's **Upgrade app and
restart** item appears once Sparkle has found a newer app, then opens Sparkle's standard window,
which installs it and relaunches the app. While the CLI is too new or broken, **Check for app
update** opens the same window. It is documented apart because it ties the app to the App
release pipeline ([release.md](release.md)): the appcast, its signing key and the bundle version.

## Where things live

| File | Role |
|------|------|
| `macos/Skillm/SparkleUpdater.swift` | Sparkle's controller and delegate: the silent check, the install, the postponed relaunch and the recovery from a failed one |
| `macos/SkillmKit/AppUpgrade.swift` | `UpdateFeed` (the Info.plist's feed and key) and `AppUpgrade`: when Sparkle is asked and when the item shows |
| `macos/Skillm/Info.plist` | `SUFeedURL` and `SUPublicEDKey` from build settings, automatic checks off; merged into the generated Info.plist |
| `macos/project.yml` | The pinned Sparkle package, the feed URL, the public key, the empty Debug feed, the bundle version |
| `macos/SkillmKitTests/AppUpgradeTests.swift` | The feed checks and when the updater is asked, against a fake updater |

`AppModelTests` and `CLIStateTests` cover when the model asks (a missing or broken CLI and
failing refreshes included), the postponed relaunch and the resume after a failed install;
`RealCLITests` check that the built app embeds Sparkle with a valid key and no CLI.

## Noteworthy

### Sparkle is asked on the app's schedule, never the CLI's

`SUEnableAutomaticChecks` is off. `AppUpgrade` asks Sparkle silently at launch, on a tick once
the refresh interval (a day while the settings are unknown) has passed since the last ask, and
after the Refresh item, whether or not the CLI works: a CLI that is missing, too new or broken
must never hide the app release that fixes it. With Auto check off and the CLI ready, only
launch and Refresh ask. Nothing in the Refresh cache says whether a newer app exists.

### A check that did not happen is asked again

A check Sparkle did not start (another session was running) or that failed (offline, no
appcast) does not count: the next tick asks again. "No update" and a cancelled install are not
failures.

### Skip This Version keeps the item

Sparkle's silent checks never report a skipped version, but a check the user starts does. So
Skip only stops Sparkle's reminders: the item stays and still installs that version, and the
menu's dot stays until then.

### The relaunch waits for skillm

Sparkle's relaunch is always postponed: `AppModel.postponeRelaunch` stops the schedule,
interrupts every running skillm and waits for it to exit, then lets Sparkle go on. Its installer
quits the app with an ordinary quit, which finds nothing running. If the install fails after
that, Sparkle ends its session with an error and `resumeAfterAbortedUpdate` restarts the model
and its schedule, with a notice that the update could not be installed.

### Sparkle compares CFBundleVersion

Sparkle orders versions by `CFBundleVersion` against the appcast's `sparkle:version`, which
`generate_appcast` copies from the released app's `CFBundleVersion`. `CURRENT_PROJECT_VERSION`
follows `MARKETING_VERSION`, which `release.sh` sets from the `app-vX.Y.Z` tag, so a release is
newer than the last one and a local `0.0.0` build older than every release.

### A debug build never updates itself

Debug empties the feed URL, so `UpdateFeed` is nil and Sparkle never starts: a developer build
is never replaced by the latest release. `UpdateFeed` also refuses a key that is not a 32-byte
Ed25519 key, so a placeholder cannot ship an updater that trusts nothing. `project.yml` says
which settings a debug build needs to try Sparkle.

### The feed and its key

The appcast lives on the fixed `macos-appcast` GitHub release
(`releases/download/macos-appcast/appcast.xml`), which every App release replaces; it is never
GitHub's latest release, which stays the CLI's. The public key in `project.yml` is the one
`generate_keys --account skillm` printed; its private key stays in the release Mac's login
keychain (keep a backup). Losing it means no signed update reaches installed apps.

## Integration

`AppDelegate` attaches `SparkleUpdater` before the model starts, so the launch asks it; the
model's ticks and the Refresh item call `AppUpgrade`; `MenuContent` shows the item in both the
ready and the not-ready menu. The installer's quit reaches `applicationShouldTerminate` like any
other quit ([TECHNICAL.md](TECHNICAL.md)).
