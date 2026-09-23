import AppKit
import SkillmKit
import SwiftUI

/// The menu bar app: a status item whose menu is `MenuContent`, and the
/// windows it opens (View skills, Add skill, Settings). There is no Dock
/// icon or main window (`LSUIElement`).
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

        Window("Skills", id: WindowID.skills) {
            SkillsView(skills: appDelegate.skills)
        }
        .defaultSize(width: 820, height: 440)

        Window("Add Skill", id: WindowID.addSkill) {
            AddSkillView(add: appDelegate.addSkill)
        }
        .defaultSize(width: 520, height: 560)
        .windowResizability(.contentMinSize)

        Settings {
            SettingsView(settings: appDelegate.settings)
        }
    }
}

/// The ids of the windows the menu opens.
enum WindowID {
    static let skills = "skills"
    static let addSkill = "add-skill"
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
    /// The windows' state, kept while a window is closed.
    private(set) lazy var skills = SkillsModel(app: model)
    private(set) lazy var addSkill = AddSkillModel(app: model)
    private(set) lazy var settings = SettingsModel(app: model)
    /// Sparkle; nil in a build that does not update itself (a debug build).
    private var updater: SparkleUpdater?

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Before the model starts, so the launch's status read can ask it.
        updater = SparkleUpdater.start(model: model)
        Task { await model.start() }
    }

    /// Quits the app. Every quit the app starts itself goes through here.
    ///
    /// `terminate` runs from the run loop, never from inside a main-queue
    /// job (a MainActor `Task`, a `DispatchQueue.main` block, an XPC reply
    /// on the main queue): when skillm is running, `applicationShouldTerminate`
    /// replies later from a MainActor task, and GCD does not drain the main
    /// queue re-entrantly, so a `terminate` called from a main-queue job
    /// would wait forever for that reply.
    static func quit() {
        RunLoop.main.perform {
            MainActor.assumeIsolated { NSApplication.shared.terminate(nil) }
        }
    }

    /// A quit while skillm runs interrupts it (as Ctrl-C does) and waits
    /// for it to exit, so it never dies halfway through a write.
    ///
    /// `.terminateLater` (never `.terminateCancel`, which would cancel a
    /// logout or restart): the reply comes from a MainActor task once skillm
    /// has exited. So `terminate` must not be called from a main-queue job;
    /// use `AppDelegate.quit()`.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard model.hasRunningCommands else { return .terminateNow }
        Task {
            await model.shutdown()
            sender.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }
}
