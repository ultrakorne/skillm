# macOS App — Design

## Overview

A native menu bar app for people who want skillm's update badge and commands without a
terminal. It is a view, not a second implementation: every action runs the skillm command-line
tool in JSON mode and shows what it answers, so the app and a future Linux widget share all
behaviour through the one CLI. The app ships its own copy of that tool, the **Bundled CLI**, so
the two always speak the same protocol version.

## Surface

- **Status item** — a monochrome books glyph in the menu bar that follows the light or dark
  menu bar. The app has no Dock icon and no main window.
- **Menu** — "Starting…" while the launch checks run; nothing extra once the CLI is ready; the
  problem and how to fix it when the CLI cannot be used; and always **Quit skillm** (⌘Q).

## Flows

- **Launch** — the app finds the Bundled CLI, asks it for its version and API version, and
  checks that a usable git is on the path skillm will get. Any failure becomes the menu's
  problem line and fix.
- **Problems the menu names** — the CLI is missing from the app (reinstall); it speaks an API
  version the app does not know (reinstall so both match); it answers with something that is not
  the protocol (its error output is shown); no usable git (install Apple's Command Line Tools
  with `xcode-select --install`, or git with Homebrew, then relaunch).
- **Cancelling a command** — the app interrupts skillm the way Ctrl-C does and waits for it to
  finish its current write and exit before going on, so it never reads Home half-written or
  while skillm still holds the Home lock ([FLOW.mermaid](FLOW.mermaid)).

## Decisions

- **A subprocess over the Bundled CLI** — the CLI is already the tested surface, and running it
  keeps Go free of cgo and the release pipeline unchanged ([JSON API](../json-api/DESIGN.md)).
- **The CLI lives inside the bundle** — a CLI under the app's `Contents/` judges its Upgrade
  method as bundled, so `skillm upgrade` refuses to swap it and the app upgrades both together.
- **An unknown API version is refused at launch** — better one clear "reinstall" message than
  commands that fail one by one on data the app cannot decode. Unknown error codes, statuses and
  event types from a newer CLI still decode, so a new code never breaks an older app.
- **Git is checked by the app too** — a Mac without the Command Line Tools has a `git` that is
  only an installer stub; skillm sees a git on the path, so the app judges the stub itself and
  names the fix before any command needs git.
- **Homebrew's tools come first** — an app starts with a minimal PATH, where Apple's stub would
  always win; the app puts Homebrew's folders ahead of it, as a login shell would.
- **A cancel never cuts a write short** — skillm stops cleanly on an interrupt; the hard stop
  that follows only after a long grace is a guard against a hung process, since skillm cannot
  clean up after it.
- **No sandbox, hardened runtime** — skillm writes agents' folders, projects and Home and runs
  git, which the App Sandbox forbids; the hardened runtime is what notarization needs.
- **macOS 14 or later, signed by the team** — the menu bar and settings APIs the app builds on
  need macOS 14; the app is signed with Starberry Games' team so colleagues' Macs accept it.
- **A debug build finds a locally built CLI** — so the app can run against a CLI built from the
  same checkout without rebuilding the bundle; a release build only ever runs the Bundled CLI.
