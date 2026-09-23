# Core — Check and List

## Overview

`check` and `list` are the two commands that run over the presentation-free `internal/core`
package and render its Events through `ui.Checklist`. Their result semantics and cancellation
behaviour matter to any caller of `core.Check`/`core.List`, not only the CLI, so they are
documented apart from the Home-safety rules in [TECHNICAL.md](TECHNICAL.md).

## Noteworthy

### `check`'s "error" names its cause in `core`, and reads "untracked" on screen

`core.Check` keeps a failed lookup (`error`) apart from `untracked` (a `*gitx.NotFoundError`:
the repo was read but the subdir is gone), and an `error` Event's Text names the cause.
`checkReporter` in `cmd/check.go` rewrites that Text to `core.UntrackedText`, the single
source of the untracked sentence, so the CLI prints the same line for both.

### Cancellation leaves rows unresolved and returns promptly

Once the context is cancelled, `core.Check` treats every `error` result as interrupted, whether
or not its error wraps the context's, so a quit never shows as a failed row; `untracked` is a
real answer and still resolves. `runGit` sets a `WaitDelay`, because a killed `git clone` over
HTTP(S) leaves its remote helper holding git's stderr and an unbounded wait hangs for as long
as the server stalls; a `WaitDelay` hit after git itself succeeded counts as success.

### A checklist's `OnAbort` fires on any abnormal end, not just a user quit

Core, not the checklist, owns the work, so `ChecklistOptions.OnAbort` must cancel it when the
live view ends any way other than every row resolving (a user quit, a renderer error, a final
model of the wrong type); otherwise the workers run on unobserved while `Done` calls are dropped.

### A `core.List` install exists only if recorded or linked

An `Install` is listed when the Registry records a Canonical copy there (`Recorded`) or it
serves at least one enabled agent through a Link. A Canonical copy counts only when `Recorded`,
so a foreign `.agents/skills/<id>` directory is never claimed as skillm's, and `Exists` is
meaningful only for a recorded install (always false otherwise).

## Integration

`cmd/check.go` and `cmd/list.go` build `core.Options` through `coreOptions` in
`cmd/reporter.go`; `check` renders through `termReporter` and `list` prints one table.
`cmd/golden_test.go` pins the printed output, `internal/core/check_test.go` and
`internal/core/list_test.go` the typed results, and `internal/gitx/gitx_test.go` the cancelled clone against a stalled HTTP server.
