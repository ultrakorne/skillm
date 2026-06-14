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
skillm link grill-with-docs --global                        # symlink it into your agents
skillm check                                                # see what has updates
skillm update                                               # pull the updates in
```

## Commands

| Command                              | Description                                           |
| ------------------------------------ | ----------------------------------------------------- |
| `add <url\|path> [id] [--as <name>] [--ref <ref>] [--global\|--local]` | Fetch a skill into Home (optionally link it). |
| `link <id> [--global\|--local]`       | Symlink a skill into every enabled agent.             |
| `unlink <id> [--global\|--local]`     | Remove the symlinks for a skill.                      |
| `list`                               | Show every skill, where it is linked, and its status. |
| `check`                              | Report which git skills have upstream updates.        |
| `update [id]`                        | Pull updates for outdated git skills (all, or one).   |
| `remove <id>`                        | Unlink everywhere, then delete from Home.             |
| `agent`                              | Interactively choose the enabled agents.              |

Global flags: `--force` / `--yes` (skip confirmations), `--home <path>` (override Home, default `~/.skillm`).

## License

MIT
