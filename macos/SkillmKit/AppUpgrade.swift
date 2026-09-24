import Foundation
import Observation

/// Where the app's updater (Sparkle) looks for a new version of the app, as
/// the app's Info.plist names it (`SUFeedURL`, `SUPublicEDKey`, filled from
/// build settings in `macos/project.yml`).
public struct UpdateFeed: Equatable, Sendable {
    /// The appcast.
    public var url: URL
    /// The EdDSA public key the appcast's updates are signed for (base64).
    public var publicKey: String

    /// The feed an Info.plist names, or nil when the app does not update
    /// itself: a debug build names no feed URL, and a key that is missing
    /// or not an Ed25519 public key (32 bytes, base64) could verify nothing.
    public init?(info: [String: Any]?) {
        guard let info,
            let feed = info["SUFeedURL"] as? String, !feed.isEmpty,
            let url = URL(string: feed), url.scheme != nil,
            let key = info["SUPublicEDKey"] as? String,
            let bytes = Data(base64Encoded: key), bytes.count == 32
        else { return nil }
        self.url = url
        self.publicKey = key
    }
}

/// The app's updater as the model sees it. The app implements it with
/// Sparkle, which replaces the app bundle. The CLI is not part of it: it is
/// released and upgraded on its own (`skillm upgrade`).
@MainActor
public protocol AppUpdater: AnyObject {
    /// Asks the feed for a newer app without showing anything. The answer
    /// comes back through `AppUpgrade.found(version:)` or `notFound()`, or
    /// `probeFailed()` when the check failed. False when no check started
    /// (the updater is busy with another one).
    func probe() -> Bool
    /// Checks the feed with the updater's own window: shows the update found
    /// (or that there is none) and installs it when the user agrees; the app
    /// then quits (through `AppModel.postponeRelaunch`) and relaunches.
    func install()
}

/// "Upgrade app and restart": shown once the updater has found a newer app.
///
/// The updater never checks on its own schedule (`SUEnableAutomaticChecks`
/// is off). The model asks it silently on a schedule that does not depend on
/// the CLI: at launch, then on the model's ticks once `interval` has passed
/// since the last check (the refresh interval from config.toml, or a day
/// while the settings are unknown), and after the Refresh item. So a CLI
/// that is missing, too new or broken never hides the app update that fixes
/// it. The app's version is independent of the CLI's, so nothing in the
/// Refresh cache says whether a newer app exists: only the updater's answer
/// does. A check that did not start or failed is asked again at the next
/// tick.
///
/// "Skip This Version" in the updater's window keeps the item: the updater
/// stops reminding, but the item still installs the skipped version (the
/// user-started check finds it), and the menu's dot stays until then.
@MainActor
@Observable
public final class AppUpgrade {
    /// How often the updater is asked while the refresh settings are
    /// unknown (the CLI cannot be used, or has not answered yet).
    public static let defaultInterval: TimeInterval = 24 * 3600

    /// The version the updater found; nil while it has found none.
    public private(set) var version: String?
    /// The app's updater; nil when the app does not update itself (a debug
    /// build, see `UpdateFeed`).
    @ObservationIgnored public private(set) var updater: (any AppUpdater)?
    /// When the updater last started a check; nil before the first, and
    /// after one failed.
    @ObservationIgnored private var lastProbe: Date?

    public init() {}

    /// Show "Upgrade app and restart".
    public var isAvailable: Bool { updater != nil && version != nil }

    /// The app updates itself: "Check for app update" can be offered.
    public var canCheck: Bool { updater != nil }

    /// Connects the app's updater. It is asked at the model's next tick (the
    /// launch's, when it is attached before the model starts).
    public func attach(_ updater: any AppUpdater) {
        self.updater = updater
    }

    /// A tick of the model's schedule: asks the updater when it was never
    /// asked, or was last asked `interval` ago or longer. A nil `interval`
    /// (auto check is off) asks nothing; the Refresh item still asks.
    public func tick(now: Date = Date(), every interval: TimeInterval?) {
        guard let interval else { return }
        if let lastProbe, now.timeIntervalSince(lastProbe) < interval { return }
        probe(now: now)
    }

    /// The Refresh item ran: asks the updater now, however recently it was
    /// asked.
    public func checkNow(now: Date = Date()) {
        probe(now: now)
    }

    /// Asks the updater, unless it found an update already.
    private func probe(now: Date) {
        guard let updater, version == nil else { return }
        if updater.probe() { lastProbe = now }
    }

    /// The updater found a newer app.
    public func found(version: String) {
        self.version = version
    }

    /// The updater found none.
    public func notFound() {
        version = nil
    }

    /// The updater's check failed (offline, no appcast yet): the next tick
    /// asks it again.
    public func probeFailed() {
        lastProbe = nil
    }

    /// The "Upgrade app and restart" item, and "Check for app update" while
    /// the CLI cannot be used.
    public func upgrade() {
        updater?.install()
    }
}
