import Foundation
import XCTest

@testable import SkillmKit

/// Finding the binary, the child's PATH and the git check.
final class SetupTests: XCTestCase {
    private var scratch: URL!

    override func setUpWithError() throws {
        scratch = FileManager.default.temporaryDirectory.appending(path: "skillm-setup-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: scratch)
    }

    private func makeExecutable(_ relative: String) throws -> URL {
        let url = scratch.appending(path: relative)
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data("#!/bin/sh\n".utf8).write(to: url)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
        return url
    }

    func testReleaseUsesOnlyTheBundledCLI() throws {
        let app = scratch.appending(path: "skillm.app")
        let root = scratch.appending(path: "repo")
        _ = try makeExecutable("repo/skillm")
        let env = ["SKILLM_BIN": root.appending(path: "skillm").path]
        let urls = SkillmBinary.candidates(bundleURL: app, environment: env, debug: false, sourceRoot: root)
        XCTAssertEqual(urls.map(\.path), [app.appending(path: "Contents/Helpers/skillm").path])
        XCTAssertThrowsError(try SkillmBinary.locate(bundleURL: app, environment: env, debug: false, sourceRoot: root))

        let bundled = try makeExecutable("skillm.app/Contents/Helpers/skillm")
        XCTAssertEqual(
            try SkillmBinary.locate(bundleURL: app, environment: env, debug: false, sourceRoot: root).path, bundled.path)
    }

    func testDebugOrder() throws {
        let app = scratch.appending(path: "skillm.app")
        let root = scratch.appending(path: "repo")
        let goBuild = try makeExecutable("repo/skillm")
        XCTAssertEqual(
            try SkillmBinary.locate(bundleURL: app, environment: [:], debug: true, sourceRoot: root).path, goBuild.path,
            "falls back to the go build output")

        let bundled = try makeExecutable("skillm.app/Contents/Helpers/skillm")
        XCTAssertEqual(
            try SkillmBinary.locate(bundleURL: app, environment: [:], debug: true, sourceRoot: root).path, bundled.path,
            "prefers the bundled CLI to the go build output")

        let custom = try makeExecutable("custom/skillm")
        XCTAssertEqual(
            try SkillmBinary.locate(
                bundleURL: app, environment: ["SKILLM_BIN": custom.path], debug: true, sourceRoot: root
            ).path, custom.path, "$SKILLM_BIN wins")
    }

    func testMissingBinaryListsWhereItLooked() {
        let app = scratch.appending(path: "skillm.app")
        XCTAssertThrowsError(try SkillmBinary.locate(bundleURL: app, environment: [:], debug: false, sourceRoot: nil)) {
            guard case SkillmError.binaryNotFound(let searched) = $0 else { return XCTFail("got \($0)") }
            XCTAssertEqual(searched, [app.appending(path: "Contents/Helpers/skillm").path])
        }
    }

    func testDebugSourceRootIsTheRepository() throws {
        let root = try XCTUnwrap(SkillmBinary.debugSourceRoot)
        XCTAssertTrue(FileManager.default.fileExists(atPath: root.appending(path: "go.mod").path), root.path)
    }

    func testExtendedPath() {
        XCTAssertEqual(
            ChildEnvironment.extendedPath("/usr/bin:/bin:/usr/sbin:/sbin"),
            "/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin")
        XCTAssertEqual(ChildEnvironment.extendedPath(nil), "/opt/homebrew/bin:/usr/local/bin:/usr/bin")
        XCTAssertEqual(
            ChildEnvironment.extendedPath("/opt/homebrew/bin:/x"), "/opt/homebrew/bin:/x:/usr/local/bin:/usr/bin")
        XCTAssertEqual(ChildEnvironment.make(from: ["A": "b"])["A"], "b")
    }

    func testGitCheck() async throws {
        // No git anywhere on PATH.
        do {
            try await GitCheck.check(path: scratch.path, developerToolsInstalled: { true })
            XCTFail("no git passed")
        } catch let e as SkillmError {
            guard case .gitMissing = e else { return XCTFail("got \(e)") }
            XCTAssertEqual(e.recoverySuggestion, SkillmError.gitFix)
        }

        // A real git on PATH: the developer tools are not asked about.
        _ = try makeExecutable("bin/git")
        try await GitCheck.check(path: scratch.appending(path: "bin").path, developerToolsInstalled: {
            XCTFail("asked about the developer tools")
            return false
        })

        // Apple's /usr/bin/git stub without the developer tools.
        do {
            try await GitCheck.check(path: "/usr/bin", developerToolsInstalled: { false })
            XCTFail("the git stub passed")
        } catch SkillmError.gitMissing(let detail) {
            XCTAssertTrue(detail.contains("Command Line Tools"), detail)
        }
        try await GitCheck.check(path: "/usr/bin", developerToolsInstalled: { true })
    }

    func testDeveloperToolsProbeRuns() async {
        // This Mac builds with Xcode, so a developer directory is set.
        let installed = await GitCheck.developerToolsInstalled()
        XCTAssertTrue(installed)
    }
}
