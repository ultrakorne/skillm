# JSON API — Commands

## Overview

What each command with a JSON mode takes and returns, and how a GUI answers its refusals. The
exact field set of every data type is in its golden fixture in `internal/protocol/testdata/`
and its Go type in `internal/protocol/data.go` (read-only commands) or
`internal/protocol/data_mutating.go` (changing ones). Every list in a result is present, never
`null`. A changing command in JSON mode never asks: each terminal question becomes either a
required flag or a typed refusal the GUI answers by running the command again with a flag.

## Reading

**`version`** returns `{version, api_version, capabilities}`; it needs no git, so a GUI runs it
first and refuses an `api_version` it does not know. **`list`** returns every skill and its
installs, offline, in Registry order. **`check`** returns each skill's upstream status
(`up_to_date`, `update_available`, `untracked`, `local`, `error`) and the `updates` count; a
skill that could not be checked is an `error` row, not a failed command, and `--events` streams
one row per skill as it resolves.

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

Files skillm did not create fail the install with `foreign_files`, listing them in `paths`,
before anything is written. The GUI asks the user, then retries with one of three answers:
`--yes` overwrites the canonical copies listed; `--force` also takes over agent link paths held
by another tool; `--skip-foreign` installs the rest and reports each left-out skill as `skipped`
with an `install_blocked` warning. `--skip-foreign` together with `--yes` or `--force` is
`usage`. Other refusals: `source_collision` (retry with `--as`), `as_multiple`,
`local_scope_aliased`, `commit_mismatch` and `not_installed` for an unknown id.

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

## Integration

`cmd/json_test.go` and `cmd/json_mutating_test.go` run the built binary as a GUI does: the
refused questions, the install, update and uninstall round trip, the uninstall retry after
`needs_confirm`, `update_failed`, import's missing directory, and `upgrade` on a source build
and inside an app bundle.
