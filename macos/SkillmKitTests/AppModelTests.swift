import AppKit
import XCTest

@testable import SkillmKit

/// Runs AppModel against TestSupport/fake-skillm and checks which commands
/// it runs, and what it makes of their answers.
@MainActor
final class AppModelTests: XCTestCase {
    private var scratch: URL!
    private var models: [AppModel] = []
    private let center = NotificationCenter()

    override func setUp() async throws {
        scratch = FileManager.default.temporaryDirectory.appending(path: "skillm-model-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
    }

    override func tearDown() async throws {
        for m in models { await m.shutdown() }
        models = []
        try? FileManager.default.removeItem(at: scratch)
    }

    private var exitedMarker: URL { scratch.appending(path: "exited") }

    /// A model over the fake. The timer is hourly unless given, so only the
    /// launch tick runs; a wake ticks at once.
    private func model(
        _ env: [String: String] = [:],
        timing: RefreshScheduler.Timing = .init(interval: .seconds(3600), wakeDelay: .zero)
    ) -> AppModel {
        var environment = [
            "FAKE_SKILLM_FIXTURES": TestPaths.fixtures.path,
            "FAKE_SKILLM_LOG": scratch.appending(path: "log").path,
            "FAKE_SKILLM_EXITED": exitedMarker.path,
            "PATH": "/usr/bin:/bin",
        ]
        environment.merge(env) { $1 }
        let client = SkillmClient(executable: TestPaths.fakeSkillm, environment: environment)
        let m = AppModel(makeClient: { client }, checksGit: false, timing: timing, wakeCenter: center)
        models.append(m)
        return m
    }

    /// Every command the fake ran, one line of arguments each.
    private func commands() -> [String] {
        let s = (try? String(contentsOf: scratch.appending(path: "log"), encoding: .utf8)) ?? ""
        return s.split(separator: "\n").map(String.init)
    }

    private func eventually(
        _ what: String, timeout: Duration = .seconds(10), _ condition: () -> Bool
    ) async throws {
        let deadline = ContinuousClock.now + timeout
        while !condition() {
            guard ContinuousClock.now < deadline else {
                return XCTFail("timed out waiting for \(what); commands: \(commands())")
            }
            try await Task.sleep(for: .milliseconds(20))
        }
    }

    /// Starts the model and waits for the launch tick to finish.
    private func started(_ env: [String: String] = [:]) async -> AppModel {
        let m = model(env)
        await m.start()
        await m.waitUntilIdle()
        return m
    }

    // MARK: - Launch

    func testStartReadsStatusAndSettingsThenTicks() async throws {
        let m = await started()
        XCTAssertEqual(m.cli, .ready(version: "0.4.0"))
        XCTAssertEqual(
            commands(), ["version --json", "status --json", "config get --json", "refresh --if-due --json"])
        // The tick's answer (refresh.json) replaced the status read first.
        XCTAssertEqual(m.status?.cache.selfStatus?.error, "check for a newer skillm: dial tcp: no route to host")
        XCTAssertTrue(m.badge)
        XCTAssertEqual(m.settings, RefreshSettings(enabled: true, intervalHours: 12))
        XCTAssertEqual(m.activity, .idle)
        XCTAssertNil(m.notice)
    }

    func testStartFailureShowsTheProblemAndSchedulesNothing() async throws {
        let m = model(["FAKE_SKILLM_MODE": "api99"])
        await m.start()
        guard case .failed(let message, let fix) = m.cli else { return XCTFail("started: \(m.cli)") }
        XCTAssertTrue(message.contains("API version 99"), message)
        XCTAssertNotNil(fix)
        XCTAssertNil(m.refresh())
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(commands(), ["version --json"])
        XCTAssertFalse(m.badge)
    }

    // MARK: - Refresh

    func testRefreshChecksWhetherOrNotDue() async throws {
        let m = await started()
        await m.refresh()?.value
        XCTAssertEqual(commands().last, "refresh --json")
        XCTAssertTrue(m.badge)
        XCTAssertNil(m.notice)
    }

    func testWakeRunsAScheduledRefresh() async throws {
        let m = await started()
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await eventually("the wake tick") { commands().filter { $0 == "refresh --if-due --json" }.count == 2 }
        await m.waitUntilIdle()
    }

    func testTimerTicks() async throws {
        let m = model(timing: .init(interval: .milliseconds(200), wakeDelay: .zero))
        await m.start()
        try await eventually("two timer ticks") { commands().filter { $0 == "refresh --if-due --json" }.count >= 3 }
    }

    func testScheduledRefreshFailureIsShown() async throws {
        let m = await started(["FAKE_SKILLM_FAIL_ON": "refresh"])
        XCTAssertEqual(m.notice?.isError, true)
        XCTAssertTrue(m.notice?.text.hasPrefix("another skillm operation") == true, "\(String(describing: m.notice))")
        // The status read at launch stays.
        XCTAssertEqual(m.status?.cache.selfStatus?.latest, "0.5.0")
    }

    // MARK: - Update

    func testUpdateAllReportsTheOutcomeAndRereadsStatus() async throws {
        let m = await started()
        let task = m.updateAll()
        XCTAssertNotNil(task)
        guard case .updating = m.activity else { return XCTFail("activity: \(m.activity)") }
        XCTAssertNil(m.refresh(), "a second command started while one runs")
        await task?.value
        XCTAssertEqual(Array(commands().suffix(2)), ["update --json --events", "status --json"])
        XCTAssertEqual(m.notice, .init(text: "Updated 1 skill, some installs were not updated", isError: false))
        XCTAssertEqual(m.activity, .idle)
        // status.json has the self check's latest release, refresh.json not.
        XCTAssertEqual(m.status?.cache.selfStatus?.latest, "0.5.0")
    }

    func testFailedUpdateNamesTheSkills() async throws {
        let m = await started(["FAKE_SKILLM_FAIL_ON": "update"])
        await m.updateAll()?.value
        XCTAssertEqual(m.notice, .init(text: "2 skills failed to update: alpha, beta", isError: true))
        XCTAssertEqual(commands().last, "status --json")
    }

    func testStoppingAnUpdateWaitsForSkillmThenRereadsStatus() async throws {
        let m = await started(["FAKE_SKILLM_HANG_ON": "update"])
        let task = m.updateAll()
        // The fake reports its batch once it traps SIGINT.
        try await eventually("the update's batch") {
            if case .updating(let p) = m.activity { return p.total == 1 }
            return false
        }
        m.cancel()
        XCTAssertTrue(m.isStopping)
        await task?.value
        XCTAssertTrue(FileManager.default.fileExists(atPath: exitedMarker.path), "returned before skillm exited")
        XCTAssertEqual(m.notice, .init(text: "Update stopped", isError: false))
        XCTAssertFalse(m.isStopping)
        XCTAssertEqual(m.activity, .idle)
        XCTAssertEqual(Array(commands().suffix(2)), ["update --json --events", "status --json"])
    }

    func testShutdownWaitsForTheRunningCommand() async throws {
        let m = model(["FAKE_SKILLM_HANG_ON": "refresh"])
        await m.start()
        try await eventually("the launch tick") { commands().last == "refresh --if-due --json" }
        // Let the fake reach its traps.
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertTrue(m.isBusy)
        await m.shutdown()
        XCTAssertTrue(FileManager.default.fileExists(atPath: exitedMarker.path), "returned before skillm exited")
        XCTAssertFalse(m.isBusy)
        XCTAssertNil(m.notice, "a quit is not a failure")
        XCTAssertNil(m.updateAll(), "a command started after shutdown")
    }

    // MARK: - Auto refresh

    func testAutoRefreshToggleWritesTheSetting() async throws {
        let m = await started()
        await m.setAutoRefresh(false)?.value
        await m.waitUntilIdle()
        XCTAssertEqual(commands().last, "config set refresh.enabled false --json")
        XCTAssertEqual(m.settings?.enabled, false)

        await m.setAutoRefresh(true)?.value
        await m.waitUntilIdle()
        // Turning it on asks for a scheduled refresh at once.
        XCTAssertEqual(
            Array(commands().suffix(2)), ["config set refresh.enabled true --json", "refresh --if-due --json"])
        XCTAssertEqual(m.settings?.enabled, true)
    }
}
