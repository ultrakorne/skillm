# skillm — Ubiquitous Language

The canonical glossary for the project. Glossary only — no implementation details.
Terms are added/sharpened as the design is resolved.

## Core nouns

### Skill
A self-contained unit of agent instructions. On disk it is a directory whose entry
point is a `SKILL.md` file (with YAML frontmatter), optionally accompanied by supporting
files (references, sub-docs, scripts). One skill = one directory.

### Home (a.k.a. the Store)
The single central directory where every skill `skillm` knows about physically lives —
`~/.skillm/`, with skills under `~/.skillm/skills/<id>/`. There is exactly one Home per
machine. It is laid out **flat by Skill ID** (one directory per skill); two skills cannot
share an ID, so a colliding `add` is an error that the user resolves with `--as <name>`.
Everything an agent sees is a symlink back into Home.

### Agent
A tool that consumes skills by reading them from a skill folder. An Agent is **defined**
by a name and the skill-folder **location** it reads at each Scope — its Global location
and its Local location, which need not mirror each other. These definitions live in
Config, so supporting a new agent means **declaring its locations in config**, never
changing skillm's source. skillm ships built-in definitions for **Claude** and **Codex**;
those are also what a fresh Config is seeded with, but neither is privileged — any defined
agent can be disabled, including Claude.

### Source
A location skills are fetched from. Primary kind is a **git repository**, which may hold
one or many skills (it acts as a catalog). Also supported: a **local path** to a skill
directory. A Source is remembered for every added skill so it can be re-checked and updated.

### Skill ID
The stable name used to refer to and select a skill — by convention its directory name
(e.g. `grill-with-docs`). Used to disambiguate when a Source holds multiple skills and to
script non-interactive `add`.

### Revision
The per-skill content identity recorded at `add` time and compared on update checks. For a
git-sourced skill it is the **git tree object SHA of the skill's own subdirectory** (read via
`git ls-tree <ref>:<path>` against a treeless fetch — no full clone). It is scoped to a single
skill's files, never the whole repo: a commit that touches a different skill must not register
as an update to this one.

### Check
Report which git-sourced skills have an upstream Revision different from the installed one,
without changing anything. Read-only.

### Update
Pull the current upstream Revision of outdated git-sourced skills into Home (default: all of
them; optionally one Skill ID). Because agents see skills through symlinks into Home, updating
the Home copy updates every install automatically. Shows a progress bar when there is enough work
to warrant one. Does not show diffs.

### List
Show every skill in Home with its Source, the Scopes/Agents it is currently installed at
(read live from disk), and its update status (up-to-date / update available / local /
untracked).

### Local skill
A skill added from a local path. It is copied into Home, where the Home copy becomes the
canonical source of truth. Local skills have no upstream and are **not** Revision-tracked;
updating one means editing it directly in Home (skillm warns rather than checking for updates).

### Scope
*Where* a skill is made available to an agent. Two scopes:
- **Global** — available to the agent everywhere (the agent's user-level skill folder).
- **Local** — available only within one project (the agent's project-level skill folder).

The on-disk skill format is identical across all supported agents, so a single copy in Home
can be installed to any combination of agents and scopes; an Install is always the same
operation.

## Core verbs

### Add
Fetch a skill from a Source into Home. Does not, by itself, expose it to any agent.

### Install
Make a skill visible to agents by creating a symlink from each Enabled agent's skill folder
(at a chosen Scope) back to the skill in Home. This is what turns an added skill into one an
agent can actually see. An Install always targets **every Enabled agent** at the chosen
Scope — there is no per-command agent choice — and a single Install command applies one Scope
to every skill it acts on. Acts on one or more named skills, or interactively on a multiselect
of every skill in Home. Passing a Scope (`--global`/`--local`) to `add` installs in the same
step; bare `add` is fetch-only. Which installs currently exist is never stored — it is read
live by scanning agents' skill folders for symlinks pointing into Home, so it never drifts.

### Uninstall
Remove a skill entirely — the inverse of **Add**, not of Install. Uninstall first removes the
skill's symlink from every Agent and Scope it was installed at (the global folder and every
tracked project, across all defined Agents — even ones now disabled, so nothing is left
dangling), then deletes the Home copy and its Registry entry. There is **no per-scope
uninstall**: it always clears every reference. Safe by default — on a terminal it confirms
first (skip with `--yes`/`--force`). Acts on one or more named skills, or interactively on a
multiselect of every skill in Home.

### Enabled agents
The Agents that Links are applied to: the subset of agents **defined** in Config whose
`enabled` flag is set. An agent must be defined in Config before it can be enabled.
Managed by editing config or interactively via `skillm agent` (a multiselect over the
defined agents that writes the flags back). Disabling an agent keeps its definition — and
its locations — intact, so it can be re-enabled without re-entering paths.

## Persistence

### Config
`~/.skillm/config.toml` — user-owned, hand-editable, and the **single source of truth for
where skills are installed**. It holds the Agent definitions: for each known agent, the
skill-folder locations it reads at each Scope and whether it is Enabled. skillm seeds it
with the built-in defaults the first time Home is created, and otherwise avoids rewriting
it (only `skillm agent` does, to toggle the Enabled flags).

### Registry
`~/.skillm/state.toml` — machine-managed record skillm writes freely. One entry per added
skill holding what cannot be re-derived: its Source (URL, subpath, ref), kind (git/local),
the Revision recorded at `add`, and the install timestamp.
