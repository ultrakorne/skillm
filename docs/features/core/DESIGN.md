# Core — Design

## Overview

The core is the skillm program behind every command: it owns Home (Config and the Registry)
and every skill install on the machine, and each command is one operation on that state. Its
shaping idea: several skillm processes can run at once (two terminals, a background script)
and must never corrupt Home or each other's installs.

## Surface

| Command | Changes Home and installs | Holds the Home lock |
|---------|---------------------------|---------------------|
| `install` | yes | yes, while it writes |
| `update` | yes | yes, while it writes |
| `uninstall` | yes | yes, while it writes |
| `import` | yes | yes, while it writes |
| `agent` | yes | yes, while it writes |
| `list`, `check`, `version` | no | no |
| `upgrade` | no (replaces the skillm binary only) | no |

`--home` or `$SKILLM_HOME` points any command at a different Home; the default is `~/.skillm`.

## Flows

- **A changing command** — asks its questions and fetches with Home free, then takes the Home
  lock, re-reads Config and the Registry, writes, saves, and releases the lock when it ends.
- **Updating and importing** — skillm fetches every source with Home free, then takes the lock,
  re-reads Home and writes. A skill another skillm process reinstalled or updated meanwhile is
  not rolled back: that skill fails with "run update again" and the rest are updated.
- **Installing from a Source** — skillm reads the Source and asks which skills and where before
  it takes the Home lock; an overwrite question is also asked with Home free. Once locked, it
  re-reads Home and re-checks the choice, then installs exactly the commit it read.
- **Uninstalling** — the confirmation names every project whose committed copies are deleted. If
  another skillm process installs one of the skills into a new project meanwhile, skillm asks
  again with the new list. Ctrl-C stops between skills; those already removed stay removed.
- **Home is busy** — the second command prints `waiting for another skillm operation (pid …:
  skillm install) to finish…`, runs as soon as the first ends, or gives up after 30 seconds
  with an error naming the holder. Ctrl-C stops the wait.
- **A read during a write** — `list` or `check` run alongside a changing command and see the
  Registry as it was before or after that command's save, never a half-written file.
- **A crash mid-save or mid-copy** — Config and the Registry keep their previous content. A
  skill's Canonical copy is never half-written: a crash while copying leaves the old copy, and
  only a crash in the brief swap step leaves it missing. The next write to that skill clears
  any staging leftovers.
- **Upgrading skillm** — `upgrade` swaps in a newer release and leaves a source build alone; the
  skillm inside the macOS app refuses, even offline, and points at the app's Upgrade menu item.
- **Quitting a live `check` or `update`** — rows still running stay unresolved rather than
  showing as failed, `update` writes no skill, and the command returns within seconds even when
  a git server stalls.

## Decisions

- **One lock over read, decide and write, not per file** — a command reads, decides and writes
  across Config, the Registry, Canonical copies and Lockfiles; locking only the saves would
  still let two commands act on the same stale read.
- **Fetch and ask before locking** — a question can stay open indefinitely and a fetch can
  stall, and holding Home through either would make every other command wait and then fail.
  Re-reading Home under the lock keeps the decision fresh.
- **A relative path is resolved where the user typed it, then recorded absolute** — a GUI
  runs from `/`, so the caller names the directory to resolve against; `skillm install
  ./skills/foo` then records an absolute Source that `update` finds from any directory.
- **Read-only commands take no lock** — every save replaces its file in one step, so readers
  are safe without waiting, and `list`/`check` stay instant while a long update runs.
- **Wait, then fail with a name** — a bounded wait keeps scripts from hanging forever, and
  naming the holding command tells the user what to wait for or stop.
- **The lock is advisory and per Home** — it guards skillm against itself; a different Home,
  or a hand edit to `config.toml`, is outside its reach.
- **Saves follow a symlinked Config** — a `config.toml` linked from a dotfiles repo stays a
  link, and an existing file keeps its permissions.
