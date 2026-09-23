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

Error codes are stable: a code is never renamed or given a second meaning. Every command can
fail with `usage` (a bad command line, including a flag JSON mode requires), `json_unsupported`,
`git_missing`, `cancelled` (retryable), `home_locked` (retryable, changing commands only) and
`error` for anything without a more specific code (a broken `config.toml`, say). The codes a
changing command adds, and the retry each one calls for, are listed per command.

## Commands

| Command | Returns | Refusals the GUI answers |
|---------|---------|--------------------------|
| `version` | CLI version, `api_version`, `capabilities` | — |
| `list` | Every skill and its installs, offline | — |
| `check` | Each skill's upstream status and the update count | — |
| `install` | The scope and what happened to each skill | `foreign_files` → retry with `--yes`, `--force` or `--skip-foreign` |
| `update` | Each skill's outcome and the adoption sweep's imports | `update_failed` (the others were written) |
| `import` | The project's Lockfile entries and what happened to each | — |
| `uninstall` | Each removed skill's deleted copies | `needs_confirm` → confirm the new projects; `needs_force` → retry with `--force` |
| `upgrade` | The Self status (`--check`), or what was replaced | `managed_by_app` → use the app's Upgrade |

[commands.md](commands.md) has each command's arguments, data, event stream and error codes.

## Flows

- **Answering a refusal** — a changing command that would have asked the user fails instead,
  before it changes anything, with a code and the facts the question needs (`paths` of the
  foreign files, or of the projects to confirm). The GUI asks, then runs the same command again
  with the flag that carries the answer.
- **Retrying after a stop part-way** — an uninstall stopped by a blocked entry or by
  cancellation is run again with the same ids; the skills it already removed are skipped with a
  warning.
- **Cancelling** — SIGINT is the cancel signal: the run fails with `cancelled` and no partial
  data. SIGTERM is not handled and kills the process with no result line.

## Decisions

- **A subprocess, not a daemon or a linked library** — the binary is already the tested surface,
  and any GUI toolkit can run a process and parse JSON.
- **Two versions** — `schema_version` changes only when the envelope or event-line shape breaks;
  `api_version` changes when a command's arguments or data break, so the two evolve apart.
- **Opt-in per command** — `--json` on a command without a JSON mode fails loudly rather than
  printing terminal output a GUI would misread.
- **A question becomes a flag, never a default** — JSON mode refuses a missing scope or
  confirmation with `usage` instead of picking one, so a GUI bug can never install into the
  wrong place or delete a project's committed copies unasked.
- **Git stays quiet** — a JSON run asks git for no credentials and runs ssh in batch mode, so an
  expired token shows as a check `error`, never as a process waiting on an invisible prompt.
- **Waiting for Home is an event** — the "waiting for …" notice becomes an info event
  `lock_wait` naming the holder, since stdout carries only the protocol.
