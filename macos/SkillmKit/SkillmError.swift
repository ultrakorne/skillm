import Foundation

/// Everything `SkillmClient` can fail with. A command that ran and reported
/// a failure is `.command`; the other cases mean the CLI could not be used.
public enum SkillmError: Error, Sendable, Equatable {
    /// No skillm binary was found at any of the searched paths.
    case binaryNotFound(searched: [String])
    /// The binary exists but could not be started.
    case launchFailed(path: String, reason: String)
    /// The CLI speaks an API version this app does not know.
    case incompatibleCLI(version: String, apiVersion: Int, supported: [Int])
    /// A document or event line carried an unknown schema_version.
    case unsupportedSchema(Int)
    /// The CLI wrote something that is not the protocol (it crashed, or it
    /// is not skillm). `stderr` is the tail of what it printed there.
    case malformedOutput(detail: String, stderr: String, exitCode: Int32?)
    /// No usable git: skillm needs the system git binary.
    case gitMissing(detail: String)
    /// The command ran and failed; `error.code` says why. `warnings` are the
    /// problems it reported before failing (a failed update lists its failed
    /// skills there).
    case command(ProtocolError, warnings: [Warning])

    /// The protocol error of a `.command` failure.
    public var protocolError: ProtocolError? {
        if case .command(let e, _) = self { return e }
        return nil
    }
}

extension SkillmError: LocalizedError {
    public var errorDescription: String? {
        switch self {
        case .binaryNotFound:
            return "The skillm command-line tool is missing from the app."
        case .launchFailed(let path, let reason):
            return "Could not start \(path): \(reason)"
        case .incompatibleCLI(let version, let api, _):
            return "The bundled skillm \(version) speaks API version \(api), which this app does not support."
        case .unsupportedSchema(let v):
            return "skillm wrote output in an unknown format (schema version \(v))."
        case .malformedOutput(let detail, _, _):
            return "skillm did not answer as expected: \(detail)"
        case .gitMissing(let detail):
            return "git is not available. \(detail)"
        case .command(let e, _):
            return e.message
        }
    }

    public var recoverySuggestion: String? {
        switch self {
        case .binaryNotFound(let searched):
            return "Reinstall skillm. Looked in: \(searched.joined(separator: ", "))"
        case .incompatibleCLI:
            return "Reinstall skillm so the app and its command-line tool match."
        case .gitMissing:
            return Self.gitFix
        case .command(let e, _) where e.code == .gitMissing:
            return Self.gitFix
        case .malformedOutput(_, let stderr, _) where !stderr.isEmpty:
            return stderr
        default:
            return nil
        }
    }

    /// How to get a working git on a Mac.
    public static let gitFix =
        "Install Apple's Command Line Tools by running `xcode-select --install` in Terminal "
        + "(or install git with Homebrew: `brew install git`), then relaunch skillm."
}
