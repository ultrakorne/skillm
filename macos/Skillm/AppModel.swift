import Foundation
import Observation
import SkillmKit

/// The app's state. Views read it and call its methods; only the model
/// talks to `SkillmClient`.
@MainActor
@Observable
final class AppModel {
    /// Whether the bundled CLI is usable.
    enum CLIState: Equatable {
        case starting
        case ready(version: String)
        /// The CLI cannot be used: `message` says why, `fix` how to repair it.
        case failed(message: String, fix: String?)
    }

    private(set) var cli: CLIState = .starting
    private(set) var client: SkillmClient?

    /// Finds the bundled skillm, refuses one with an unknown API version,
    /// and checks for git.
    func start() async {
        do {
            let client = try SkillmClient.located()
            let version = try await client.connect()
            try await client.checkGit()
            self.client = client
            cli = .ready(version: version.version)
        } catch {
            let localized = error as? LocalizedError
            cli = .failed(
                message: localized?.errorDescription ?? error.localizedDescription,
                fix: localized?.recoverySuggestion)
        }
    }
}
