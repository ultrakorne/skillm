import Foundation

/// The one status line at the top of the menu.
public struct StatusLine: Equatable, Sendable, Identifiable {
    public enum Kind: Equatable, Sendable {
        case info
        /// A lookup that failed.
        case problem
    }

    public var text: String
    public var kind: Kind
    public var id: String { text }

    public init(_ text: String, _ kind: Kind) {
        self.text = text
        self.kind = kind
    }
}

/// The words the menu uses for the Refresh cache and for an update's
/// outcome. A skill whose check failed is a problem, never "up to date".
public enum StatusSummary {
    /// The menu's one status line for `status`, or nil when there is
    /// nothing to say beyond the menu's own items: an available skill
    /// update shows as the count on "Update all skills", and a newer
    /// skillm as "Upgrade and restart". A skill whose check failed is a
    /// problem, never "up to date".
    public static func line(_ status: StatusData) -> StatusLine? {
        let cache = status.cache
        guard cache.checkedAt != nil else {
            return StatusLine("Not checked for updates yet", .info)
        }
        let failed = cache.skills.filter { $0.status == .error || $0.status == .untracked }.count
        if failed > 0 {
            return StatusLine(count(failed, "skill", "skills") + " could not be checked", .problem)
        }
        if cache.updates > 0 { return nil }
        if cache.selfStatus?.error != nil {
            return StatusLine("Could not check for a newer skillm", .problem)
        }
        return StatusLine("All skills are up to date", .info)
    }

    /// What a finished `update` did, for the menu's notice. "Up to date"
    /// only when the run did nothing at all (as the CLI's "Everything is up
    /// to date."): a re-synced copy, a dropped install or an imported skill
    /// is work done. `warnings` are the result's envelope warnings; the
    /// ones the user should act on are named.
    public static func updateResult(_ data: UpdateData, warnings: [Warning] = []) -> (text: String, isError: Bool) {
        let failed = data.skills.filter { $0.outcome == .failed }.count
        let repaired = data.skills.filter { $0.outcome == .synced }.count
        // Every place an install vanished from (install_forgotten included).
        let removed = data.skills.reduce(0) { $0 + $1.pruned.count }
        let imported = data.imported.filter { $0.outcome == "imported" || $0.outcome == "adopted" }.count
        let partial = data.skills.contains { !$0.warnings.isEmpty }
        let unchecked = data.skills.filter { $0.outcome == .driftCheckSkipped }.count

        var parts: [String] = []
        if data.updated > 0 { parts.append("updated " + count(data.updated, "skill", "skills")) }
        if repaired > 0 { parts.append("repaired " + count(repaired, "skill", "skills")) }
        if removed > 0 { parts.append("removed " + count(removed, "missing install", "missing installs")) }
        if imported > 0 { parts.append("imported " + count(imported, "skill", "skills")) }
        if parts.isEmpty, data.synced { parts.append("repaired installed copies") }
        let didWork = !parts.isEmpty
        if failed > 0 { parts.append(count(failed, "skill", "skills") + " failed") }
        if partial { parts.append("some installs were not updated") }
        parts += actionableWarnings(warnings)
        if unchecked > 0 { parts.append(count(unchecked, "skill", "skills") + " not checked for drift") }

        if !didWork, failed == 0, !partial {
            parts.insert("All skills are up to date", at: 0)
        }
        var text = parts.joined(separator: ", ")
        text = text.prefix(1).uppercased() + text.dropFirst()
        return (text, failed > 0)
    }

    /// What `update <id>` did to that one skill, for the Skills window.
    public static func skillUpdateResult(
        _ id: String, _ data: UpdateData, warnings: [Warning] = []
    ) -> (text: String, isError: Bool) {
        guard let skill = data.skills.first(where: { $0.id == id }) else {
            return ("\(id) is up to date", false)
        }
        var parts: [String]
        var isError = false
        switch skill.outcome {
        case .updated: parts = ["Updated \(id)"]
        case .upToDate: parts = ["\(id) is up to date"]
        case .synced: parts = ["Repaired the copies of \(id)"]
        case .pruned: parts = ["Removed the missing installs of \(id)"]
        case .driftCheckSkipped: parts = ["\(id) was not checked for drift"]
        case .failed:
            parts = ["\(id) failed to update" + (skill.error.map { ": \($0)" } ?? "")]
            isError = true
        default: parts = ["\(id): \(skill.outcome.rawValue)"]
        }
        if skill.outcome != .pruned, !skill.pruned.isEmpty {
            parts.append("removed " + count(skill.pruned.count, "missing install", "missing installs"))
        }
        if !skill.warnings.isEmpty { parts.append("some installs were not updated") }
        parts += actionableWarnings(warnings)
        return (parts.joined(separator: ", "), isError)
    }

    /// The envelope warnings of an update that the menu names: a local
    /// skill whose source is gone (its copies were left as they were) and a
    /// status cache that could not be brought in line. The others repeat a
    /// row of `data` (a skill's `warnings`, its `pruned` places).
    static func actionableWarnings(_ warnings: [Warning]) -> [String] {
        var parts: [String] = []
        let missing = warnings.filter { $0.code == "source_missing" }.compactMap(\.skillId)
        if missing.count == 1 {
            parts.append("the source of \(missing[0]) is gone")
        } else if missing.count > 1 {
            parts.append("the sources of \(missing.count) local skills are gone")
        }
        if warnings.contains(where: { $0.code == "status_not_saved" }) {
            parts.append("the update status was not saved")
        }
        return parts
    }

    static func count(_ n: Int, _ one: String, _ many: String) -> String {
        "\(n) \(n == 1 ? one : many)"
    }
}
