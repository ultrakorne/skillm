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

    /// What a finished `update` did, for the menu's notice.
    public static func updateResult(_ data: UpdateData) -> (text: String, isError: Bool) {
        let failed = data.skills.filter { $0.outcome == .failed }.count
        let partial = data.skills.contains { !$0.warnings.isEmpty }
        if data.updated == 0, failed == 0, !partial {
            return ("All skills are up to date", false)
        }
        var parts: [String] = []
        if data.updated > 0 { parts.append("Updated " + count(data.updated, "skill", "skills")) }
        if failed > 0 { parts.append(count(failed, "skill", "skills") + " failed") }
        if partial { parts.append("some installs were not updated") }
        var text = parts.joined(separator: ", ")
        text = text.prefix(1).uppercased() + text.dropFirst()
        return (text, failed > 0)
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
