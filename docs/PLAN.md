# skillm — Implementation Plan (v0.1.0)

A single, self-contained spec for building `skillm`. Pair this with
[CONTEXT.md](./CONTEXT.md) (the glossary) — terms in **Title Case** are defined there.

---

## 1. What skillm is

`skillm` is a Go CLI that manages AI-agent **Skills**. It keeps every skill in one central
**Home** and **Links** them (via symlinks) into the skill folders that **Agents** read.
Supported agents at launch: **Claude** and **Codex**, which share an identical on-disk skill
format (a directory with `SKILL.md`), so one Home copy serves both.

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

Agent skill folders that get symlinks into Home:

| Scope  | Claude                      | Codex                      |
|--------|-----------------------------|----------------------------|
| Global | `~/.claude/skills/<id>`     | `~/.codex/skills/<id>`     |
| Local  | `<cwd>/.claude/skills/<id>` | `<cwd>/.codex/skills/<id>` |

Each entry is a symlink: `<agent-folder>/<id> → ~/.skillm/skills/<id>`.

### config.toml

```toml
agents = ["claude", "codex"]  # Enabled agents; managed by `skillm agent`
# Scope is not configured here: `link`/`unlink` ask interactively
# (Global / Local / custom path) when neither --global nor --local is given.
# theme/color is auto-detected from the terminal; no field needed
```

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
# local skills have no ref/revision and are not update-tracked

# Project directories skillm has linked skills into at local scope. Not link
# state — only the set of folders to scan, so `list`/`remove` find local links
# outside the current directory. Pruned when a folder holds no skillm link.
local_roots = ["/home/ultra/projA", "/home/ultra/projB"]
```

**Link existence is never stored** — links are read live by scanning each agent folder (global,
plus the current directory and every `local_roots` entry) for symlinks whose target resolves
into `~/.skillm/skills/`. Only the *set of local folders to scan* is persisted, so a recorded
root never lets stale link state survive: a folder with no skillm link is pruned.

---

## 3. Command surface

```
skillm add <url> [skill_id] [--as <name>] [--ref <ref>] [--global|--local]
skillm add <local-path>      [--as <name>]              [--global|--local]
skillm link    <skill_id> [--global|--local]
skillm unlink  <skill_id> [--global|--local]
skillm list
skillm check
skillm update [skill_id]            # no arg = all outdated
skillm remove  <skill_id>
skillm agent                        # interactive multiselect of Enabled agents
```

Global flags: `--force` / `--yes` (skip confirmations), `--home <path>` (override Home).

### add
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
- `--global` / `--local` → after adding, also **link** at that scope (see link). Bare `add`
  is fetch-only.
- **Collision:** if the target ID already exists in Home → error suggesting `update` or `--as`.

### link / unlink
- Symlink (or remove the symlink for) `<skill_id>` into **every Enabled agent** at the chosen
  Scope. `--global`/`--local` selects scope explicitly; with neither flag, skillm asks
  interactively — **Global**, **Local** (the current directory), or a **custom path** typed with
  Tab path-completion (on a non-TTY it refuses and requires a flag). `--local` and the custom
  path use that directory's `.claude/skills` / `.codex/skills` (created if missing).
- **Safe by default:** `link` refuses to overwrite any existing entry skillm didn't create
  (i.e. not a symlink into Home) — it errors and leaves your own skill untouched.
- Re-linking an already-correct symlink is a no-op.

### list
- Table (lipgloss): `ID | Source | Linked (scopes×agents, read from disk) | Status`.
- Status ∈ `up-to-date`, `update available`, `local`, `untracked` (git skill whose subdir
  vanished upstream).

### check
- Read-only. For each `kind=git` skill: treeless-fetch its ref, compute the current upstream
  subdir tree SHA, compare to `revision`. Print which skills have updates available. Changes
  nothing. Per-skill — a commit touching a different skill never flags this one.

### update
- Default (no arg): update **all** outdated git skills; `update <id>` does one. For each, pull
  the current revision's subdir into Home (overwrite the Home copy), then update `revision` +
  `installed_at` in the registry. Because agents see Home through symlinks, every Link updates
  automatically. Shows a **bubbles/progress** bar when there is enough work to warrant it. No
  diffs. Local skills are skipped with a note ("edit in Home directly").

### remove
- Auto-unlink from all agents/scopes, then delete the Home copy and its registry entry. On a
  TTY, confirm first (unless `--yes`).

### agent
- **huh** multiselect seeded from `config.agents`; writes the new selection back to
  `config.toml`. Does not retroactively link/unlink existing skills (only affects future links).

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
├── cmd/                    # cobra commands, one file each (add, link, unlink,
│                           #   list, check, update, remove, agent, root)
└── internal/
    ├── config/             # load/save ~/.skillm/config.toml (go-toml/v2)
    ├── state/              # load/save ~/.skillm/state.toml registry
    ├── store/              # Home bootstrap + layout; add/remove skill dirs
    ├── skill/              # Skill model; parse SKILL.md YAML frontmatter
    ├── source/             # parse URL vs local path; discover SKILL.md dirs
    ├── gitx/               # shell-out git: treeless fetch, ls-tree, sparse-checkout
    ├── agentdir/           # agent definitions + skill-folder paths per scope
    ├── linker/             # create/remove symlinks; scan-for-links; safe overwrite
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

---

## 9. Build order (phases for ultracode)

1. **Scaffold** — `go mod init github.com/ultrakorne/skillm`, `main.go`, cobra+fang root,
   `config`/`state` packages with TOML round-trip, Home bootstrap, startup git check.
2. **Core libs** — `skill` (frontmatter), `source` (classify + discover), `gitx` (treeless
   fetch, `ls-tree`, sparse-checkout), `store` (copy skill into Home).
3. **add** — git (discover + huh select, `--as`/`--ref`), local copy, optional `--global/--local`.
4. **agentdir + linker** — folder mapping, symlink create/remove, scan-for-links, safe overwrite.
5. **link / unlink** — wire linker to Enabled agents + scope; `add --global/--local` reuses it.
6. **agent** — huh multiselect → config.
7. **list / check** — registry + live link scan + per-skill tree-SHA compare.
8. **update** — all/one, registry rewrite, bubbles/progress bar.
9. **remove** — auto-unlink + delete + confirm.
10. **ui polish** — lipgloss tables, TTY degradation, fang help/errors.
11. **dist** — `.goreleaser.yaml`, `install.sh`, CI workflows, `LICENSE` (MIT), `README.md`;
    tag `v0.1.0`.

Tests land alongside each phase, not at the end.
