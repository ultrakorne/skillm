The Go program behind every skillm command: it runs each operation on Config, the Registry and the skill installs, and keeps concurrent runs safe with a Home lock, atomic saves and staged copies.

| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | Commands, which ones lock Home, and the concurrency guarantees users see |
| [TECHNICAL.md](TECHNICAL.md) | How commands are wired, where things live, the lock and save invariants |
| [check-and-list.md](check-and-list.md) | `core.Check`/`core.List` result semantics and cancellation |
| [Inspect and install](inspect-and-install.md), [primitives](install-primitives.md) | Pinned commits, batch checks, overwrite approvals, path resolution and write failures |
| [update-and-import.md](update-and-import.md) | `core.Update`/`core.Import`: the unlocked fetch, stale-fetch judgement, per-skill outcomes |
| [uninstall-and-agents.md](uninstall-and-agents.md) | Scoped uninstall, project confirmations, retries and agent enable/disable sweeps |
| [settings.md](settings.md) | `config get`/`set` and the `[refresh]` settings: defaults, lenient decoding, whole-file saves |
| [self-upgrade.md](self-upgrade.md) | `core.CheckSelf`/`core.UpgradeSelf`: the Upgrade method, `Available` vs `Eligible`, the app-bundle guard |
