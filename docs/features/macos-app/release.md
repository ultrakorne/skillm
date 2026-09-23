# macOS App — Release

## Overview

A release tag (`vX.Y.Z`) ships the app next to the CLI archives on the same GitHub release: the
notarized, stapled app as `skillm_<version>_macos_app.zip`, its `.sha256`, and the **Appcast**
installed apps read ([updates.md](updates.md)). One script builds it, the same on a Mac that
holds the credentials and in the release workflow's `macos` job; a second uploads it. A
pre-release (a tag with `-`) has no app: Sparkle and the Bundled CLI both need a clean `X.Y.Z`.

## Where things live

| File | Role |
|------|------|
| `macos/scripts/release.sh` | Archive, sign, notarize, staple, zip and write the appcast for one version; `--help` lists the credentials, the one-time setup and the CI secrets with their commands |
| `macos/scripts/publish-release.sh` | Upload the zip, its `.sha256` and `appcast.xml` to the tag's release, replacing earlier uploads |
| `macos/scripts/find-identity.sh` | The keychain's signing identity of a certificate kind and team |
| `macos/scripts/test-find-identity.sh` | Tests it against a stub `security`; the release workflow runs it first |
| `macos/scripts/verify-update-signature.swift` | Checks the appcast's EdDSA signature against the app's `SUPublicEDKey` |
| `.github/workflows/release.yml` | goreleaser, then the `macos` job (a throwaway keychain, `release.sh`, `publish-release.sh`), then `appcast-fallback` |

## Noteworthy

### Xcode builds, the script signs

The archive is built with `CODE_SIGNING_ALLOWED=NO`, then signed inside-out, never with
`--deep`: the Bundled CLI, Sparkle's XPC services, `Autoupdate` and `Updater.app`, Sparkle, then
the app, each with the Developer ID identity, the hardened runtime and a secure timestamp.
Signing the same way everywhere needs no provisioning or Xcode account on the CI runner. The
script refuses a `project.yml` with `CODE_SIGN_ENTITLEMENTS`, since its signing would drop them.

### The build is checked before anything ships

`release.sh` stops unless the bundle version and `CFBundleVersion` equal the tag (Sparkle
compares the latter), the feed is a `releases/latest/download/appcast.xml` URL, the public key
is 32 bytes, both binaries hold arm64 and x86_64, the Bundled CLI reports the tag, every Mach-O
carries the team, the hardened runtime and a timestamp, and the appcast's one item points at
this release's zip, with its size and a signature the app's key accepts.

### A dry run is never published

`--dry-run` signs with whatever identity exists (Apple Development without a Developer ID one),
skips notarization, allows a dirty tree and writes to `<version>-dry-run/` with a `DRY-RUN`
marker that `publish-release.sh` refuses.

### Notarization keeps its submission ID

`notarytool submit` returns at once and its ID is saved in `notarize/submit.json` before the
wait starts. The wait's timeout (40 minutes, `SKILLM_NOTARY_TIMEOUT`) stays inside the job's
90, so a slow notarization fails the script, which fetches the log, rather than being cancelled
by GitHub; the job keeps `notarize/*.json` either way.

### The appcast has one item

The feed is the latest release's asset, so an app only ever needs the newest version, and
`generate_appcast` runs on a folder holding the new zip alone. The private key comes from
`SPARKLE_ED_PRIVATE_KEY` in CI and from the login keychain (account `skillm`) on a Mac.

### The latest release can lack an appcast

goreleaser publishes the release, and makes it the latest, before the app job starts, because
`skillm upgrade` and `install.sh` read the latest release too. Until the job uploads, apps
fail their Sparkle check and ask again at the next status. If the job fails or is cancelled,
`appcast-fallback` copies the newest earlier release's `appcast.xml`, which still points at
that release's app; rerunning the `macos` job replaces it with the new one.

### The app follows the CLI release

`publish-release.sh` requires goreleaser's darwin archives and `checksums.txt` on the release:
the Bundled CLI reports a newer skillm, and the app asks Sparkle, only when its archive exists.

### Identities are matched as fixed strings

A Developer ID certificate's name ends with the team in parentheses; `find-identity.sh` matches
it with `grep -F`, since a regular expression would read the parentheses as a group.
