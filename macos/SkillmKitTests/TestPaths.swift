import Foundation

/// Paths into the repository the tests run from.
enum TestPaths {
    /// The repository root (macos/SkillmKitTests/TestPaths.swift → ../..).
    static let repoRoot = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    /// The protocol's golden fixtures, shared with the Go tests.
    static let fixtures = repoRoot.appending(path: "internal/protocol/testdata")

    /// The fake skillm that answers from the fixtures.
    static let fakeSkillm = repoRoot.appending(path: "macos/TestSupport/fake-skillm")
}
