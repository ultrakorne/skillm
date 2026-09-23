# skillm — Documentation Index
`skillm` is a Go CLI that fetches AI-agent **skills** from git or local sources and installs them for every enabled agent: a canonical copy in each scope's cross-agent `.agents/skills` store, symlinked into every other agent's folder (Claude, Cursor, …). Home (`~/.skillm`) holds only skillm's config, registry and lock file.

## Features
| Feature | Description |
|---------|-------------|
| [Core](features/core/INDEX.md) | The commands, inspect-then-install, agents and settings, self-upgrade, Home persistence, atomic saves and the cross-process Home lock |
| [JSON API](features/json-api/INDEX.md) | `--json`/`--events`: the machine-readable protocol GUIs run skillm through |

## Quick Links
- [CONTEXT.md](CONTEXT.md) — ubiquitous language (read first)
- [vercel-skills-comparison.md](vercel-skills-comparison.md) — comparison with vercel-labs/skills and the interop decisions; [known-issues.md](known-issues.md) — deferred defects, including a plaintext credential leak into `state.toml`
