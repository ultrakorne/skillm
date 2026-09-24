import Foundation
import XCTest

@testable import SkillmKit

/// Finding the installed CLI, the child's PATH and the git check.
final class SetupTests: XCTestCase {
    private var scratch: URL!

    override func setUpWithError() throws {
        scratch = FileManager.default.temporaryDirectory.appending(path: "skillm-setup-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: scratch, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: scratch)
    }

    private func makeExecutable(_ relative: String) throws -> URL {
        let url = scratch.appending(path: relative)
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data("#!/bin/sh\n".utf8).write(to: url)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
        return url
    }

    /// A stand-in login shell: records its arguments, prints a banner line
    /// (as startup files may), then `$FAKE_SHELL_ANSWER` when set.
    private func fakeShell(_ body: String? = nil) throws -> URL {
        let url = scratch.appending(path: "shell-\(UUID().uuidString)")
        let script =
            body ?? """
            #!/bin/sh
            printf '%s\\n' "$@" > "$FAKE_SHELL_ARGS"
            echo "Last login: today"
            [ -n "${FAKE_SHELL_ANSWER:-}" ] && echo "$FAKE_SHELL_ANSWER"
            exit 0
            """
        try Data(script.utf8).write(to: url)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
        return url
    }

    private func shell(_ url: URL, answer: String? = nil, timeout: Duration = .seconds(5)) -> LoginShell {
        var env = ["FAKE_SHELL_ARGS": scratch.appending(path: "shell-args").path]
        env["FAKE_SHELL_ANSWER"] = answer
        return LoginShell(shell: url, timeout: timeout, environment: env)
    }

    func testCandidates() {
        let dirs = [scratch.appending(path: "a"), scratch.appending(path: "b")]
        let env = ["SKILLM_BIN": "/custom/skillm"]
        XCTAssertEqual(
            SkillmBinary.candidates(environment: env, debug: false, directories: dirs).map(\.path),
            dirs.map { $0.appending(path: "skillm").path }, "a release ignores $SKILLM_BIN")
        XCTAssertEqual(
            SkillmBinary.candidates(environment: env, debug: true, directories: dirs).map(\.path),
            ["/custom/skillm"] + dirs.map { $0.appending(path: "skillm").path }, "$SKILLM_BIN first in a debug build")
    }

    func testTheInstallDirectories() {
        XCTAssertEqual(
            SkillmBinary.installDirectories(home: "/Users/me").map(\.path),
            ["/usr/local/bin", "/Users/me/.local/bin", "/opt/homebrew/bin"])
    }

    func testLocateTakesTheFirstLocationThatHasSkillm() async throws {
        let dirs = ["usr-local-bin", "local-bin", "homebrew-bin"].map { scratch.appending(path: $0) }
        func locate() async throws -> URL {
            try await SkillmBinary.locate(environment: [:], debug: false, directories: dirs, loginShell: nil)
        }

        _ = try makeExecutable("homebrew-bin/skillm")
        var found = try await locate()
        XCTAssertEqual(found.path, dirs[2].appending(path: "skillm").path)
        _ = try makeExecutable("local-bin/skillm")
        found = try await locate()
        XCTAssertEqual(found.path, dirs[1].appending(path: "skillm").path)

        // A symlink counts when it resolves to an executable; a dangling one does not.
        try FileManager.default.createDirectory(at: dirs[0], withIntermediateDirectories: true)
        try FileManager.default.createSymbolicLink(
            at: dirs[0].appending(path: "skillm"), withDestinationURL: scratch.appending(path: "gone"))
        found = try await locate()
        XCTAssertEqual(found.path, dirs[1].appending(path: "skillm").path)
        _ = try makeExecutable("gone")
        found = try await locate()
        XCTAssertEqual(found.path, dirs[0].appending(path: "skillm").path)

        // A folder named skillm is not the CLI.
        let custom = try makeExecutable("custom/skillm")
        try FileManager.default.createDirectory(
            at: scratch.appending(path: "dir/skillm"), withIntermediateDirectories: true)
        found = try await SkillmBinary.locate(
            environment: ["SKILLM_BIN": custom.path], debug: true, directories: [scratch.appending(path: "dir")],
            loginShell: nil)
        XCTAssertEqual(found.path, custom.path, "$SKILLM_BIN wins in a debug build")
    }

    func testMissingCLIListsWhereItLooked() async throws {
        let dirs = [scratch.appending(path: "a")]
        let fake = try fakeShell()
        do {
            _ = try await SkillmBinary.locate(
                environment: [:], debug: false, directories: dirs, loginShell: shell(fake))
            XCTFail("found a skillm")
        } catch SkillmError.binaryNotFound(let searched) {
            XCTAssertEqual(searched, [dirs[0].appending(path: "skillm").path, "the PATH of \(fake.lastPathComponent)"])
        }
    }

    func testTheLoginShellIsAskedLast() async throws {
        let elsewhere = try makeExecutable("mise/shims/skillm")
        let fake = try fakeShell()
        let found = try await SkillmBinary.locate(
            environment: [:], debug: false, directories: [scratch.appending(path: "a")],
            loginShell: shell(fake, answer: elsewhere.path))
        XCTAssertEqual(found.path, elsewhere.path)
        let args = try String(contentsOf: scratch.appending(path: "shell-args"), encoding: .utf8)
        XCTAssertEqual(args, "-l\n-i\n-c\ncommand -v skillm\n", "a login, interactive shell")

        // An install folder that has it wins; the shell is not asked.
        try FileManager.default.removeItem(at: scratch.appending(path: "shell-args"))
        let usual = try makeExecutable("a/skillm")
        let first = try await SkillmBinary.locate(
            environment: [:], debug: false, directories: [scratch.appending(path: "a")],
            loginShell: shell(fake, answer: elsewhere.path))
        XCTAssertEqual(first.path, usual.path)
        XCTAssertFalse(FileManager.default.fileExists(atPath: scratch.appending(path: "shell-args").path))
    }

    func testTheLoginShellAnswersOnlyWithAnExecutable() async throws {
        let fake = try fakeShell()
        let found = await shell(fake).find("skillm")
        XCTAssertNil(found, "no answer")
        let missing = await shell(fake, answer: scratch.appending(path: "nothing/skillm").path).find("skillm")
        XCTAssertNil(missing, "a path that is not there")
        let alias = await shell(fake, answer: "skillm: aliased to skillm --home x").find("skillm")
        XCTAssertNil(alias, "not a path")
        let failing = await LoginShell(shell: scratch.appending(path: "no-such-shell")).find("skillm")
        XCTAssertNil(failing, "a shell that does not start")
    }

    func testAShellThatHangsIsStopped() async throws {
        // An interactive shell ignores SIGINT and SIGTERM.
        let hang = try fakeShell(
            """
            #!/bin/sh
            trap '' INT TERM
            while :; do /bin/sleep 1; done
            """)
        let started = ContinuousClock.now
        let found = await shell(hang, timeout: .milliseconds(300)).find("skillm")
        XCTAssertNil(found)
        XCTAssertLessThan(ContinuousClock.now - started, .seconds(5), "waited for the hung shell")
    }

    func testExtendedPath() {
        XCTAssertEqual(
            ChildEnvironment.extendedPath("/usr/bin:/bin:/usr/sbin:/sbin"),
            "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
            "Homebrew's git comes before Apple's /usr/bin/git")
        XCTAssertEqual(ChildEnvironment.extendedPath(nil), "/opt/homebrew/bin:/usr/local/bin:/usr/bin")
        XCTAssertEqual(
            ChildEnvironment.extendedPath("/x:/opt/homebrew/bin"), "/usr/local/bin:/x:/opt/homebrew/bin:/usr/bin",
            "entries already on PATH keep their place")
        XCTAssertEqual(ChildEnvironment.make(from: ["A": "b"])["A"], "b")
    }

    func testGitCheck() async throws {
        // No git anywhere on PATH.
        do {
            try await GitCheck.check(path: scratch.path, developerToolsInstalled: { true })
            XCTFail("no git passed")
        } catch let e as SkillmError {
            guard case .gitMissing = e else { return XCTFail("got \(e)") }
            XCTAssertEqual(e.recoverySuggestion, SkillmError.gitFix)
        }

        // A real git on PATH: the developer tools are not asked about.
        _ = try makeExecutable("bin/git")
        try await GitCheck.check(path: scratch.appending(path: "bin").path, developerToolsInstalled: {
            XCTFail("asked about the developer tools")
            return false
        })

        // Apple's /usr/bin/git stub without the developer tools.
        do {
            try await GitCheck.check(path: "/usr/bin", developerToolsInstalled: { false })
            XCTFail("the git stub passed")
        } catch SkillmError.gitMissing(let detail) {
            XCTAssertTrue(detail.contains("Command Line Tools"), detail)
        }
        try await GitCheck.check(path: "/usr/bin", developerToolsInstalled: { true })
    }

    func testDeveloperToolsProbeRuns() async {
        // This Mac builds with Xcode, so a developer directory is set.
        let installed = await GitCheck.developerToolsInstalled()
        XCTAssertTrue(installed)
    }
}
