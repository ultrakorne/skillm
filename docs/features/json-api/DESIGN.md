# JSON API — Design

## Overview

The JSON API lets a GUI (the macOS menu bar app, later a Linux widget) drive skillm by running
the CLI with `--json` and decoding stdout. Every behaviour is written once in the CLI and
exposed once here; a GUI only adds a control for it. The shaping idea: in JSON mode stdout holds
protocol output and nothing else, and skillm never prompts, so a GUI can never hang on a
question or read terminal text where it expects JSON.

## Contract

| Element | Meaning |
|---------|---------|
| `--json` | Write one JSON document to stdout. Refused with `json_unsupported` on a command without a JSON mode, and on `--help`, `--version` or no command at all |
| `--events` | With `--json`: an NDJSON stream of `{"type":"event",…}` lines as work happens, ending in one `{"type":"result",…}` line holding the envelope. Without `--json` it is an ordinary CLI error |
| Envelope | `{schema_version, data, warnings, error}`: `data` on success (`null` on error), `error` on failure (`null` on success), `warnings` always a list |
| Warning | `{code, message, skill_id?}`: a problem the command reported without failing |
| Error | `{code, message, skill_id?, path?, paths?, retryable}`; a GUI switches on `code`, and `retryable` says the same command may succeed if simply run again |
| Exit code | 0 on success, non-zero on error; the error envelope still goes to stdout and stderr stays empty |
| Encoding | `snake_case` keys; times are RFC 3339 in UTC, whole seconds |

Error codes are stable: a code is never renamed or given a second meaning. `version`, `list`
and `check` can fail with `usage` (a bad command line), `json_unsupported`, `git_missing`,
`cancelled` (retryable), and `error` for anything without a more specific code (a broken
`config.toml`, say). The code table also covers every typed error of the changing commands (`home_locked`,
`foreign_files`, `needs_force`, `needs_confirm`, …); see [TECHNICAL.md](TECHNICAL.md).

## Commands

### `version`

`{version, api_version, capabilities}`. `api_version` is the command-and-data contract a GUI
was built against; a GUI refuses a CLI whose `api_version` it does not know. `capabilities` lists
the commands with a JSON mode plus `events`, so a GUI can hide a control the CLI cannot serve.
It works without git on the PATH, so a GUI can run it before anything else.

### `list`

`{skills: [...]}` in Registry order, offline. Each skill carries its id, kind (`git`/`local`),
Source, subpath, the Source as the CLI shows it, ref, Revision and install time, and its
installs: scope, project root (Local only), the Canonical copy's path, the enabled agents it
serves, whether the Registry records it, and whether that recorded copy is on disk.

### `check`

`{skills: [...], updates}`: each skill's status (`up_to_date`, `update_available`, `untracked`,
`local` or `error`), its installed and upstream Revision, and the lookup failure behind
`untracked`/`error`; `updates` counts `update_available`. A skill that could not be checked is
an `error` row, not a failed command. With `--events`, one row per skill streams as it resolves.
A cancelled run fails with `cancelled` and returns no partial data. SIGINT is the cancel signal:
a GUI cancels by sending it. SIGTERM is not handled and kills the process with no result line.

## Decisions

- **A subprocess, not a daemon or a linked library** — the binary is already the tested surface,
  and any GUI toolkit can run a process and parse JSON.
- **Two versions** — `schema_version` changes only when the envelope or event-line shape breaks;
  `api_version` changes when a command's arguments or data break, so the two evolve apart.
- **Opt-in per command** — `--json` on a command without a JSON mode fails loudly rather than
  printing terminal output a GUI would misread.
- **Git stays quiet** — a JSON run asks git for no credentials and runs ssh in batch mode, so an
  expired token shows as a check `error`, never as a process waiting on an invisible prompt.
- **Waiting for Home is an event** — the "waiting for …" notice becomes an info event
  `lock_wait` naming the holder, since stdout carries only the protocol.
