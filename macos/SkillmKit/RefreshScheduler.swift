import AppKit
import Foundation

/// When the app asks skillm for a scheduled refresh: once at start, every
/// `interval` of awake time after that, and `wakeDelay` after the Mac wakes.
/// Each tick only asks (`refresh --if-due`); skillm decides whether a check
/// is due, so ticking more often than the refresh interval costs nothing.
///
/// After a wake the wake tick alone asks: the hourly wait does not count
/// time asleep, and a wake restarts it after the wake tick. So a deadline
/// that passed during sleep never fires a tick at the wake itself, before
/// the network is back.
@MainActor
public final class RefreshScheduler {
    public struct Timing: Sendable, Equatable {
        /// Between two ticks, in time the Mac was awake.
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

    /// Waits for a duration; throws when the waiting task is cancelled.
    public typealias Sleep = @Sendable (Duration) async throws -> Void

    /// `Task.sleep` on the suspending clock, which stops while the Mac
    /// sleeps (the continuous clock would end an hourly wait at the wake).
    public nonisolated static let suspendingSleep: Sleep = { try await Task.sleep(for: $0, clock: .suspending) }

    private let timing: Timing
    private let center: NotificationCenter
    private let sleep: Sleep
    private let tick: @MainActor () -> Void
    private var loop: Task<Void, Never>?
    private var wake: Task<Void, Never>?
    private var observer: (any NSObjectProtocol)?

    /// - Parameters:
    ///   - notificationCenter: the center `NSWorkspace.didWakeNotification`
    ///     is posted to (NSWorkspace's own in the app).
    ///   - sleep: how the scheduler waits (tests pass a controllable one).
    ///   - tick: runs on each tick.
    public init(
        timing: Timing, notificationCenter: NotificationCenter, sleep: @escaping Sleep = suspendingSleep,
        tick: @escaping @MainActor () -> Void
    ) {
        self.timing = timing
        center = notificationCenter
        self.sleep = sleep
        self.tick = tick
    }

    /// Ticks now (unless `tickNow` is false), then starts the hourly wait
    /// and watches for wakes.
    public func start(tickNow: Bool = true) {
        guard observer == nil else { return }
        observer = center.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) {
            [weak self] _ in
            MainActor.assumeIsolated { self?.didWake() }
        }
        startLoop()
        if tickNow { tick() }
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

    /// Ticks every `interval` from now.
    private func startLoop() {
        loop?.cancel()
        let interval = timing.interval
        let sleep = sleep
        loop = Task { [weak self] in
            while !Task.isCancelled {
                do { try await sleep(interval) } catch { return }
                guard !Task.isCancelled else { return }
                self?.tick()
            }
        }
    }

    /// Stops the hourly wait, ticks after `wakeDelay`, then waits an
    /// `interval` from that tick. A second wake in the meantime starts the
    /// delay over.
    private func didWake() {
        loop?.cancel()
        loop = nil
        wake?.cancel()
        let delay = timing.wakeDelay
        let sleep = sleep
        wake = Task { [weak self] in
            do { try await sleep(delay) } catch { return }
            guard !Task.isCancelled, let self else { return }
            self.tick()
            self.startLoop()
        }
    }
}
