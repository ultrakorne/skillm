import Foundation

/// "Install command-line tool": runs skillm's own `install.sh` (bundled in
/// the app), which downloads the release binary into `/usr/local/bin`, or
/// `~/.local/bin` when that is not writable. The terminal's skillm is then a
/// standalone install that upgrades itself (`skillm upgrade`); the app keeps
/// using its bundled CLI.
public enum CommandLineTool {
    public struct Failure: Error, Equatable, LocalizedError {
        public var message: String
        public var errorDescription: String? { message }
    }

    /// Where a `skillm` already on this Mac is looked for, in order: where
    /// `install.sh` puts it, then Homebrew's folder.
    public static func defaultDirectories(home: String = NSHomeDirectory()) -> [URL] {
        [
            URL(fileURLWithPath: "/usr/local/bin"),
            URL(fileURLWithPath: home).appending(path: ".local/bin"),
            URL(fileURLWithPath: "/opt/homebrew/bin"),
        ]
    }

    /// The first executable `skillm` in `directories` (a symlink counts when
    /// it resolves to one), whoever installed it.
    public static func installed(in directories: [URL]) -> URL? {
        directories.map { $0.appending(path: "skillm") }.first {
            FileManager.default.isExecutableFile(atPath: $0.resolvingSymlinksInPath().path)
        }
    }

    /// The release tag `install.sh` should fetch so the terminal's skillm
    /// matches the app's: "0.5.0" or "v0.5.0" → "v0.5.0". A development
    /// build ("dev", "0.5.0-3-gabc") has no release of its own, so nil
    /// (the script then takes the latest release).
    public static func releaseTag(forVersion version: String) -> String? {
        let bare = version.hasPrefix("v") ? String(version.dropFirst()) : version
        let parts = bare.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 3, parts.allSatisfy({ !$0.isEmpty && $0.allSatisfy(\.isNumber) }) else { return nil }
        return "v" + bare
    }

    /// Runs `script` with `/bin/sh` and waits for it. `version` pins the
    /// release (`SKILLM_VERSION`); `environment` adds to the script's
    /// environment (tests set `SKILLM_BIN_DIR`). Throws the script's last
    /// error line when it fails.
    public static func runInstallScript(
        _ script: URL, version: String?, environment: [String: String] = [:]
    ) async throws {
        var env = ProcessInfo.processInfo.environment
        // A GUI app's PATH is minimal; the script needs curl, tar and mktemp.
        env["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin"
        if let version { env["SKILLM_VERSION"] = version }
        env.merge(environment) { _, new in new }

        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = [script.path]
        process.environment = env
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = FileHandle.nullDevice
        let stderr = Pipe()
        process.standardError = stderr

        let status: Int32 = try await withCheckedThrowingContinuation { continuation in
            process.terminationHandler = { continuation.resume(returning: $0.terminationStatus) }
            do {
                try process.run()
            } catch {
                process.terminationHandler = nil
                continuation.resume(throwing: Failure(message: "Could not run the install script: \(error.localizedDescription)"))
            }
        }
        guard status == 0 else {
            let output = String(decoding: stderr.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            let last = output.split(separator: "\n").last.map(String.init) ?? "exit status \(status)"
            throw Failure(message: "Install failed: " + last.replacingOccurrences(of: "error: ", with: ""))
        }
    }
}
