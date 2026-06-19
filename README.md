# skillm

Manage AI-agent skills from one central home and link them into the folders
Claude and Codex read. One copy, symlinked everywhere.

## Install

**macOS / Linux**
```sh
curl -fsSL https://raw.githubusercontent.com/ultrakorne/skillm/main/install.sh | sh
```

**Windows** (PowerShell — requires [Developer Mode](ms-settings:developers) for symlinks)
```powershell
irm https://raw.githubusercontent.com/ultrakorne/skillm/main/install.ps1 | iex
```

**Go**
```sh
go install github.com/ultrakorne/skillm@latest
```

## Quickstart

```sh
skillm add https://github.com/ultrakorne/skill-collection   # fetch a skill into Home
skillm agent                                                # pick which agents get links
skillm install grill-with-docs --global                     # symlink it into your agents
skillm install grill-with-docs --local --copy               # or vendor a copy you can commit to git
skillm check                                                # see what has updates
skillm update                                               # pull the updates in
```

By default skillm symlinks one central copy everywhere. For a project you track in git, add
`--copy` to a local install: skillm writes a real, committed copy into the project's agent
folders (so teammates get the skill on clone, not a broken symlink). `update` keeps those copies
in sync, and `uninstall` removes them.

## Commands

| Command                              | Description                                           |
| ------------------------------------ | ----------------------------------------------------- |
| `add <url\|path> [id] [--as <name>] [--ref <ref>] [--global\|--local] [--copy]` | Fetch a skill into Home (optionally install it). |
| `install [id...] [--all] [--global\|--local] [--copy]` | Install skills into every enabled agent — symlink, or `--copy` to vendor a committable copy locally (interactive picker if no id). |
| `uninstall [id...] [--all]`           | Unlink everywhere, delete any vendored copies, then delete from Home (interactive picker if no id). |
| `list`                               | Show every skill, where it is installed, and its status. |
| `check`                              | Report which git skills have upstream updates.        |
| `update [id]`                        | Pull updates for outdated git skills (all, or one).   |
| `agent`                              | Enable/disable agents, reconciling their links right away (skills stay in Home). |

Global flags: `--force` / `--yes` (skip confirmations), `--home <path>` (override Home, default `~/.skillm`).

## License

MIT
