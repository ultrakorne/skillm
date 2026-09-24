import Foundation

// Codable mirrors of every command's `data` (internal/protocol/data*.go and
// internal/status). The Swift tests decode, and re-encode, every golden
// fixture in internal/protocol/testdata/ with these types, so a field the Go
// side adds or renames fails the tests here too.

// MARK: - Shared values

public struct SkillKind: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let git: SkillKind = "git"
    public static let local: SkillKind = "local"
}

public struct Scope: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let global: Scope = "global"
    public static let local: Scope = "local"
}

/// A skill's upstream status (check, refresh, status).
public struct SkillStatus: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let upToDate: SkillStatus = "up_to_date"
    public static let updateAvailable: SkillStatus = "update_available"
    /// The skill's directory is gone upstream.
    public static let untracked: SkillStatus = "untracked"
    /// A local skill: nothing upstream to check.
    public static let local: SkillStatus = "local"
    /// The upstream could not be read; never "current".
    public static let error: SkillStatus = "error"
}

/// How the running skillm gets upgraded.
public struct UpgradeMethod: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let binary: UpgradeMethod = "binary"
    /// Inside the app bundle: the app upgrades it (Sparkle).
    public static let bundled: UpgradeMethod = "bundled"
    public static let dev: UpgradeMethod = "dev"
}

// MARK: - version

/// `skillm version --json`.
public struct VersionData: Codable, Sendable, Equatable {
    public var version: String
    public var apiVersion: Int
    /// The commands with a JSON mode ("check", "agent ls", …) and "events".
    public var capabilities: [String]
}

// MARK: - list, check

/// `skillm list --json`.
public struct ListData: Codable, Sendable, Equatable {
    public var skills: [ListedSkill]
}

public struct ListedSkill: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var kind: SkillKind
    public var source: String
    public var subpath: String?
    /// The Source as the CLI shows it.
    public var sourceLabel: String
    public var ref: String?
    public var revision: String?
    public var installedAt: Date?
    public var installs: [SkillInstall]
}

/// One place a skill is installed, read live from disk.
public struct SkillInstall: Codable, Sendable, Equatable {
    public var scope: Scope
    /// A Local install's project root.
    public var root: String?
    /// Where the canonical copy lives.
    public var path: String
    public var agents: [String]
    public var recorded: Bool
    public var exists: Bool
}

/// `skillm check --json`.
public struct CheckData: Codable, Sendable, Equatable {
    public var skills: [CheckedSkill]
    public var updates: Int
}

public struct CheckedSkill: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var kind: SkillKind
    public var status: SkillStatus
    public var installedRev: String?
    public var upstreamRev: String?
    public var error: String?
}

// MARK: - install, update, import, uninstall, upgrade

/// `skillm install --json`.
public struct InstallData: Codable, Sendable, Equatable {
    public var scope: Scope
    public var root: String?
    public var skills: [InstalledSkill]
}

public struct InstalledSkill: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var action: InstallAction
}

public struct InstallAction: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let installed: InstallAction = "installed"
    public static let converted: InstallAction = "converted"
    public static let refreshed: InstallAction = "refreshed"
    public static let overwritten: InstallAction = "overwritten"
    public static let skipped: InstallAction = "skipped"
}

/// `skillm update --json`. A failed update has no data: read the failed
/// skills from the envelope's warnings (`update_failed`/`update_skipped`).
public struct UpdateData: Codable, Sendable, Equatable {
    public var skills: [UpdatedSkill]
    public var imported: [ImportedSkill]
    public var updated: Int
    public var synced: Bool
}

public struct UpdatedSkill: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var kind: SkillKind
    public var outcome: UpdateOutcome
    public var revision: String?
    public var advanced: Bool
    public var pruned: [String]
    public var error: String?
    /// Non-empty means a partial success.
    public var warnings: [String]
}

public struct UpdateOutcome: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let updated: UpdateOutcome = "updated"
    public static let upToDate: UpdateOutcome = "up_to_date"
    public static let synced: UpdateOutcome = "synced"
    public static let pruned: UpdateOutcome = "pruned"
    public static let failed: UpdateOutcome = "failed"
    public static let driftCheckSkipped: UpdateOutcome = "drift_check_skipped"
}

/// `skillm import --json`.
public struct ImportData: Codable, Sendable, Equatable {
    public var root: String
    public var entries: Int
    public var skills: [ImportedSkill]
}

public struct ImportedSkill: Codable, Sendable, Equatable {
    public var id: String
    public var root: String
    /// "imported", "adopted" or "skipped".
    public var outcome: String
    public var error: String?
}

/// `skillm uninstall --json`.
public struct UninstallData: Codable, Sendable, Equatable {
    public var skills: [UninstalledSkill]
}

public struct UninstalledSkill: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var removedCopies: [String]
    public var warnings: [String]
}

/// `skillm upgrade --check --json`, and the `self` entry of the status cache
/// (which also carries `error` when the lookup failed).
public struct SelfStatus: Codable, Sendable, Equatable {
    public var current: String
    public var latest: String?
    public var available: Bool
    public var eligible: Bool
    public var method: UpgradeMethod
    public var executable: String?
    public var error: String?
}

/// `skillm upgrade --json`.
public struct UpgradeData: Codable, Sendable, Equatable {
    public var upgraded: Bool
    public var from: String
    public var to: String
    public var path: String?
}

// MARK: - source inspect, agent ls/set, config get/set

/// `skillm source inspect --json`.
public struct InspectData: Codable, Sendable, Equatable {
    public var source: String
    public var kind: SkillKind
    public var ref: String?
    /// Pass it to install as `--commit`.
    public var commit: String?
    public var skills: [InspectedSkill]
}

public struct InspectedSkill: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var name: String
    public var description: String
    public var path: String
}

/// `skillm agent ls --json`.
public struct AgentsData: Codable, Sendable, Equatable {
    public var agents: [Agent]
}

public struct Agent: Codable, Sendable, Equatable, Identifiable {
    public var name: String
    public var enabled: Bool
    public var global: String?
    public var local: String?
    public var id: String { name }
}

/// `skillm agent set --json`.
public struct AgentsSetData: Codable, Sendable, Equatable {
    public var enabled: [String]
    public var changes: [AgentChange]
}

public struct AgentChange: Codable, Sendable, Equatable {
    public var name: String
    public var enabled: Bool
    public var skills: [String]
    public var places: [String]
    public var warnings: [String]
}

/// `skillm config get/set --json`.
public struct ConfigData: Codable, Sendable, Equatable {
    public var refresh: RefreshSettings
}

public struct RefreshSettings: Codable, Sendable, Equatable {
    public var enabled: Bool
    public var intervalHours: Int
}

// MARK: - refresh, status

/// The refresh cache, `<home>/status.json`, as written on disk.
public struct StatusCache: Codable, Sendable, Equatable {
    public var schemaVersion: Int
    public var checkedAt: Date?
    public var nextDueAt: Date?
    public var skills: [StatusSkill]
    public var updates: Int
    /// The running skillm against the latest release (JSON key "self");
    /// nil when no refresh ever ran.
    public var selfStatus: SelfStatus?
    public var errors: [StatusProblem]
    /// Show the red dot: `updates > 0 || self.available`.
    public var badge: Bool

    private enum CodingKeys: String, CodingKey {
        case schemaVersion, checkedAt, nextDueAt, skills, updates, errors, badge
        case selfStatus = "self"
    }
}

public struct StatusSkill: Codable, Sendable, Equatable, Identifiable {
    public var id: String
    public var status: SkillStatus
    public var installedRev: String?
    public var upstreamRev: String?
    public var error: String?
}

public struct StatusProblem: Codable, Sendable, Equatable {
    /// "error", "untracked" or "self_check".
    public var code: String
    public var skillId: String?
    public var message: String
}

/// `skillm status --json`: the cache's fields at the top level, plus
/// `stale` (informational; never hide the badge for it).
public struct StatusData: Codable, Sendable, Equatable {
    public var cache: StatusCache
    public var stale: Bool

    private enum CodingKeys: String, CodingKey { case stale }

    public init(cache: StatusCache, stale: Bool) {
        self.cache = cache
        self.stale = stale
    }

    public init(from decoder: any Decoder) throws {
        cache = try StatusCache(from: decoder)
        stale = try decoder.container(keyedBy: CodingKeys.self).decode(Bool.self, forKey: .stale)
    }

    public func encode(to encoder: any Encoder) throws {
        try cache.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(stale, forKey: .stale)
    }
}

/// `skillm refresh --json`: the cache after the run. `refreshed` is false
/// for `--if-due` when nothing was due.
public struct RefreshData: Codable, Sendable, Equatable {
    public var status: StatusData
    public var refreshed: Bool

    private enum CodingKeys: String, CodingKey { case refreshed }

    public init(status: StatusData, refreshed: Bool) {
        self.status = status
        self.refreshed = refreshed
    }

    public init(from decoder: any Decoder) throws {
        status = try StatusData(from: decoder)
        refreshed = try decoder.container(keyedBy: CodingKeys.self).decode(Bool.self, forKey: .refreshed)
    }

    public func encode(to encoder: any Encoder) throws {
        try status.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(refreshed, forKey: .refreshed)
    }
}
