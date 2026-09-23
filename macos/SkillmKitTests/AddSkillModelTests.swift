import XCTest

@testable import SkillmKit

/// The Add Skill window's model against the fake: inspect, install at the
/// inspected commit, the foreign-files question, and a Source that moved.
@MainActor
final class AddSkillModelTests: XCTestCase {
    private var harness: FakeHarness!

    override func tearDown() async throws {
        await harness?.tearDown()
        harness = nil
    }

    private func started(_ env: [String: String] = [:]) async throws -> AddSkillModel {
        harness = try FakeHarness(env)
        try await harness.start()
        return AddSkillModel(app: harness.app)
    }

    /// Inspects acme/skills and chooses grill-with-docs.
    private func inspected(_ env: [String: String] = [:]) async throws -> AddSkillModel {
        let m = try await started(env)
        m.source = "  acme/skills "
        await m.inspect()?.value
        m.selected = ["grill-with-docs"]
        return m
    }

    private static let installGit =
        "install --ref main --commit 9fceb02d0ae598e95dc970b74767f19372d61af8"

    func testInspectListsTheSkills() async throws {
        let m = try await started()
        m.source = "acme/skills"
        m.ref = "v2"
        await m.inspect()?.value
        XCTAssertEqual(harness.commands(), ["source inspect --ref v2 --json -- acme/skills"])
        XCTAssertEqual(m.inspection?.skills.map(\.id), ["grill-with-docs", "notes"])
        XCTAssertEqual(m.selected, [], "nothing is chosen for the user out of several")
        XCTAssertFalse(m.isInspecting)
        XCTAssertFalse(m.canInstall)
        m.selected = ["notes", "grill-with-docs"]
        XCTAssertEqual(m.selectedIDs, ["grill-with-docs", "notes"])
        XCTAssertTrue(m.canInstall)
    }

    func testARelativeFolderIsRefused() async throws {
        let m = try await started()
        m.source = "./skills"
        XCTAssertNil(m.inspect())
        XCTAssertEqual(m.message?.isError, true)
        XCTAssertEqual(harness.commands(), [])
    }

    func testNormalizedSource() {
        func norm(_ s: String) -> String? { try? AddSkillModel.normalizedSource(s, home: "/Users/me").get() }
        XCTAssertEqual(norm(" owner/repo "), "owner/repo")
        XCTAssertEqual(norm("~/skills/a"), "/Users/me/skills/a")
        XCTAssertEqual(norm("~"), "/Users/me")
        XCTAssertEqual(norm("https://github.com/a/b"), "https://github.com/a/b")
        XCTAssertNil(norm(""))
        XCTAssertNil(norm("../x"))
    }

    func testInstallGlobalAtTheInspectedCommit() async throws {
        let m = try await inspected(["FAKE_SKILLM_NO_FOREIGN": "1"])
        let version = harness.app.installsVersion
        let task = m.install()
        guard case .installing = harness.app.activity else { return XCTFail("activity: \(harness.app.activity)") }
        await task?.value
        XCTAssertEqual(
            Array(harness.commands().suffix(2)),
            [Self.installGit + " --global --json --events -- https://github.com/acme/skills grill-with-docs", "status --json"])
        XCTAssertEqual(m.installed?.skills.map(\.id), ["alpha"])
        XCTAssertEqual(m.message, .init(text: "Installed alpha globally", isError: false))
        XCTAssertEqual(harness.app.installsVersion, version + 1)
        XCTAssertEqual(harness.app.activity, .idle)
    }

    func testForeignFilesAreAskedThenAnswered() async throws {
        let m = try await inspected()
        m.target = .project(URL(fileURLWithPath: "/Users/me/src/app"))
        await m.install()?.value
        let question = try XCTUnwrap(m.foreignFiles)
        XCTAssertEqual(question.paths, ["/Users/me/.agents/skills/beta"])
        XCTAssertEqual(question.links, ["beta: /Users/me/.claude/skills/beta is not a skillm link; left alone"])
        XCTAssertNil(m.installed)

        for (answer, flag) in [(AddSkillModel.ForeignFilesAnswer.overwrite, "--yes"), (.takeOver, "--force"), (.skip, "--skip-foreign")] {
            await m.install(answer: answer)?.value
            XCTAssertEqual(
                harness.commands().filter { $0.hasPrefix("install") }.last,
                Self.installGit
                    + " --project /Users/me/src/app \(flag) --json --events -- https://github.com/acme/skills grill-with-docs")
            XCTAssertNil(m.foreignFiles)
            XCTAssertNotNil(m.installed)
        }
    }

    func testAMovedSourceIsInspectedAgain() async throws {
        let m = try await inspected(["FAKE_SKILLM_FAIL_ON": "install"])
        await m.install()?.value
        XCTAssertEqual(m.message?.isError, true)
        let deadline = ContinuousClock.now + .seconds(5)
        while harness.commands().filter({ $0.hasPrefix("source inspect") }).count < 2 || m.isInspecting {
            guard ContinuousClock.now < deadline else { return XCTFail("no second inspect: \(harness.commands())") }
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTAssertEqual(
            harness.commands().last, "source inspect --ref main --json -- https://github.com/acme/skills")
        XCTAssertEqual(m.selected, ["grill-with-docs"], "the choice is kept")
        XCTAssertTrue(m.message?.text.contains("changed") == true, "\(String(describing: m.message))")
    }

    func testInstallSummary() throws {
        let data = InstallData(
            scope: .local, root: NSHomeDirectory() + "/src/app",
            skills: [
                InstalledSkill(id: "alpha", action: .installed), InstalledSkill(id: "beta", action: .refreshed),
                InstalledSkill(id: "omega", action: .skipped),
            ])
        XCTAssertEqual(
            AddSkillModel.installSummary(
                data, warnings: [Warning(code: "install_blocked", message: "omega blocked", skillId: "omega")]),
            .init(text: "Installed alpha, beta in ~/src/app; skipped omega", isError: false))
        XCTAssertEqual(
            AddSkillModel.installSummary(
                data, warnings: [Warning(code: "link_refused", message: "beta: link left alone", skillId: "beta")]),
            .init(text: "Installed alpha, beta in ~/src/app; skipped omega. beta: link left alone", isError: true))
    }

    func testLocalSourceNeedsNoCommit() {
        let local = InspectData(source: "/Users/me/skills", kind: .local, ref: nil, commit: nil, skills: [])
        XCTAssertEqual(
            AddSkillModel.installArguments(local, ids: ["a"], target: .global, answer: nil),
            ["install", "--global", "--", "/Users/me/skills", "a"])
    }
}
