import XCTest

@testable import SkillmKit

/// "Install command-line tool": finding a skillm already installed, the
/// release tag it pins, and running an install script, in temporary folders.
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

    private func executable(_ url: URL) throws {
        try fm.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        fm.createFile(atPath: url.path, contents: Data("#!/bin/sh\n".utf8), attributes: [.posixPermissions: 0o755])
    }

    private func script(_ body: String) -> URL {
        let url = root.appending(path: "install-\(UUID().uuidString).sh")
        fm.createFile(atPath: url.path, contents: Data(body.utf8))
        return url
    }

    func testFindsAnInstalledSkillmOfAnyKind() throws {
        let first = root.appending(path: "usr-local-bin")
        let second = root.appending(path: "local-bin")
        XCTAssertNil(CommandLineTool.installed(in: [first, second]))

        // A plain binary, as install.sh leaves it.
        try executable(second.appending(path: "skillm"))
        XCTAssertEqual(CommandLineTool.installed(in: [first, second]), second.appending(path: "skillm"))

        // A symlink counts when it resolves to an executable; a dangling one does not.
        try fm.createDirectory(at: first, withIntermediateDirectories: true)
        try fm.createSymbolicLink(at: first.appending(path: "skillm"), withDestinationURL: root.appending(path: "gone"))
        XCTAssertEqual(CommandLineTool.installed(in: [first, second]), second.appending(path: "skillm"))
        try executable(root.appending(path: "gone"))
        XCTAssertEqual(CommandLineTool.installed(in: [first, second]), first.appending(path: "skillm"))
    }

    func testReleaseTag() {
        XCTAssertEqual(CommandLineTool.releaseTag(forVersion: "0.5.0"), "v0.5.0")
        XCTAssertEqual(CommandLineTool.releaseTag(forVersion: "v1.12.3"), "v1.12.3")
        XCTAssertNil(CommandLineTool.releaseTag(forVersion: "dev"))
        XCTAssertNil(CommandLineTool.releaseTag(forVersion: "0.5.0-3-gabc123"))
        XCTAssertNil(CommandLineTool.releaseTag(forVersion: "0.5"))
    }

    func testRunsTheScriptWithTheVersionAndEnvironment() async throws {
        let bin = root.appending(path: "bin")
        let s = script("""
            mkdir -p "$SKILLM_BIN_DIR"
            printf '%s' "$SKILLM_VERSION" > "$SKILLM_BIN_DIR/version"
            printf '#!/bin/sh\\n' > "$SKILLM_BIN_DIR/skillm" && chmod +x "$SKILLM_BIN_DIR/skillm"
            """)
        try await CommandLineTool.runInstallScript(s, version: "v0.5.0", environment: ["SKILLM_BIN_DIR": bin.path])
        XCTAssertEqual(try String(contentsOf: bin.appending(path: "version"), encoding: .utf8), "v0.5.0")
        XCTAssertEqual(CommandLineTool.installed(in: [bin]), bin.appending(path: "skillm"))
    }

    func testAFailingScriptReportsItsLastErrorLine() async throws {
        let s = script("""
            echo "resolving latest release..." >&2
            echo "error: download failed: https://example.invalid/x.tar.gz" >&2
            exit 1
            """)
        do {
            try await CommandLineTool.runInstallScript(s, version: nil)
            XCTFail("expected a failure")
        } catch let failure as CommandLineTool.Failure {
            XCTAssertEqual(failure.message, "Install failed: download failed: https://example.invalid/x.tar.gz")
        }
    }
}
