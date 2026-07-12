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
changing skillm's source. skillm ships built-in definitions for **Claude** and **agents**
(the universal `.agents/skills` entry); those are also what a fresh Config is seeded with,
but neither is privileged — any defined agent can be disabled, including Claude. The
"agents" definition points at the cross-agent `.agents/skills` folders (`~/.agents/skills`
/ `.agents/skills`), which Codex, Cursor, Amp, Gemini CLI and others read natively — Codex
does not read `.codex/skills` — and is named for the folder because toggling it affects
every agent that reads it.

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
the Home copy updates every symlink install automatically. **Vendored copies** are not symlinks,
so Update also re-syncs them: a git skill's copies are overwritten only when it actually changed
upstream, and a local skill's copies are re-synced from Home whenever their content differs (so
an unchanged skill produces no git churn). A recorded vendored root whose copy has vanished is
reported and forgotten. Shows a progress bar when there is enough work to warrant one. Does not
show diffs.

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
can be installed to any combination of agents and scopes. A Global Install is always a Link
(symlink); a Local Install can be materialized either as a Link or as a Vendored copy.

### Vendored copy (a.k.a. vendoring)
An alternative way to materialize a **Local** Install: instead of a symlink into Home, skillm
writes a real, self-contained copy of the skill's files into the agent's project skill folder.
Chosen so the skill can be committed to the project's git repository and travel to teammates who
clone it — a symlink would point at the installer's Home and be broken for everyone else. Only
the Local Scope can be vendored; a Global Install is always a Link. A vendored copy is still
skillm-managed: skillm records which project roots hold one (in the Registry) so Update can
refresh it and Uninstall can clear it, like any Install. Vendoring and a Link can coexist for the
same skill — e.g. a Global Link for personal use alongside a vendored Local copy committed to a
project. The committed copy carries no skillm metadata of its own, so on another machine it is
just files; a teammate who uses skillm installs the skill from its Source rather than adopting
the copy.

## Core verbs

### Add
Fetch a skill from a Source into Home. Does not, by itself, expose it to any agent.

### Install
Make a skill visible to agents by creating a symlink from each Enabled agent's skill folder
(at a chosen Scope) back to the skill in Home. This is what turns an added skill into one an
agent can actually see. An Install always targets **every Enabled agent** at the chosen
Scope — there is no per-command agent choice — and a single Install command applies one Scope
to every skill it acts on. Acts on one or more named skills, or interactively on a multiselect
of every skill in Home. Install can also act directly on a **Source** (a git repo or local
path) instead of an already-added Skill: given a Source it first **Adds** it to Home — choosing
interactively which skills when the Source is a catalog of several — and then installs the
result, making the whole fetch → choose → expose path one step. A Source whose Skill is already
in Home from the **same** Source reuses the Home copy without re-fetching (refresh it with
**Update**); the same Skill ID arriving from a **different** Source is a collision the user
resolves by renaming. **Add itself never installs** — exposing a skill is always Install's job.
Which symlink installs currently exist is never stored — it is
read live by scanning agents' skill folders for symlinks pointing into Home, so it never
drifts. A **Local** Install may instead be materialized as a **Vendored copy** (`--copy`, or
the interactive prompt) — a real copy of the files committed to the project; switching a
skill's Local install between a Link and a copy converts it in place, and skillm refuses to
overwrite files it did not create unless forced.

### Uninstall
Remove a skill entirely — the inverse of **Add**, not of Install. Uninstall first removes the
skill's symlink from every Agent and Scope it was installed at (the global folder and every
tracked project, across all defined Agents — even ones now disabled, so nothing is left
dangling), **deletes any Vendored copies** it has in tracked projects (committed files in the
user's repos — the confirmation names those projects), then deletes the Home copy and its
Registry entry. There is **no per-scope uninstall**: it always clears every reference. Safe by
default — on a terminal it confirms first (skip with `--yes`/`--force`). Acts on one or more
named skills, or interactively on a multiselect of every skill in Home.

### Enable (an agent)
Start applying Installs for an Agent. Enabling creates a symlink for that agent at every place
the already-Enabled agents are currently linked — the Global folder and every tracked
project — and, at every recorded Vendored root where a peer still holds a copy, writes a copy
for the agent too, bringing it to parity with its peers. Enabling an agent while nothing is
installed anywhere does nothing. Performed interactively via `skillm agent`.

### Disable (an agent)
Stop applying Installs for an Agent: remove that agent's symlinks across every Scope and every
tracked project, and delete its Vendored copies (its committed files) in every project. **Distinct
from Uninstall** — the skill stays in Home and stays installed for the other Agents; only this
agent's footprint goes away. At least one Agent must always remain Enabled, so deselecting every
agent is refused (use Uninstall to remove the skills themselves). Disabling keeps the agent's
definition — and its locations — intact in Config, so it can be re-enabled without re-entering
paths.

### Enabled agents
The Agents that Links are applied to: the subset of agents **defined** in Config whose
`enabled` flag is set. An agent must be defined in Config before it can be enabled. The
Enabled set is changed interactively via `skillm agent` (a multiselect over the defined
agents); changing it Enables or Disables the affected agents, reconciling their Links
immediately rather than only affecting future installs.

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
the Revision recorded at `add`, and the install timestamp. It also records, per skill, the
project roots where a Local Install was materialized as a Vendored copy — the one piece of
install state skillm stores, because a copy (unlike a Link) cannot be re-discovered by a live
disk scan.
