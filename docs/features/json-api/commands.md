# JSON API — Commands

## Overview

What each command with a JSON mode takes and returns, and how a GUI answers its refusals. The
exact field set of every data type is in its golden fixture in `internal/protocol/testdata/`
and its Go type in `internal/protocol/data.go` (`version`, `list`, `check`),
`internal/protocol/data_mutating.go` (the skill-changing commands) or
`internal/protocol/data_settings.go` (`source inspect`, `agent`, `config`) or
`internal/protocol/data_status.go` (`refresh`, `status`). Every list in a
result is present, never `null`. A changing command in JSON mode never asks: each terminal
question becomes either a required flag or a typed refusal the GUI answers by running the
command again with a flag.

## Reading

**`version`** returns `{version, api_version, capabilities}`; it needs no git, so a GUI runs it
first and refuses an `api_version` it does not know. `capabilities` are command paths
(`agent ls`, `config set`, …) plus `events`. **`list`** returns every skill and its installs,
offline, in Registry order. **`check`** returns each skill's upstream status (`up_to_date`,
`update_available`, `untracked`, `local`, `error`) and the `updates` count; a skill that could
not be checked is an `error` row, not a failed command, and `--events` streams one row per skill
as it resolves.

**`source inspect <src> [--ref R]`** reads a Source once and returns `{source, kind, ref, commit,
skills: [{id, name, description, path}]}`, the data of the Add Skill picker. `ref` is the branch
or tag an install records (`--ref`, or the default branch) and `commit` the full SHA read; both
are omitted for a local Source, where `--ref` is `usage`. A GUI passes a local Source absolute.
**`agent ls`** returns every defined agent, sorted by name, with `enabled` and its `global` and
`local` folders as `config.toml` writes them. **`config get [key]`** returns every setting
(`{refresh: {enabled, interval_hours}}`) with its effective value, the default where
`config.toml` has none; a key, when given, is only checked (`unknown_key`). `agent ls` and
`config get` need no git, so a GUI's Settings work before it can report git missing.

## `refresh` and `status`

**`status`** returns the Refresh cache offline, plus `stale`: the cache's own `schema_version`,
`checked_at`, `next_due_at`, one row per skill (`up_to_date`, `update_available`, `untracked`,
`local`, `error`, with the Revisions compared and the lookup error), `updates`, `self` (the
Self status of `upgrade --check`, plus the lookup's `error`), `errors` (each `untracked` or
`error` skill and a failed self lookup, coded `self_check`) and `badge`. Before any refresh the
times are absent, `self` is `null`, the lists are empty, `badge` is false and `stale` true. It
needs no git. `self` is judged for the running skillm, so it can differ from the file's.

**`refresh [--if-due]`** returns the same data plus `refreshed`: false when `--if-due` found no
check due, in which case nothing was looked up and the data is the cache as it was. With
`--events` it streams `check`'s rows. A failed lookup is never a failed command: a skill's is its
row and an `errors` entry, the self lookup's an `errors` entry and a `self_check_failed` warning.
It writes under the Home lock, so it can fail with `home_locked`. A cache that cannot be read is
a `status_unreadable` warning and reads as never written. A GUI shows `badge` as it is, never
recomputing it, and schedules nothing itself: it runs `refresh --if-due` as often as it likes.
`install`, `update`, `uninstall` and `upgrade` may add a `status_not_saved` warning when they
could not bring the cache in line; their own work is done.

## `install`

Needs skill ids (or `--all`) and exactly one of `--global`, `--local` or `--project <dir>`,
otherwise `usage`. `--project` is the working-directory-free spelling of `--local` and must name
an existing directory; with `--global` or an absolute `--project`, skillm does not need a
working directory at all. The first argument may be a Source, followed by the ids to take from
it. The result is `{scope, root, skills: [{id, action}]}`, `root` only for a Local install; the
actions are `installed`, `converted`, `refreshed`, `overwritten` and `skipped`. `--all` with
nothing installed succeeds with no skills. With `--events`: a `batch` of the final ids, an
`item_start` as each skill's content is staged and an `item_done` coded `installed`,
`install_blocked` or `install_failed`.

Files skillm did not create at the canonical slots fail the install with `foreign_files`, listing
them in `paths`, before anything is written. The GUI asks, then retries the same command with
`--yes` (overwrite the copies listed) or `--skip-foreign` (install the rest; each left-out skill
is `skipped` with an `install_blocked` warning). An agent link path held by another tool is left
alone and reported, once the copy landed, as a `link_refused` warning on the result, never with
`foreign_files`; `--force` takes such links over as well. `--skip-foreign` together with `--yes`
or `--force` is `usage`. Other refusals: `source_collision` (retry with `--as`), `as_multiple`,
`local_scope_aliased` and `not_installed` for an unknown id.

`--commit <sha>` installs from a git Source only if it is still at the commit `source inspect`
reported (the full SHA or at least 7 hex characters of it); the GUI passes the same `--ref`, so
the branch or tag stays recorded for `update`. A Source that moved fails with `commit_mismatch`
before any question or write: inspect it again and show the new skills. A value that is not a
7 to 64 character hex SHA, or `--commit` without a git Source, is `usage`.

## `update`

`update [id]` returns every skill in scope with its `outcome` (`updated`, `up_to_date`,
`synced`, `pruned`, `failed`, `drift_check_skipped`), plus what the adoption of tracked projects
imported, the `updated` count and `synced`. `--events` streams one row per fetched skill. A
skill that failed fails the run with `update_failed` after every other skill was written, and
the error envelope has no data: each failed skill is a warning with its `skill_id`
(`update_failed` for a failed fetch, `update_skipped` for a skill another process changed
meanwhile), the error's `skill_id` is set when exactly one failed, and the others' outcomes are
only on the `--events` stream. An unknown id is `not_installed`.

## `import`

`import <dir>` returns `{root, entries, skills}`, one row per entry `imported`, `adopted` or
`skipped` (with its reason). A GUI passes an absolute directory. A directory without
`skills-lock.json` succeeds with 0 entries; a missing directory, or a file, fails with `error`
and the directory in `path`.

## `uninstall`

Needs `--yes` and skill ids (or `--all`), otherwise `usage`: the GUI confirms with the user
first. Its confirmation names the projects whose committed copies will be deleted (the Local
roots `list` reports for those skills), and it passes each with `--confirmed-root <dir>`
(repeated; `--confirmed-root=` when it named none). If the skills meanwhile gained a Local install
in a project not named, nothing is removed and the run fails with `needs_confirm`, `paths`
holding the full new list to confirm and pass back. Without the flag, every recorded project
is cleared unchecked.

The result lists each removed skill with `removed_copies` (`global` or a project root) and
`warnings`; a non-empty `warnings` is a partial success whose entries were left in place. A
failure part-way leaves the skills before it removed: `needs_force` (another tool's entry is in
the way; retry with `--force`), `uninstall_failed` (an I/O error; no force offer) or `cancelled`
(the message counts what was removed). An id that is no longer installed is skipped with a
`not_installed` warning, so each of these is retried with the same ids.

## `upgrade`

`upgrade --check` returns the Self status: `{current, latest, available, eligible, method,
executable}`; a `dev` build looks nothing up and omits `latest`, and a `bundled` one reports
`available` but never `eligible`. A plain `upgrade` returns `{upgraded, from, to, path}`, with
`upgraded` false when skillm is already the latest release. It refuses a source build with
`source_build` and the app's bundled skillm with `managed_by_app`, both before any network
request; the app treats `managed_by_app` as "use the app's own Upgrade", not as a failure.
Neither form needs git.

## `agent set`

`agent set --enable a --disable b` (either flag repeated or comma-separated) enables and
disables agents as the Settings toggles do, reconciling their links at once; the skills stay
installed. A run with `--disable` needs `--yes`, since it removes links: the GUI confirms first.
JSON mode does not treat the working directory as a project: only the global folders and the
recorded projects are reconciled. The result is `{enabled, changes: [{name, enabled, skills,
places, warnings}]}`; an agent already in the asked-for state is left out, and empty `changes`
means nothing was written. A non-empty `warnings` is a partial success whose links were left as
they were. A name both enabled and disabled is `usage`; an undefined agent is `unknown_agent` and
a change leaving no agent enabled `no_agent_enabled`, both before anything is written.

## `config set`

`config set <key> <value>` stores one setting and returns every setting afterwards, like
`config get`. The keys are `refresh.enabled` (`true`/`false`) and `refresh.interval_hours` (a
whole number from 1 to 720). An unknown key is `unknown_key` and a value the key refuses
`invalid_value`; either way nothing is written, and neither waits for Home. It rewrites the
whole `config.toml` under the Home lock, so comments in it are dropped. Needs no git.

## Integration

`cmd/json_test.go`, `cmd/json_mutating_test.go` and `cmd/json_settings_test.go` run the built
binary as a GUI does: the refused questions, the install, update and uninstall round trip, the
uninstall retry after `needs_confirm`, `update_failed`, import's missing directory, `upgrade` on a
source build and inside an app bundle, inspect-then-install with `--commit` (and
`commit_mismatch` after the Source moves), `agent ls`/`agent set`, the `config get`/`config set`
round trip with no git on `PATH`, and the group commands refused. `cmd/json_status_test.go`
runs the badge cycle: `status` with no git, `refresh`, `refresh --if-due`, then `update` and
`uninstall` clearing the badge.
