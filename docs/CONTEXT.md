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
**only** `config.toml`, `state.toml`, the `.lock` file behind the **Home lock** and, once a
**Refresh** ran, the **Refresh cache** `status.json`. There is exactly one Home per machine. Home does
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
one or many skills (it acts as a catalog), named either by URL or by GitHub **`owner/repo`
shorthand** (the form `npx skills add` takes — and the form the Lockfile records for a
GitHub HTTPS source, so both tools read the same identity). A shorthand is expanded to its
HTTPS clone URL, so `owner/repo` and `https://github.com/owner/repo` are one Source; being
path-shaped too, it loses to an existing local directory of that name. Also supported: a
**local path** to a skill directory, recorded as its absolute directory however it was typed.
A Source is remembered for every installed skill so it can be re-checked and updated.

### Inspection
A Source read once, ahead of an Install: a git repository cloned and pinned to one commit, or a
local directory, together with the skills found in it (Skill ID, name, description, location).
Inspecting prompts for nothing and installs nothing; an Install from it copies exactly the
inspected commit, while the recorded ref stays the branch or tag so Update keeps following it.
`skillm source inspect` takes one on its own; an Install run afterwards names the inspected
commit, and refuses a Source that has moved on since rather than install what nobody saw.
_Avoid_: preview

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
install, its Registry entry is dropped. Update fetches before it takes the **Home lock**; a
skill another skillm process reinstalled or updated meanwhile is never rolled back: its update
fails and asks to run Update again. Shows a progress bar when there is enough work to
warrant one. Does not show diffs.

### List
Show every installed skill with its Source, its kind (git or local) and the Scopes/Agents it
is currently installed at (read live from disk). Offline; upstream status is Check's job.

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

### Foreign entry
A file, directory or link at a path skillm would write (a canonical slot or an agent's link
path) that skillm did not create: a skill copied in by hand or by another tool. skillm never
overwrites one silently; the user overwrites it (**yes**, canonical slots only), takes it over
(**force**, link paths too) or skips that skill (**skip foreign**).
_Avoid_: conflict, unmanaged file

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
or local path) — skillm takes an **Inspection** of it (treelessly for git), lets the user pick
which skills when it is a catalog of several, and installs the inspected content straight into
the chosen Scope — or a bare **Skill ID** of an already-installed skill, to add another
Scope/project. Installing a bare id copies the skill from its existing Global Canonical copy
when there is one (no network), otherwise re-fetches it from its recorded Source@ref (which may advance the recorded Revision).
Installing from the **same** Source refreshes the skill to the freshly fetched content; the
same Skill ID arriving from a **different** Source is a collision the user resolves by renaming.
A skill installed **only globally** is globally active — that is the accepted meaning of a
global install. Agent links are never stored — they are read live by scanning agents' skill
folders for skillm-owned symlinks, so they never drift; only the Canonical copies are recorded
(in the Registry), and a skill's Registry entry is written the moment its first install lands.
An Install at either Scope is a **Canonical copy** plus agent Links (a Local one adds a Lockfile
entry); re-installing over a recorded copy refreshes it in place, a legacy absolute symlink into
the old Home skills subtree at the canonical slot is converted to a copy, and a **Foreign
entry** in the way stops the install until the user decides what to do with it.

### Uninstall
Remove a skill entirely. Uninstall removes the skill's Global install (agent links and the
`~/.agents/skills` Canonical copy) and its Local installs from every tracked project (**agent
links, the Canonical copy, and the Lockfile entry** — committed files in the user's repos, so
the confirmation names those projects) — the only copies there are — sweeping every Agent and
Scope across all defined Agents (even ones now disabled, so nothing is left dangling), then
drops its Registry entry. There is **no per-scope uninstall**: it always clears every reference.
Safe by default — on a terminal it confirms first (skip with `--yes`/`--force`), and asks again
if another process added a project to delete from while the question was open; a GUI asks its
own question and names the projects it confirmed. Acts on one or more named skills, or
interactively on a multiselect of every installed skill.

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
nothing is installed anywhere does nothing. Performed via `skillm agent` (a picker) or
`skillm agent set --enable`.

### Disable (an agent)
Stop applying Installs for an Agent: remove that agent's symlinks across every Scope and every
tracked project. **Canonical copies are never deleted by a disable** — they are the Scope's
skill store and the other agents' link target; removing copies is Uninstall's job. **Distinct from Uninstall** — the skill stays installed for
the other Agents; only this agent's footprint goes away. At least one Agent must always remain
Enabled, so deselecting every agent is refused (use Uninstall to remove the skills themselves).
Disabling keeps the agent's definition — and its locations — intact in Config, so it can be
re-enabled without re-entering paths.

### Upgrade
Replace the running **skillm binary** with the latest published release. Distinct from
**Update** in what it acts on: Update pulls new Revisions of installed Skills, Upgrade
replaces the CLI itself and touches no skill, install, or Registry entry. It compares the
running build's version against the latest release tag; when the release is newer it
downloads this platform's archive, verifies it against the release's published SHA256
manifest, and swaps the new binary into the path skillm is running from (a symlinked
install is resolved to the real file first). Nothing is written until the checksum
verifies, and the previous binary is restored if the swap fails. Only a skillm whose
**Upgrade method** is binary is upgraded this way.

### Upgrade method
How the running skillm gets upgraded, judged from its version and its executable's resolved
path alone, with no network request. **binary**: a release build installed on its own, which
Upgrade replaces in place. **bundled**: a release build inside a macOS app bundle, which must
upgrade it, so Upgrade refuses it and only reports a newer release (the skillm app carries no
CLI; this covers a copy another bundle holds). **dev**: a build whose version is not a clean release tag; it corresponds to no
published release, so it is reported and left alone, even inside a bundle.
_Avoid_: install method, channel

### Self status
The running skillm measured against the latest release: its current and latest versions,
whether a newer release is **available**, and whether Upgrade is **eligible** to install it
(available and binary). A bundled skillm can be available yet not eligible: its app upgrades it.
The macOS app offers "Upgrade skillm CLI to X" only when the **Installed CLI** is eligible.

### Enabled agents
The Agents that Links are applied to: the subset of agents **defined** in Config whose
`enabled` flag is set. An agent must be defined in Config before it can be enabled. The
Enabled set is changed via `skillm agent` (a multiselect over the defined agents) or
`skillm agent set --enable/--disable`, and listed by `skillm agent ls`; changing it Enables or Disables the affected agents, reconciling their Links
immediately rather than only affecting future installs.

## Update status

### Refresh
One **Check** of every skill together with a lookup of the latest release (the **Self status**),
saved as the **Refresh cache**. A scheduled Refresh (`skillm refresh --if-due`) runs only when
one is **Due**, and otherwise changes nothing and makes no network request.
_Avoid_: sync, poll

### Refresh cache
`~/.skillm/status.json` — the outcome of the last Refresh: each skill's upstream status, the Self
status, the problems it hit and the **Badge**, with when it was checked and when the next
scheduled Refresh is due. Only a Refresh creates it; Install, Update, Uninstall and Upgrade then
keep it in line with what they change. `skillm status` reads it offline, and a GUI may watch the
file itself.
_Avoid_: status file, update cache

### Badge
The one update indicator every GUI shows: on when a skill has an update available or a newer
skillm release exists. A lookup that failed is a problem in the Refresh cache, never part of the
Badge and never "up to date".
_Avoid_: dot, notification

### Due
The condition under which a scheduled Refresh runs: the refresh Setting is on, and no Refresh
ever ran, the refresh interval has passed since the last one (an hour, when every lookup of the
last one failed), or the last one is dated in the future. Which skillm wrote the cache never
makes a Refresh Due.

### Stale
A Refresh cache older than the refresh interval, dated in the future, or never written. A stale
cache is still read; how to show its age is the GUI's choice.

## JSON protocol

### JSON mode
A run of a skillm command with `--json`: stdout carries only protocol output (one **Envelope**,
or an **Event stream** with `--events`), and skillm never prompts or draws terminal UI. Only
commands that declare a JSON mode accept it; the rest refuse with `json_unsupported`. A
question the terminal would ask becomes a flag the run must carry, or a refusal whose error
code tells the GUI what to ask before running the command again with the answer.
_Avoid_: machine mode, API mode

### Envelope
The one JSON document that is a command's outcome in JSON mode: its data on success or its
error (a stable **error code**, a message and whether a plain retry may succeed) on failure,
plus the warnings it reported either way. A failed command still writes one.
_Avoid_: response, payload

### Event stream
The NDJSON form of JSON mode (`--events`): one line per core event as the work happens, ending
in exactly one line that holds the Envelope.
_Avoid_: progress feed

### API version
The number `skillm version --json` reports for the commands' arguments and data; a GUI refuses a
CLI whose API version it does not know. Distinct from the **schema version** every document and
event line carries, which covers only the Envelope and event-line shape. The **capabilities**
reported alongside it name the commands that have a JSON mode.

### Installed CLI
The skillm binary the macOS app drives: the one the user installed on their own (`install.sh`,
Homebrew, or anywhere on the login shell's PATH), looked up at every launch check and again
whenever its file changes. The app carries no CLI; the two are released and versioned apart, and
the **API version** keeps them compatible: a CLI too old for the app is **too old** (Upgrade
skillm CLI), one too new is **too new** (Check for app update), and none at all is **missing**
(Install skillm CLI).
_Avoid_: bundled CLI, helper, embedded binary

### App release
A release of the macOS app alone, tagged `mac-vX.Y.Z` with its own version and never GitHub's
"latest" release, which stays the CLI's (`vX.Y.Z`), since `install.sh` and Upgrade read it.
_Avoid_: app version of the CLI release

### Appcast
The feed of App releases the macOS app's updater (Sparkle) reads, kept on the fixed
`macos-appcast` GitHub release and replaced by every App release. The updater installs only an
app signed with the EdDSA key whose public half the running app carries, and the menu offers
"Upgrade app and restart" once the appcast lists a newer app than the running one.

## Persistence

### Config
`~/.skillm/config.toml` — user-owned, hand-editable, and the **single source of truth for
where skills are installed**. It holds the Agent definitions: for each known agent, the
skill-folder locations it reads at each Scope and whether it is Enabled, and the **Settings**.
skillm seeds it with the built-in defaults the first time Home is created, and otherwise
rewrites it only on an explicit change: `skillm agent` (the Enabled flags) and `skillm config
set` (a Setting). Such a rewrite replaces the whole file, dropping hand-written comments.

### Setting
A named preference a GUI offers, kept in Config beside the Agent definitions and read or changed
by key (`refresh.enabled`, `refresh.interval_hours`). A Setting Config does not hold, or holds a
value it cannot use, has its default. The **refresh** Settings say whether a scheduled
**Refresh** runs at all and how many hours apart.
_Avoid_: option, preference

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

### Home lock
The exclusive, cross-process lock on Home that serializes skillm processes. Every command that
changes Config, the Registry or any install (Install, Update, Uninstall, Import, enabling or
disabling agents, and changing a Setting) holds it across its writes, and so does every write of
the **Refresh cache**. Each asks its questions (and fetches or checks) before locking, then
re-reads Home under the lock. List, Check, Status, taking an Inspection, listing agents and
reading Settings take none; Upgrade takes it only to record its result in the Refresh cache. A command that
finds Home locked waits for the holder, then gives up after a bounded wait with an error naming
the holding command. Readers need no lock because every save of `config.toml` and `state.toml`
replaces the file in one step.
_Avoid_: Lockfile (that is the per-project `skills-lock.json`)
