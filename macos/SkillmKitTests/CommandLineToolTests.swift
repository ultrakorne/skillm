import XCTest

@testable import SkillmKit

/// Installing and upgrading the CLI outside the protocol: running an install
/// script and a plain `skillm upgrade`, in temporary folders.
final class CommandLineToolTests: XCTestCase {
    private var root: URL!
    private let fm = FileManager.default

    override func setUpWithError() throws {
        root = fm.temporaryDirectory.appending(path: "skillm-cli-\(UUID().uuidString)")
        try fm.createDirectory(at: root, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? fm.removeItem(at: root)
    }

    private func script(_ body: String, executable: Bool = false) -> URL {
        let url = root.appending(path: "script-\(UUID().uuidString)")
        fm.createFile(
            atPath: url.path, contents: Data(body.utf8), attributes: executable ? [.posixPermissions: 0o755] : nil)
        return url
    }

    func testRunsTheScriptForTheLatestReleaseWithTheEnvironment() async throws {
        let bin = root.appending(path: "bin")
        let s = script("""
            mkdir -p "$SKILLM_BIN_DIR"
            printf '%s' "${SKILLM_VERSION:-latest}" > "$SKILLM_BIN_DIR/version"
            printf '#!/bin/sh\\n' > "$SKILLM_BIN_DIR/skillm" && chmod +x "$SKILLM_BIN_DIR/skillm"
            """)
        setenv("SKILLM_VERSION", "v0.1.0", 1)
        defer { unsetenv("SKILLM_VERSION") }
        try await CommandLineTool.runInstallScript(s, environment: ["SKILLM_BIN_DIR": bin.path])
        XCTAssertEqual(
            try String(contentsOf: bin.appending(path: "version"), encoding: .utf8), "latest",
            "the app's environment pinned a version")
        XCTAssertTrue(fm.isExecutableFile(atPath: bin.appending(path: "skillm").path))
    }

    func testAFailingScriptReportsItsLastErrorLine() async throws {
        let s = script("""
            echo "resolving latest release..." >&2
            echo "error: download failed: https://example.invalid/x.tar.gz" >&2
            exit 1
            """)
        do {
            try await CommandLineTool.runInstallScript(s)
            XCTFail("expected a failure")
        } catch let failure as CommandLineTool.Failure {
            XCTAssertEqual(failure.message, "Install failed: download failed: https://example.invalid/x.tar.gz")
        }
    }

    func testUpgradeRunsAPlainSkillmUpgrade() async throws {
        let args = root.appending(path: "args")
        let cli = script(
            """
            #!/bin/sh
            printf '%s\\n' "$@" > "\(args.path)"
            echo "Upgraded skillm 0.3.0 → 0.5.0"
            """, executable: true)
        try await CommandLineTool.runUpgrade(cli, environment: [:])
        XCTAssertEqual(try String(contentsOf: args, encoding: .utf8), "upgrade\n", "no --json")
    }

    func testAFailingUpgradeReportsItsLastLine() async throws {
        let cli = script(
            """
            #!/bin/sh
            printf '\\n   ERROR  \\n\\n  check for a newer skillm: dial tcp: no route to host  \\n\\n' >&2
            exit 1
            """, executable: true)
        do {
            try await CommandLineTool.runUpgrade(cli, environment: [:])
            XCTFail("expected a failure")
        } catch let failure as CommandLineTool.Failure {
            XCTAssertEqual(failure.message, "Upgrade failed: check for a newer skillm: dial tcp: no route to host")
        }
    }
}
