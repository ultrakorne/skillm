import Foundation
import XCTest

@testable import SkillmKit

/// The CLI built from this checkout (`go build`), once per test run. The
/// app carries no CLI, so these tests build one to prove the Go CLI and the
/// Swift client agree. SKILLM_TEST_CLI names a binary to use instead; with
/// no Go toolchain the tests are skipped.
actor RealCLI {
    static let shared = RealCLI()
    private var built: URL?

    func url() async throws -> URL {
        if let path = ProcessInfo.processInfo.environment["SKILLM_TEST_CLI"], !path.isEmpty {
            return URL(fileURLWithPath: path)
        }
        if let built { return built }
        guard let go = await Self.findGo() else { throw XCTSkip("no Go toolchain to build skillm with") }
        let out = FileManager.default.temporaryDirectory.appending(path: "skillm-test-cli-\(UUID().uuidString)/skillm")
        let process = Process()
        process.executableURL = go
        process.arguments = ["build", "-o", out.path, "."]
        process.currentDirectoryURL = TestPaths.repoRoot
        var env = ProcessInfo.processInfo.environment
        env["PATH"] = go.deletingLastPathComponent().path + ":/usr/bin:/bin"
        env["CGO_ENABLED"] = "0"
        process.environment = env
        let stderr = Pipe()
        process.standardError = stderr
        try process.run()
        let message = stderr.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            throw NSError(
                domain: "RealCLI", code: Int(process.terminationStatus),
                userInfo: [NSLocalizedDescriptionKey: "go build failed: " + String(decoding: message, as: UTF8.self)])
        }
        built = out
        return out
    }

    /// Go where a developer's terminal finds it, else its usual places.
    private static func findGo() async -> URL? {
        let shell = LoginShell(environment: ProcessInfo.processInfo.environment, timeout: .seconds(10))
        if let go = await shell.find("go") { return go }
        let home = NSHomeDirectory()
        return ["/opt/homebrew/bin/go", "/usr/local/go/bin/go", "/usr/local/bin/go", "\(home)/go/bin/go"]
            .map { URL(fileURLWithPath: $0) }
            .first { FileManager.default.isExecutableFile(atPath: $0.path) }
    }
}

/// Runs the real skillm (built from this checkout), proving the Go CLI and
/// the Swift client agree, and checks the app the scheme built.
final class RealCLITests: XCTestCase {
    /// The app built next to this test bundle embeds Sparkle, and its
    /// Info.plist carries the updater's key (merged from Skillm/Info.plist).
    /// A debug build names no feed, so it never updates itself. It carries
    /// no CLI: it drives the one the user installed.
    func testAppEmbedsSparkleWithItsKeyAndNoCLI() throws {
        let app = Bundle(for: Self.self).bundleURL.deletingLastPathComponent().appending(path: "skillm.app")
        guard let bundle = Bundle(url: app), let info = bundle.infoDictionary else {
            throw XCTSkip("no app at \(app.path)")
        }
        XCTAssertTrue(
            FileManager.default.fileExists(
                atPath: app.appending(path: "Contents/Frameworks/Sparkle.framework").path))
        XCTAssertFalse(
            FileManager.default.fileExists(atPath: app.appending(path: "Contents/Helpers").path),
            "the app bundles a CLI")
        XCTAssertNotNil(bundle.url(forResource: "install", withExtension: "sh"), "Install skillm CLI has no script")
        // SkillmKit is static, linked into the executable: an embedded copy
        // is unused code the release would still have to sign.
        XCTAssertFalse(
            FileManager.default.fileExists(
                atPath: app.appending(path: "Contents/Frameworks/SkillmKit.framework").path),
            "the app embeds the static SkillmKit framework")
        var withFeed = info
        withFeed["SUFeedURL"] = "https://example.com/appcast.xml"
        XCTAssertNotNil(UpdateFeed(info: withFeed), "SUPublicEDKey: \(String(describing: info["SUPublicEDKey"]))")
        XCTAssertEqual(info["SUEnableAutomaticChecks"] as? Bool, false)
        // Sparkle compares CFBundleVersion with the appcast's version.
        XCTAssertEqual(
            info["CFBundleVersion"] as? String, info["CFBundleShortVersionString"] as? String,
            "CFBundleVersion does not follow the release version")
        if info["SUFeedURL"] as? String == "" {
            XCTAssertNil(UpdateFeed(info: info), "a debug build updates itself")
        }
    }

    func testVersionAndStatus() async throws {
        let home = FileManager.default.temporaryDirectory.appending(path: "skillm-home-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: home) }
        let client = SkillmClient(executable: try await RealCLI.shared.url(), home: home.path)

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
        let client = SkillmClient(executable: try await RealCLI.shared.url())
        do {
            let _: ListData = try await client.run(["no-such-command"])
            XCTFail("an unknown command succeeded")
        } catch let SkillmError.command(e, _) {
            XCTAssertEqual(e.code, .usage)
        }
    }

    // MARK: - The windows' commands against the real CLI

    /// Runs the windows' flows against the real CLI in a temporary HOME:
    /// inspect a git repository and install from it at the inspected commit,
    /// install a local folder into a project, list, uninstall with the
    /// confirmed project, and change the agents and the interval. Proves the
    /// arguments the models build are ones skillm accepts.
    @MainActor
    func testWindowFlows() async throws {
        let cli = try await RealCLI.shared.url()
        let fm = FileManager.default
        let root = fm.temporaryDirectory.appending(path: "skillm-flows-\(UUID().uuidString)")
            .resolvingSymlinksInPath()
        defer { try? fm.removeItem(at: root) }
        let home = root.appending(path: "home")
        let project = root.appending(path: "project")
        let repo = root.appending(path: "repo")
        let local = root.appending(path: "notes")
        for dir in [home, project, repo.appending(path: "skills/demo"), local] {
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        }
        try "---\nname: demo\ndescription: A demo skill\n---\nDemo.\n"
            .write(to: repo.appending(path: "skills/demo/SKILL.md"), atomically: true, encoding: .utf8)
        try "---\nname: notes\ndescription: Notes\n---\nNotes.\n"
            .write(to: local.appending(path: "SKILL.md"), atomically: true, encoding: .utf8)
        let gitEnv = [
            "HOME": home.path, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
            "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com", "GIT_CONFIG_NOSYSTEM": "1",
        ]
        for args in [["init", "-q", "-b", "main"], ["add", "."], ["commit", "-q", "-m", "demo"]] {
            let git = Process()
            git.executableURL = URL(fileURLWithPath: "/usr/bin/git")
            git.arguments = ["-C", repo.path] + args
            git.environment = gitEnv
            try git.run()
            git.waitUntilExit()
            XCTAssertEqual(git.terminationStatus, 0, "git \(args)")
        }

        var env = ProcessInfo.processInfo.environment
        env["HOME"] = home.path
        env["SKILLM_HOME"] = nil
        let client = SkillmClient(executable: cli, environment: env, home: home.appending(path: ".skillm").path)
        let app = AppModel(makeClient: { client }, checksGit: false, wakeCenter: NotificationCenter())
        await app.start()
        await app.waitUntilIdle()
        defer { Task { await app.shutdown() } }
        XCTAssertEqual(app.cli, .ready(version: "dev"))

        // Add Skill: a git repository, globally, at the inspected commit.
        let add = AddSkillModel(app: app)
        add.source = "file://" + repo.path
        await add.inspect()?.value
        XCTAssertNil(add.message)
        XCTAssertEqual(add.inspection?.skills.map(\.id), ["demo"])
        XCTAssertEqual(add.selected, ["demo"])
        XCTAssertNotNil(add.inspection?.commit)
        await add.install()?.value
        XCTAssertEqual(add.installed?.skills.map(\.action), [.installed], "\(String(describing: add.message))")
        XCTAssertTrue(fm.fileExists(atPath: home.appending(path: ".agents/skills/demo/SKILL.md").path))

        // A local folder into a project.
        add.source = local.path
        await add.inspect()?.value
        add.target = .project(project)
        await add.install()?.value
        XCTAssertEqual(add.installed?.root, project.path, "\(String(describing: add.message))")
        XCTAssertTrue(fm.fileExists(atPath: project.appending(path: ".agents/skills/notes/SKILL.md").path))

        // View skills: list, update one, uninstall with the project confirmed.
        let skills = SkillsModel(app: app)
        await skills.load()
        XCTAssertEqual(skills.skills.map(\.id).sorted(), ["demo", "notes"])
        await skills.update("demo")?.value
        XCTAssertEqual(skills.message, .init(text: "demo is up to date", isError: false))
        skills.askUninstall(try XCTUnwrap(skills.skills.first { $0.id == "notes" }))
        XCTAssertEqual(skills.pendingUninstall?.roots, [project.path])
        await skills.confirmUninstall()?.value
        XCTAssertEqual(skills.message, .init(text: "Uninstalled notes", isError: false))
        await skills.load()
        XCTAssertEqual(skills.skills.map(\.id), ["demo"])
        XCTAssertFalse(fm.fileExists(atPath: project.appending(path: ".agents/skills/notes").path))

        // Settings: the agents and the interval.
        let settings = SettingsModel(app: app, loginItem: FakeLoginItem())
        await settings.load()
        XCTAssertNil(settings.loadError)
        XCTAssertTrue(settings.agents.contains { $0.name == "claude" && $0.enabled })
        settings.setAgent("claude", enabled: false)
        await settings.confirmDisable()?.value
        XCTAssertEqual(settings.agents.first { $0.name == "claude" }?.enabled, false)
        XCTAssertFalse(fm.fileExists(atPath: home.appending(path: ".claude/skills/demo").path))
        await settings.setInterval(hours: 48)?.value
        await app.waitUntilIdle()
        XCTAssertEqual(app.settings?.intervalHours, 48)
        XCTAssertNil(settings.message)
    }
}
