import XCTest

@testable import SkillmKit

/// The `skillm` link "Install command-line tool" makes, in temporary folders.
final class CommandLineToolTests: XCTestCase {
    private var root: URL!
    private let fm = FileManager.default

    override func setUpWithError() throws {
        root = fm.temporaryDirectory.appending(path: "skillm-cli-\(UUID().uuidString)")
        try fm.createDirectory(at: root, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        // A read-only folder must be writable again to be removed.
        if let items = fm.enumerator(at: root, includingPropertiesForKeys: nil) {
            for case let url as URL in items { try? fm.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path) }
        }
        try? fm.removeItem(at: root)
    }

    /// A fake CLI inside a fake app bundle.
    private func bundledCLI(_ app: String) throws -> URL {
        let cli = root.appending(path: "\(app).app/Contents/Helpers/skillm")
        try fm.createDirectory(at: cli.deletingLastPathComponent(), withIntermediateDirectories: true)
        fm.createFile(atPath: cli.path, contents: Data("#!/bin/sh\n".utf8))
        return cli
    }

    private func dest(_ link: URL) throws -> String { try fm.destinationOfSymbolicLink(atPath: link.path) }

    func testLinksIntoTheFirstWritableFolder() throws {
        let cli = try bundledCLI("skillm")
        let first = root.appending(path: "usr-local-bin")
        let second = root.appending(path: "local-bin")
        try fm.createDirectory(at: first, withIntermediateDirectories: true)
        let link = try CommandLineTool.install(target: cli, directories: [first, second])
        XCTAssertEqual(link, first.appending(path: "skillm"))
        XCTAssertEqual(try dest(link), cli.path)
        XCTAssertEqual(CommandLineTool.installedLink(to: cli, in: [first, second]), link)
    }

    func testFallsBackToTheLastFolderCreated() throws {
        let cli = try bundledCLI("skillm")
        let readOnly = root.appending(path: "usr-local-bin")
        try fm.createDirectory(at: readOnly, withIntermediateDirectories: true)
        try fm.setAttributes([.posixPermissions: 0o555], ofItemAtPath: readOnly.path)
        let missing = root.appending(path: "home/.local/bin")
        let link = try CommandLineTool.install(target: cli, directories: [readOnly, missing])
        XCTAssertEqual(link, missing.appending(path: "skillm"))
        XCTAssertEqual(try dest(link), cli.path)
    }

    func testAnExistingLinkIsKept() throws {
        let cli = try bundledCLI("skillm")
        let dir = root.appending(path: "bin")
        try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        let first = try CommandLineTool.install(target: cli, directories: [dir])
        let again = try CommandLineTool.install(target: cli, directories: [dir])
        XCTAssertEqual(first, again)
    }

    func testALinkIntoAnotherAppOrADanglingOneIsReplaced() throws {
        let cli = try bundledCLI("skillm")
        let old = try bundledCLI("Old skillm")
        let dir = root.appending(path: "bin")
        try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        let link = dir.appending(path: "skillm")

        try fm.createSymbolicLink(atPath: link.path, withDestinationPath: old.path)
        XCTAssertNil(CommandLineTool.installedLink(to: cli, in: [dir]))
        XCTAssertEqual(try CommandLineTool.install(target: cli, directories: [dir]), link)
        XCTAssertEqual(try dest(link), cli.path)

        try fm.removeItem(at: link)
        try fm.createSymbolicLink(atPath: link.path, withDestinationPath: root.appending(path: "gone").path)
        XCTAssertEqual(try CommandLineTool.install(target: cli, directories: [dir]), link)
        XCTAssertEqual(try dest(link), cli.path)
    }

    func testAnotherSkillmIsLeftAlone() throws {
        let cli = try bundledCLI("skillm")
        let dir = root.appending(path: "bin")
        try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        let link = dir.appending(path: "skillm")

        // A plain binary (a release install).
        fm.createFile(atPath: link.path, contents: Data("binary".utf8))
        XCTAssertThrowsError(try CommandLineTool.install(target: cli, directories: [dir])) {
            XCTAssertEqual($0 as? CommandLineTool.Failure, .occupied(link.path))
        }

        // A link to a skillm that is not in an app (Homebrew's).
        try fm.removeItem(at: link)
        let cellar = root.appending(path: "Cellar/skillm/1.0/bin/skillm")
        try fm.createDirectory(at: cellar.deletingLastPathComponent(), withIntermediateDirectories: true)
        fm.createFile(atPath: cellar.path, contents: Data())
        try fm.createSymbolicLink(atPath: link.path, withDestinationPath: "../Cellar/skillm/1.0/bin/skillm")
        XCTAssertThrowsError(try CommandLineTool.install(target: cli, directories: [dir])) {
            XCTAssertEqual($0 as? CommandLineTool.Failure, .occupied(link.path))
        }
        XCTAssertEqual(try dest(link), "../Cellar/skillm/1.0/bin/skillm")
    }
}
