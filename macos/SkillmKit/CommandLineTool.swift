import Foundation

/// Installs and upgrades the skillm CLI the app drives, outside the JSON
/// protocol: the app has no CLI of its own, so when none is installed it runs
/// skillm's `install.sh` (bundled in the app as a resource), and when the one
/// installed is too old to speak the protocol it runs a plain `skillm
/// upgrade`.
public enum CommandLineTool {
    public struct Failure: Error, Equatable, LocalizedError {
        public var message: String
        public var errorDescription: String? { message }
    }

    /// Runs `script` with `/bin/sh` and waits for it: `install.sh` downloads
    /// the latest release into `/usr/local/bin`, or `~/.local/bin` when that
    /// is not writable. `environment` adds to the script's environment (tests
    /// set `SKILLM_BIN_DIR`). Throws the script's last error line when it
    /// fails.
    public static func runInstallScript(_ script: URL, environment: [String: String] = [:]) async throws {
        var env = ProcessInfo.processInfo.environment
        // A GUI app's PATH is minimal; the script needs curl, tar and mktemp.
        env["PATH"] = "/usr/bin:/bin:/usr/sbin:/sbin"
        // The latest release, never a version the app's environment names.
        env["SKILLM_VERSION"] = nil
        env.merge(environment) { _, new in new }
        try await run(URL(fileURLWithPath: "/bin/sh"), [script.path], environment: env, failure: "Install failed")
    }

    /// Runs `skillm upgrade` (no `--json`: a CLI too old for the app may not
    /// have it) and waits for it. Off a terminal it asks nothing. Throws its
    /// last error line when it fails.
    public static func runUpgrade(_ cli: URL, environment: [String: String] = ProcessInfo.processInfo.environment)
        async throws
    {
        let env = ChildEnvironment.make(from: environment)
        try await run(cli, ["upgrade"], environment: env, failure: "Upgrade failed")
    }

    /// Runs `executable` with stdin and stdout closed, and throws `Failure`
    /// ("<failure>: <last line of stderr>") when it exits non-zero.
    static func run(_ executable: URL, _ arguments: [String], environment: [String: String], failure: String)
        async throws
    {
        let process = Process()
        process.executableURL = executable
        process.arguments = arguments
        process.environment = environment
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
                let name = executable.lastPathComponent
                continuation.resume(
                    throwing: Failure(message: "Could not run \(name): \(error.localizedDescription)"))
            }
        }
        guard status == 0 else {
            let output = String(decoding: stderr.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            let last = output.split(separator: "\n").map { $0.trimmingCharacters(in: .whitespaces) }
                .last { !$0.isEmpty } ?? "exit status \(status)"
            throw Failure(message: "\(failure): " + last.replacingOccurrences(of: "error: ", with: ""))
        }
    }
}
