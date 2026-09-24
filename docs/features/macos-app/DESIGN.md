# macOS App — Design

## Overview

A native menu bar app for people who want skillm's update badge and commands without a
terminal. It is a view, not a second implementation: every action runs the **Installed CLI**,
the skillm the user installed on their own, in JSON mode and shows what it answers, so all
behaviour stays in the one CLI. The app and the CLI are released apart; the API version says
whether they fit, and a CLI change that breaks the app ships with an app update.

## Surface

- **Status item** — a monochrome books glyph that follows the light or dark menu bar, with a red
  dot while the Badge is on (a skill update or a newer CLI) or a newer app was found. No Dock
  icon, no main window.
- **Menu, CLI ready** — one line at most: the running command ("Refreshing skills…", "Updating
  skills… 2 of 5"), else the last command's failure, else what the last check found, else the
  last command's outcome. Then **Refresh** (⌘R), **Update all skills (2)** (⌘U), **Stop** while a
  check or an update runs, **Upgrade skillm CLI to X** when a newer CLI can be installed,
  **Upgrade app and restart** once a newer app was found, the windows, and **Quit skillm** (⌘Q).
- **Menu, CLI not ready** — "Starting…" during the launch checks; otherwise what is wrong and its
  fix: not installed (**Install skillm CLI**), too old (**Upgrade skillm CLI**), too new for this
  app, or broken (**Check for app update**), with **Upgrade app and restart** whenever a newer app
  was found, **Check Again**, Settings and Quit.
- **Windows** — **View skills…** (Update, Uninstall), **Add skill…** from a repository or folder,
  and **Settings…** (⌘,) with Start at login, Auto check skill updates and the command-line tool
  ([windows.md](windows.md)).

## Flows

- **Launch** — the app finds the CLI (the usual install folders, then the login shell's PATH),
  checks its API version and that a usable git is on the path skillm will get, asks for an app
  update, reads the Refresh cache and runs the first scheduled check. Without a usable git the
  menu says how to get one (`xcode-select --install`, or Homebrew), then relaunch.
- **Install or upgrade the CLI** — Install runs skillm's own `install.sh` (the latest release);
  Upgrade runs `skillm upgrade`. Either way the launch checks run again; a latest release still
  too old for the app says so.
- **The CLI changes in a terminal** — each tick looks again: a CLI installed while missing, or
  upgraded, replaced or removed while in use, is picked up without a relaunch, and a too-new one
  turns the menu into "This app is too old…" with Check for app update.
- **Scheduled check** — at launch, every hour the Mac is awake, and 30 seconds after it wakes,
  the app re-reads the settings and runs `refresh --if-due`; skillm checks only when one is Due.
- **Refresh** — checks every skill and the CLI now, due or not, and asks for an app update.
  **Auto check skill updates** (Settings) turns the scheduled check on or off (`config set`).
- **Update all skills** — runs `update` with events, counts skills done, then says what the run
  did and re-reads the cache, so the dot clears without another check.
- **Upgrade app and restart** — Sparkle's window installs the new app and relaunches once skillm
  has exited ([updates.md](updates.md)).
- **Stop and Quit** — both interrupt skillm the way Ctrl-C does and wait for it to finish its
  current write and exit ("Stopping…" meanwhile) ([FLOW.mermaid](FLOW.mermaid)).

## Decisions

- **A subprocess over the user's own CLI** — the CLI is already the tested surface, and running
  it keeps Go free of cgo. Driving the installed one means one skillm on the Mac, upgraded one way.
- **The API version is the contract** — an unknown API version (or a newer document schema) is
  refused at launch with the fix that matches its direction; unknown codes and events decode.
- **App updates never depend on the CLI** — the app asks its updater on its own schedule, so a
  CLI that is missing, too new or broken never hides the app release that fixes it.
- **Git is checked by the app too** — without the Command Line Tools, `/usr/bin/git` is only an
  installer stub skillm cannot tell apart; the app judges it and puts Homebrew's folders first.
- **The app asks, skillm decides** — ticks only ask whether a check is Due, so the app follows
  the interval and the Auto check setting wherever they were changed and cannot check in a loop.
- **One command at a time** — the items grey out while one runs; a scheduled check that falls
  due meanwhile waits for the next tick instead of queueing behind it.
- **No sandbox, hardened runtime; macOS 14, signed by the team** — skillm writes agents' folders,
  projects and Home and runs git, which the App Sandbox forbids; the team signature lets
  colleagues' Macs run it.
- **A debug build can run a CLI of the checkout and never updates itself** — `$SKILLM_BIN` points
  it at one; only a release build asks Sparkle.
