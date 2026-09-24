import XCTest

@testable import SkillmKit

/// The View skills window's model against the fake: list, per-skill update,
/// and the uninstall confirmation and its retries.
@MainActor
final class SkillsModelTests: XCTestCase {
    private var harness: FakeHarness!

    override func tearDown() async throws {
        await harness?.tearDown()
        harness = nil
    }

    private func started(_ env: [String: String] = [:]) async throws -> SkillsModel {
        harness = try FakeHarness(env)
        try await harness.start()
        return SkillsModel(app: harness.app)
    }

    func testLoadListsTheSkills() async throws {
        let m = try await started()
        await m.load()
        XCTAssertTrue(m.loaded)
        XCTAssertNil(m.loadError)
        XCTAssertEqual(m.skills.map(\.id), ["grill-with-docs", "notes"])
        XCTAssertEqual(harness.commands(), ["list --json"])
    }

    func testLoadFailureIsShown() async throws {
        harness = try FakeHarness()
        // Not started: nothing can run.
        let m = SkillsModel(app: harness.app)
        await m.load()
        XCTAssertFalse(m.loaded)
        XCTAssertEqual(m.loadError, "skillm is not ready yet.")
    }

    func testUpdateRunsForOneSkillThenRereadsStatus() async throws {
        let m = try await started()
        let version = harness.app.installsVersion
        let task = m.update("alpha")
        XCTAssertEqual(harness.app.activity, .updatingSkill("alpha"))
        await task?.value
        XCTAssertEqual(harness.commands(), ["update --json --events -- alpha", "status --json"])
        XCTAssertEqual(m.message, .init(text: "Updated alpha, some installs were not updated", isError: false))
        XCTAssertEqual(harness.app.installsVersion, version + 1)
        XCTAssertEqual(harness.app.activity, .idle)
    }

    func testFailedUpdateIsShownInTheWindowNotTheMenu() async throws {
        let m = try await started(["FAKE_SKILLM_FAIL_ON": "update"])
        await m.update("alpha")?.value
        XCTAssertEqual(m.message, .init(text: "2 skills failed to update: alpha, beta", isError: true))
        XCTAssertNil(harness.app.notice)
    }

    func testUninstallNamesTheRecordedProjects() async throws {
        let m = try await started()
        await m.load()
        m.askUninstall(try XCTUnwrap(m.skills.first { $0.id == "grill-with-docs" }))
        XCTAssertEqual(
            m.pendingUninstall, .init(id: "grill-with-docs", global: true, roots: ["/Users/me/src/app"]))

        m.askUninstall(try XCTUnwrap(m.skills.first { $0.id == "notes" }))
        XCTAssertEqual(m.pendingUninstall, .init(id: "notes", global: false, roots: ["/Users/me/src/other"]))
    }

    func testConfirmedUninstallPassesTheProjects() async throws {
        let m = try await started()
        let version = harness.app.installsVersion
        m.pendingUninstall = .init(id: "alpha", global: true, roots: ["/Users/me/src/app"])
        let task = m.confirmUninstall()
        XCTAssertNil(m.pendingUninstall)
        await task?.value
        XCTAssertEqual(
            harness.commands(),
            ["uninstall --yes --confirmed-root /Users/me/src/app --json -- alpha", "status --json"])
        XCTAssertEqual(m.message, .init(text: "Uninstalled alpha", isError: false))
        XCTAssertNil(m.pendingUninstall)
        XCTAssertEqual(harness.app.installsVersion, version + 1)
    }

    func testUninstallWithoutProjectsConfirmsNone() async throws {
        let m = try await started()
        m.pendingUninstall = .init(id: "beta", global: true, roots: [])
        await m.confirmUninstall()?.value
        XCTAssertEqual(harness.commands().first, "uninstall --yes --confirmed-root= --json -- beta")
        XCTAssertEqual(
            m.message,
            .init(
                text: "Uninstalled beta, but left in place: /Users/me/.claude/skills/beta: not a skillm link; left in place",
                isError: true))
    }

    func testUninstallAsksAgainWhenAProjectWasAdded() async throws {
        let failing = FileManager.default.temporaryDirectory.appending(path: "skillm-failing-\(UUID().uuidString)")
        FileManager.default.createFile(atPath: failing.path, contents: nil)
        defer { try? FileManager.default.removeItem(at: failing) }
        let skills = try await started(["FAKE_SKILLM_FAIL_ON": "uninstall", "FAKE_SKILLM_FAIL_WHILE": failing.path])

        skills.pendingUninstall = .init(id: "alpha", global: true, roots: ["/Users/me/src/app"])
        await skills.confirmUninstall()?.value
        let again = try XCTUnwrap(skills.pendingUninstall)
        XCTAssertEqual(again.roots, ["/Users/me/src/app", "/Users/me/src/new"])
        XCTAssertNotNil(again.reason)
        XCTAssertFalse(again.force)
        XCTAssertNil(skills.message)

        try FileManager.default.removeItem(at: failing)
        await skills.confirmUninstall()?.value
        XCTAssertEqual(
            harness.commands().filter { $0.hasPrefix("uninstall") }.last,
            "uninstall --yes --confirmed-root /Users/me/src/app --confirmed-root /Users/me/src/new --json -- alpha")
        XCTAssertEqual(skills.message, .init(text: "Uninstalled alpha", isError: false))
        XCTAssertNil(skills.pendingUninstall)
    }

    func testUninstallOffersForceWhenAnotherToolIsInTheWay() async throws {
        let m = try await started(["FAKE_SKILLM_NEEDS_FORCE": "1"])
        m.pendingUninstall = .init(id: "alpha", global: true, roots: [])
        await m.confirmUninstall()?.value
        let again = try XCTUnwrap(m.pendingUninstall)
        XCTAssertTrue(again.force)
        XCTAssertEqual(again.reason, "beta: /Users/me/.claude/skills/beta is not a skillm link")

        await m.confirmUninstall()?.value
        XCTAssertEqual(
            harness.commands().filter { $0.hasPrefix("uninstall") }.last,
            "uninstall --yes --force --confirmed-root= --json -- alpha")
        XCTAssertEqual(m.message, .init(text: "Uninstalled alpha", isError: false))
    }

    func testNothingRunsWhileAnotherCommandRuns() async throws {
        let m = try await started(["FAKE_SKILLM_HANG_ON": "update"])
        let running = harness.app.updateAll()
        XCTAssertNotNil(running)
        m.pendingUninstall = .init(id: "alpha", global: true, roots: [])
        XCTAssertNil(m.confirmUninstall())
        XCTAssertNotNil(m.pendingUninstall, "the question stays for a retry")
        XCTAssertNil(m.update("alpha"))
        // A read still runs beside it.
        await m.load()
        XCTAssertTrue(m.loaded)
        try await Task.sleep(for: .milliseconds(300))
        harness.app.cancel()
        await running?.value
    }

    // MARK: - Words

    func testPlaceText() {
        let global = SkillInstall(
            scope: .global, root: nil, path: "/Users/me/.agents/skills/a", agents: ["agents"], recorded: true,
            exists: true)
        let local = SkillInstall(
            scope: .local, root: "/Users/me/src/app", path: "/Users/me/src/app/.agents/skills/a", agents: [],
            recorded: true, exists: false)
        XCTAssertEqual(SkillsModel.placeText(global, home: "/Users/me"), "Global")
        XCTAssertEqual(SkillsModel.placeText(local, home: "/Users/me"), "~/src/app (missing)")
        XCTAssertEqual(SkillsModel.placeText(local, home: "/Users/other"), "/Users/me/src/app (missing)")
        let unread = SkillInstall(
            scope: .global, root: nil, path: "/Users/me/.agents/skills/a", agents: [], recorded: true, exists: true)
        XCTAssertEqual(SkillsModel.placeText(unread, home: "/Users/me"), "Global (no agent)")
        XCTAssertEqual(SkillsModel.abbreviate("/Users/me", home: "/Users/me"), "~")
        XCTAssertEqual(SkillsModel.abbreviate("/Users/meme/x", home: "/Users/me"), "/Users/meme/x")
    }

    func testSkillUpdateWords() {
        func data(_ outcome: UpdateOutcome, pruned: [String] = [], error: String? = nil) -> UpdateData {
            UpdateData(
                skills: [
                    UpdatedSkill(
                        id: "a", kind: .git, outcome: outcome, revision: nil, advanced: false, pruned: pruned,
                        error: error, warnings: [])
                ], imported: [], updated: 0, synced: false)
        }
        XCTAssertEqual(StatusSummary.skillUpdateResult("a", data(.upToDate)).text, "a is up to date")
        XCTAssertEqual(StatusSummary.skillUpdateResult("a", data(.synced)).text, "Repaired the copies of a")
        XCTAssertEqual(
            StatusSummary.skillUpdateResult("a", data(.upToDate, pruned: ["global"])).text,
            "a is up to date, removed 1 missing install")
        let failed = StatusSummary.skillUpdateResult("a", data(.failed, error: "no route"))
        XCTAssertEqual(failed.text, "a failed to update: no route")
        XCTAssertTrue(failed.isError)
        XCTAssertEqual(
            StatusSummary.skillUpdateResult(
                "a", data(.updated), warnings: [Warning(code: "status_not_saved", message: "x", skillId: nil)]
            ).text, "Updated a, the update status was not saved")
    }
}
