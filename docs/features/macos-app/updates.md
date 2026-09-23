# macOS App — Upgrade and restart

## Overview

The app updates itself with Sparkle 2, which replaces the whole bundle, the **Bundled CLI**
included (the CLI's own `skillm upgrade` refuses inside a bundle). Sparkle never checks on its
own: the app asks it after a Refresh that found a newer skillm, and the menu's **Upgrade and
restart** item appears once Sparkle has found the update, then opens Sparkle's standard window,
which installs it and relaunches the app. It is documented apart because it ties the app to the
release pipeline: the appcast, its signing key and the bundle version.

## Where things live

| File | Role |
|------|------|
| `macos/Skillm/SparkleUpdater.swift` | Sparkle's controller and delegate: the silent check, the install, the postponed relaunch and the recovery from a failed one |
| `macos/SkillmKit/AppUpgrade.swift` | `UpdateFeed` (the Info.plist's feed and key) and `AppUpgrade`: when Sparkle is asked and when the item shows |
| `macos/Skillm/Info.plist` | `SUFeedURL` and `SUPublicEDKey` from build settings, automatic checks off; merged into the generated Info.plist |
| `macos/project.yml` | The pinned Sparkle package, the feed URL, the public key, the empty Debug feed, the bundle version |
| `macos/SkillmKitTests/AppUpgradeTests.swift` | The feed checks and when the updater is asked, against a fake updater |

`AppModelTests` cover the postponed relaunch and the resume after a failed install;
`BundledCLITests` check that the built app embeds Sparkle with a valid key and a bundle version
that follows the release version.

## Noteworthy

### Sparkle is asked when the CLI's check says so

`SUEnableAutomaticChecks` is off, so the Auto refresh setting and interval cover both checks.
`AppUpgrade` asks Sparkle silently, once per `checked_at`, when the cache's self entry says a
newer skillm exists, or when the bundled CLI's own release lookup failed (a GitHub API error
must not hide a valid appcast). The item waits for Sparkle's answer: a GitHub release does not
mean its appcast and signed app were uploaded yet.

### A check that did not happen is asked again

A check Sparkle did not start (another session was running) or that failed (offline, no
appcast) does not count for its `checked_at`: the next status that arrives, the next hourly tick
at the latest, asks again. "No update" and a cancelled install are not failures.

### Skip This Version keeps the item

Sparkle's silent checks never report a skipped version, but a check the user starts does. So
Skip only stops Sparkle's reminders: the item stays and still installs that version, and the
menu's dot, which comes from the CLI's cache, has an action until the next release.

### The relaunch waits for skillm

Sparkle's relaunch is always postponed: `AppModel.postponeRelaunch` stops the schedule,
interrupts every running skillm and waits for it to exit, then lets Sparkle go on. Its installer
quits the app with an ordinary quit, which finds nothing running. If the install fails after
that, Sparkle ends its session with an error and `resumeAfterAbortedUpdate` restarts the model
and its schedule, with a notice that the update could not be installed.

### Sparkle compares CFBundleVersion

Sparkle orders versions by `CFBundleVersion` against the appcast's `sparkle:version`, which
`generate_appcast` copies from the released app's `CFBundleVersion`. `CURRENT_PROJECT_VERSION`
follows `MARKETING_VERSION`, so a release build that sets `MARKETING_VERSION` (and
`SKILLM_VERSION`) from its tag is newer than the last one, and a local `0.0.0` build is older
than every release.

### A debug build never updates itself

Debug empties the feed URL, so `UpdateFeed` is nil and Sparkle never starts: a developer build
is never replaced by the latest release. `UpdateFeed` also refuses a key that is not a 32-byte
Ed25519 key, so a placeholder cannot ship an updater that trusts nothing. `project.yml` says
which settings a debug build needs to try Sparkle.

### The feed and its key

The appcast is `releases/latest/download/appcast.xml` of the GitHub repository, so every release
must upload one. The public key in `project.yml` is the one `generate_keys --account skillm`
printed; its private key stays in the release machine's login keychain.

## Integration

`AppDelegate` starts `SparkleUpdater` before the model, so the launch's status read can ask it;
every status the model stores reaches `AppUpgrade.statusChanged`; `MenuContent` shows the item
next to the window items. The installer's quit reaches `applicationShouldTerminate` like any
other quit ([TECHNICAL.md](TECHNICAL.md)).
