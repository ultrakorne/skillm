# skillm — Implementation Plan (v0.1.0)

A single, self-contained spec for building `skillm`. Pair this with
[CONTEXT.md](./CONTEXT.md) (the glossary) — terms in **Title Case** are defined there.

---

## 1. What skillm is

`skillm` is a Go CLI that manages AI-agent **Skills**. It keeps every skill in one central
**Home** and **Links** them (via symlinks) into the skill folders that **Agents** read.
Agents are **defined in config**, not hardcoded: each agent declares the skill-folder
location it reads at each Scope. skillm ships built-in definitions for **Claude** and
**agents** — the cross-agent `.agents/skills` folder read by Codex, Cursor, Amp, Gemini CLI
and others (all agents share an identical on-disk skill format — a directory with
`SKILL.md` — so one Home copy serves them all), and a user supports a new agent (e.g.
opencode) purely by adding its locations to `config.toml`, never by changing skillm's source.

**Constraints:** Go; Linux + macOS only (amd64/arm64); MIT licensed; public repo at
`github.com/ultrakorne/skillm`; binary name `skillm`.

---

## 2. Layout on disk

```
~/.skillm/
├── config.toml      # user-owned preferences (hand-editable)
├── state.toml       # machine-managed registry (skillm writes freely)
└── skills/
    └── <skill-id>/  # one directory per skill, flat by Skill ID
        └── SKILL.md
```

Agent skill folders that get symlinks into Home. **These paths are not hardcoded** — they
come from each agent's definition in `config.toml`; the table below is just the seeded
default for `claude` and `agents`:

| Scope  | claude                      | agents                      |
|--------|-----------------------------|-----------------------------|
| Global | `~/.claude/skills/<id>`     | `~/.agents/skills/<id>`     |
| Local  | `<base>/.claude/skills/<id>`| `<base>/.agents/skills/<id>`|

The `agents` entry is the cross-agent `.agents/skills` convention, which Codex, Cursor,
Amp, Gemini CLI and others read natively (Codex does not read `.codex/skills`). It is
named for the folder rather than any one tool because toggling it affects every agent
that reads it.

Each entry is a symlink: `<agent-folder>/<id> → ~/.skillm/skills/<id>`. `<base>` is the
project root for Local scope — the current directory, or a custom path passed to `link`.

### config.toml — agent definitions

`config.toml` is the single source of truth for **where** skills are installed. Each agent
is a `[agents.<name>]` table:

```toml
[agents.claude]
enabled = true
global  = "~/.claude/skills"   # ~ expands to home; <id> is appended
local   = ".claude/skills"     # relative to the project base; <id> is appended

[agents.agents]
enabled = true
global  = "~/.agents/skills"   # cross-agent convention: Codex, Cursor, Amp, Gemini CLI, …
local   = ".agents/skills"

# Add a new agent by adding a table — no source change. Both scopes are
# optional (at least one required); omit a scope the agent has no folder for.
[agents.opencode]
enabled = true
global  = "~/.config/opencode/skill"   # global & local need not mirror
local   = ".opencode/skill"
```

Path rules: `global` expands a leading `~` to the user's home (and, if relative, is rooted
at home); `local` is a relative path joined to the project base; skillm always appends the
Skill ID. No `$ENV` expansion. `enabled` defaults to `true` when omitted.

**Seeding & ownership.** When Home is first created (`EnsureHome`), skillm writes this file
with the built-in claude+agents defaults *only if it is absent* — it never clobbers an
existing file. The same built-in defaults are the in-memory fallback if the file is missing,
so "what's written" equals "what you fall back to". Thereafter the file is hand-edited; the
only command that rewrites it is `skillm agent` (toggling `enabled`), which re-marshals the
whole file and so does not preserve hand-written comments. Agents iterate in a deterministic
order (sorted by name). Scope is not configured here; theme is auto-detected.

### state.toml (registry)

```toml
[[skills]]
id           = "grill-with-docs"
kind         = "git"                                          # "git" | "local"
source       = "https://github.com/ultrakorne/skill-collection"
path         = "grill-with-docs"                              # subpath within the repo
ref          = "refs/heads/master"                            # branch/tag/sha pinned at add
revision     = "d97bdddcddc6818bc7ae1a0ff501912739da6cf4"     # subdir tree SHA at add
installed_at = "2026-06-13T18:00:00Z"

[[skills]]
id           = "my-local-skill"
kind         = "local"
source       = "/home/ultra/dev/my-skill"                     # original path (informational)
installed_at = "2026-06-13T18:05:00Z"
vendored_at  = ["/home/ultra/projC"]                          # roots holding a committed copy
# local skills have no ref/revision and are not update-tracked

# Project directories skillm has linked skills into at local scope. Not link
# state — only the set of folders to scan, so `list`/`remove` find local links
# outside the current directory. Pruned when a folder holds no skillm link.
local_roots = ["/home/ultra/projA", "/home/ultra/projB"]
```

`vendored_at` is the **one piece of install state skillm stores**, and only for Vendored
copies: a copy (unlike a symlink) cannot be re-discovered by a live disk scan, so the project
roots that hold one are recorded per skill. Symlink installs remain scan-only.

**Link existence is never stored** — links are read live by scanning each agent folder (global,
plus the current directory and every `local_roots` entry) for symlinks whose target resolves
into `~/.skillm/skills/`. Only the *set of local folders to scan* is persisted, so a recorded
root never lets stale link state survive: a folder with no skillm link is pruned.

---

## 3. Command surface

```
skillm add <url> [skill_id] [--as <name>] [--ref <ref>] [--all]    # fetch into Home only
skillm add <local-path>      [--as <name>]                         # fetch into Home only
skillm install <url> [skill_id...] [--as <name>] [--ref <ref>] [--all] [--global|--local] [--copy]
skillm install <local-path>        [--as <name>]               [--all] [--global|--local] [--copy]
skillm install   [skill_id...] [--all] [--global|--local] [--copy]  # in-Home ids; no id = interactive multiselect
skillm uninstall [skill_id...] [--all]                      # no id = interactive multiselect
skillm list
skillm check
skillm update [skill_id]            # no arg = all outdated; also re-syncs vendored copies
skillm agent                        # interactive multiselect of Enabled agents
```

`add` **only fetches** into Home; `install` is the **only** command that exposes skills to
agents — whether from an in-Home Skill ID or directly from a Source (a `<url>`/`<local-path>`,
which it fetches first; see install §3). `--global`/`--local`/`--copy` therefore live on
`install` alone. `--copy` vendors a real copy into the project instead of a symlink (Local scope
only; see §3a); it is rejected with `--global`, and implies `--local` when given alone.

Global flags: `--force` / `--yes` (skip confirmations), `--home <path>` (override Home).

### add
`add` **only fetches** into Home — it never installs (that is `install`'s job). It takes no
scope/copy flags.
- `<url>` is a git repo (a catalog of one or more skills). skillm does a **treeless fetch**
  (`git clone --filter=tree:0 --depth 1` into a temp dir, or `--filter=blob:none`), then scans
  for directories containing `SKILL.md`.
  - 0 skills → error. 1 skill → add it. >1 → interactive **huh** multiselect (unless a
    `skill_id` arg or `--all` was given, which skips the prompt — required for non-TTY).
- For each selected skill: sparse-checkout its subdir, copy into `~/.skillm/skills/<id>/`,
  read its subdir tree SHA via `git ls-tree <ref> <path>`, and write a registry entry
  (`kind=git`, source, path, ref, revision, installed_at).
- `<local-path>` (a directory containing `SKILL.md`) → copy into Home as `kind=local`.
- `--as <name>` overrides the Skill ID (resolves collisions). `--ref <ref>` pins a
  branch/tag/sha (default: repo's default branch).
- **Collision:** if the target ID already exists in Home → error suggesting `update` or `--as`.
- This fetch → discover → select → add-to-Home pipeline is **shared code** with `install`'s
  source mode (extracted into one helper, not duplicated).

### install
- Symlink the selected skills into **every Enabled agent** at the chosen Scope, using each
  agent's location for that scope from `config.toml`. `install` is the **only** command that
  installs — from an in-Home Skill ID or directly from a Source.
- **Source mode (`install <url>` / `install <local-path>`):** when the first arg is a git URL
  or an **explicitly path-shaped** local path (`./`, `../`, `/`, `~`, or a `*.git` suffix), it
  is a **Source**. skillm runs the **same** fetch → discover → select → add-to-Home pipeline as
  `add` (interactive multiselect over a multi-skill catalog, or `[skill_id...]` / `--all` to
  select non-interactively; `--as` / `--ref` behave as in `add`), then installs the result at
  the chosen Scope — fetch → choose → expose in one command. A **bare name** is *always* an
  in-Home id, never a Source, even if a same-named directory exists in cwd (to install a local
  dir, path-qualify it: `install ./my-skill`). A Source cannot be mixed with in-Home ids in one
  invocation. Requires ≥1 Enabled agent **before** fetching; on a non-TTY, source mode needs a
  selector (`skill_id` / `--all`) **and** a scope flag, else it errors.
- **Source collision:** a selected skill already in Home from the **same** Source is installed
  from the existing Home copy *without re-fetching* (skillm says so and points at `update` to
  refresh); the same id arriving from a **different** Source is a collision error suggesting
  `--as`. This is checked across the **whole** selection before anything is added or installed
  (atomic — one different-source clash installs nothing).
- **Selection (in-Home mode):** one or more `skill_id` args act on exactly those skills; `--all`
  acts on every skill in Home; with neither, skillm shows an interactive **huh** multiselect over
  the skills in Home (refused on a non-TTY, which names the `skill_id` / `--all` escape hatch). An
  explicit id that is not in Home is an **atomic error** — nothing is installed.
- **Scope:** `--global`/`--local` selects scope explicitly; with neither flag skillm asks
  interactively — **Global**, **Local** (the current directory), or a **custom path** typed with
  Tab path-completion (on a non-TTY it refuses and requires a flag). A single chosen Scope
  applies to **every** selected skill. `--local` and the custom path join that directory to each
  agent's `local` location (created if missing).
- **Missing scope:** an Enabled agent that defines no location for the chosen scope is
  **skipped with a notice** (it simply has no folder there). If *no* Enabled agent defines a
  location for that scope, the command errors.
- **Safe by default:** `install` refuses to overwrite any existing entry skillm didn't create
  (i.e. not a symlink into Home) — it errors and leaves your own skill untouched. Re-installing
  an already-correct symlink is a no-op.

### 3a. Vendored copies (`--copy`)

A **Local** install can be materialized as a real, committed copy instead of a symlink, so the
skill travels with the project's git repo to teammates (a symlink would point at the installer's
Home and break on clone). See CONTEXT "Vendored copy".

- **Choosing it.** `--copy` (Local scope only — rejected with `--global`, implies `--local` when
  alone). Interactively, after a Local/custom-path scope choice, skillm asks "symlink or copy?"
  (cursor defaults to symlink). A bare non-interactive `--local` stays a symlink — `--copy` is the
  explicit opt-in.
- **What it writes.** A full copy per Enabled agent (`<base>/<agent.local>/<id>`), copied from the
  Home skill dir. The committed dir carries no skillm marker — on another machine it is just
  files; a teammate who uses skillm installs from the Source rather than adopting the copy.
- **Tracking.** Each base is recorded in the skill's `vendored_at`. There is no in-skill marker;
  this registry record is how `update`/`uninstall`/`list`/`agent` find the copies.
- **Conflicts & conversion.** Over skillm's own symlink, `--copy` converts in place (and `install`
  without `--copy` converts a recorded copy back to a symlink, dropping the root). Over a *foreign*
  file/dir (a hand-authored skill, or a teammate's committed copy on a fresh clone) skillm asks
  once on a TTY / refuses on a non-TTY unless `--force`/`--yes`; forcing adopts it into
  `vendored_at`. After vendoring, skillm prints a hint to commit the base.

### uninstall
- Removes skills **entirely** — the inverse of `add`, not of `install`. For each selected skill
  it auto-unlinks from **all** agents/scopes (the global folder and every tracked local root,
  across all *defined* agents — even disabled ones, so nothing dangles), **deletes its Vendored
  copies** in every recorded project (committed files in the user's repos — copies are removed
  before the symlink sweep so the sweep never trips on a real directory), then deletes the Home
  copy and its registry entry. There is **no per-scope uninstall**. The TTY confirmation names
  any project a committed copy will be deleted from.
- **Selection:** same model as install — one or more `skill_id` args, `--all`, or an interactive
  multiselect; an unknown explicit id is an atomic error (nothing is removed).
- On a TTY it confirms once for the whole batch (skip with `--yes`/`--force`); a non-TTY run
  proceeds without prompting.

### list
- Table (lipgloss): `ID | Source | Installed (scopes×agents, read from disk) | Status`.
- Symlink installs are read live from disk; **Vendored copies** are read from the recorded
  `vendored_at` roots (never an injected cwd, so a stray dir in the current folder is never
  mistaken for a copy) and rendered with a `copy` tag: `local(copy): claude,codex` or
  `local(/projB, copy): claude`.
- Status ∈ `up-to-date`, `update available`, `local`, `untracked` (git skill whose subdir
  vanished upstream).

### check
- Read-only. For each `kind=git` skill: treeless-fetch its ref, compute the current upstream
  subdir tree SHA, compare to `revision`. Print which skills have updates available. Changes
  nothing. Per-skill — a commit touching a different skill never flags this one.

### update
- Default (no arg): update **all** outdated git skills; `update <id>` does one. For each, pull
  the current revision's subdir into Home (overwrite the Home copy), then update `revision` +
  `installed_at` in the registry. Because agents see Home through symlinks, every symlink install
  updates automatically. Shows a **bubbles/progress** bar when there is enough work to warrant
  it. No diffs.
- **Vendored copies** (not symlinks) are re-synced afterward from the recorded `vendored_at`
  roots, across all *defined* agents that hold a copy: a git skill's copies are overwritten only
  if it actually changed this run; a local skill's copies are re-synced from Home only when their
  content differs (so an unchanged skill leaves the repo's files — and `git status` — untouched).
  A recorded root whose copy has vanished (project moved/deleted) is reported and pruned. A local
  skill with **no** copies still shows the "edit in Home directly" note.

### agent
- **huh** multiselect over the agents **defined** in `config.toml`, seeded with the current
  `enabled` flags; writes the toggled flags back (preserving each agent's locations).
  Defining a *new* agent is a config edit, not something this command does.
- **Reconciles links immediately** (it does not merely affect future installs): the change
  is applied in two passes — **enable pass first, then disable pass** — so a one-shot swap
  (disable A, enable B) lets B copy A's links while they are still on disk.
  - **Enable** a previously-disabled agent → for every place the **before-enabled** agents
    are currently linked (global + every tracked local root + cwd), create the same link for
    the newly-enabled agent; and at every recorded vendored root where a peer still holds a copy,
    **write a copy** for the agent too. Footprint is read live from disk. Enabling while nothing is
    installed does nothing.
  - **Disable** an agent → remove **all** its skillm-managed links across global + every
    tracked local root + cwd, and **delete its Vendored copies** in every recorded project. The
    Home copy and the other agents' installs are left intact — this is **not** uninstall. The
    confirmation names the projects whose committed copies will be deleted; emptied vendored roots
    are pruned.
  - Unchanged agents are never touched (`agent` toggles, it does not repair drift — use
    `install` for that).
- **At least one agent must stay enabled**: an empty selection is refused (pointing at
  `uninstall` for removing skills themselves).
- **Confirms** only when the change removes links (a disable is present); an enable-only
  change is additive and applies without a prompt. Skip with `--yes`/`--force`. A spot
  blocked by a foreign file/symlink is **skipped with a warning**, never aborting the sweep.
  Tracked local roots left with no link are pruned.

---

## 4. Behavior in non-interactive contexts

Pretty output on a TTY; **auto-degrade** when stdout is not a TTY (no colors, spinners, or
prompts). Commands that would prompt either accept explicit args (`add <url> <id>`, `--all`)
or fail with a clear message telling you which flag to pass. No `--json` (out of scope for
v0.1.0). This covers dotfiles/CI bootstrap via deterministic commands.

---

## 5. Architecture

```
skillm/
├── main.go                 # thin: fang.Execute(ctx, cmd.Root())
├── cmd/                    # cobra commands, one file each (add, install,
│                           #   uninstall, list, check, update, agent, root)
└── internal/
    ├── config/             # load/save ~/.skillm/config.toml (go-toml/v2); agent
    │                       #   definitions (name → enabled/global/local) + seed defaults
    ├── state/              # load/save ~/.skillm/state.toml registry
    ├── store/              # Home bootstrap + layout; add/remove skill dirs; copy/replace
    │                       #   dir + content-equality (for vendored copies)
    ├── skill/              # Skill model; parse SKILL.md YAML frontmatter
    ├── source/             # parse URL vs local path; discover SKILL.md dirs
    ├── gitx/               # shell-out git: treeless fetch, ls-tree, sparse-checkout
    ├── agentdir/           # pure path computation from a config-supplied agent
    │                       #   (global/local templates) → skill-folder path per scope
    ├── linker/             # create/remove symlinks; scan-for-links; safe overwrite;
    │                       #   Classify (target-kind for the vendoring layer in cmd)
    └── ui/                 # lipgloss v2 styles, huh pickers, progress, tables, isatty
```

**Dependencies:** `spf13/cobra`, `charmbracelet/fang`, `charmbracelet/huh`,
`charmbracelet/lipgloss/v2` (+ `lipgloss/table`), `charmbracelet/bubbletea` +
`bubbles/progress`, `pelletier/go-toml/v2`, `goccy/go-yaml` or `gopkg.in/yaml.v3`
(frontmatter), `mattn/go-isatty` (TTY detection). System `git` is required at runtime —
checked once at startup with a friendly error if absent.

---

## 6. Distribution

- **GoReleaser** (`.goreleaser.yaml`): build `darwin`/`linux` × `amd64`/`arm64`, archive +
  checksums, GitHub Release on tag. No Homebrew tap.
- **`install.sh`**: detect OS/arch, download the matching release asset, install to
  `/usr/local/bin` (fallback `~/.local/bin`). README one-liner: `curl -fsSL <raw>/install.sh | sh`.
- **`go install github.com/ultrakorne/skillm@latest`** works for Go users.
- **CI** (GitHub Actions):
  - `ci.yml` on push/PR → `go build`, `go test ./...`, `go vet`, `golangci-lint`.
  - `release.yml` on tag `v*` → `goreleaser release --clean`.
- **Versioning:** semver tags, first release `v0.1.0`. Version embedded via ldflags
  (`skillm --version`).

---

## 7. README (concise, minimalist)

One-line install (`curl | sh`), a 4–5 line quickstart (`add` → `agent` → `link` → `check`/
`update`), and a compact command table. Nothing more.

---

## 8. Testing

- **Unit:** source classification (URL vs path), frontmatter parsing, revision comparison,
  registry read/write round-trip, link-path computation, safe-overwrite decisions.
- **Integration:** spin up a temp Home + a local git repo (init, commit several skill dirs),
  then exercise `add`/`link`/`check`/`update`/`remove` and assert symlink targets and registry
  contents. System git is available in CI.
- **Vendoring:** the vendored-copy decision matrix (write / convert-from-symlink / refresh /
  foreign-refuse-then-force / remove) is unit-tested in-process (`cmd/vendor_test.go`), plus an
  end-to-end test (`TestVendoredCopyLifecycle`, `TestVendorSymlinkConversion`) driving the real
  binary: copies are real dirs not symlinks, a global symlink coexists, `update` refreshes both
  copy and symlink, `uninstall` deletes the copies, and `--copy --global`/foreign-overwrite
  guards hold. `store.ReplaceDir`/`DirContentEqual` and the `state` vendored-root helpers are
  unit-tested too.

---

## 9. Build order (phases for ultracode)

1. **Scaffold** — `go mod init github.com/ultrakorne/skillm`, `main.go`, cobra+fang root,
   `config`/`state` packages with TOML round-trip (agent definitions: name →
   enabled/global/local, built-in claude+agents defaults), Home bootstrap that seeds
   `config.toml` when absent, startup git check.
2. **Core libs** — `skill` (frontmatter), `source` (classify + discover), `gitx` (treeless
   fetch, `ls-tree`, sparse-checkout), `store` (copy skill into Home).
3. **add** — git (discover + huh select, `--as`/`--ref`), local copy. Fetch-only (no scope/copy
   flags); the fetch → discover → select → add-to-Home pipeline is factored into one helper
   shared with install's source mode.
4. **agentdir + linker** — folder mapping from config-supplied agents (global/local
   templates, skip a scope an agent doesn't define), symlink create/remove, scan-for-links,
   safe overwrite.
5. **install** — wire linker to Enabled agents + scope (variadic ids / `--all` / interactive
   multiselect, one scope for all); skip agents missing the scope (error if none has it).
   **Source mode** (`install <url|path>`) reuses add's fetch helper, then installs the result;
   smart same-Source-installs / different-Source-errors collision, atomic across the selection.
6. **agent** — huh multiselect over defined agents → toggle `enabled` flags in config.
7. **list / check** — registry + live link scan + per-skill tree-SHA compare.
8. **update** — all/one, registry rewrite, bubbles/progress bar.
9. **uninstall** — variadic ids / `--all` / interactive multiselect; auto-unlink everywhere +
   delete + confirm.
10. **ui polish** — lipgloss tables, TTY degradation, fang help/errors.
11. **dist** — `.goreleaser.yaml`, `install.sh`, CI workflows, `LICENSE` (MIT), `README.md`;
    tag `v0.1.0`.

Tests land alongside each phase, not at the end.
