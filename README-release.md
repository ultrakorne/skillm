# Releasing

The CLI and the macOS app are versioned and released separately. Release from a clean, up to
date `master`.

## CLI (`vX.Y.Z`)

```sh
git checkout master && git pull
git tag v0.6.0 && git push origin v0.6.0     # CI (goreleaser) builds the release
```

The CLI release is GitHub's "latest": `install.sh` and `skillm upgrade` read it.

## macOS app (`mac-vX.Y.Z`)

```sh
git checkout master && git pull
git tag mac-v1.0.0 && git push origin mac-v1.0.0
macos/scripts/release.sh --no-notarize mac-v1.0.0   # build, sign, dmg + zip, appcast
macos/scripts/publish-release.sh mac-v1.0.0         # GitHub release + update feed
```

- The app drives the installed CLI; its `api_version` must match. When a CLI change breaks the
  app, release both: the CLI first, then the app.
- Needs `gh auth login` and the Sparkle key in the login keychain (account `skillm`).
  Back it up: `macos/build/SourcePackages/artifacts/sparkle/Sparkle/bin/generate_keys --account skillm -x sparkle_key.txt`.
- Installed apps update from the `macos-appcast` release's `appcast.xml`, which each publish
  replaces.

### Installing (not notarized)

Open the `.dmg`, drag skillm to Applications and open it. When macOS blocks it: System Settings →
Privacy & Security → **Open Anyway** (or `xattr -dr com.apple.quarantine /Applications/skillm.app`).
Updates from the app's menu open without asking. Without a CLI, the menu offers **Install
skillm CLI**.

### Notarized (with a Developer ID)

Once the keychain has a "Developer ID Application" certificate for team 6LH2JMGD3J and a
notary profile (`xcrun notarytool store-credentials skillm-notary --apple-id <apple-id> --team-id 6LH2JMGD3J`),
drop `--no-notarize`: the app then opens with no prompt.
