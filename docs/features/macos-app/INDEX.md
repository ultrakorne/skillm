# macOS App

The macOS menu bar app (`macos/`) is a thin SwiftUI view over skillm: it carries its own version-matched **Bundled CLI** and drives it only through the [JSON API](../json-api/INDEX.md), so every behaviour stays in Go. Today it is a status item that finds and checks the Bundled CLI at launch, shows a problem with its fix when the CLI cannot be used, and quits; `SkillmKit` holds the client every later menu item runs commands through.

## Documents
| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | What the user sees, the launch checks, and the decisions that shape the app and its bundle |
| [TECHNICAL.md](TECHNICAL.md) | The XcodeGen targets, how the client runs and cancels skillm, where things live, the invariants |
| [FLOW.mermaid](FLOW.mermaid) | Cancelling a command: SIGINT, the `cancelled` answer, and the grace before SIGTERM |
