# Core — Uninstall and agents

## Overview

`uninstall` runs over `core.Uninstall` (`internal/core/uninstall.go`), and `agent` over
`core.Agents` and `core.SetAgents` (`internal/core/agents.go`). The pruning of tracked
project roots both share lives in `internal/core/roots.go`. `cmd/uninstall.go` and
`cmd/agent.go` keep the pickers and the confirmations, run them with Home unlocked, then take
the Home lock around the one core call, which re-reads Config and the Registry under it.
Neither operation fetches, so neither uses `Options.Lock`: the caller holds the lock for the
whole call. Each returns what it did per skill or per agent: an `UninstalledSkill` or an
`AgentChange`.

## Noteworthy

### A confirmation holds the uninstall to the projects it named

The question names every project whose committed copies the uninstall deletes
(`core.UninstallRoots`). With `UninstallRequest.CheckRoots`, core refuses with an
`*UninstallScopeChangedError` before removing anything when the skills now have Local installs
in a project the confirmation did not name, because another process installed or adopted one
while the question was open. `cmd` releases the lock, asks again with the new list and
retries. A caller that asked its own question passes the projects it named with
`--confirmed-root`, which sets both fields. A run that asked nothing (`--yes`, `--force`, no
terminal, and no `--confirmed-root`) clears every recorded project.

### Missing ids refuse the batch, unless the caller asks to skip them

An id that is not installed (any more) is a `*NotInstalledError` before anything is removed, so a
typo removes nothing. With `UninstallRequest.SkipMissing`, which JSON mode sets, each such id is
a `not_installed` warning instead and the rest are removed, so a batch that stopped part-way can
be retried with the same ids.

### `ErrNeedsConfirm` and `ErrNeedsForce` mean different retries

`ErrNeedsConfirm` (`internal/core/errors.go`) is matched only by an error returned before
anything changed: ask again, then retry with the new answer. `ErrNeedsForce` may follow partial
progress, and only `Options.Force` steps past it, never `Options.Yes`. An
`*UninstallBlockedError` matches `ErrNeedsForce` only for a foreign entry
(`linker.ErrNotManaged`); an I/O failure is a plain error, which `Force` still steps past but
for which a caller should not offer "force".

### Uninstall saves after each skill and stops only between skills

The Registry is saved after every skill, so disk and Registry never drift when a later skill
fails; a cancelled context stops the batch between skills, and the skills already removed are
saved and in the result (the CLI prints "uninstall interrupted; N of M skills removed").
Canonical copies go first, then the link sweep, so the sweep finds an empty slot rather than
refusing on a real directory. The sweep covers every defined agent, disabled ones included, so a
link made while an agent was enabled is never left dangling.

### A Global link left behind warns; a Local one blocks

Nothing revisits the Global link paths, so an unlink failure there is recorded in
`UninstalledSkill.Warnings` and never blocks. A Local link failure is revisited by the sweep
over tracked roots, where it blocks the skill (or, under `Force`, warns).

### SetAgents saves intent first, then reconciles best effort

The new Enabled flags are saved before any link changes; a blocked spot is then a warning
event and an entry in `AgentChange.Warnings`, never an abort. Every refusal (an undefined name,
a name in both lists, leaving no agent enabled) comes before anything is written. Enables run
before disables, so a swap lets the new agent copy the old one's links while they are still on
disk. SetAgents toggles, it does not repair drift: an agent already in the asked-for state is
left alone, and a call that changes nothing writes nothing.

### Enabling mirrors live peers, never into a missing copy

A newly enabled agent gets a link wherever the previously enabled agents hold one (read live
from disk) and at every recorded install whose Canonical copy exists. At a directory where a
peer's local folder is its global one (the home directory), that peer's links are ignored, or
global-only skills would be mirrored as local ones. A link is made only where the copy exists,
since it would dangle otherwise.

### Tracked roots are pruned against every defined agent

`reconcileLocalRoots` drops a tracked project only when it holds no recorded copy and no link
of any defined agent, so a root kept alive by a disabled agent's links survives; a legacy root
that aliases the global folders for every agent is dropped. `reconcileVendoredRoots` (run by
SetAgents) forgets a recorded install whose copy vanished.

### Core texts name no CLI command

`agent_enabled_empty` and `copies_kept` get their `skillm install`/`skillm uninstall` advice from
`flagAdvice` in `cmd/reporter.go`, which also prints `copies_kept` as a hint; `cmd/agent.go`
words `ErrNoAgentEnabled` for the terminal.

## Integration

`internal/core/setagents_test.go` covers `Agents`, the swap, the refusals and the uninstall
errors (not installed, blocked by a foreign entry or by I/O, scope changed, the Global unlink
warning); `internal/core/agents_test.go` the enable and disable sweeps; `internal/core/roots_test.go`
the home-alias pruning. `cmd/agent_test.go` pins the printed advice, and `cmd/lock_test.go`
checks that both commands wait for a held Home lock.
