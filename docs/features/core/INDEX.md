# Core

The Go program behind every skillm command: it runs each operation on Config, the Registry and
the skill installs, and keeps concurrent runs safe with a Home lock, atomic saves and staged copies.

## Documents

| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | Commands, which ones lock Home, and the concurrency guarantees users see |
| [TECHNICAL.md](TECHNICAL.md) | How commands are wired, where things live, the lock and save invariants |
| [check-and-list.md](check-and-list.md) | `core.Check`/`core.List` result semantics and cancellation |
