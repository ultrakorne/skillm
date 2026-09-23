import AppKit
import SwiftUI

/// The status item's menu. For now it only reports a CLI problem and quits.
struct MenuContent: View {
    let model: AppModel

    var body: some View {
        switch model.cli {
        case .starting:
            Text("Starting…")
            Divider()
        case .ready:
            EmptyView()
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
}
