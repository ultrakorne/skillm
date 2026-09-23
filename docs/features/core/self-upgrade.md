# Core — Self-upgrade

## Overview

`upgrade` replaces the skillm binary itself and skips the git check. It changes nothing in Home
but the Refresh cache: when one exists, `cmd/upgrade.go` takes the Home lock just to record the
Self status it found or installed ([refresh-status](../refresh-status/TECHNICAL.md)); otherwise it
takes no lock. `cmd/upgrade.go` keeps only the prompt and the printing over three core calls in `internal/core/selfstatus.go`: `core.SelfMethod` (how this
binary is upgraded, judged offline), `core.CheckSelf` (the running version against the latest
release, as a `SelfStatus`) and `core.UpgradeSelf` (install the release `CheckSelf` found).
`internal/selfupdate` does the release lookup, download, checksum and swap. These rules matter
to any caller of the core calls, not only the CLI, so they are documented apart from the
Home-safety rules in [TECHNICAL.md](TECHNICAL.md).

## Noteworthy

### A bundled skillm is never replaced, and two layers refuse it

The skillm inside a macOS app bundle is upgraded by the app, which replaces the whole bundle;
swapping the binary alone would break the bundle's code signature. `core.UpgradeSelf` refuses a
`bundled` method with `core.ErrManagedByApp`, and `selfupdate.Apply` independently refuses a
target inside a bundle with `selfupdate.ErrBundled` before downloading anything, which
`UpgradeSelf` maps back to `ErrManagedByApp`. The second guard covers an executable that moved
into a bundle after `CheckSelf` judged it, and any caller of `Apply` that skips core.

### The method is judged offline, and `dev` wins over `bundled`

`core.SelfMethod` reads only the version and the executable path, so `skillm upgrade` refuses a
bundled skillm before any network request and gives the same answer offline; `--check` still
looks the release up and points at the app instead of `skillm upgrade`. A build whose version is
not a release tag is `dev` even inside a bundle (a debug app bundling a source build), and
`CheckSelf` then returns without a lookup, leaving `Latest` empty.

### Bundle detection reads the resolved path, lexically

The executable is judged after symlink resolution, so a command-line link such as
`/usr/local/bin/skillm` pointing into an app counts as bundled. `selfupdate.InBundle` matches any
`<name>.app` path component followed by `Contents`, case-insensitively (the default macOS
filesystem is) and with either separator; it touches no file, so every platform tests it.

### `Available` is not `Eligible`

`Available` means a newer release exists; `Eligible` means `skillm upgrade` can install it
(`Available` and method `binary`). A bundled skillm with a newer release is `Available` but not
`Eligible`: the app, not the CLI, acts on it.

### `UpgradeSelf` installs only what `CheckSelf` found

`SelfStatus` carries the resolved release unexported, so a status that did not come from
`CheckSelf` has nothing to install and `UpgradeSelf` returns `core.ErrNoUpgrade`. It also
refuses `dev` with `core.ErrSourceBuild`. Every refusal comes before anything is written.

### The swap never leaves the user without a skillm

The archive is verified against the release's `checksums.txt` before anything is written; the
new binary is staged beside the target (one filesystem), the old one renamed aside, and restored
if the final rename fails. A symlink that cannot be resolved fails the upgrade, since renaming
the link would report success and leave the real binary stale.

## Integration

`internal/core/selfstatus_test.go` pins the three methods with an injected executable path and a
stubbed release lookup; `internal/selfupdate/bundle_test.go` pins `InBundle` and `Apply`'s
refusal; `cmd/upgrade_bundle_test.go` builds a release-stamped binary inside a fake `.app`, runs
it with every request routed to a dead proxy, and checks that `upgrade` refuses without a lookup,
leaves the binary unchanged, and refuses through a symlink too.
