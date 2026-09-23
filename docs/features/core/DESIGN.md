# Core — Design

## Overview

The core is the skillm program behind every command: it owns Home (Config and the Registry)
and every skill install on the machine, and each command is one operation on that state. The
idea that shapes it is that several skillm processes can run at once (two terminals, a
script running in the background) and must never corrupt Home or each other's installs.

## Surface

| Command | Changes Home and installs | Holds the Home lock |
|---------|---------------------------|---------------------|
| `install` | yes | yes |
| `update` | yes | yes |
| `uninstall` | yes | yes |
| `import` | yes | yes |
| `agent` | yes | yes |
| `list`, `check` | no | no |
| `upgrade` | no (replaces the skillm binary only) | no |

`--home` or `$SKILLM_HOME` points any command at a different Home; the default is `~/.skillm`.

## Flows

- **A changing command** — takes the Home lock, reads Config and the Registry, does its work
  (fetching, prompting, writing copies and links), saves, and releases the lock when it ends.
- **Home is busy** — the second command prints `waiting for another skillm operation (pid …:
  skillm install) to finish…`, runs as soon as the first ends, or gives up after 30 seconds
  with an error naming the holder. Ctrl-C stops the wait.
- **A read during a write** — `list` or `check` run alongside a changing command and see the
  Registry as it was before or after that command's save, never a half-written file.
- **A crash mid-save or mid-copy** — Config and the Registry keep their previous content. A
  skill's Canonical copy is never half-written: a crash while copying leaves the old copy, and
  only a crash in the brief swap step leaves it missing. The next write to that skill clears
  any staging leftovers.

## Decisions

- **One lock for the whole command, not per file** — a command reads, decides and writes
  across Config, the Registry, Canonical copies and Lockfiles; locking only the saves would
  still let two commands act on the same stale read.
- **Read-only commands take no lock** — every save replaces its file in one step, so readers
  are safe without waiting, and `list`/`check` stay instant while a long update runs.
- **Wait, then fail with a name** — a bounded wait keeps scripts from hanging forever, and
  naming the holding command tells the user what to wait for or stop.
- **The lock is advisory and per Home** — it guards skillm against itself; a different Home,
  or a hand edit to `config.toml`, is outside its reach.
- **Saves follow a symlinked Config** — a `config.toml` linked from a dotfiles repo stays a
  link, and an existing file keeps its permissions.
