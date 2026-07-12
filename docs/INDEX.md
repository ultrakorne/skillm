# skillm — Documentation Index

`skillm` is a CLI that manages AI-agent **skills**: it fetches them from sources into a
single central store (Home) and installs them for the agents that read them — a canonical
copy in the cross-agent `.agents/skills` folder at each scope (global `~/.agents/skills`,
or per-project), symlinked into every other agent's folder (Claude, Cursor, …).

## Project-level docs
- [CONTEXT.md](./CONTEXT.md) — ubiquitous language / glossary (read first)
- [vercel-skills-comparison.md](./vercel-skills-comparison.md) — honest comparison with
  vercel-labs/skills; records the interop decisions (canonical `.agents/skills` stores,
  lockfile compatibility) and their rationale
