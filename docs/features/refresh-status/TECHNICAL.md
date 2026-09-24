# Refresh and Status — Technical

## Architecture

`internal/status` owns the Refresh cache as a file: its format, the atomic load and save
(through `store.WriteFileAtomic`), and the policies every reader shares: `Recount` (the update
count, the problem list and the Badge, derived from the rows), `Due`/`DueAt` and `Stale`. It
decides nothing about content. `internal/core/status.go` does: `core.Refresh` runs `core.Check`
and `core.CheckSelf` with Home unlocked, then takes the lock through `Options.Lock`, re-reads the
Registry and the previous cache, merges and saves; `core.ReadStatus` reads the cache offline.
A cache the running binary did not just write passes through `adjustSelf` before it is returned.

The commands that change what the cache reports update it themselves, under the Home lock they
already hold: `core.InstallSkills`, `core.Update` and `core.Uninstall` call the unexported
`record*` helpers after saving the Registry. `upgrade` holds no lock for its own work, so
`cmd/upgrade.go`'s `recordSelf` takes it only when a cache exists and calls `core.RecordSelf`.
`cmd/refresh.go` holds both commands; `status` skips the git check, `refresh` does not.

## Where things live

| File | Role |
|------|------|
| `internal/status/status.go` | The `status.json` format and its schema version, load/save, `Recount`, `Due`, `DueAt`, `Stale`, `RetryAfterFailure` |
| `internal/core/status.go` | `Refresh`, `ReadStatus`, `RecordSelf`, the locked merge, the racing-refresh test, `adjustSelf`, the per-command record helpers and the event codes |
| `cmd/refresh.go` | `refresh [--if-due]` and `status`, their terminal summary and JSON branches |
| `cmd/upgrade.go` | `recordSelf`: the Self status after `upgrade --check` or an upgrade |
| `internal/protocol/data_status.go` | `StatusData` (the file's fields plus `stale`) and `RefreshData` (plus `refreshed`) |
| `internal/protocol/testdata/cache/status.json` | The cache file's golden fixture, byte for byte as `status.Save` writes it |
| `internal/status/status_test.go` | Due, due-at and stale rules with an injected clock; the Badge; load and save |
| `internal/core/status_test.go` | The merge, failed lookups, cancellation, each command's cache update, a shared Home, racing refreshes |
| `cmd/json_status_test.go` | The badge cycle on the built binary: `status` without git, `refresh`, `--if-due`, then `update`/`uninstall` |

## Noteworthy

### The file is a contract of its own

A Quickshell widget decodes `status.json` directly, so its shape changes with its own
`schema_version` (`status.SchemaVersion`) and its fixture, not only with the protocol's
`api_version`. `status --json` carries the same fields at the top level of its data, including
the file's `schema_version`. An undecodable file or an unknown schema version reads as never
written, with a `status_unreadable` warning, and the next refresh replaces it.

### Derived fields are never trusted from disk

`Load` and `Save` both run `Recount`, so `updates`, `errors` and `badge` always follow the rows
and the Self entry; a writer cannot set the Badge on its own, and times are stored in UTC, whole
seconds.

### The merge keeps what another process wrote meanwhile

The lookups run unlocked, so under the lock `refreshedRow` accepts a Check row only when the skill
still tracks the checked Kind, Source, Path and Ref (`sameTracking`) at the checked Revision.
Otherwise the previous cache's row stays if it judged the current Revision, or the skill is up to
date if the upstream Revision found is the one now recorded; failing all three it gets no row.
Skills uninstalled meanwhile are dropped, and local skills are always `local`.

### A slower refresh never overwrites a newer one

When the cache on disk was checked after this refresh started and no later than its start plus
the time its lookups took (on the monotonic clock, plus a second for the truncated timestamp),
another refresh that started later has saved: its cache stays. A `checked_at` beyond that window
comes from a clock that went back and is replaced. A failed or cancelled Check writes nothing.

### The retry hour counts real lookups only

`next_due_at` moves to `min(interval, RetryAfterFailure)` only when at least one lookup ran and
none succeeded; the lookups are the git skills' and the self lookup, which a `dev` build skips.
A Home with only local skills on a source build therefore waits the full interval.

### Due is judged by time; the Self entry by the running binary

Several skillm binaries may share one Home (two installs, or one before and after an upgrade), so `Due`
ignores who wrote the cache. Instead `adjustSelf` re-judges the Self entry offline whenever its
current version, method or executable differs from the running binary's: `available` compares
the recorded latest release with the running version, a `dev` build drops `latest`, and only a
`binary` method is `eligible`. The file on disk keeps the writer's view until a command rewrites it.

### Keeping the cache current never fails a command

Every record helper does nothing when no cache exists, and a cache it cannot read or write
becomes a `status_not_saved` warning; the next refresh repairs it. An install from a Source, or
an id-mode install that re-fetched from upstream, marks the git skill up to date even when its
Revision did not move; one copied from its Global copy keeps its row unless the Revision moved.
`Uninstall` records in a `defer`, so skills removed before a later failure lose their rows too.
