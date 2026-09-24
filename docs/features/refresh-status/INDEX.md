# Refresh and Status

`skillm refresh` checks every skill upstream and looks up the latest skillm release in one pass, and writes the outcome to the Refresh cache, `~/.skillm/status.json`; `skillm status` reads it back offline. Every skillm GUI draws its one update Badge from that cache, and `refresh --if-due` lets the CLI, not the GUI, decide when a scheduled check runs. `install`, `update`, `uninstall` and `upgrade` keep the cache in line with what they change.

## Documents
| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | The commands, the Badge, when a check is due or the cache stale, and which commands keep it current |
| [TECHNICAL.md](TECHNICAL.md) | `internal/status` and `internal/core/status.go`: the file contract, the locked merge, racing refreshes, a Home shared by several skillm binaries |
