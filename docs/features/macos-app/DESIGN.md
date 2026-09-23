# macOS App — Design

## Overview

A native menu bar app for people who want skillm's update badge and commands without a
terminal. It is a view, not a second implementation: every action runs the skillm command-line
tool in JSON mode and shows what it answers, so the app and a future Linux widget share all
behaviour through the one CLI. The app ships its own copy of that tool, the **Bundled CLI**, so
the two always speak the same protocol version.

## Surface

- **Status item** — a monochrome books glyph that follows the light or dark menu bar, with a red
  dot while the Badge is on (a skill update or a newer skillm). No Dock icon, no main window.
- **Menu** — "Starting…" during the launch checks, or the problem and its fix when the CLI
  cannot be used. Once ready: what the last check found (updates available or "All skills are up
  to date", skills that could not be checked, a newer skillm, and "Last checked at 10:00"); a
  notice with the last command's outcome or failure; the running command ("Updating skills… 2 of
  5"); then **Refresh** (⌘R), **Auto refresh** ✓, **Update all skills** (⌘U), **Stop** while a
  check or an update runs, the windows, and always **Quit skillm** (⌘Q).
- **Windows** — **View skills…** (Update, Uninstall), **Add skill…** from a repository or folder,
  and **Settings…** (⌘,) with Start at login and the command-line tool ([windows.md](windows.md)).

## Flows

- **Launch** — the app finds the Bundled CLI, checks its API version and that a usable git is on
  the path skillm will get, reads the Refresh cache and runs the first scheduled check. A failed
  launch check becomes the menu's problem line and fix: the CLI is missing (reinstall), speaks an
  unknown API version (reinstall so both match), answers with something that is not the protocol
  (its error output is shown), or no usable git (install Apple's Command Line Tools with
  `xcode-select --install`, or git with Homebrew, then relaunch).
- **Scheduled check** — at launch, every hour the Mac is awake, and 30 seconds after it wakes,
  the app re-reads the settings and runs `refresh --if-due`; skillm checks only when one is Due.
- **Refresh** — checks every skill and skillm itself now, due or not.
- **Auto refresh** — writes `refresh.enabled` through `config set`; turning it on runs a
  scheduled check at once. A change made in a terminal shows after the next scheduled check.
- **Update all skills** — runs `update` with events, counts skills done in the menu, then says
  what the run did (updated, repaired, removed missing installs, imported, failed, sources gone)
  and re-reads the cache, so the dot clears without another check.
- **Stop and Quit** — both interrupt skillm the way Ctrl-C does and wait for it to finish its
  current write and exit ("Stopping…" meanwhile), so the app never reads Home half-written or
  while skillm holds the Home lock ([FLOW.mermaid](FLOW.mermaid)).

## Decisions

- **A subprocess over the Bundled CLI** — the CLI is already the tested surface, and running it
  keeps Go free of cgo and the release pipeline unchanged ([JSON API](../json-api/DESIGN.md)).
- **The CLI lives inside the bundle** — a CLI under the app's `Contents/` judges its Upgrade
  method as bundled, so `skillm upgrade` refuses to swap it and the app upgrades both together.
- **An unknown API version is refused at launch** — one clear "reinstall" beats commands that
  fail one by one; unknown error codes, statuses and event types from a newer CLI still decode.
- **Git is checked by the app too** — without the Command Line Tools, `/usr/bin/git` is only an
  installer stub skillm cannot tell apart; the app judges it and puts Homebrew's folders first.
- **The app asks, skillm decides** — ticks only ask whether a check is Due, so the app follows
  the interval and the Auto refresh setting wherever they were changed and cannot check in a loop.
- **One command at a time** — the items grey out while one runs; a scheduled check that falls
  due meanwhile waits for the next tick instead of queueing behind it.
- **The wake check waits 30 seconds** — the network is often not back at the wake itself, and a
  check whose every lookup failed is retried only an hour later.
- **Check times are absolute** — "at 10:00", never "5 minutes ago", which an open menu would
  leave stale.
- **Background failures clear themselves** — a failed launch read or scheduled check shows until
  the next one succeeds; the outcome of a command the user chose stays until they choose another.
- **A cancel never cuts a write short** — the hard stop that follows only after a long grace is
  a guard against a hung process, since skillm cannot clean up after it.
- **No sandbox, hardened runtime; macOS 14, signed by the team** — skillm writes agents' folders,
  projects and Home and runs git, which the App Sandbox forbids; notarization needs the hardened
  runtime, the menu bar APIs need macOS 14, and the team signature lets colleagues' Macs run it.
- **A debug build finds a locally built CLI** — so the app runs against a CLI built from the same
  checkout without rebuilding the bundle; a release build only ever runs the Bundled CLI.
