import AppKit
import OSLog
import SkillmKit
import Sparkle

/// Sparkle, the app's updater: it replaces the app bundle. The CLI the app
/// drives is installed and upgraded on its own (`skillm upgrade`).
///
/// It never checks on its own schedule (`SUEnableAutomaticChecks` is off):
/// `AppUpgrade` asks it silently after every check the app's schedule or the
/// Refresh item ran, and the "Upgrade app and restart" item (or "Check for
/// app update", when the CLI is newer than this app supports) shows
/// Sparkle's standard update window. Before it relaunches the app, every
/// running skillm exits.
@MainActor
final class SparkleUpdater: NSObject, AppUpdater, SPUUpdaterDelegate {
    private let model: AppModel
    private var controller: SPUStandardUpdaterController?
    /// The relaunch was postponed, so the model is shut down until the app
    /// quits, or until Sparkle gives up on the update.
    private var relaunchPending = false

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

    func probe() -> Bool {
        guard let updater = controller?.updater, updater.canCheckForUpdates else { return false }
        updater.checkForUpdateInformation()
        return true
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

    // "Skip This Version" is left alone: the item stays, and its
    // user-started check still finds the skipped version (see AppUpgrade).

    nonisolated func updater(_ updater: SPUUpdater, didAbortWithError error: any Error) {
        let error = error as NSError
        let message = error.localizedDescription
        if Self.isQuiet(error) {
            // Nothing new, or the user cancelled: not a failure.
            Self.log.debug("Sparkle ended: \(message, privacy: .public)")
            return
        }
        Self.log.error("Sparkle aborted: \(message, privacy: .public)")
        MainActor.assumeIsolated { model.upgrade.probeFailed() }
    }

    /// Sparkle's session ended. One that ends with an error after the
    /// relaunch was postponed leaves the app running with its model shut
    /// down: start it again.
    nonisolated func updater(
        _ updater: SPUUpdater, didFinishUpdateCycleFor updateCheck: SPUUpdateCheck, error: (any Error)?
    ) {
        MainActor.assumeIsolated {
            guard relaunchPending, error != nil else { return }
            relaunchPending = false
            model.resumeAfterAbortedUpdate()
        }
    }

    /// Codes Sparkle reports through the abort callback that are not
    /// failures: no update found, and an install the user cancelled.
    nonisolated private static func isQuiet(_ error: NSError) -> Bool {
        error.domain == SUSparkleErrorDomain
            && (error.code == Int(SUError.noUpdateError.rawValue)
                || error.code == Int(SUError.installationCanceledError.rawValue))
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
            relaunchPending = true
            return model.postponeRelaunch { proceed() }
        }
    }
}
