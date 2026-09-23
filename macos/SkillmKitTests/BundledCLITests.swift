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

    // MARK: - The windows' commands against the real CLI

    /// Runs the windows' flows against the real CLI in a temporary HOME:
    /// inspect a git repository and install from it at the inspected commit,
    /// install a local folder into a project, list, uninstall with the
    /// confirmed project, and change the agents and the interval. Proves the
    /// arguments the models build are ones skillm accepts.
    @MainActor
    func testWindowFlows() async throws {
        let cli = try bundledCLI()
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
        let settings = SettingsModel(
            app: app, loginItem: FakeLoginItem(), toolDirectories: [root.appending(path: "bin")])
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
