import Foundation

/// One informational line at the top of the menu.
public struct StatusLine: Equatable, Sendable, Identifiable {
    public enum Kind: Equatable, Sendable {
        case info
        /// Something to act on: an update.
        case update
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
    /// The menu's status lines for `status`.
    public static func lines(
        _ status: StatusData, now: Date = .now, calendar: Calendar = .current
    ) -> [StatusLine] {
        let cache = status.cache
        guard let checkedAt = cache.checkedAt else {
            return [StatusLine("Not checked for updates yet", .info)]
        }
        var lines: [StatusLine] = []
        let failed = cache.skills.filter { $0.status == .error || $0.status == .untracked }.count
        if cache.updates > 0 {
            lines.append(StatusLine(count(cache.updates, "skill update", "skill updates") + " available", .update))
        } else if failed == 0 {
            lines.append(StatusLine("All skills are up to date", .info))
        }
        if failed > 0 {
            lines.append(StatusLine(count(failed, "skill", "skills") + " could not be checked", .problem))
        }
        if let me = cache.selfStatus {
            if me.available, let latest = me.latest {
                lines.append(StatusLine("skillm \(latest) is available", .update))
            } else if me.error != nil {
                lines.append(StatusLine("Could not check for a newer skillm", .problem))
            }
        }
        lines.append(StatusLine("Last checked " + checkedText(checkedAt, now: now, calendar: calendar), .info))
        return lines
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

    /// "at 10:00" today, else "on 22 Sep at 10:00" (never relative, so an
    /// open menu cannot show an outdated "5 minutes ago").
    static func checkedText(_ date: Date, now: Date, calendar: Calendar) -> String {
        var time = Date.FormatStyle(date: .omitted, time: .shortened)
        time.timeZone = calendar.timeZone
        if calendar.isDate(date, inSameDayAs: now) {
            return "at " + date.formatted(time)
        }
        var day = Date.FormatStyle(date: .abbreviated, time: .omitted)
        day.timeZone = calendar.timeZone
        return "on " + date.formatted(day) + " at " + date.formatted(time)
    }

    static func count(_ n: Int, _ one: String, _ many: String) -> String {
        "\(n) \(n == 1 ? one : many)"
    }
}
