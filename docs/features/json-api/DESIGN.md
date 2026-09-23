# JSON API — Design

## Overview

The JSON API lets a GUI (the macOS menu bar app, a Linux widget) drive skillm by running the CLI
with `--json` and decoding stdout; every behaviour lives once in the CLI. The shaping idea: stdout
holds protocol output only and skillm never prompts, so a GUI never hangs on a question or misreads text.

## Contract

| Element | Meaning |
|---------|---------|
| `--json` | Write one JSON document to stdout. Refused with `json_unsupported` on a command without a JSON mode, on a group such as `config` alone, and on `--help`, `--version` or no command at all |
| `--events` | With `--json`: an NDJSON stream of `{"type":"event",…}` lines as work happens, ending in one `{"type":"result",…}` line holding the envelope. Without `--json` it is an ordinary CLI error |
| Envelope | `{schema_version, data, warnings, error}`: `data` on success (`null` on error), `error` on failure (`null` on success), `warnings` always a list |
| Warning | `{code, message, skill_id?}`: a problem the command reported without failing |
| Error | `{code, message, skill_id?, path?, paths?, retryable}`; a GUI switches on `code`, and `retryable` says the same command may succeed if simply run again |
| Exit code | 0 on success, non-zero on error; the error envelope still goes to stdout and stderr stays empty |
| Encoding | `snake_case` keys; times are RFC 3339 in UTC, whole seconds |

Error codes are stable: never renamed or given a second meaning. Any command can fail with `usage`
(a bad command line, including a flag JSON mode requires), `json_unsupported`, `git_missing`,
`cancelled` (retryable), `home_locked` (retryable; commands that write Home) and `error` (no more
specific code). Each command's own codes and retries are in [commands.md](commands.md).

## Commands

| Command | Returns | Refusals the GUI answers |
|---------|---------|--------------------------|
| `version` | CLI version, `api_version`, `capabilities` | — |
| `list` | Every skill and its installs, offline | — |
| `check` | Each skill's upstream status and the update count | — |
| `source inspect` | A Source's ref, commit and skills, installing nothing | — |
| `install` | The scope and what happened to each skill | `foreign_files` → retry with `--yes`, `--force` or `--skip-foreign`; `commit_mismatch` → inspect again |
| `update` | Each skill's outcome and the adoption sweep's imports | `update_failed` (the others were written) |
| `import` | The project's Lockfile entries and what happened to each | — |
| `uninstall` | Each removed skill's deleted copies | `needs_confirm` → confirm the new projects; `needs_force` → retry with `--force` |
| `upgrade` | The Self status (`--check`), or what was replaced | `managed_by_app` → use the app's Upgrade |
| `agent ls`, `agent set` | The defined agents; what enabling and disabling changed | `no_agent_enabled`, `unknown_agent` |
| `config get`, `config set` | Every setting's effective value | `unknown_key`, `invalid_value` |
| `refresh`, `status` | The Refresh cache after a check (or, not due, as it was); offline, and whether it is stale | — |

A GUI draws its update badge from `badge` as the CLI computed it ([refresh-status](../refresh-status/DESIGN.md)).
For the app's bundled skillm (`method: bundled`), `self.available` means a newer GitHub release
exists (with this platform's archive and checksum manifest), not that the app's updater has one ready.

## Flows

- **Answering a refusal** — a command that would have asked fails before changing anything, with a
  code and the facts the question needs (`paths`); the GUI asks, then reruns it with the answer's flag.
- **Adding a skill** — `source inspect` fills the picker; the install passes its `--commit` and `--ref`,
  copying what the user saw yet following the branch. A Source that moved since is refused, re-inspected.
- **Retrying after a stop part-way** — an uninstall stopped by a blocked entry or by cancellation
  is run again with the same ids; the skills it already removed are skipped with a warning.
- **Cancelling** — SIGINT fails the run with `cancelled` and no partial data; SIGTERM is not
  handled and leaves no result line.

## Decisions

- **A subprocess, not a daemon or a linked library** — the binary is already the tested surface,
  and any GUI toolkit can run a process and parse JSON.
- **Two versions** — `schema_version` changes only when the envelope or event-line shape breaks;
  `api_version` changes when a command's arguments or data break, so the two evolve apart.
- **A question becomes a flag, never a default** — a missing scope or confirmation is `usage`, so a
  GUI bug can never install into the wrong place or delete a project's committed copies unasked.
- **The working directory is never a project** — a GUI runs from wherever it was launched, so
  JSON mode infers no project from it: `install` names its scope, and `agent set` reconciles
  only the global folders and the recorded projects.
- **Git stays quiet** — a JSON run asks git for no credentials and runs ssh in batch mode, so an
  expired token shows as a check `error`, never as a process waiting on an invisible prompt.
