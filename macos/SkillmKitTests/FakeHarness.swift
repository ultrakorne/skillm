import Foundation
import XCTest

@testable import SkillmKit

/// An AppModel over TestSupport/fake-skillm, started, with a log of the
/// commands the fake ran. For the window models' tests.
@MainActor
final class FakeHarness {
    let scratch: URL
    let app: AppModel
    private let center = NotificationCenter()

    init(_ env: [String: String] = [:]) throws {
        scratch = FileManager.default.temporaryDirectory.appending(path: "skillm-windows-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
        var environment = [
            "FAKE_SKILLM_FIXTURES": TestPaths.fixtures.path,
            "FAKE_SKILLM_LOG": scratch.appending(path: "log").path,
            "PATH": "/usr/bin:/bin",
        ]
        environment.merge(env) { $1 }
        let client = SkillmClient(executable: TestPaths.fakeSkillm, environment: environment)
        app = AppModel(
            makeClient: { client }, checksGit: false,
            timing: .init(interval: .seconds(3600), wakeDelay: .seconds(3600)), wakeCenter: center)
    }

    /// Starts the model and waits for the launch tick; the log then starts
    /// empty.
    func start() async throws {
        await app.start()
        await app.waitUntilIdle()
        try? FileManager.default.removeItem(at: scratch.appending(path: "log"))
    }

    func tearDown() async {
        await app.shutdown()
        try? FileManager.default.removeItem(at: scratch)
    }

    /// Every command the fake ran since `start`, one line of arguments each.
    func commands() -> [String] {
        let s = (try? String(contentsOf: scratch.appending(path: "log"), encoding: .utf8)) ?? ""
        return s.split(separator: "\n").map(String.init)
    }
}
