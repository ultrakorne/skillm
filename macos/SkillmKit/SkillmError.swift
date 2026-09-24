import Foundation

/// Everything `SkillmClient` can fail with. A command that ran and reported
/// a failure is `.command`; the other cases mean the CLI could not be used.
public enum SkillmError: Error, Sendable, Equatable {
    /// No skillm CLI is installed: none at any of the searched places.
    case binaryNotFound(searched: [String])
    /// The binary exists but could not be started.
    case launchFailed(path: String, reason: String)
    /// The CLI speaks an API version this app does not know: older than
    /// `supported` (0 for a skillm from before the JSON API, whose `version`
    /// is then unknown, "") or newer (`Int.max`, version "", for one whose
    /// documents carry a newer schema_version).
    case incompatibleCLI(version: String, apiVersion: Int, supported: [Int])

    /// For `.incompatibleCLI`: the CLI is older than this app supports.
    public var isCLITooOld: Bool {
        if case .incompatibleCLI(_, let api, let supported) = self, let min = supported.min() { return api < min }
        return false
    }
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
            return "skillm CLI is not installed"
        case .launchFailed(let path, let reason):
            return "Could not start \(path): \(reason)"
        case .incompatibleCLI(let version, _, _) where isCLITooOld:
            return version.isEmpty ? "skillm CLI is too old" : "skillm CLI \(version) is too old"
        case .incompatibleCLI(let version, _, _):
            return version.isEmpty
                ? "This app is too old for this skillm CLI" : "This app is too old for skillm \(version)"
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
            return "Install it with Install skillm CLI. Looked in: \(searched.joined(separator: ", "))"
        case .incompatibleCLI where isCLITooOld:
            return "Upgrade it with Upgrade skillm CLI."
        case .incompatibleCLI:
            return "Check for an app update."
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
