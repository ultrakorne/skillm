# skillm — Documentation Index
`skillm` is a Go CLI that fetches AI-agent **skills** from git or local sources and installs them for every enabled agent: a canonical copy in each scope's cross-agent `.agents/skills` store, symlinked into every other agent's folder (Claude, Cursor, …). Home (`~/.skillm`) holds only skillm's config, registry, lock file and Refresh cache; a macOS menu bar app (`macos/`, Swift) drives the CLI through its JSON API.

## Features
| Feature | Description |
|---------|-------------|
| [Core](features/core/INDEX.md) | The commands, inspect-then-install, agents and settings, self-upgrade, Home persistence, atomic saves and the cross-process Home lock |
| [JSON API](features/json-api/INDEX.md) | `--json`/`--events`: the machine-readable protocol GUIs run skillm through |
| [Refresh and Status](features/refresh-status/INDEX.md) | `refresh [--if-due]` and `status`: the `status.json` cache, the one update Badge, when a check is due |
| [macOS App](features/macos-app/INDEX.md) | The menu bar app over the Installed CLI: the Badge dot, Refresh, Update all skills (with the update count), Upgrade skillm CLI and Upgrade app and restart; finding the CLI and keeping it compatible, the Swift client that runs and cancels `skillm --json`, and the app's own signed, notarized App release |

## Quick Links
- [CONTEXT.md](CONTEXT.md) — ubiquitous language (read first); [vercel-skills-comparison.md](vercel-skills-comparison.md) — comparison with vercel-labs/skills and the interop decisions; [known-issues.md](known-issues.md) — deferred defects, including a plaintext credential leak into `state.toml`
