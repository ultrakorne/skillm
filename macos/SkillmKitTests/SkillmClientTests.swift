import Foundation
import XCTest

@testable import SkillmKit

/// Runs SkillmClient against TestSupport/fake-skillm, which answers from the
/// golden fixtures.
final class SkillmClientTests: XCTestCase {
    private var scratch: URL!

    override func setUpWithError() throws {
        scratch = FileManager.default.temporaryDirectory.appending(path: "skillm-client-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: scratch)
    }

    private var exitedMarker: URL { scratch.appending(path: "exited") }

    /// A client for the fake. The default `interruptGrace` stays: a cancel
    /// must never need SIGTERM.
    private func client(
        mode: String? = nil, home: String? = nil, path: String = "/usr/bin:/bin", cancelDelay: Double = 0
    ) -> SkillmClient {
        var env = [
            "FAKE_SKILLM_FIXTURES": TestPaths.fixtures.path,
            "FAKE_SKILLM_ARGS": scratch.appending(path: "args").path,
            "FAKE_SKILLM_PATH": scratch.appending(path: "path").path,
            "FAKE_SKILLM_SIGNALS": scratch.appending(path: "signals").path,
            "FAKE_SKILLM_EXITED": exitedMarker.path,
            "FAKE_SKILLM_CANCEL_DELAY": String(cancelDelay),
            "PATH": path,
        ]
        if let mode { env["FAKE_SKILLM_MODE"] = mode }
        return SkillmClient(executable: TestPaths.fakeSkillm, environment: env, home: home)
    }

    /// Waits until the fake has started (it records its arguments first).
    private func waitForStart() async throws {
        let url = scratch.appending(path: "args")
        let deadline = ContinuousClock.now + .seconds(5)
        while !FileManager.default.fileExists(atPath: url.path) {
            guard ContinuousClock.now < deadline else { return XCTFail("the fake skillm did not start") }
            try await Task.sleep(for: .milliseconds(10))
        }
        // Let the shell reach its `trap` lines.
        try await Task.sleep(for: .milliseconds(200))
    }

    private func recordedSignals() -> [String] {
        let s = (try? String(contentsOf: scratch.appending(path: "signals"), encoding: .utf8)) ?? ""
        return s.split(separator: "\n").map(String.init)
    }

    private func recordedArgs() throws -> [String] {
        try String(contentsOf: scratch.appending(path: "args"), encoding: .utf8)
            .split(separator: "\n", omittingEmptySubsequences: false).dropLast().map(String.init)
    }

    func testConnectAcceptsKnownAPIVersion() async throws {
        let v = try await client().connect()
        XCTAssertEqual(v.version, "0.4.0")
        XCTAssertEqual(v.apiVersion, 1)
        XCTAssertEqual(try recordedArgs(), ["version", "--json"])
    }

    func testConnectRefusesUnknownAPIVersion() async throws {
        do {
            try await client(mode: "api99").connect()
            XCTFail("connect accepted api_version 99")
        } catch SkillmError.incompatibleCLI(let version, let api, let supported) {
            XCTAssertEqual(version, "9.0.0")
            XCTAssertEqual(api, 99)
            XCTAssertEqual(supported, [1])
        }
    }

    func testRunDecodesData() async throws {
        let list: ListData = try await client().run(["list"])
        XCTAssertEqual(list.skills.map(\.id), ["grill-with-docs", "notes"])
        let status: StatusData = try await client().run(["status"])
        XCTAssertTrue(status.cache.badge)
    }

    func testArgumentsAreAnArrayNotAShellLine() async throws {
        let odd = "it's a \"path\"; rm -rf / $(echo nope) *"
        let _: ListData = try await client(home: "/tmp/my home").run(["list", odd])
        XCTAssertEqual(try recordedArgs(), ["list", odd, "--json", "--home", "/tmp/my home"])
    }

    func testPathIsExtended() async throws {
        let _: ListData = try await client(path: "/custom/bin:/bin").run(["list"])
        let path = try String(contentsOf: scratch.appending(path: "path"), encoding: .utf8)
            .trimmingCharacters(in: .newlines)
        XCTAssertEqual(path, "/opt/homebrew/bin:/usr/local/bin:/custom/bin:/bin:/usr/bin")
    }

    func testCommandErrorKeepsCodeAndWarnings() async throws {
        do {
            let _: InstallData = try await client().run(["install", "https://example.com/r", "a", "--global"])
            XCTFail("install succeeded")
        } catch let SkillmError.command(e, warnings) {
            XCTAssertEqual(e.code, .foreignFiles)
            XCTAssertEqual(e.paths, ["/Users/me/.agents/skills/beta"])
            XCTAssertEqual(warnings.map(\.code), ["agent_skipped"])
        }

        do {
            let _: UpdateData = try await client().run(["update"])
            XCTFail("update succeeded")
        } catch let SkillmError.command(e, warnings) {
            XCTAssertEqual(e.code, .updateFailed)
            XCTAssertEqual(warnings.compactMap(\.skillId), ["alpha", "beta"])
        }
    }

    func testRunEnvelopeReturnsFailures() async throws {
        let env: Envelope<UpdateData> = try await client().runEnvelope(["update"])
        XCTAssertNil(env.data)
        XCTAssertEqual(env.error?.code, .updateFailed)
    }

    func testGitMissingCarriesItsFix() async throws {
        do {
            let _: CheckData = try await client(mode: "git_missing").run(["check"])
            XCTFail("check succeeded")
        } catch let e as SkillmError {
            guard case .gitMissing = e else { return XCTFail("got \(e)") }
            XCTAssertEqual(e.recoverySuggestion, SkillmError.gitFix)
            XCTAssertTrue(e.recoverySuggestion?.contains("xcode-select --install") ?? false)
        }
    }

    func testMalformedOutputIncludesStderr() async throws {
        do {
            let _: ListData = try await client(mode: "garbage").run(["list"])
            XCTFail("garbage decoded")
        } catch SkillmError.malformedOutput(_, let stderr, let status) {
            XCTAssertEqual(stderr, "panic: something broke")
            XCTAssertEqual(status, 2)
        }
    }

    func testMalformedOutputKeepsTheEndOfStderr() async throws {
        // The diagnostic comes last, after more than a pipe holds; the error
        // must carry it every time (stderr is read to EOF before it is built).
        for _ in 0..<20 {
            do {
                let _: ListData = try await client(mode: "stderr_flood").run(["list"])
                XCTFail("garbage decoded")
            } catch SkillmError.malformedOutput(_, let stderr, let status) {
                XCTAssertTrue(stderr.hasSuffix("panic: the real reason"), String(stderr.suffix(200)))
                XCTAssertEqual(status, 2)
            }
        }
    }

    func testClientFlagsGoBeforeSeparator() {
        let c = SkillmClient(executable: URL(fileURLWithPath: "/x"), environment: [:], home: "/h")
        XCTAssertEqual(
            c.arguments(["install", "src", "--", "-x"], events: true),
            ["install", "src", "--json", "--events", "--home", "/h", "--", "-x"])
        XCTAssertEqual(
            c.arguments(["list", "--json", "--", "--home"], events: false),
            ["list", "--json", "--home", "/h", "--", "--home"],
            "flags after -- do not count")
        XCTAssertEqual(c.arguments(["list"], events: false), ["list", "--json", "--home", "/h"])
    }

    func testUnknownSchemaIsRefused() async throws {
        do {
            let _: ListData = try await client(mode: "schema2").run(["list"])
            XCTFail("schema 2 decoded")
        } catch SkillmError.unsupportedSchema(let v) {
            XCTAssertEqual(v, 2)
        }
    }

    func testMissingBinary() async throws {
        let c = SkillmClient(executable: scratch.appending(path: "nope"), environment: [:])
        do {
            let _: ListData = try await c.run(["list"])
            XCTFail("ran a missing binary")
        } catch SkillmError.launchFailed(let path, _) {
            XCTAssertTrue(path.hasSuffix("/nope"))
        }
    }

    func testCancellingRunSendsSIGINT() async throws {
        let c = client(mode: "hang")
        let task = Task { () -> ListData in try await c.run(["list"]) }
        try await waitForStart()
        task.cancel()
        do {
            _ = try await task.value
            XCTFail("a cancelled run returned data")
        } catch is CancellationError {}
        XCTAssertEqual(recordedSignals(), ["INT"])
        XCTAssertTrue(FileManager.default.fileExists(atPath: exitedMarker.path), "returned before the fake exited")
    }

    func testCancelledRunReturnsOnlyOnceSkillmHasExited() async throws {
        // The fake goes on working for a second after SIGINT, as skillm does
        // when it is finishing a write; the call must wait for it.
        let c = client(mode: "hang", cancelDelay: 1)
        let task = Task { () -> Envelope<ListData> in try await c.runEnvelope(["list"]) }
        try await waitForStart()
        let start = ContinuousClock.now
        task.cancel()
        let env = try await task.value
        XCTAssertGreaterThanOrEqual(ContinuousClock.now - start, .milliseconds(900))
        XCTAssertTrue(FileManager.default.fileExists(atPath: exitedMarker.path), "returned before the fake exited")
        XCTAssertEqual(env.error?.code, .cancelled)
        XCTAssertEqual(env.warnings.map(\.code), ["copy_failed"], "a cancelled command's warnings are kept")
        XCTAssertEqual(recordedSignals(), ["INT"], "no SIGTERM while skillm is finishing")
    }

    func testDefaultGraceIsLong() {
        let c = SkillmClient(executable: URL(fileURLWithPath: "/x"), environment: [:])
        XCTAssertGreaterThanOrEqual(c.interruptGrace, .seconds(30))
    }

    func testHungSkillmGetsSIGTERMAfterTheGrace() async throws {
        var c = client(mode: "ignore_int")
        c.interruptGrace = .seconds(1)
        let task = Task { () -> ListData in try await c.run(["list"]) }
        try await waitForStart()
        let start = ContinuousClock.now
        task.cancel()
        do {
            _ = try await task.value
            XCTFail("a cancelled run returned data")
        } catch is CancellationError {}
        XCTAssertGreaterThanOrEqual(ContinuousClock.now - start, .milliseconds(900))
        XCTAssertEqual(recordedSignals(), ["INT", "TERM"])
        XCTAssertTrue(FileManager.default.fileExists(atPath: exitedMarker.path), "returned before the fake exited")
    }

    func testStreamDeliversEventsThenResult() async throws {
        var events: [Event] = []
        var result: CheckData?
        for try await message in client().stream(["check"], as: CheckData.self) {
            switch message {
            case .event(let ev):
                XCTAssertNil(result, "event after the result")
                events.append(ev)
            case .result(let data, let warnings):
                result = data
                XCTAssertEqual(warnings, [])
            }
        }
        XCTAssertEqual(try recordedArgs(), ["check", "--json", "--events"])
        XCTAssertEqual(events.map(\.event), [.batch, .itemStart, .itemStart, .itemDone, .itemDone, .progress])
        XCTAssertEqual(events.first?.items, ["alpha", "beta"])
        XCTAssertEqual(events[3].code, "update_available")
        XCTAssertEqual(result?.updates, 1)
    }

    func testCancellingStreamSendsSIGINT() async throws {
        let c = client(mode: "stream_hang", cancelDelay: 1)
        let firstEvent = expectation(description: "first event")
        let task = Task { () -> [Event] in
            var events: [Event] = []
            for try await message in c.stream(["update"], as: UpdateData.self) {
                if case .event(let ev) = message {
                    events.append(ev)
                    if events.count == 1 { firstEvent.fulfill() }
                }
            }
            XCTFail("a cancelled stream finished normally with \(events)")
            return events
        }
        await fulfillment(of: [firstEvent], timeout: 5)
        task.cancel()
        do {
            _ = try await task.value
            XCTFail("a cancelled stream finished normally")
        } catch is CancellationError {}
        XCTAssertTrue(FileManager.default.fileExists(atPath: exitedMarker.path), "finished before the fake exited")
        XCTAssertEqual(recordedSignals(), ["INT"])
    }

    func testCancelledStreamDeliversEventsUntilSkillmExits() async throws {
        let c = client(mode: "stream_hang", cancelDelay: 0.5)
        let task = Task { () -> [EventType] in
            var kinds: [EventType] = []
            do {
                for try await message in c.stream(["update"], as: UpdateData.self) {
                    if case .event(let ev) = message {
                        kinds.append(ev.event)
                        if kinds.count == 1 { withUnsafeCurrentTask { $0?.cancel() } }
                    }
                }
                XCTFail("a cancelled stream finished normally")
            } catch is CancellationError {
            } catch {
                XCTFail("got \(error)")
            }
            return kinds
        }
        let kinds = await task.value
        XCTAssertEqual(kinds, [.batch, .itemStart], "events reported after SIGINT are delivered")
        XCTAssertTrue(FileManager.default.fileExists(atPath: exitedMarker.path), "finished before the fake exited")
    }

    func testLineSplitter() {
        var s = LineSplitter()
        XCTAssertEqual(s.append(Data("{\"a\":1}\n{\"b\"".utf8)), [Data("{\"a\":1}".utf8)])
        XCTAssertEqual(s.append(Data(":2}\n\n  \n{\"c\":3}".utf8)), [Data("{\"b\":2}".utf8)])
        XCTAssertEqual(s.finish(), Data("{\"c\":3}".utf8))
        XCTAssertNil(s.finish())
    }
}
