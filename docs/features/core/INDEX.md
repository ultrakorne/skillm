# Core

The Go program behind every skillm command: it resolves Home, runs each operation on Config,
the Registry and the skill installs, and keeps concurrent skillm processes safe through a
cross-process Home lock, atomic saves and staged install copies.

## Documents

| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | Commands, which ones lock Home, and the concurrency guarantees users see |
| [TECHNICAL.md](TECHNICAL.md) | How commands are wired, where things live, the lock and save invariants |
