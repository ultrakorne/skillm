import AppKit
import SkillmKit
import SwiftUI

/// The status item's menu: what the last check found, the running command,
/// and the commands. Every action goes through `AppModel`.
struct MenuContent: View {
    let model: AppModel

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
        Button("Quit skillm") {
            NSApplication.shared.terminate(nil)
        }
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
        if let activity = activityText {
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
    }

    /// The running command, in words.
    private var activityText: String? {
        switch model.activity {
        case .idle, .savingSettings: nil
        case .refreshing: "Checking for updates…"
        case .updating(let progress): progress.text
        }
    }

    /// Only a check or an update is worth stopping; a settings write is
    /// instant.
    private var canStop: Bool {
        switch model.activity {
        case .refreshing, .updating: true
        case .idle, .savingSettings: false
        }
    }
}
