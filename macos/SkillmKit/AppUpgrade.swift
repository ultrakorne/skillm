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
/// Sparkle, which replaces the whole bundle (the bundled CLI with it).
@MainActor
public protocol AppUpdater: AnyObject {
    /// Asks the feed for a newer app without showing anything. The answer
    /// comes back through `AppUpgrade.found(version:)` or `notFound()`, or
    /// `probeFailed()` when the check failed. False when no check started
    /// (the updater is busy with another one).
    func probe() -> Bool
    /// Shows the update found and installs it when the user agrees; the app
    /// then quits (through `AppModel.postponeRelaunch`) and relaunches.
    func install()
}

/// "Upgrade and restart": shown once the updater has found a newer app.
///
/// The updater never checks on its own schedule. A Refresh that finds a
/// newer skillm release (`status.self.available`), or whose own lookup of
/// the latest release failed (`status.self.error`, for the bundled CLI),
/// asks it once per check, so the Auto refresh setting covers both. The item
/// waits for the updater's answer rather than showing on `self.available`
/// alone: that says a GitHub release exists, not that its appcast (and a
/// signed app) is there yet. A check that did not start or failed is asked
/// again with the next status that arrives (the next tick at the latest).
///
/// "Skip This Version" in the updater's window keeps the item: the updater
/// stops reminding, but the item still installs the skipped version (the
/// user-started check finds it), and the menu's dot, which comes from the
/// CLI's cache, stays until then.
@MainActor
@Observable
public final class AppUpgrade {
    /// The version the updater found; nil while it has found none.
    public private(set) var version: String?
    /// The app's updater; nil when the app does not update itself (a debug
    /// build, see `UpdateFeed`).
    @ObservationIgnored public private(set) var updater: (any AppUpdater)?
    /// The check (its `checked_at`) the updater was last asked about.
    @ObservationIgnored private var probedCheck: Date?
    /// The last status seen, for an updater attached after it arrived.
    @ObservationIgnored private var lastStatus: StatusData?

    public init() {}

    /// Show "Upgrade and restart".
    public var isAvailable: Bool { updater != nil && version != nil }

    /// Connects the app's updater; it is asked at once if the status seen
    /// so far says a newer skillm exists.
    public func attach(_ updater: any AppUpdater) {
        self.updater = updater
        statusChanged(lastStatus)
    }

    /// A status arrived (every `status`/`refresh` answer). When it says a
    /// newer skillm exists (or that the CLI could not look) and the updater
    /// has not found an update yet, the updater is asked, once per check: a
    /// re-read of the same cache asks nothing.
    public func statusChanged(_ status: StatusData?) {
        lastStatus = status
        guard let updater, version == nil,
            let cache = status?.cache, let me = cache.selfStatus,
            me.available || (me.error != nil && me.method == .bundled),
            let checkedAt = cache.checkedAt, checkedAt != probedCheck
        else { return }
        if updater.probe() { probedCheck = checkedAt }
    }

    /// The updater found a newer app.
    public func found(version: String) {
        self.version = version
    }

    /// The updater found none.
    public func notFound() {
        version = nil
    }

    /// The updater's check failed (offline, no appcast yet): the next
    /// status that arrives asks it again, even for the same check.
    public func probeFailed() {
        probedCheck = nil
    }

    /// The "Upgrade and restart" item.
    public func upgrade() {
        updater?.install()
    }
}
