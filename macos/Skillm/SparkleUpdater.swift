import AppKit
import OSLog
import SkillmKit
import Sparkle

/// Sparkle, the app's updater: it replaces the whole bundle, the bundled CLI
/// included (the CLI's own `skillm upgrade` refuses inside a bundle).
///
/// It never checks on its own schedule (`SUEnableAutomaticChecks` is off):
/// `AppUpgrade` asks it after a Refresh that found a newer skillm, and the
/// "Upgrade and restart" item shows Sparkle's standard update window for
/// what it found. Before it relaunches the app, every running skillm exits.
@MainActor
final class SparkleUpdater: NSObject, AppUpdater, SPUUpdaterDelegate {
    private let model: AppModel
    private var controller: SPUStandardUpdaterController?

    nonisolated private static let log = Logger(subsystem: "games.starberry.skillm", category: "updates")

    /// Starts Sparkle and attaches it to the model when the app's Info.plist
    /// names a feed and a valid key; a debug build names no feed, and then
    /// the app never updates itself (nil).
    static func start(model: AppModel, bundle: Bundle = .main) -> SparkleUpdater? {
        guard UpdateFeed(info: bundle.infoDictionary) != nil else { return nil }
        let updater = SparkleUpdater(model: model)
        let controller = SPUStandardUpdaterController(
            startingUpdater: false, updaterDelegate: updater, userDriverDelegate: nil)
        do {
            try controller.updater.start()
        } catch {
            log.error("Sparkle did not start: \(error.localizedDescription, privacy: .public)")
            return nil
        }
        updater.controller = controller
        model.upgrade.attach(updater)
        return updater
    }

    private init(model: AppModel) {
        self.model = model
    }

    // MARK: - AppUpdater

    func probe() {
        guard let updater = controller?.updater, updater.canCheckForUpdates else { return }
        updater.checkForUpdateInformation()
    }

    func install() {
        // A menu bar app is not active on its own: bring Sparkle's window
        // to the front.
        NSApp.activate()
        controller?.checkForUpdates(nil)
    }

    // MARK: - SPUUpdaterDelegate (Sparkle calls these on the main thread)

    nonisolated func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        let version = item.displayVersionString
        MainActor.assumeIsolated { model.upgrade.found(version: version) }
    }

    nonisolated func updaterDidNotFindUpdate(_ updater: SPUUpdater) {
        MainActor.assumeIsolated { model.upgrade.notFound() }
    }

    nonisolated func updater(
        _ updater: SPUUpdater, userDidMake choice: SPUUserUpdateChoice,
        forUpdate updateItem: SUAppcastItem, state: SPUUserUpdateState
    ) {
        // "Skip This Version": Sparkle will not offer it again, so neither
        // does the menu.
        guard choice == .skip else { return }
        MainActor.assumeIsolated { model.upgrade.notFound() }
    }

    nonisolated func updater(_ updater: SPUUpdater, didAbortWithError error: any Error) {
        let message = error.localizedDescription
        Self.log.error("Sparkle aborted: \(message, privacy: .public)")
    }

    /// Sparkle is about to quit the app and relaunch the new one: skillm
    /// must not die halfway through a write, so the model stops everything
    /// first and Sparkle goes on once every skillm has exited.
    nonisolated func updater(
        _ updater: SPUUpdater, shouldPostponeRelaunchForUpdate item: SUAppcastItem,
        untilInvokingBlock installHandler: @escaping () -> Void
    ) -> Bool {
        nonisolated(unsafe) let proceed = installHandler
        return MainActor.assumeIsolated {
            model.postponeRelaunch { proceed() }
        }
    }
}
