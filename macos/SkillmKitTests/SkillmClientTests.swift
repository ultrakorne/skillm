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

    private func client(mode: String? = nil, home: String? = nil, path: String = "/usr/bin:/bin") -> SkillmClient {
        var env = [
            "FAKE_SKILLM_FIXTURES": TestPaths.fixtures.path,
            "FAKE_SKILLM_ARGS": scratch.appending(path: "args").path,
            "FAKE_SKILLM_PATH": scratch.appending(path: "path").path,
            "FAKE_SKILLM_SIGNALS": scratch.appending(path: "signals").path,
            "PATH": path,
        ]
        if let mode { env["FAKE_SKILLM_MODE"] = mode }
        var c = SkillmClient(executable: TestPaths.fakeSkillm, environment: env, home: home)
        c.interruptGrace = .seconds(2)
        return c
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
        XCTAssertEqual(path, "/custom/bin:/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin")
    }

    func testCommandErrorKeepsCodeAndWarnings() async throws {
        do {
            let _: InstallData = try await client().run(["install", "https://example.com/r", "a", "--global"])
            XCTFail("install succeeded")
        } catch let SkillmError.command(e, warnings) {
            XCTAssertEqual(e.code, .foreignFiles)
            XCTAssertEqual(e.paths, ["/Users/me/.agents/skills/beta"])
            XCTAssertEqual(warnings.map(\.code), ["link_refused"])
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
        try await Task.sleep(for: .milliseconds(500))
        let start = ContinuousClock.now
        task.cancel()
        do {
            _ = try await task.value
            XCTFail("a cancelled run returned data")
        } catch is CancellationError {
            // The fake answered SIGINT itself, well before SIGTERM would be sent.
            XCTAssertLessThan(ContinuousClock.now - start, .seconds(2))
        }
        try await waitForSignal("INT")
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
        let c = client(mode: "stream_hang")
        let firstEvent = expectation(description: "first event")
        let task = Task { () -> [Event] in
            var events: [Event] = []
            for try await message in c.stream(["update"], as: UpdateData.self) {
                if case .event(let ev) = message {
                    events.append(ev)
                    firstEvent.fulfill()
                }
            }
            // A cancelled consumer's iteration simply ends (AsyncStream
            // semantics); the consumer notices by checking for cancellation.
            try Task.checkCancellation()
            return events
        }
        await fulfillment(of: [firstEvent], timeout: 5)
        task.cancel()
        do {
            let events = try await task.value
            XCTFail("a cancelled stream finished normally with \(events)")
        } catch is CancellationError {}
        try await waitForSignal("INT")
    }

    /// Waits until the fake recorded `signal` (well before SIGTERM would follow).
    private func waitForSignal(_ signal: String, timeout: Duration = .seconds(1.5)) async throws {
        let url = scratch.appending(path: "signals")
        let deadline = ContinuousClock.now + timeout
        while ContinuousClock.now < deadline {
            if let s = try? String(contentsOf: url, encoding: .utf8),
                s.trimmingCharacters(in: .whitespacesAndNewlines) == signal
            {
                return
            }
            try await Task.sleep(for: .milliseconds(20))
        }
        XCTFail("the fake skillm did not receive SIG\(signal)")
    }

    func testLineSplitter() {
        var s = LineSplitter()
        XCTAssertEqual(s.append(Data("{\"a\":1}\n{\"b\"".utf8)), [Data("{\"a\":1}".utf8)])
        XCTAssertEqual(s.append(Data(":2}\n\n  \n{\"c\":3}".utf8)), [Data("{\"b\":2}".utf8)])
        XCTAssertEqual(s.finish(), Data("{\"c\":3}".utf8))
        XCTAssertNil(s.finish())
    }
}
