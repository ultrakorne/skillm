import XCTest

@testable import SkillmKit

/// The menu's words for the Refresh cache and an update, and the update
/// progress folded from events, all from the golden fixtures.
final class StatusSummaryTests: XCTestCase {
    private func fixture<T: Codable & Sendable>(_ name: String, as: T.Type) throws -> T {
        let data = try Data(contentsOf: TestPaths.fixtures.appending(path: name))
        return try XCTUnwrap(protocolDecoder().decode(Envelope<T>.self, from: data).data)
    }

    private var utc: Calendar {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "UTC")!
        return c
    }

    /// The fixtures were checked at 2026-09-23T10:00:00Z.
    private let sameDay = parseProtocolDate("2026-09-23T15:00:00Z")!

    func testStatusWithUpdatesProblemsAndANewerSkillm() throws {
        let status = try fixture("status.json", as: StatusData.self)
        let lines = StatusSummary.lines(status, now: sameDay, calendar: utc)
        XCTAssertEqual(lines.map(\.kind), [.update, .problem, .update, .info])
        XCTAssertEqual(lines[0].text, "1 skill update available")
        // gamma (untracked) and delta (error): never "up to date".
        XCTAssertEqual(lines[1].text, "2 skills could not be checked")
        XCTAssertEqual(lines[2].text, "skillm 0.5.0 is available")
        XCTAssertTrue(lines[3].text.hasPrefix("Last checked at "), lines[3].text)
    }

    func testFailedSelfCheckIsAProblem() throws {
        let status = try fixture("refresh.json", as: RefreshData.self).status
        let lines = StatusSummary.lines(status, now: sameDay, calendar: utc)
        XCTAssertTrue(lines.contains(StatusLine("Could not check for a newer skillm", .problem)), "\(lines)")
    }

    func testNeverChecked() throws {
        let status = try fixture("status_never.json", as: StatusData.self)
        XCTAssertEqual(StatusSummary.lines(status), [StatusLine("Not checked for updates yet", .info)])
    }

    func testUpToDateOnlyWhenEveryCheckSucceeded() throws {
        var status = try fixture("status.json", as: StatusData.self)
        status.cache.skills = status.cache.skills.filter { $0.status == .upToDate || $0.status == .local }
        status.cache.updates = 0
        status.cache.selfStatus?.available = false
        let lines = StatusSummary.lines(status, now: sameDay, calendar: utc)
        XCTAssertEqual(lines.first, StatusLine("All skills are up to date", .info))
        XCTAssertEqual(lines.count, 2)
    }

    func testCheckedOnAnotherDayNamesTheDay() {
        let checked = parseProtocolDate("2026-09-20T10:00:00Z")!
        let text = StatusSummary.checkedText(checked, now: sameDay, calendar: utc)
        XCTAssertTrue(text.hasPrefix("on "), text)
    }

    func testUpdateResult() throws {
        let data = try fixture("update.json", as: UpdateData.self)
        let r = StatusSummary.updateResult(data)
        XCTAssertEqual(r.text, "Updated 2 skills, some installs were not updated")
        XCTAssertFalse(r.isError)

        let nothing = UpdateData(skills: [], imported: [], updated: 0, synced: true)
        XCTAssertEqual(StatusSummary.updateResult(nothing).text, "All skills are up to date")

        var failed = data
        failed.skills[1].outcome = .failed
        failed.skills[0].warnings = []
        failed.updated = 0
        let f = StatusSummary.updateResult(failed)
        XCTAssertEqual(f.text, "1 skill failed")
        XCTAssertTrue(f.isError)
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
