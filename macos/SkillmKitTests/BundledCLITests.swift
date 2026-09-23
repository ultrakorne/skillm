import Foundation
import XCTest

@testable import SkillmKit

/// Runs the real skillm the app bundles (built by the scheme next to this
/// test bundle), proving the Go CLI and the Swift client agree. Skipped when
/// no app was built (set SKILLM_TEST_CLI to point at a binary).
final class BundledCLITests: XCTestCase {
    private func bundledCLI() throws -> URL {
        if let path = ProcessInfo.processInfo.environment["SKILLM_TEST_CLI"], !path.isEmpty {
            return URL(fileURLWithPath: path)
        }
        let products = Bundle(for: Self.self).bundleURL.deletingLastPathComponent()
        let url = products.appending(path: "skillm.app").appending(path: SkillmBinary.bundledPath)
        guard FileManager.default.isExecutableFile(atPath: url.path) else {
            throw XCTSkip("no bundled skillm at \(url.path)")
        }
        return url
    }

    func testVersionAndStatus() async throws {
        let home = FileManager.default.temporaryDirectory.appending(path: "skillm-home-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: home) }
        let client = SkillmClient(executable: try bundledCLI(), home: home.path)

        let v = try await client.connect()
        XCTAssertEqual(v.apiVersion, 1)
        XCTAssertTrue(v.capabilities.contains("status"), "\(v.capabilities)")
        XCTAssertTrue(v.capabilities.contains("events"), "\(v.capabilities)")

        let status: StatusData = try await client.run(["status"])
        XCTAssertTrue(status.stale, "a fresh Home has never been refreshed")
        XCTAssertNil(status.cache.checkedAt)
        XCTAssertFalse(status.cache.badge)

        let list: ListData = try await client.run(["list"])
        XCTAssertEqual(list.skills, [])
    }

    func testUnknownCommandIsAUsageError() async throws {
        let client = SkillmClient(executable: try bundledCLI())
        do {
            let _: ListData = try await client.run(["no-such-command"])
            XCTFail("an unknown command succeeded")
        } catch let SkillmError.command(e, _) {
            XCTAssertEqual(e.code, .usage)
        }
    }
}
