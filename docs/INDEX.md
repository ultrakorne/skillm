# skillm — Documentation Index

`skillm` is a CLI that manages AI-agent **skills**: it fetches them from sources and installs
them for the agents that read them — a canonical copy in the cross-agent `.agents/skills`
folder at each scope (global `~/.agents/skills`, or per-project), symlinked into every other
agent's folder (Claude, Cursor, …). The canonical copies are the skill's only copies; Home
(`~/.skillm`) holds just skillm's config and registry.

## Project-level docs
- [CONTEXT.md](./CONTEXT.md) — ubiquitous language / glossary (read first)
- [vercel-skills-comparison.md](./vercel-skills-comparison.md) — honest comparison with
  vercel-labs/skills; records the interop decisions (canonical `.agents/skills` stores,
  lockfile compatibility) and their rationale
- [known-issues.md](./known-issues.md) — confirmed defects that are deferred, not
  scheduled; currently the remote-URL normalization gaps, including a **plaintext
  credential leak** into a world-readable `state.toml` and `skillm list` output
