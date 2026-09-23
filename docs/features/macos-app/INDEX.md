# macOS App
The macOS menu bar app (`macos/`) is a thin SwiftUI view over skillm: it carries its own version-matched **Bundled CLI** and drives it only through the [JSON API](../json-api/INDEX.md), so every behaviour stays in Go. Its status item shows the update Badge as a red dot; its menu shows what the last Refresh found and runs Refresh, the Auto refresh toggle and Update all skills, with Stop and Quit waiting for skillm to exit, opens the Skills, Add Skill and Settings windows, and offers Upgrade and restart once Sparkle has found a newer app. It asks for a scheduled Refresh at launch, hourly and after a wake, and leaves whether one is Due to the CLI.

## Documents
| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | The menu, the launch checks, the scheduled check, and the decisions that shape the app and its bundle |
| [TECHNICAL.md](TECHNICAL.md) | The XcodeGen targets, the model and its scheduler, the badge icon, quitting, where things live |
| [windows.md](windows.md) | The Skills, Add Skill and Settings windows: their questions, Start at login, the command-line tool |
| [updates.md](updates.md) | Upgrade and restart: Sparkle, when it is asked, the postponed relaunch, the feed, key and bundle version |
| [release.md](release.md) | The release pipeline: the local script and CI job that sign, notarize and upload the app and its appcast |
| [client.md](client.md) | How the client runs, cancels ([FLOW.mermaid](FLOW.mermaid): SIGINT, `cancelled`, the grace before SIGTERM) and decodes skillm, and where it finds the CLI and git |
