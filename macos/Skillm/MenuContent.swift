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
        if let status = model.status {
            ForEach(StatusSummary.lines(status)) { line in
                switch line.kind {
                case .info: Text(line.text)
                case .update: Label(line.text, systemImage: "arrow.down.circle")
                case .problem: Label(line.text, systemImage: "exclamationmark.triangle")
                }
            }
        }
        if let notice = model.notice {
            if notice.isError {
                Label(notice.text, systemImage: "xmark.octagon")
            } else {
                Text(notice.text)
            }
        }
        if let activity = model.activity.text {
            Text(activity)
        }
        Divider()
        Button("Refresh") { _ = model.refresh() }
            .keyboardShortcut("r")
            .disabled(model.isBusy)
        Toggle(
            "Auto refresh",
            isOn: Binding(
                get: { model.settings?.enabled ?? false },
                set: { _ = model.setAutoRefresh($0) })
        )
        .disabled(model.isBusy || model.settings == nil)
        Button("Update all skills") { _ = model.updateAll() }
            .keyboardShortcut("u")
            .disabled(model.isBusy)
        if canStop {
            Button(model.isStopping ? "Stopping…" : "Stop") { model.cancel() }
                .disabled(model.isStopping)
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
}
