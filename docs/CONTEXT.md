# skillm — Ubiquitous Language

The canonical glossary for the project. Glossary only — no implementation details.
Terms are added/sharpened as the design is resolved.

## Core nouns

### Skill
A self-contained unit of agent instructions. On disk it is a directory whose entry
point is a `SKILL.md` file (with YAML frontmatter), optionally accompanied by supporting
files (references, sub-docs, scripts). One skill = one directory.

### Home
The single central directory holding skillm's own state — `~/.skillm/`, which contains
**only** `config.toml` and `state.toml`. There is exactly one Home per machine. Home does
**not** store skills: there is no skills library. A skill's files live solely in its
**Canonical copies** (the Installs), which are the only copies of its content. Two installs
of the same skill cannot share a Skill ID; a colliding install from a different Source is an
error the user resolves with `--as <name>`.

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
directory. A Source is remembered for every installed skill so it can be re-checked and updated.

### Skill ID
The stable name used to refer to and select a skill — by convention its directory name
(e.g. `grill-with-docs`). Used to disambiguate when a Source holds multiple skills and to
script a non-interactive `install`.

### Revision
The per-skill content identity recorded at install time and compared on update checks. For a
git-sourced skill it is the **git tree object SHA of the skill's own subdirectory** (read via
`git ls-tree <ref>:<path>` against a treeless fetch — no full clone). It is scoped to a single
skill's files, never the whole repo: a commit that touches a different skill must not register
as an update to this one.

### Check
Report which git-sourced skills have an upstream Revision different from the installed one,
without changing anything. Read-only.

### Update
Pull the current upstream Revision of outdated git-sourced skills (default: all of them;
optionally one Skill ID). A changed git skill is fetched once and its new content is written
**straight into every recorded install** — the Global copy in `~/.agents/skills` and the Local
copies across every tracked project (a Local copy's Lockfile entry refreshed alongside) — with
no intermediate library. A git skill's copies are overwritten only when it actually changed
upstream; a local skill's copies are re-synced from its recorded **source directory** whenever
their content differs (so an unchanged skill produces no git churn), and left as-is with a
warning when that source directory is gone. An all-skills Update also runs **Import**'s adoption
over every tracked project first, so skills a teammate added to a Lockfile join the update. A
recorded install whose copy has vanished is reported and forgotten; if that was a skill's last
install, its Registry entry is dropped. Shows a progress bar when there is enough work to
warrant one. Does not show diffs.

### List
Show every installed skill with its Source, the Scopes/Agents it is currently installed at
(read live from disk), and its update status (up-to-date / update available / local /
untracked).

### Local skill
A skill installed from a local path. Its recorded **source directory is its upstream**: Update
re-syncs the installs from that path when their content differs, and warns (leaving the installs
as-is) when the path no longer exists. Local skills carry no ref/Revision and are not fetched
over the network.

### Scope
*Where* a skill is made available to an agent. Two scopes:
- **Global** — available to the agent everywhere (the agent's user-level skill folder).
- **Local** — available only within one project (rooted at the project directory).

The on-disk skill format is identical across all supported agents, so one skill's files
can be installed to any combination of agents and scopes. An Install at either Scope is
materialized the same way: a **Canonical copy** in that Scope's `.agents/skills` store plus a
Link for every other enabled agent (see below).

### Canonical copy (the Install shape at both Scopes)
An Install writes a real, self-contained copy of the skill's files into the Scope's
**canonical store** — `<project>/.agents/skills/<id>` for a Local Install,
`~/.agents/skills/<id>` for a Global one. That is the cross-agent convention read natively by
Codex, Cursor, Amp, Gemini CLI and others, and the same locations vercel's `npx skills` CLI
installs to. Every other enabled agent (e.g. Claude) gets a symlink from its own folder at
that Scope into the copy: **relative** and in-repo at Local (`.claude/skills/<id> →
../../.agents/skills/<id>`), absolute at Global (`~/.claude/skills/<id> →
~/.agents/skills/<id>`). A Local Install's copy, links, and the Lockfile entry written
alongside are all committable, so the install travels to teammates who clone the repo — an
absolute symlink into the installer's machine would be broken for everyone else. A canonical copy
is still skillm-managed: skillm records which installs hold one (in the Registry — the project
roots per skill, and the Global flag) so Update can refresh it and Uninstall can clear it. A
Local copy and a Global install can coexist for the same skill.

### Lockfile
`skills-lock.json` at a project root — the committable, per-project record of where each
locally installed skill came from (source, ref, path of its `SKILL.md`) and a content hash of
the installed folder. The format is byte-compatible with vercel's `npx skills` CLI: either
tool can read, extend, and update a repo the other set up, and keys skillm does not model are
preserved verbatim on rewrite. skillm's own source of truth stays the Registry; the Lockfile
is the shared interop surface, and what **Import** consumes.

## Core verbs

### Install
Fetch a skill and make it visible to agents in one step: materialize its **Canonical copy** at
a chosen Scope and link each Enabled agent's skill folder to it. Install is the single entry
point — there is no separate fetch-into-a-library step. It always targets **every Enabled
agent** at the chosen Scope (there is no per-command agent choice), and a single Install command
applies one Scope to every skill it acts on. The first argument may be a **Source** (a git repo
or local path) — skillm fetches it (treelessly for git), lets the user pick which skills when
it is a catalog of several, and installs the result straight into the chosen Scope — or a bare
**Skill ID** of an already-installed skill, to add another Scope/project. Installing a bare id
copies the skill from its existing Global Canonical copy when there is one (no network),
otherwise re-fetches it from its recorded Source@ref (which may advance the recorded Revision).
Installing from the **same** Source refreshes the skill to the freshly fetched content; the
same Skill ID arriving from a **different** Source is a collision the user resolves by renaming.
A skill installed **only globally** is globally active — that is the accepted meaning of a
global install. Agent links are never stored — they are read live by scanning agents' skill
folders for skillm-owned symlinks, so they never drift; only the Canonical copies are recorded
(in the Registry), and a skill's Registry entry is written the moment its first install lands.
An Install at either Scope is a **Canonical copy** plus agent Links (a Local one adds a Lockfile
entry); re-installing over a recorded copy refreshes it in place, a legacy absolute symlink into
the old Home skills subtree at the canonical slot is converted to a copy, and skillm refuses to
overwrite files it did not create unless forced.

### Uninstall
Remove a skill entirely. Uninstall removes the skill's Global install (agent links and the
`~/.agents/skills` Canonical copy) and its Local installs from every tracked project (**agent
links, the Canonical copy, and the Lockfile entry** — committed files in the user's repos, so
the confirmation names those projects) — the only copies there are — sweeping every Agent and
Scope across all defined Agents (even ones now disabled, so nothing is left dangling), then
drops its Registry entry. There is **no per-scope uninstall**: it always clears every reference.
Safe by default — on a terminal it confirms first (skip with `--yes`/`--force`). Acts on one or
more named skills, or interactively on a multiselect of every installed skill.

### Import
Adopt a project's **Lockfile** into skillm's tracking — the bridge from a repo someone else
set up (with skillm on another machine, or with vercel's `npx skills`). For every entry that
describes a git remote, Import fetches the source at the locked ref (recording its Revision),
records the project as a tracked Local install root, writes a missing Canonical copy from the
fetched content (or restores it from an existing install), and creates missing agent Links. An
existing copy on disk is left untouched — reconciling content with upstream is Update's job.
Entries that are not git remotes (local paths, node_modules, registry skills) are reported and
skipped; a name already installed from a different Source is a collision that is skipped, never
overwritten. An all-skills **Update** runs Import's adoption automatically over every tracked
project.

### Enable (an agent)
Start applying Installs for an Agent. Enabling creates a symlink for that agent at every place
the already-Enabled agents are currently linked — the Global folder and every tracked
project — and, at every recorded install whose Canonical copy still exists (the Global one and
every Local root), gives the agent its link into it, bringing it to parity with its peers (a
canonical-folder agent is served by the copy itself and needs nothing). Enabling an agent while
nothing is installed anywhere does nothing. Performed interactively via `skillm agent`.

### Disable (an agent)
Stop applying Installs for an Agent: remove that agent's symlinks across every Scope and every
tracked project. **Canonical copies are never deleted by a disable** — they are the Scope's
skill store and the other agents' link target; removing copies is Uninstall's job. **Distinct from Uninstall** — the skill stays installed for
the other Agents; only this agent's footprint goes away. At least one Agent must always remain
Enabled, so deselecting every agent is refused (use Uninstall to remove the skills themselves).
Disabling keeps the agent's definition — and its locations — intact in Config, so it can be
re-enabled without re-entering paths.

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
`~/.skillm/state.toml` — machine-managed record skillm writes freely. One entry per installed
skill holding what cannot be re-derived: its Source (URL, subpath, ref), kind (git/local),
the Revision recorded at install time, and the install timestamp. It also records, per skill,
where its Canonical copies live — the project roots holding a Local Install, and a flag for the
Global one — the one piece of install state skillm stores, because a copy (unlike a Link)
cannot be re-discovered by a live disk scan. **An entry exists if and only if the skill is
installed somewhere**: installing a skill creates its entry, and removing its last install (via
Uninstall, or when Update prunes a vanished copy) drops it. This machine-wide index of installs
is what lets one `skillm update` sweep every project on the machine — the capability per-repo
lockfiles alone cannot provide.
