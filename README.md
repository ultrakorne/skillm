# skillm

Manage AI-agent skills from one central home and link them into the folders your
agents read — Claude, Codex, Cursor, Amp, Gemini CLI, and more. One copy,
symlinked everywhere.

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
skillm agent                                                # pick which agents get skills
skillm install https://github.com/ultrakorne/skill-collection   # fetch + pick + install, one step
skillm install grill-with-docs --local                      # committable project install (.agents/skills + lockfile)
skillm add https://github.com/ultrakorne/skill-collection   # or just fetch into Home, install later
skillm import                                               # adopt a repo's skills-lock.json (skillm's or `npx skills`')
skillm check                                                # see what has updates
skillm update                                               # pull the updates in, everywhere on this machine
```

A **global** install symlinks one central copy everywhere. The seeded `claude` entry
links into `~/.claude/skills`; the seeded `agents` entry links into `~/.agents/skills` —
the cross-agent folder read by Codex, Cursor, Amp, Gemini CLI, and more.

A **local** install writes a real copy into the project's canonical `.agents/skills`
folder, gives every other enabled agent a relative in-repo symlink to it (e.g.
`.claude/skills/<id>`), and records it in `skills-lock.json` — all committable, so
teammates get working skills on clone with no tooling. The layout and lockfile are the
same ones vercel's [`npx skills`](https://github.com/vercel-labs/skills) CLI uses, so
either tool can manage the same repo: `skillm import` adopts entries a teammate added,
and `skillm update` re-syncs every tracked project on the machine (auto-adopting new
lockfile entries as it goes) — the one-command whole-machine update per-repo tools can't do.

## Commands

| Command                              | Description                                           |
| ------------------------------------ | ----------------------------------------------------- |
| `add <url\|path> [id] [--as <name>] [--ref <ref>] [--all]` | Fetch a skill into Home. Fetch only — never installs. |
| `install [<url\|path>] [id...] [--all] [--as <name>] [--ref <ref>] [--global\|--local]` | Install into every enabled agent — from an in-Home id, or straight from a repo URL/path (fetch + pick + install in one step). Local scope writes the committable project install; interactive pickers if no id. |
| `import [dir]`                       | Adopt a project's `skills-lock.json` into skillm's tracking: fetch sources into Home, restore missing copies/links. |
| `uninstall [id...] [--all]`           | Unlink everywhere, delete project copies + lock entries, then delete from Home (interactive picker if no id). |
| `list`                               | Show every skill, where it is installed, and its status. |
| `check`                              | Report which git skills have upstream updates.        |
| `update [id]`                        | Pull updates for outdated git skills (all, or one), re-sync every tracked project's copies and lock entries, and adopt teammate-added lockfile entries. |
| `agent`                              | Enable/disable agents, reconciling their links right away (skills stay in Home). |

Global flags: `--force` / `--yes` (skip confirmations), `--home <path>` (override Home, default `~/.skillm`).

## License

MIT
