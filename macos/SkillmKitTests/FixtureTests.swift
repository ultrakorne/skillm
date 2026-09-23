import Foundation
import XCTest

@testable import SkillmKit

/// Decodes every golden fixture in internal/protocol/testdata with the Swift
/// models, and re-encodes it to prove nothing was dropped: a field the Go
/// side adds, renames or retypes fails here until the models follow.
final class FixtureTests: XCTestCase {
    /// How to check each fixture: by file name, the data type its command
    /// returns. Every file directly in testdata/ must be listed.
    private static let checks: [String: @Sendable (URL) throws -> Void] = [
        "agents.json": document(AgentsData.self),
        "agents_set.json": document(AgentsSetData.self),
        "agents_set_unchanged.json": document(AgentsSetData.self),
        "check.json": document(CheckData.self),
        "check_events.ndjson": events(CheckData.self),
        "config.json": document(ConfigData.self),
        "error_cancelled.json": document(CheckData.self),
        "error_foreign_files.json": document(InstallData.self),
        "error_home_locked.json": document(InstallData.self),
        "error_invalid_value.json": document(ConfigData.self),
        "error_needs_confirm.json": document(UninstallData.self),
        "error_update_failed.json": document(UpdateData.self),
        "import.json": document(ImportData.self),
        "inspect.json": document(InspectData.self),
        "inspect_local.json": document(InspectData.self),
        "install.json": document(InstallData.self),
        "install_events.ndjson": events(InstallData.self),
        "install_global_empty.json": document(InstallData.self),
        "list.json": document(ListData.self),
        "list_empty.json": document(ListData.self),
        "refresh.json": document(RefreshData.self),
        "self_status.json": document(SelfStatus.self),
        "self_status_dev.json": document(SelfStatus.self),
        "status.json": document(StatusData.self),
        "status_never.json": document(StatusData.self),
        "uninstall.json": document(UninstallData.self),
        "update.json": document(UpdateData.self),
        "update_events.ndjson": events(UpdateData.self),
        "upgrade.json": document(UpgradeData.self),
        "version.json": document(VersionData.self),
    ]

    func testEveryFixtureDecodesLosslessly() throws {
        let files = try FileManager.default.contentsOfDirectory(
            at: TestPaths.fixtures, includingPropertiesForKeys: [.isDirectoryKey])
        let names = try files.filter { try $0.resourceValues(forKeys: [.isDirectoryKey]).isDirectory != true }
            .map(\.lastPathComponent).filter { $0.hasSuffix(".json") || $0.hasSuffix(".ndjson") }
        XCTAssertFalse(names.isEmpty, "no fixtures in \(TestPaths.fixtures.path)")
        XCTAssertEqual(Set(names), Set(Self.checks.keys), "every fixture needs a data type in FixtureTests.checks")
        for name in names.sorted() {
            guard let check = Self.checks[name] else { continue }
            XCTAssertNoThrow(try check(TestPaths.fixtures.appending(path: name)), name)
        }
    }

    func testStatusCacheFile() throws {
        let url = TestPaths.fixtures.appending(path: "cache/status.json")
        let data = try Data(contentsOf: url)
        let cache = try protocolDecoder().decode(StatusCache.self, from: data)
        try assertLossless(cache, data, url.lastPathComponent)
        XCTAssertTrue(cache.badge)
        XCTAssertEqual(cache.selfStatus?.method, .bundled)
    }

    func testDecodedValues() throws {
        let version: Envelope<VersionData> = try decodeFixture("version.json")
        XCTAssertEqual(version.data?.apiVersion, 1)
        XCTAssertTrue(version.data?.capabilities.contains("status") ?? false)

        let list: Envelope<ListData> = try decodeFixture("list.json")
        let skill = try XCTUnwrap(list.data?.skills.first)
        XCTAssertEqual(skill.kind, .git)
        XCTAssertEqual(skill.installs.first?.scope, .global)
        XCTAssertEqual(skill.installedAt, ISO8601DateFormatter().date(from: "2026-09-01T08:30:00Z"))

        let status: Envelope<StatusData> = try decodeFixture("status.json")
        let st = try XCTUnwrap(status.data)
        XCTAssertTrue(st.cache.badge)
        XCTAssertFalse(st.stale)
        XCTAssertEqual(st.cache.skills.map(\.status), [.upToDate, .updateAvailable, .untracked, .error, .local])
        XCTAssertEqual(st.cache.selfStatus?.available, true)

        let never: Envelope<StatusData> = try decodeFixture("status_never.json")
        XCTAssertNil(never.data?.cache.checkedAt)
        XCTAssertNil(never.data?.cache.selfStatus)
        XCTAssertEqual(never.data?.stale, true)

        let refresh: Envelope<RefreshData> = try decodeFixture("refresh.json")
        XCTAssertEqual(refresh.data?.refreshed, true)
        XCTAssertEqual(refresh.warnings.first?.code, "self_check_failed")

        let foreign: Envelope<InstallData> = try decodeFixture("error_foreign_files.json")
        XCTAssertNil(foreign.data)
        XCTAssertEqual(foreign.error?.code, .foreignFiles)
        XCTAssertEqual(foreign.error?.paths, ["/Users/me/.agents/skills/beta"])

        let locked: Envelope<InstallData> = try decodeFixture("error_home_locked.json")
        XCTAssertEqual(locked.error?.code, .homeLocked)
        XCTAssertEqual(locked.error?.retryable, true)
    }

    func testUnknownValuesStayOpen() throws {
        let json = #"{"id":"a","kind":"svn","status":"quarantined"}"#
        let s = try protocolDecoder().decode(CheckedSkill.self, from: Data(json.utf8))
        XCTAssertEqual(s.status.rawValue, "quarantined")
        XCTAssertNotEqual(s.status, .error)
        XCTAssertEqual(s.kind, SkillKind(rawValue: "svn"))
    }

    func testDatesWithAndWithoutFractionalSeconds() throws {
        let whole = try XCTUnwrap(parseProtocolDate("2026-09-23T10:00:00Z"))
        XCTAssertEqual(parseProtocolDate("2026-09-23T10:00:00.250Z")?.timeIntervalSince(whole), 0.25)
        XCTAssertEqual(parseProtocolDate("2026-09-23T12:00:00+02:00"), whole)
        XCTAssertNil(parseProtocolDate("yesterday"))

        let json = #"{"schema_version":1,"skills":[],"updates":0,"self":null,"errors":[],"badge":false,"#
            + #""checked_at":"2026-09-23T10:00:00.5Z"}"#
        let cache = try protocolDecoder().decode(StatusCache.self, from: Data(json.utf8))
        XCTAssertEqual(cache.checkedAt?.timeIntervalSince(whole), 0.5)
    }

    // MARK: - Helpers

    private func decodeFixture<T>(_ name: String) throws -> Envelope<T> {
        let data = try Data(contentsOf: TestPaths.fixtures.appending(path: name))
        return try SkillmClient.decodeDocument(data, as: T.self)
    }

    /// Checks a single-document fixture.
    private static func document<T: Codable & Sendable>(_: T.Type) -> @Sendable (URL) throws -> Void {
        { url in
            let data = try Data(contentsOf: url)
            let env = try SkillmClient.decodeDocument(data, as: T.self)
            XCTAssertNil(env.type, url.lastPathComponent)
            XCTAssertTrue((env.data == nil) != (env.error == nil), "\(url.lastPathComponent): data xor error")
            try assertLossless(env, data, url.lastPathComponent)
        }
    }

    /// Checks an --events fixture: event lines, then exactly one result.
    private static func events<T: Codable & Sendable>(_: T.Type) -> @Sendable (URL) throws -> Void {
        { url in
            let name = url.lastPathComponent
            let lines = try String(contentsOf: url, encoding: .utf8).split(separator: "\n").map { Data($0.utf8) }
            XCTAssertGreaterThan(lines.count, 1, name)
            for (i, line) in lines.enumerated() {
                switch try StreamLine<T>.decode(line) {
                case .event(let ev):
                    XCTAssertLessThan(i, lines.count - 1, "\(name): an event after the result")
                    try assertLossless(ev, line, "\(name) line \(i + 1)")
                case .result(let env):
                    XCTAssertEqual(i, lines.count - 1, "\(name): the result must be the last line")
                    XCTAssertEqual(env.type, "result")
                    try assertLossless(env, line, "\(name) line \(i + 1)")
                }
            }
        }
    }
}

/// Fails unless encoding `value` gives back the JSON in `original` (nulls
/// aside: Go writes some absent values as null, Swift omits them).
func assertLossless<T: Encodable>(_ value: T, _ original: Data, _ name: String,
                                  file: StaticString = #filePath, line: UInt = #line) throws {
    let encoded = try protocolEncoder().encode(value)
    let want = dropNulls(try JSONSerialization.jsonObject(with: original))
    let got = dropNulls(try JSONSerialization.jsonObject(with: encoded))
    XCTAssertEqual(got as? NSObject, want as? NSObject,
                   "\(name) does not round-trip:\nwant \(want)\ngot  \(got)", file: file, line: line)
}

private func dropNulls(_ v: Any) -> Any {
    if let d = v as? [String: Any] {
        return d.filter { !($0.value is NSNull) }.mapValues(dropNulls)
    }
    if let a = v as? [Any] { return a.map(dropNulls) }
    return v
}
