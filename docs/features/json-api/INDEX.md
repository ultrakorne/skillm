# JSON API

`skillm <command> --json` is skillm's machine-readable surface: one JSON document (or, with `--events`, an NDJSON event stream ending in a result line) on stdout, never a prompt or terminal UI. GUIs such as the macOS menu bar app run the CLI this way instead of parsing its terminal output. `version`, `list`, `check`, `source inspect`, `install`, `update`, `import`, `uninstall`, `upgrade`, `agent ls`/`set`, `config get`/`set`, `refresh` and `status` have a JSON mode; a question the terminal would ask becomes a required flag or a typed refusal the GUI answers by retrying with a flag.

## Documents
| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | The contract: flags, envelope, event stream, error codes, the commands and how a GUI answers a refusal |
| [commands.md](commands.md) | Each command's arguments, data, event stream, error codes and retries |
| [TECHNICAL.md](TECHNICAL.md) | How JSON mode is wired through `cmd` and `internal/protocol`, the golden fixtures, the invariants |
| [FLOW.mermaid](FLOW.mermaid) | A GUI running `check --json --events`: events, then the result line |
