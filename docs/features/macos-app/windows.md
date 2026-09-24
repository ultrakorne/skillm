# macOS App — Windows

The menu's **View skills…**, **Add skill…** and **Settings…** (⌘,) open three windows. Each has
its own `@MainActor` model in `SkillmKit`, kept while the window is closed, that runs every
command through `AppModel`: reads (`list`, `source inspect`, `config get`, `agent ls`) run beside
the one running command, and changes (install, update, uninstall, `config set`, `agent set`) are
that one command, after which the model re-reads the status and bumps `installsVersion`.

## Surface

- **Skills** — a table of what `list` reports: the skill, its Source and ref, git or local, and
  each install (Global or its project, "(missing)" when the copy is gone) with the agents that
  read it. Per row: Reveal in Finder for each install, Update (that skill only), and Uninstall…,
  whose sheet names every copy it deletes, the committed copies in projects included.
- **Add Skill** — a Source (a git URL, `owner/repo`, or a folder, typed or chosen) and an
  optional branch or tag → Read Skills lists its skills to tick (a lone skill is ticked) → Every
  project (Global) or One project (a folder panel) → Install, with progress and Stop.
- **Settings** — Start at login, Auto check skill updates (on by default) with its interval, one
  toggle per agent, and Install for the command-line tool.

## Where things live

| File | Role |
|------|------|
| `macos/Skillm/SkillsView.swift` | The Skills table, its row menu, the Uninstall sheet and the shared notice line |
| `macos/Skillm/AddSkillView.swift` | The Add Skill form and its two questions: foreign files and refused links |
| `macos/Skillm/SettingsView.swift` | The `Settings` scene and the Disable agent sheet |
| `macos/SkillmKit/SkillsModel.swift` | `list`, per-skill `update`, and `uninstall` with its confirmation |
| `macos/SkillmKit/AddSkillModel.swift` | `source inspect`, the install request and its follow-up questions |
| `macos/SkillmKit/SettingsModel.swift` | Settings, agents, Start at login, the command-line tool |
| `macos/SkillmKit/CommandLineTool.swift` | Finds a `skillm` already installed and runs the bundled `install.sh` |
| `macos/SkillmKit/LoginItem.swift` | Start at login over `SMAppService.mainApp`, behind a protocol the tests fake |
| `macos/SkillmKitTests/FakeHarness.swift` | A started `AppModel` over `fake-skillm`, with the log of commands it ran |

## Noteworthy

### An install's question retries that install, never the form

An install records its request (the inspection, the ticked ids, the target) when it starts, and
a follow-up question carries it: an answer re-runs exactly that request with one flag, whatever
the form holds by then. The form is disabled while an install runs, and a question about an
inspection that Read Skills has since replaced is dropped.

### Foreign files and refused links are two questions

`foreign_files` comes before anything is written and lists only canonical slots, so its sheet
offers Overwrite (`--yes`) or Skip These (`--skip-foreign`). Agent link paths held by another
tool are reported only once the copies landed, as `link_refused` warnings on the result (or on a
failure that came after some skills landed); their own sheet offers Take Over Links, the same
request narrowed to those skills with `--force`, so it cannot overwrite a copy Skip left out.
`link_failed` is an I/O failure that `--force` does not fix, so it gets no offer.

### A question survives a busy app

A confirmation (Uninstall, Disable, both install questions) closes only once its command has
started. While another command runs, such as a scheduled check, its buttons are greyed out with
"Waiting for the running command…", instead of closing on a click that did nothing.

### The Add Skill install is pinned to what was read

A git install passes `--ref <branch> --commit <sha from source inspect>`, so the branch stays
tracked for updates and a Source that moved since fails with `commit_mismatch`; the window then
reads the Source again and keeps the ticked skills still there. A relative folder is refused (the
app has no working directory); `~` is expanded.

### A read that overlaps a write is dropped

Reads run beside the one command, so a `config get` or `agent ls` can start before a `config set`
or `agent set` and finish after it. Each write bumps a counter when it starts and when it ends,
and a read whose counter moved meanwhile keeps what the write left. The Skills list drops an
older `list` the same way.

### The windows load when the CLI is ready

A window macOS restores at launch opens before the launch checks pass. The Skills window lists on
open, whenever `installsVersion` goes up, and when `isReady` turns true; Settings loads on open,
on readiness and whenever the app becomes active, since the login item and `config.toml` can
change outside the app.

### Start at login is the system's, not config.toml's

The checkbox shows `SMAppService.mainApp.status`, read again after every change and every time
Settings loads; `requiresApproval` shows a hint with a button to the Login Items pane.

### The command-line tool is skillm's own install

Settings shows the tool as installed when an executable `skillm` is in `/usr/local/bin`,
`~/.local/bin` or `/opt/homebrew/bin`, whoever put it there. Install runs the repository's
`install.sh`, bundled in the app's Resources, with a minimal PATH and `SKILLM_VERSION` set to
the Bundled CLI's release tag (a dev build takes the latest release). The script downloads the
release binary into `/usr/local/bin`, or `~/.local/bin` when that is not writable, so it never
asks for sudo. That skillm is standalone: it upgrades itself with `skillm upgrade`, while the
app keeps using its Bundled CLI.
