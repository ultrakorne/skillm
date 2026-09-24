import AppKit
import SkillmKit
import SwiftUI

/// The status item's menu: what the last check found, the running command,
/// and the commands. Every action goes through `AppModel`.
struct MenuContent: View {
    let model: AppModel
    @Environment(\.openWindow) private var openWindow
    @Environment(\.openSettings) private var openSettings

    var body: some View {
        switch model.cli {
        case .starting:
            Text("Starting…")
            Divider()
        case .ready:
            ready
            Divider()
        case .failed(let message, let fix):
            Text(message)
            if let fix {
                Text(fix)
            }
            Divider()
        }
        Button("Quit skillm") { AppDelegate.quit() }
        .keyboardShortcut("q")
    }

    @ViewBuilder private var ready: some View {
        // One line at most: what is running, else a failure, else what the
        // last check found, else the last outcome.
        if let activity = model.activity.text {
            Text(activity)
            Divider()
        } else if let notice = model.notice, notice.isError {
            Label(notice.text, systemImage: "xmark.octagon")
            Divider()
        } else if let status = model.status, let line = StatusSummary.line(status) {
            switch line.kind {
            case .info: Text(line.text)
            case .problem: Label(line.text, systemImage: "exclamationmark.triangle")
            }
            Divider()
        } else if let notice = model.notice {
            Text(notice.text)
            Divider()
        }
        Button("Refresh") { _ = model.refresh() }
            .keyboardShortcut("r")
            .disabled(model.isBusy)
        Button(updateAllTitle) { _ = model.updateAll() }
            .keyboardShortcut("u")
            .disabled(model.isBusy)
        if canStop {
            Button(model.isStopping ? "Stopping…" : "Stop") { model.cancel() }
                .disabled(model.isStopping)
        }
        if model.upgrade.isAvailable {
            // Sparkle's window shows the new version and installs it; the
            // app waits for skillm to exit before it relaunches.
            Button("Upgrade and restart") { model.upgrade.upgrade() }
        }
        Divider()
        Button("View skills…") { show(WindowID.skills) }
        Button("Add skill…") { show(WindowID.addSkill) }
        Button("Settings…") {
            NSApp.activate()
            openSettings()
        }
        .keyboardShortcut(",")
    }

    /// Opens a window in front: a menu bar app is not active on its own.
    private func show(_ id: String) {
        NSApp.activate()
        openWindow(id: id)
    }

    private var canStop: Bool { model.activity.canStop }

    /// "Update all skills (2)" when the last check found updates.
    private var updateAllTitle: String {
        let updates = model.status?.cache.updates ?? 0
        return updates > 0 ? "Update all skills (\(updates))" : "Update all skills"
    }
}
