import AppKit
import XCTest

@testable import SkillmKit

/// RefreshScheduler with a sleep the test ends by hand, so "an hour" passes
/// only when the test says so.
@MainActor
final class RefreshSchedulerTests: XCTestCase {
    /// The sleeps the scheduler started; `finish` ends one.
    @MainActor
    private final class ManualSleep {
        struct Pending {
            let id: Int
            let duration: Duration
        }

        private var nextID = 0
        private var finished: Set<Int> = []
        private(set) var pending: [Pending] = []

        func sleep(_ duration: Duration) async throws {
            let id = nextID
            nextID += 1
            pending.append(Pending(id: id, duration: duration))
            defer { pending.removeAll { $0.id == id } }
            while !finished.contains(id) {
                try await Task.sleep(for: .milliseconds(5))
            }
        }

        /// Ends the one pending sleep of `duration`.
        func finish(_ duration: Duration) {
            let matching = pending.filter { $0.duration == duration }
            precondition(matching.count == 1, "pending sleeps: \(pending)")
            finished.insert(matching[0].id)
        }
    }

    private let center = NotificationCenter()
    private let timing = RefreshScheduler.Timing(interval: .seconds(3600), wakeDelay: .seconds(30))
    private var clock: ManualSleep!
    private var ticks = 0
    private var scheduler: RefreshScheduler!

    override func setUp() async throws {
        let clock = ManualSleep()
        self.clock = clock
        ticks = 0
        scheduler = RefreshScheduler(
            timing: timing, notificationCenter: center, sleep: { try await clock.sleep($0) }
        ) { [weak self] in self?.ticks += 1 }
    }

    override func tearDown() async throws {
        scheduler.stop()
    }

    private func eventually(_ what: String, _ condition: () -> Bool) async throws {
        let deadline = ContinuousClock.now + .seconds(5)
        while !condition() {
            guard ContinuousClock.now < deadline else { return XCTFail("timed out waiting for \(what)") }
            try await Task.sleep(for: .milliseconds(5))
        }
    }

    private func pendingDurations() -> [Duration] { clock.pending.map(\.duration) }

    func testTicksAtStartThenEveryInterval() async throws {
        scheduler.start()
        XCTAssertEqual(ticks, 1)
        try await eventually("the hourly wait") { pendingDurations() == [.seconds(3600)] }
        clock.finish(.seconds(3600))
        try await eventually("the hourly tick") { ticks == 2 }
        try await eventually("the next hourly wait") { pendingDurations() == [.seconds(3600)] }
    }

    /// A wake ends the hourly wait: only the wake tick runs, `wakeDelay`
    /// after the wake, and the next hourly wait starts from that tick.
    func testWakeOwnsTheTickAfterAWake() async throws {
        scheduler.start()
        try await eventually("the hourly wait") { pendingDurations() == [.seconds(3600)] }

        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await eventually("the wake delay alone") { pendingDurations() == [.seconds(30)] }
        XCTAssertEqual(ticks, 1, "ticked at the wake itself")

        clock.finish(.seconds(30))
        try await eventually("the wake tick") { ticks == 2 }
        try await eventually("the hourly wait from the wake tick") { pendingDurations() == [.seconds(3600)] }
    }

    func testASecondWakeStartsTheDelayOver() async throws {
        scheduler.start()
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await eventually("the wake delay") { pendingDurations() == [.seconds(30)] }
        let first = clock.pending.map(\.id)
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await eventually("a new wake delay alone") {
            pendingDurations() == [.seconds(30)] && clock.pending.map(\.id) != first
        }
        clock.finish(.seconds(30))
        try await eventually("one wake tick") { ticks == 2 }
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertEqual(ticks, 2)
    }

    func testStopEndsEveryWait() async throws {
        scheduler.start()
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await eventually("the wake delay") { pendingDurations() == [.seconds(30)] }
        scheduler.stop()
        try await eventually("no waits") { pendingDurations().isEmpty }
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertTrue(pendingDurations().isEmpty)
        XCTAssertEqual(ticks, 1)
    }
}
