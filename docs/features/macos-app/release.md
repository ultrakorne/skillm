# macOS App — Release

## Overview

The app ships as an **App release** of its own, apart from the CLI: a tag `mac-vX.Y.Z` with its
own version, whose GitHub release holds the notarized, stapled app twice: in
`skillm_<version>_macos_app.dmg`, the disk image people download, and in
`skillm_<version>_macos_app.zip`, which Sparkle updates from, each with its `.sha256`, while the **Appcast** installed apps read is
replaced on the fixed `macos-appcast` release ([updates.md](updates.md)). One script builds it on
a Mac that holds the credentials; a second uploads it. There is no CI job for the app. The CLI
keeps its `vX.Y.Z` tags and goreleaser's CI job, unchanged, and only it is GitHub's "latest".

## Where things live

| File | Role |
|------|------|
| `macos/scripts/release.sh` | Archive, sign, notarize, staple, pack the disk image and the zip, and write the appcast for one `mac-vX.Y.Z`; `--help` lists the credentials and the one-time setup with their commands |
| `macos/scripts/publish-release.sh` | Create the `mac-vX.Y.Z` release (never latest) and upload the disk image, the zip and their `.sha256`s, then replace `appcast.xml` on `macos-appcast` |
| `macos/scripts/find-identity.sh` | The keychain's signing identity of a certificate kind and team |
| `macos/scripts/test-find-identity.sh` | Tests it against a stub `security` |
| `macos/scripts/verify-update-signature.swift` | Checks the appcast's EdDSA signature against the app's `SUPublicEDKey` |
| `.github/workflows/release.yml` | The CLI's goreleaser job, for `v*` tags only |

## Noteworthy

### Xcode builds, the script signs

The archive is built with `CODE_SIGNING_ALLOWED=NO`, then signed inside-out, never with
`--deep`: Sparkle's XPC services, `Autoupdate` and `Updater.app`, Sparkle, then the app, each
with the Developer ID identity, the hardened runtime and a secure timestamp. Signing this way
needs no provisioning profile or Xcode account. The script refuses a `project.yml` with
`CODE_SIGN_ENTITLEMENTS`, since its signing would drop them.

### The build is checked before anything ships

`release.sh` stops unless the tag is `mac-vX.Y.Z` (a `vX.Y.Z` is the CLI's), the bundle version
and `CFBundleVersion` equal it (Sparkle compares the latter), the feed is the `macos-appcast`
URL, the public key is 32 bytes, the app has no `Contents/Helpers` but has `install.sh`, holds
arm64 and x86_64, every Mach-O carries the team, the hardened runtime and a timestamp, and the
appcast's one item points at this release's zip, with its size and a signature the app accepts.

### A dry run is never published

`--dry-run` signs with whatever identity exists (Apple Development without a Developer ID one),
skips notarization, allows a dirty tree and writes to `<version>-dry-run/` with a `DRY-RUN`
marker that `publish-release.sh` refuses.

### A release can skip notarization

`--no-notarize` builds a publishable release without a Developer ID: it signs with Apple
Development when no Developer ID identity exists, skips notarization, and leaves a
`NOT-NOTARIZED` marker, from which `publish-release.sh` adds the Open Anyway steps to the release
notes. It still needs a clean checkout of the tag and the Sparkle key. macOS blocks such an app on
its first launch until the user allows it in Privacy & Security; Sparkle's updates carry no
quarantine, so later versions open without asking.

### The download is a disk image, the update a zip

The disk image holds the stapled app beside an `Applications` link, the usual drag-to-install
window. It is signed with the same identity and, unless `--no-notarize` or `--dry-run`,
notarized and stapled on its own, so it opens offline. The appcast keeps pointing at the zip,
which Sparkle installs from without mounting anything.

### Notarization keeps its submission ID

`notarytool submit` returns at once and its ID is saved in `notarize/<app|dmg>-submit.json` before the
wait starts. After the wait's timeout (40 minutes, `SKILLM_NOTARY_TIMEOUT`) the script fails
and fetches the log; `notarize/*.json` stays either way.

### The appcast has one item, and the feed never names a missing zip

An app only ever needs the newest version, so `generate_appcast` runs on a folder holding the
new zip alone. `publish-release.sh` uploads the zip to its release before it replaces the feed.
The private key comes from the login keychain (account `skillm`), or `SPARKLE_ED_PRIVATE_KEY`.

### The app's tags stay out of the CLI's releases

App releases are created with `--latest=false`, since `install.sh` and `skillm upgrade` read the
latest release. goreleaser would take the nearest tag of any name as the previous release, so
the workflow names the previous `v*` tag itself and the CLI's changelog skips `mac-v*` and
`macos-appcast`.

### Release the CLI first when the app needs a newer one

Install skillm CLI and Upgrade skillm CLI fetch the latest CLI release. An app whose API version
only an unreleased CLI speaks would find that release too old (the menu then says so), so a
breaking CLI change is released before, or with, the app that needs it.

### Identities are matched as fixed strings

A Developer ID certificate's name ends with the team in parentheses; `find-identity.sh` matches
it with `grep -F`, since a regular expression would read the parentheses as a group.
