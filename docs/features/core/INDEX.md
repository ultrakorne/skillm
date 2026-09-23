# Core

The Go program behind every skillm command: it runs each operation on Config, the Registry and the skill installs, and keeps concurrent runs safe with a Home lock, atomic saves and staged copies.

## Documents
| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | Commands, which ones lock Home, and the concurrency guarantees users see |
| [TECHNICAL.md](TECHNICAL.md) | How commands are wired, where things live, the lock and save invariants |
| [check-and-list.md](check-and-list.md) | `core.Check`/`core.List` result semantics and cancellation |
| [install-primitives.md](install-primitives.md) | Copy, Link, Lockfile and Source-identity primitives: event codes, force and failure rules |
| [inspect-and-install.md](inspect-and-install.md) | `core.Inspect`/`core.InstallSkills` and `source inspect`: the pinned commit, `--commit`, batch checks, lock hand-off, path resolution |
| [update-and-import.md](update-and-import.md) | `core.Update`/`core.Import`: the unlocked fetch, stale-fetch judgement, per-skill outcomes |
| [uninstall-and-agents.md](uninstall-and-agents.md) | `core.Uninstall`/`core.SetAgents` and `agent ls`/`set`: confirmed roots, `ErrNeedsConfirm` vs `ErrNeedsForce`, agent sweeps |
| [settings.md](settings.md) | `config get`/`set` and the `[refresh]` settings: defaults, lenient decoding, whole-file saves |
| [self-upgrade.md](self-upgrade.md) | `core.CheckSelf`/`core.UpgradeSelf`: the Upgrade method, `Available` vs `Eligible`, the app-bundle guard |
