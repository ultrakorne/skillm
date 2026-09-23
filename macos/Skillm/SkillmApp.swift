import AppKit
import SkillmKit
import SwiftUI

/// The menu bar app: a status item whose menu is `MenuContent`. There is no
/// Dock icon or main window (`LSUIElement`).
@main
struct SkillmApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    var body: some Scene {
        MenuBarExtra {
            MenuContent(model: appDelegate.model)
        } label: {
            StatusLabel(model: appDelegate.model)
        }
        .menuBarExtraStyle(.menu)
    }
}

/// The status item's image: the glyph, with a red dot when the Badge is on.
struct StatusLabel: View {
    let model: AppModel

    var body: some View {
        Image(nsImage: StatusIcon.image(badge: model.badge))
    }
}

/// Owns the model, starts it once the app has launched, and lets a running
/// command finish before the app quits.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let model = AppModel()

    func applicationDidFinishLaunching(_ notification: Notification) {
        Task { await model.start() }
    }

    /// A quit while skillm runs interrupts it (as Ctrl-C does) and waits
    /// for it to exit, so it never dies halfway through a write.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard model.isBusy else { return .terminateNow }
        Task {
            await model.shutdown()
            sender.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }
}
