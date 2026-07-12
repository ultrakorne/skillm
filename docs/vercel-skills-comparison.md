# skillm vs. vercel-labs/skills — honest evaluation

*Written 2026-07-11. Based on reading both codebases (vercel-labs/skills cloned at HEAD),
the official Codex and Claude Code skill-discovery docs, and openai/codex issue #22590.*

## TL;DR

- **skillm had a real bug** (fixed 2026-07-11, see Recommendations): its default Codex
  paths (`~/.codex/skills`, `.codex/skills`) pointed at a location current Codex
  **does not scan**. Codex reads `.agents/skills`
  (cwd → repo root), `~/.agents/skills`, and `/etc/codex/skills`. A Codex maintainer
  closed the request to support `.codex/skills`: *"We're unlikely to add this. We
  previously used this path but shifted away when the industry rallied around
  standardization."* ([openai/codex#22590](https://github.com/openai/codex/issues/22590))
- **Claude Code does *not* read `.agents/skills`** — only `.claude/skills` (project,
  parents, nested), `~/.claude/skills`, enterprise, and plugins. So "one path for both"
  is not possible; Claude always needs its own links.
- **As a competitor to vercel's CLI, skillm doesn't make sense. As an opinionated
  personal/small-team tool it does** — its legible single Home, host-agnostic revision
  tracking, and vendoring lifecycle are genuinely better — but only after the Codex
  path fix, because today it fails at half its stated job.

## The path landscape (verified July 2026)

| Agent | Project scope | Global scope | Source |
|---|---|---|---|
| Codex | `.agents/skills` (cwd up to repo root) | `~/.agents/skills`, `/etc/codex/skills` | [Codex docs](https://developers.openai.com/codex/skills) |
| Claude Code | `.claude/skills` (+ parents, nested, `--add-dir`) | `~/.claude/skills` | [Claude Code docs](https://code.claude.com/docs/en/skills) |

Both agents explicitly follow symlinked skill directories, so a link-based design works
for either. `.agents/skills` is also the native project dir for Cursor, Amp, Gemini CLI,
Antigravity and a growing list — it is becoming the cross-agent convention
([agentskills.io](https://agentskills.io)).

## How vercel-labs/skills works (from source, not README)

- **Canonical store**: `.agents/skills` (project) / `~/.agents/skills` (global).
  Agents are classified *universal* (their skillsDir **is** `.agents/skills` — Codex,
  Cursor, Amp, …) or *non-universal* (Claude Code, …). Universal agents get the
  canonical dir directly — no links at all; non-universal agents get per-skill symlinks
  from e.g. `~/.claude/skills/<skill>` into the canonical dir (copy as fallback).
- **Global lock**: `~/.agents/.skill-lock.json` (v3) — source, sourceType, sourceUrl,
  ref, skillPath, `skillFolderHash` (GitHub tree SHA), timestamps. **Wipes itself on
  schema version bumps** (`version < CURRENT_VERSION` → discard), losing update
  tracking for everything previously installed.
- **Project lock**: `skills-lock.json` at repo root, meant to be committed.
  Deliberately timestamp-free and alphabetically sorted to merge cleanly. Content hash
  is computed from files on disk (host-agnostic). `skills install` restores a fresh
  clone from this lockfile — teammate reproducibility works, but restores only into
  `.agents/skills`, not agent-specific dirs.
- **Update**: batches lock entries by source repo. GitHub sources: fetches the repo
  tree via the GitHub API and compares folder tree SHAs — no clone. Non-GitHub: full
  clone per source, hash from disk. Skills without a recorded hash/path are skipped as
  "No version tracking". Applying an update **re-spawns the CLI**
  (`node cli.mjs add <url> -g -y`) per skill — functional, not atomic, re-runs the whole
  add pipeline each time.
- **Extras skillm has no answer to**: `skills find` (registry search), `skills use`
  (run once without installing), `skills init` (scaffold), node_modules skill discovery
  (`experimental_sync`), Claude-plugin-marketplace compat, telemetry-driven leaderboard.

## What vercel does better

1. **Agent coverage and maintenance.** ~70 agents with detection, env overrides
   (`CODEX_HOME`, `CLAUDE_CONFIG_DIR`), XDG and platform quirks — and the catalog is
   updated for you with every `npx` run. skillm ships two defaults, one currently stale,
   and a stale path sits in the user's config.toml forever.
2. **The committable project lockfile.** Any teammate can `skills update` /
   `skills install` in the repo. skillm's `--copy` vendoring gives teammates the files
   (nicer for read access — no tool needed on clone), but provenance lives only in the
   installer's `~/.skillm/state.toml` (`VendoredAt`); on anyone else's machine the
   vendored copy is an orphan nobody can update.
3. **The universal-dir architecture.** Making `.agents/skills` the canonical store means
   most agents need *zero* links. skillm's Home (`~/.skillm/skills`) is a private
   location no agent reads, so every agent needs links.
4. **Zero-install distribution** (`npx skills`) and ecosystem gravity (registry,
   leaderboard, discovery).
5. **Cheap update checks at scale** for GitHub sources (tree API, no clone). skillm does
   a treeless clone per skill — fine at 10 skills, slower at 100 (mitigated by
   concurrency).

## What skillm does better

1. **The "where is everything" story.** One Home, one machine-owned `state.toml`
   (kind/source/ref/revision per skill), links read live from disk instead of trusted
   from a database, `list` showing skill → agents → status, `check` as a read-only
   first-class verb. Vercel diffuses this across a global lock, per-project locks,
   canonical dirs and symlinks — and folds checking into `update`.
2. **Registry durability.** skillm's TOML is stable and hand-readable; vercel's global
   lock silently discards itself on schema bumps.
3. **Host-agnostic, precise update detection.** Pinning the skill *subdirectory's* git
   tree SHA works identically for GitHub, GitLab, or any bare remote. Vercel's fast path
   is GitHub-API-specific and skills can end up untracked ("No version tracking").
4. **Atomic, in-process updates.** New content is staged before the Home copy is
   replaced; a fetch failure never destroys the existing copy. Vercel shells out to a
   full re-add per skill.
5. **Vendored-copy lifecycle.** `update` re-syncs copies, prunes vanished roots, avoids
   git churn on identical content; `uninstall` cleans them up. Vercel's copy mode is a
   symlink fallback, not a managed lifecycle.
6. **Clean config/state separation.** User-owned `config.toml` (agent catalog, hand
   editable, never rewritten except by `skillm agent`) vs. machine-owned `state.toml`.

*Fairness note:* the pain that motivated skillm — not knowing what's installed where and
no system-wide update — has been partially fixed in vercel's tool since (lockfiles,
`list`, ref-aware `update`). The gap is narrower than it was.

## Verdict

skillm's differentiators are real but modest: legibility, durability, host-agnostic
correctness, vendoring. They matter most to exactly one persona — a developer who wants
one auditable home for skills across 2–4 agents and doesn't want an npm-ecosystem tool
managing it. That persona exists (it's you), so the tool makes sense — as a sharp
personal tool, not a vercel competitor. Don't chase the 70-agent matrix, discovery, or a
registry: the `.agents/skills` convention is erasing the need for per-agent path
knowledge anyway, which shrinks vercel's biggest advantage over time and plays to
skillm's simplicity.

## Local vs global installs (vercel, from source)

Project scope is vercel's **default** (`-g` opts into global; interactive prompt offers
Project first). Global and project installs are fully independent. A project install
writes, entirely inside the repo:

1. Real files → `<repo>/.agents/skills/<skill>/` (project-level canonical copy).
2. Universal agents (Codex, Cursor, Amp, …): nothing else — they read that dir natively.
3. Non-universal agents (Claude Code): a **relative** symlink
   `.claude/skills/<skill> → ../../.agents/skills/<skill>` (Windows: junction, absolute).
4. `skills-lock.json` at repo root (source/ref/skillPath/hash; merge-friendly, committed).

Everything is committable — files, relative in-repo symlink, lockfile — so teammates get
working skills on clone with no tooling, and anyone can `skills update` from the
committed provenance. Caveat: committed symlinks need Developer Mode on Windows.

~~skillm's "local" install is local only in link placement~~ **(fixed 2026-07-12,
rec #3):** skillm's local install now produces exactly vercel's project layout — real
files in `.agents/skills/<id>`, a relative `.claude/skills/<id>` symlink, and a
byte-compatible `skills-lock.json` entry — all committable. Verified live against
vercel's CLI: `npx skills ls` reads a skillm-made project, both tools cohabit one
lockfile without clobbering each other's entries, and skillm's Go reimplementation of
their `computedHash` (SHA-256 over ICU-collated file paths + contents) reproduces their
hash byte-for-byte on real multi-file skills.

## Command cookbook: multi-repo workflow on vercel's CLI

Verified from source (`cli.ts`, `update.ts`, `remove.ts`):

```sh
# Global install, many agents
npx skills add owner/skills-repo -g -a claude-code -a codex -a cursor -s frontend-design -y

# Per-repo copies (run in each repo; committable files + lockfile)
npx skills add owner/skills-repo -s frontend-design -a '*' -y

# List (per scope only — no machine-wide view)
npx skills ls          # current repo
npx skills ls -g       # global

# Update (no read-only check; discovers and applies)
npx skills update -g -y    # global
npx skills update -p -y    # current repo only
npx skills update          # interactive: Project / Global / Both (Both = global + cwd)

# Updating ALL repos requires a user-supplied loop:
for d in ~/dev/repo-*; do (cd "$d" && npx skills update -p -y); done

# Remove (also per scope/repo)
npx skills remove frontend-design -g -y
npx skills remove --skill '*' -a cursor
```

Limitations vs. skillm, confirmed in code: project scope always means *cwd only*
(`hasProjectSkills`/`resolveUpdateScope` never look beyond the current directory);
there is no cross-repo update, no read-only `check` verb, and no machine-wide
"where is this skill installed" inventory. skillm's `state.toml`
(`LocalRoots`/`VendoredAt`) is what enables one-command whole-machine check/update —
vercel's per-repo-lockfile architecture has no central index of repos and can't
easily grow one. That single property is skillm's clearest surviving reason to exist.

## Recommendations (priority order)

1. ✅ **DONE (2026-07-11): Fix the Codex default.** `internal/config/config.go` now seeds
   codex with `Global: ~/.agents/skills`, `Local: .agents/skills`. A migration in
   `cmd/migrate.go` (run from the root `PersistentPreRunE`) detects config entries still
   carrying the exact dead pair (`~/.codex/skills` + `.codex/skills`), and with consent
   (prompt on TTY; `--yes`/`--force` non-interactive; warn-and-proceed otherwise)
   rewrites the config and relocates existing global links, local links across tracked
   roots, and vendored copies into the `.agents/skills` locations — link-created-before-
   old-removed, foreign files never touched, idempotent after the config rewrite.
   Hand-customized paths are never rewritten. Because the codex entry now points at the
   cross-agent convention, the one entry also serves Cursor, Amp, Gemini CLI, and any
   other `.agents/skills`-native agent.
2. ✅ **DONE (2026-07-11, superseded 2026-07-12): `.agents/skills` is the canonical
   install location.** Making Home literally `~/.agents/skills` was considered and
   rejected: Home holds *fetched* skills, and `.agents/skills` is a live discovery dir,
   so merging them would collapse skillm's add-vs-install distinction (everything added
   would instantly be active for every `.agents`-native agent). The 2026-07-11 pass
   placed one *symlink* per skill into `~/.agents/skills` and renamed the seeded config
   entry `codex` → `agents` so `skillm agent` reflects that toggling it affects every
   `.agents`-native agent (Codex, Cursor, Amp, Gemini CLI, …), not just Codex.
   **Superseded 2026-07-12:** global installs now mirror vercel's global model exactly —
   a real canonical copy in `~/.agents/skills/<id>` (recorded as `global = true` in
   `state.toml`), with absolute symlinks in every other agent's user-level folder
   (`~/.claude/skills/<id> → ~/.agents/skills/<id>`); `update` re-syncs the global copy
   like a vendored root. Home remains the fetched library no agent reads. The
   `cmd/migrate.go` auto-migration was dropped with this change (manual policy: rerunning
   `skillm install --global` converts a legacy layout in place, same as the local story).
   Not adopted: vercel's global lock `~/.agents/.skill-lock.json` (v3, self-wiping) —
   skillm's registry stays `state.toml`; interop remains project-lockfile-only.

   **Reversed again 2026-07-13 (decided 2026-07-12, by explicit user decision):** the
   `~/.skillm/skills` Home library — and with it the add-vs-install distinction that was
   the whole reason to keep Home separate — was removed. `add` is gone; `install` is the
   single entry point (fetch + pick + install into a chosen scope in one step, resolving
   the scope up front). The canonical install copies (`~/.agents/skills/<id>` global,
   `<project>/.agents/skills/<id>` local) are now a skill's *only* copies; `~/.skillm`
   holds just `config.toml` and `state.toml`, and a Registry entry exists iff the skill is
   installed somewhere. `update` writes fetched content straight into every install (no
   library round-trip); install-by-id copies from the global canonical copy or re-fetches.
   What was **accepted** by this reversal: installed-globally == globally active (there is
   no way to have a globally-fetched-but-inactive skill). What was **given up**:
   fetch-without-activating — staging a skill on the machine without exposing it to any
   agent. Per the standing no-migration policy, there is no auto-migration off the old
   layout: rerunning `skillm install` at the affected scope converts it in place (the
   linker still recognizes legacy symlinks into `~/.skillm/skills`), and any leftover
   `~/.skillm/skills` directory is inert and can be removed by hand.
3. ✅ **DONE (2026-07-12): Local installs are vercel-project-format, interoperable.**
   Rather than inventing provenance metadata, skillm adopted vercel's project format
   wholesale as its only local install mode (the symlink-into-Home local mode and
   `--copy` flag were removed; old absolute local links are migrated by hand — rerunning
   `skillm install --local` converts them in place):
   - `install --local` writes the canonical copy into `<repo>/.agents/skills/<id>`,
     relative in-repo symlinks for non-`.agents` agents (`.claude/skills/<id> →
     ../../.agents/skills/<id>`), and a `skills-lock.json` entry (`internal/lockfile`,
     byte-compatible writer incl. GitHub `owner/repo` normalization, SSH-preserved
     sources, and their exact SHA-256/ICU-collation content hash — pinned by test
     vectors generated from their code). Unknown lock keys (e.g. `subagents`) survive
     rewrites.
   - `skillm import [dir]` adopts a lockfile a teammate committed (written by skillm or
     `npx skills`): fetches each git source at the locked ref (one clone per source repo),
     records the root, writes any missing canonical copy from the fetched content, creates
     missing links.
   - An all-skills `skillm update` runs that adoption over every tracked root
     automatically, then re-syncs each root's copies and lock hashes — so
     `state.toml`'s machine-wide root index (skillm's clearest surviving reason to
     exist, see above) now covers teammate-added skills too.
4. **Group update checks by source repo.** `check`/`update` still do one treeless clone
   per skill; skills sharing a catalog repo (the common case) could share a single clone
   per (source, ref) and read every `SubtreeSHA` from it — the import pipeline already
   works this way (`cmd/import.go` groups by clone URL + ref), so the helper exists.
   Host-agnostic, unlike vercel's GitHub-API fast path, and removes the "slower at 100
   skills" caveat above.

## Sources

- [Codex skills docs](https://developers.openai.com/codex/skills.md) — discovery paths, symlink support
- [openai/codex#22590](https://github.com/openai/codex/issues/22590) — `.codex/skills` rejected, closed
- [Claude Code skills docs](https://code.claude.com/docs/en/skills) — discovery paths, symlink support
- [agentskills.io](https://agentskills.io) — Agent Skills open standard, client list
- [vercel-labs/skills](https://github.com/vercel-labs/skills) — `src/skill-lock.ts`, `src/local-lock.ts`, `src/agents.ts`, `src/installer.ts`, `src/update.ts`, `src/install.ts`
