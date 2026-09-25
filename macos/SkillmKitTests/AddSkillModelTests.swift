import XCTest

@testable import SkillmKit

/// The Add Skill window's model against the fake: inspect, install at the
/// inspected commit, the foreign-files and refused-links questions, and a
/// Source that moved.
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

    func testContinueOpensTheTargetPageAndReadingAgainLeavesIt() async throws {
        let m = try await started()
        m.source = "acme/skills"
        await m.inspect()?.value
        XCTAssertFalse(m.canContinue)
        m.continueToTarget()
        XCTAssertEqual(m.step, .skills, "nothing chosen: no target page")
        m.selected = ["notes"]
        m.continueToTarget()
        XCTAssertEqual(m.step, .target)
        m.back()
        XCTAssertEqual(m.step, .skills)
        XCTAssertEqual(m.selected, ["notes"], "back keeps the choice")
        m.continueToTarget()
        await m.inspect()?.value
        XCTAssertEqual(m.step, .skills)
    }

    func testFuzzyFilter() {
        func skill(_ id: String, _ description: String = "") -> InspectedSkill {
            InspectedSkill(id: id, name: id, description: description, path: id)
        }
        let skills = [
            skill("notes", "Take notes"), skill("grill-with-docs"), skill("git-commit", "Write good commits"),
            skill("docs-writer"),
        ]
        func ids(_ q: String) -> [String] { AddSkillModel.fuzzyFilter(skills, query: q).map(\.id) }
        XCTAssertEqual(ids(""), skills.map(\.id))
        XCTAssertEqual(ids("  "), skills.map(\.id))
        XCTAssertEqual(ids("DOCS"), ["docs-writer", "grill-with-docs"], "a prefix beats a substring")
        XCTAssertEqual(ids("gwd"), ["grill-with-docs"], "letters in order")
        XCTAssertEqual(ids("good"), ["git-commit"], "the description counts")
        XCTAssertEqual(ids("git commit"), ["git-commit"], "every word must match")
        XCTAssertEqual(ids("zzz"), [])
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
        XCTAssertTrue(m.isDone, "installed: Done replaces Install")
        m.target = .global
        XCTAssertTrue(m.isDone, "the same target stays done")
        m.target = .project(URL(fileURLWithPath: "/Users/me/src/app"))
        XCTAssertFalse(m.isDone, "another target offers Install again")
        XCTAssertNil(m.message, "the summary was about the old target")
    }

    func testBackForgetsAFinishedInstall() async throws {
        let m = try await inspected(["FAKE_SKILLM_NO_FOREIGN": "1"])
        m.continueToTarget()
        await m.install()?.value
        XCTAssertTrue(m.isDone)
        m.back()
        XCTAssertFalse(m.isDone)
        XCTAssertNil(m.message)
        XCTAssertEqual(m.selected, ["grill-with-docs"], "back keeps the choice")
    }

    func testResetEmptiesTheForm() async throws {
        let m = try await inspected(["FAKE_SKILLM_NO_FOREIGN": "1"])
        m.continueToTarget()
        await m.install()?.value
        XCTAssertTrue(m.isDone)
        m.reset()
        XCTAssertFalse(m.isDone)
        XCTAssertEqual(m.source, "")
        XCTAssertNil(m.inspection)
        XCTAssertEqual(m.selected, [])
        XCTAssertEqual(m.step, .skills)
        XCTAssertEqual(m.target, .global)
        XCTAssertNil(m.message)
    }

    func testForeignFilesAreAskedThenAnswered() async throws {
        let m = try await inspected()
        m.target = .project(URL(fileURLWithPath: "/Users/me/src/app"))
        await m.install()?.value
        let question = try XCTUnwrap(m.foreignFiles)
        XCTAssertEqual(question.paths, ["/Users/me/.agents/skills/beta"])
        XCTAssertNil(m.installed)
        XCTAssertNil(m.refusedLinks)

        for (answer, flag) in [(AddSkillModel.ForeignFilesAnswer.overwrite, "--yes"), (.skip, "--skip-foreign")] {
            await m.answer(question, with: answer)?.value
            XCTAssertEqual(
                harness.commands().filter { $0.hasPrefix("install") }.last,
                Self.installGit
                    + " --project /Users/me/src/app \(flag) --json --events -- https://github.com/acme/skills grill-with-docs")
            XCTAssertNil(m.foreignFiles)
            XCTAssertNotNil(m.installed)
        }
    }

    /// The form edited while the install ran does not change what an answer
    /// retries: the question is about the install as it was started.
    func testAnAnswerRetriesTheInstallItWasAskedAbout() async throws {
        let m = try await inspected()
        m.target = .project(URL(fileURLWithPath: "/Users/me/src/app"))
        let task = m.install()
        XCTAssertTrue(m.isInstalling)
        m.target = .global
        m.selected = ["grill-with-docs", "notes"]
        await task?.value
        let question = try XCTUnwrap(m.foreignFiles)
        await m.answer(question, with: .overwrite)?.value
        XCTAssertEqual(
            harness.commands().filter { $0.hasPrefix("install") }.last,
            Self.installGit
                + " --project /Users/me/src/app --yes --json --events -- https://github.com/acme/skills grill-with-docs")
    }

    /// An answer given while another command runs is not lost: the
    /// question stays until the retry can start.
    func testAQuestionWaitsForTheRunningCommand() async throws {
        let m = try await inspected(["FAKE_SKILLM_HANG_ON": "update"])
        await m.install()?.value
        let question = try XCTUnwrap(m.foreignFiles)
        let update = harness.app.updateAll()
        XCTAssertNotNil(update)
        XCTAssertNil(m.answer(question, with: .overwrite))
        XCTAssertEqual(m.foreignFiles, question)
        harness.app.cancel()
        await update?.value
        await m.answer(question, with: .overwrite)?.value
        XCTAssertNil(m.foreignFiles)
        XCTAssertNotNil(m.installed)
    }

    /// skillm reports refused agent links on a result whose copies landed:
    /// taking them over retries the same install, of those skills only,
    /// with --force.
    func testRefusedLinksAreOfferedForTakeOver() async throws {
        let m = try await inspected(["FAKE_SKILLM_NO_FOREIGN": "1", "FAKE_SKILLM_LINK_REFUSED": "notes"])
        m.selected = ["grill-with-docs", "notes"]
        await m.install()?.value
        let question = try XCTUnwrap(m.refusedLinks)
        XCTAssertEqual(question.request.ids, ["notes"])
        XCTAssertEqual(question.links, ["notes: /Users/me/.claude/skills/notes is not a skillm link; left alone"])
        XCTAssertNil(m.foreignFiles)
        XCTAssertEqual(m.message?.isError, true, "the summary names the refused link")

        await m.takeOverLinks(question)?.value
        XCTAssertEqual(
            harness.commands().filter { $0.hasPrefix("install") }.last,
            Self.installGit + " --global --force --json --events -- https://github.com/acme/skills notes")
        XCTAssertNil(m.refusedLinks)
        XCTAssertEqual(m.message?.isError, false)
    }

    /// Overwriting the foreign files can still leave links refused: the
    /// take-over question follows.
    func testRefusedLinksAfterAnOverwrite() async throws {
        let m = try await inspected(["FAKE_SKILLM_LINK_REFUSED": "grill-with-docs"])
        await m.install()?.value
        let foreign = try XCTUnwrap(m.foreignFiles)
        await m.answer(foreign, with: .overwrite)?.value
        XCTAssertNil(m.foreignFiles)
        XCTAssertEqual(m.refusedLinks?.request.ids, ["grill-with-docs"])
    }

    func testRefusedLinksQuestion() {
        let inspection = InspectData(source: "/Users/me/skills", kind: .local, ref: nil, commit: nil, skills: [])
        let request = AddSkillModel.InstallRequest(inspection: inspection, ids: ["a", "b", "c"], target: .global)
        XCTAssertNil(
            AddSkillModel.refusedLinks(
                request, warnings: [Warning(code: "link_failed", message: "a: permission denied", skillId: "a")]),
            "an I/O failure is not something --force fixes")
        let question = AddSkillModel.refusedLinks(
            request,
            warnings: [
                Warning(code: "link_refused", message: "c: held", skillId: "c"),
                Warning(code: "agent_skipped", message: "skipped opencode", skillId: nil),
                Warning(code: "link_refused", message: "a: held", skillId: "a"),
            ])
        XCTAssertEqual(question?.request.ids, ["a", "c"])
        XCTAssertEqual(question?.links, ["c: held", "a: held"])
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
            AddSkillModel.installArguments(.init(inspection: local, ids: ["a"], target: .global), flag: nil),
            ["install", "--global", "--", "/Users/me/skills", "a"])
    }
}
