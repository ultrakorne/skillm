import XCTest

@testable import SkillmKit

/// The menu's words for the Refresh cache and an update, and the update
/// progress folded from events, all from the golden fixtures.
final class StatusSummaryTests: XCTestCase {
    private func fixture<T: Codable & Sendable>(_ name: String, as: T.Type) throws -> T {
        let data = try Data(contentsOf: TestPaths.fixtures.appending(path: name))
        return try XCTUnwrap(protocolDecoder().decode(Envelope<T>.self, from: data).data)
    }

    func testUpdatesLeaveTheLineToTheMenuItems() throws {
        // 1 update, 2 failed checks, a newer skillm: the failure is the line.
        var status = try fixture("status.json", as: StatusData.self)
        XCTAssertEqual(StatusSummary.line(status), StatusLine("2 skills could not be checked", .problem))

        // Only updates: the count goes on "Update all skills", no line.
        status.cache.skills = status.cache.skills.filter { $0.status != .error && $0.status != .untracked }
        XCTAssertNil(StatusSummary.line(status))
    }

    func testFailedSelfCheckIsAProblem() throws {
        var status = try fixture("refresh.json", as: RefreshData.self).status
        status.cache.skills = status.cache.skills.filter { $0.status == .upToDate || $0.status == .local }
        status.cache.updates = 0
        XCTAssertEqual(StatusSummary.line(status), StatusLine("Could not check for a newer skillm", .problem))
    }

    func testNeverChecked() throws {
        let status = try fixture("status_never.json", as: StatusData.self)
        XCTAssertEqual(StatusSummary.line(status), StatusLine("Not checked for updates yet", .info))
    }

    func testUpToDateOnlyWhenEveryCheckSucceeded() throws {
        var status = try fixture("status.json", as: StatusData.self)
        status.cache.skills = status.cache.skills.filter { $0.status == .upToDate || $0.status == .local }
        status.cache.updates = 0
        status.cache.selfStatus?.available = false
        XCTAssertEqual(StatusSummary.line(status), StatusLine("All skills are up to date", .info))
    }

    func testUpdateResult() throws {
        let data = try fixture("update.json", as: UpdateData.self)
        let r = StatusSummary.updateResult(data)
        XCTAssertEqual(
            r.text,
            "Updated 2 skills, repaired 1 skill, removed 2 missing installs, imported 1 skill, "
                + "some installs were not updated, 1 skill not checked for drift")
        XCTAssertFalse(r.isError)

        let nothing = UpdateData(skills: [], imported: [], updated: 0, synced: false)
        XCTAssertEqual(StatusSummary.updateResult(nothing).text, "All skills are up to date")

        var failed = data
        failed.skills = [data.skills[1]]
        failed.skills[0].outcome = .failed
        failed.imported = []
        failed.updated = 0
        failed.synced = false
        let f = StatusSummary.updateResult(failed)
        XCTAssertEqual(f.text, "1 skill failed")
        XCTAssertTrue(f.isError)
    }

    /// A run that re-synced, dropped or imported did work, as the CLI says.
    func testUpdateResultCountsWorkWithoutNewRevisions() throws {
        let data = try fixture("update.json", as: UpdateData.self)
        func only(_ ids: Set<String>, imported: Bool = false) -> UpdateData {
            var d = data
            d.skills = data.skills.filter { ids.contains($0.id) }
            d.skills = d.skills.map { var s = $0; s.advanced = false; return s }
            d.imported = imported ? data.imported : []
            d.updated = 0
            return d
        }
        XCTAssertEqual(StatusSummary.updateResult(only(["omega"])).text, "Repaired 1 skill")
        XCTAssertEqual(StatusSummary.updateResult(only(["gamma"])).text, "Removed 2 missing installs")
        XCTAssertEqual(StatusSummary.updateResult(only(["beta"], imported: true)).text, "Imported 1 skill")
        XCTAssertEqual(StatusSummary.updateResult(only(["beta"])).text, "Repaired installed copies")
        var quiet = only(["beta"])
        quiet.synced = false
        XCTAssertEqual(StatusSummary.updateResult(quiet).text, "All skills are up to date")
        XCTAssertEqual(
            StatusSummary.updateResult(only(["beta", "delta"])).text,
            "Repaired installed copies, 1 skill not checked for drift")
    }

    func testUpdateResultNamesActionableWarnings() throws {
        var data = try fixture("update.json", as: UpdateData.self)
        data.skills = [data.skills[1]]
        data.imported = []
        data.updated = 0
        data.synced = false
        let warnings = [
            Warning(code: "source_missing", message: "omega is a local skill whose source … is gone", skillId: "omega"),
            Warning(code: "status_not_saved", message: "could not update status.json", skillId: nil),
            // Repeats a skill's own warnings: not named twice.
            Warning(code: "lockfile_not_updated", message: "skills-lock.json not updated", skillId: "alpha"),
        ]
        let r = StatusSummary.updateResult(data, warnings: warnings)
        XCTAssertEqual(
            r.text, "All skills are up to date, the source of omega is gone, the update status was not saved")
        XCTAssertFalse(r.isError)

        let two = [warnings[0], Warning(code: "source_missing", message: "", skillId: "tau")]
        XCTAssertEqual(
            StatusSummary.updateResult(data, warnings: two).text,
            "All skills are up to date, the sources of 2 local skills are gone")
    }

    func testUpdateProgressFromEvents() throws {
        let text = try String(contentsOf: TestPaths.fixtures.appending(path: "update_events.ndjson"), encoding: .utf8)
        var progress = UpdateProgress()
        XCTAssertEqual(progress.text, "Updating skills…")
        var seen: [UpdateProgress] = []
        for line in text.split(separator: "\n") {
            if case .event(let e) = try StreamLine<UpdateData>.decode(Data(line.utf8)) {
                progress.apply(e)
                seen.append(progress)
            }
        }
        XCTAssertEqual(seen.map(\.running), [[], ["alpha"], []])
        XCTAssertEqual(seen.map(\.text), ["Updating skills… 0 of 1", "Updating skills… 0 of 1", "Updating skills… 1 of 1"])
    }

    func testUpdateProgressEventOverridesCounts() {
        var progress = UpdateProgress()
        progress.apply(
            Event(
                schemaVersion: 1, type: "event", event: .progress, level: nil, index: nil, skillId: nil, code: nil,
                text: nil, items: nil, done: 3, total: 7))
        XCTAssertEqual(progress.text, "Updating skills… 3 of 7")
    }
}
