import AppKit
import SwiftUI

/// The menu bar app: a status item whose menu is `MenuContent`. There is no
/// Dock icon or main window (`LSUIElement`).
@main
struct SkillmApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    var body: some Scene {
        // A system symbol is drawn as a template image, so it follows the
        // light and dark menu bar.
        MenuBarExtra("skillm", systemImage: "books.vertical") {
            MenuContent(model: appDelegate.model)
        }
        .menuBarExtraStyle(.menu)
    }
}

/// Owns the model and starts it once the app has launched.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let model = AppModel()

    func applicationDidFinishLaunching(_ notification: Notification) {
        Task { await model.start() }
    }
}
