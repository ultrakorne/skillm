# skillm

Fetch AI-agent skills from git repos or local paths and install them into the folders
your agents read — Claude, Codex, Cursor, Amp, Gemini CLI, and more. One canonical
`.agents/skills` copy per scope, symlinked into every other agent. `~/.skillm` holds only
skillm's config and registry; the installs themselves are the skill's only copies.

## Install

**macOS / Linux**
```sh
curl -fsSL https://raw.githubusercontent.com/ultrakorne/skillm/master/install.sh | sh
```

**Windows** (PowerShell — requires [Developer Mode](ms-settings:developers) for symlinks)
```powershell
irm https://raw.githubusercontent.com/ultrakorne/skillm/master/install.ps1 | iex
```

**Go**
```sh
go install github.com/ultrakorne/skillm@latest
```

## Quickstart

```sh
skillm agent                                                    # pick which agents get skills
skillm install https://github.com/ultrakorne/skill-collection --global   # fetch + pick + install, one step
skillm install grill-with-docs --local                          # add a scope: committable project install (.agents/skills + lockfile)
skillm import                                                   # adopt a repo's skills-lock.json (skillm's or `npx skills`')
skillm check                                                    # see what has updates
skillm update                                                   # pull the updates in, everywhere on this machine
```

A **global** install writes a real copy into the canonical `~/.agents/skills` folder —
the cross-agent store read natively by Codex, Cursor, Amp, Gemini CLI, and more — and
symlinks every other enabled agent's user-level folder to it (the seeded `claude` entry
links `~/.claude/skills/<id>`).

A **local** install works the same way inside a project: a real copy in the canonical
`.agents/skills` folder, a relative in-repo symlink for every other enabled agent (e.g.
`.claude/skills/<id>`), and an entry in `skills-lock.json` — all committable, so
teammates get working skills on clone with no tooling. The layout and lockfile are the
same ones vercel's [`npx skills`](https://github.com/vercel-labs/skills) CLI uses, so
either tool can manage the same repo: `skillm import` adopts entries a teammate added,
and `skillm update` re-syncs every tracked project on the machine (auto-adopting new
lockfile entries as it goes) — the one-command whole-machine update per-repo tools can't do.

## Commands

| Command                              | Description                                           |
| ------------------------------------ | ----------------------------------------------------- |
| `install [<url\|path>] [id...] [--all] [--as <name>] [--ref <ref>] [--global\|--local]` | Install into every enabled agent — straight from a repo URL/path (fetch + pick + install in one step), or by the id of an already-installed skill to add another scope/project. Local scope writes the committable project install; interactive pickers if no id. |
| `import [dir]`                       | Adopt a project's `skills-lock.json` into skillm's tracking: fetch the sources and write any missing copies/links. |
| `uninstall [id...] [--all]`           | Unlink everywhere and delete the global and project copies + lock entries + registry entry (interactive picker if no id). |
| `list`                               | Show every installed skill, where it is installed, and its status. |
| `check`                              | Report which git skills have upstream updates.        |
| `update [id]`                        | Pull updates for outdated git skills (all, or one), writing the new content into every install — the global copy and every tracked project's copies and lock entries — and adopt teammate-added lockfile entries. |
| `agent`                              | Enable/disable agents, reconciling their links right away (skills stay installed). |

A skill installed **globally** is globally active — installing is what fetches and
activates it; there is no separate "fetch without activating" step. Uninstalling a
skill's last install removes it entirely.

Global flags: `--force` / `--yes` (skip confirmations), `--home <path>` (override Home, default `~/.skillm`).

## License

MIT
