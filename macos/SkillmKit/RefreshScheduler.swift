import AppKit
import Foundation

/// When the app asks skillm for a scheduled refresh: once at start, every
/// `interval` after that, and `wakeDelay` after the Mac wakes. Each tick
/// only asks (`refresh --if-due`); skillm decides whether a check is due,
/// so ticking more often than the refresh interval costs nothing.
@MainActor
public final class RefreshScheduler {
    public struct Timing: Sendable, Equatable {
        /// Between two ticks. The clock keeps running while the Mac sleeps.
        public var interval: Duration
        /// After a wake, before the tick: the network is often not back yet
        /// at the wake itself, and a refresh whose every lookup failed is
        /// retried only an hour later.
        public var wakeDelay: Duration

        public init(interval: Duration, wakeDelay: Duration) {
            self.interval = interval
            self.wakeDelay = wakeDelay
        }

        /// Hourly, and 30 seconds after a wake.
        public static let standard = Timing(interval: .seconds(3600), wakeDelay: .seconds(30))
    }

    private let timing: Timing
    private let center: NotificationCenter
    private let tick: @MainActor () -> Void
    private var loop: Task<Void, Never>?
    private var wake: Task<Void, Never>?
    private var observer: (any NSObjectProtocol)?

    /// - Parameters:
    ///   - notificationCenter: the center `NSWorkspace.didWakeNotification`
    ///     is posted to (NSWorkspace's own in the app).
    ///   - tick: runs on each tick.
    public init(
        timing: Timing, notificationCenter: NotificationCenter, tick: @escaping @MainActor () -> Void
    ) {
        self.timing = timing
        center = notificationCenter
        self.tick = tick
    }

    /// Ticks now, then starts the timer and watches for wakes.
    public func start() {
        guard loop == nil else { return }
        let interval = timing.interval
        loop = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: interval)
                guard !Task.isCancelled else { return }
                self?.tick()
            }
        }
        observer = center.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) {
            [weak self] _ in
            MainActor.assumeIsolated { self?.didWake() }
        }
        tick()
    }

    /// Stops ticking.
    public func stop() {
        loop?.cancel()
        loop = nil
        wake?.cancel()
        wake = nil
        if let observer { center.removeObserver(observer) }
        observer = nil
    }

    private func didWake() {
        wake?.cancel()
        let delay = timing.wakeDelay
        wake = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled else { return }
            self?.tick()
        }
    }
}
