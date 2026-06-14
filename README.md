# skillm

Manage AI-agent skills from one central home and link them into the folders
Claude and Codex read. One copy, symlinked everywhere.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/ultrakorne/skillm/main/install.sh | sh
```

Or with Go: `go install github.com/ultrakorne/skillm@latest`

## Quickstart

```sh
skillm add https://github.com/ultrakorne/skill-collection   # fetch a skill into Home
skillm agent                                                # pick which agents get links
skillm install grill-with-docs --global                     # symlink it into your agents
skillm check                                                # see what has updates
skillm update                                               # pull the updates in
```

## Commands

| Command                              | Description                                           |
| ------------------------------------ | ----------------------------------------------------- |
| `add <url\|path> [id] [--as <name>] [--ref <ref>] [--global\|--local]` | Fetch a skill into Home (optionally install it). |
| `install [id...] [--all] [--global\|--local]` | Symlink skills into every enabled agent (interactive picker if no id). |
| `uninstall [id...] [--all]`           | Unlink everywhere, then delete from Home (interactive picker if no id). |
| `list`                               | Show every skill, where it is installed, and its status. |
| `check`                              | Report which git skills have upstream updates.        |
| `update [id]`                        | Pull updates for outdated git skills (all, or one).   |
| `agent`                              | Interactively choose the enabled agents.              |

Global flags: `--force` / `--yes` (skip confirmations), `--home <path>` (override Home, default `~/.skillm`).

## License

MIT
