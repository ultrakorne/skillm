# Refresh and Status — Design

## Overview

Every skillm GUI (the macOS menu bar app, a Quickshell widget) shows one update badge, and they
must agree on when it is on. `skillm refresh` runs `check` and `upgrade --check` together and
writes the outcome to the Refresh cache in Home; `skillm status` reads the cache offline. The
shaping idea: the CLI owns the badge policy and the schedule, so a GUI runs `refresh --if-due`
as often as it likes and draws what the cache says.

## Surface

| Command | What it does | Network | Home lock |
|---------|--------------|---------|-----------|
| `refresh` | Checks every skill and the latest skillm release, rewrites the cache | yes | only while writing the cache |
| `refresh --if-due` | The same when a scheduled check is due; otherwise returns the cache unchanged | only when due | only when due |
| `status` | The cache as the last refresh (and later commands) left it, and whether it is stale | no; needs no git | no |

Both print a summary in the terminal and have a `--json` mode
([json-api commands](../json-api/commands.md)). The cache holds when it was checked, when the
next scheduled check is due, each skill's upstream status, the update count, the Self status,
every problem the check hit, and the Badge.

- **Badge** — on when a skill has an update available or a newer skillm release exists. A skill
  whose lookup failed, or whose subdir is gone upstream, is listed as a problem: it never turns
  the Badge on and never reads as up to date.
- **Due** — only while `refresh.enabled` is on, and then when no refresh ever ran, when the
  recorded next check time is reached, when `refresh.interval_hours` have passed since the last
  check (the interval may have been shortened since), or when the last check is dated in the
  future (the clock went back). The next check is one interval after a refresh, or one hour
  after it when every lookup failed (typically: offline).
- **Stale** — the cache is older than the interval, dated in the future, or never written.
  `status` still returns it; the GUI decides how to show its age.

## Flows

- **Scheduled check** — a GUI or a timer runs `refresh --if-due` whenever it likes; skillm checks
  only when due, and otherwise answers from the cache with no network request.
- **Clearing the badge** — `update` marks each skill it fetched up to date, `install` marks a
  skill it just fetched from its Source up to date, `uninstall` drops the skills it removed, and
  `upgrade` (including `upgrade --check`) rewrites the Self status, so the badge follows without
  another check. A skill whose update failed keeps its row: the update is still due. `import`,
  `agent` and `config` leave the cache alone; skills they bring in get a row at the next refresh.
- **Never refreshed** — `status` reports an empty, stale cache with the Badge off, and no command
  creates the file except `refresh`: a machine without a GUI never gets one.
- **Two refreshes at once** — the one that started later wins, whichever finishes first.
- **The app and a terminal skillm on one Home** — each reads the Self status judged for itself,
  offline, and neither makes a check due for the other.

## Decisions

- **One badge policy in the CLI** — two GUIs reimplementing it would disagree; the CLI computes
  the Badge once and every reader shows it.
- **A cache file beside the commands** — a widget can watch `status.json` without running
  anything, so the file is a contract like the JSON protocol, and every write replaces it in one
  step so a watcher never reads half of it.
- **A failed lookup is never "current"** — a check that could not run says nothing about
  upstream, so it stays an error; a badge off because the network was down would hide updates.
- **The CLI decides when a check is due** — the interval lives in Config, where `config set`
  changes it, so every GUI and timer follows one schedule and none can check in a loop.
- **Which skillm wrote the cache never makes a check due** — the app's bundled skillm and a
  terminal install at another version share one Home; re-judging the Self status offline is
  enough, and a version test would make each trigger a full check for the other.
- **Commands keep the cache current instead of re-checking** — after `update` the facts are
  known, so a network pass to clear the badge would only add latency and failure modes.
