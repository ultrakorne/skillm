# macOS App
The macOS menu bar app (`macos/`) is a thin SwiftUI view over skillm: it carries no CLI of its own and drives the **Installed CLI** only through the [JSON API](../json-api/INDEX.md), so every behaviour stays in Go; the API version keeps the two, released apart, compatible. Its status item shows the update Badge as a red dot; its menu shows one status line and runs Refresh and Update all skills (with the update count), with Stop and Quit waiting for skillm to exit, opens the Skills, Add Skill and Settings windows, and offers Upgrade skillm CLI and Upgrade app and restart. When the CLI is missing, too old or too new, the menu says so and offers the fix. It asks for a scheduled Refresh at launch, hourly and after a wake, and leaves whether one is Due to the CLI.

## Documents
| Document | Purpose |
|----------|---------|
| [DESIGN.md](DESIGN.md) | The menu, the CLI states and their fixes, the scheduled check, and the decisions that shape the app |
| [TECHNICAL.md](TECHNICAL.md) | The XcodeGen targets, the model, its CLI states and scheduler, the badge icon, quitting, where things live |
| [windows.md](windows.md) | The Skills, Add Skill and Settings windows: their questions, Start at login, the command-line tool |
| [updates.md](updates.md) | Upgrade app and restart: Sparkle, when it is asked, the postponed relaunch, the feed, key and bundle version |
| [release.md](release.md) | The App release: the scripts that sign, notarize and upload the app and its appcast from a Mac |
| [client.md](client.md) | How the client runs, cancels ([FLOW.mermaid](FLOW.mermaid): SIGINT, `cancelled`, the grace before SIGTERM) and decodes skillm, and where it finds the CLI and git |
