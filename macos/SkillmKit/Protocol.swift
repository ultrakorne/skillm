import Foundation

// The JSON protocol's envelope and event lines, mirroring internal/protocol
// (protocol.go, writer.go). Keys are snake_case on the wire; the coders below
// convert them to camelCase property names, and times are RFC 3339 UTC.

/// The schema version every document and event line carries. It changes only
/// when the envelope or event-line shape changes incompatibly.
public let protocolSchemaVersion = 1

/// A JSON decoder configured for the protocol.
public func protocolDecoder() -> JSONDecoder {
    let d = JSONDecoder()
    d.keyDecodingStrategy = .convertFromSnakeCase
    d.dateDecodingStrategy = .custom { decoder in
        let c = try decoder.singleValueContainer()
        let text = try c.decode(String.self)
        guard let date = parseProtocolDate(text) else {
            throw DecodingError.dataCorruptedError(in: c, debugDescription: "not an RFC 3339 time: \(text)")
        }
        return date
    }
    return d
}

/// Parses an RFC 3339 time, with or without fractional seconds. skillm
/// writes whole seconds, but a hand-edited or future cache may not.
public func parseProtocolDate(_ text: String) -> Date? {
    if let d = try? Date(text, strategy: .iso8601) { return d }
    return try? Date(text, strategy: Date.ISO8601FormatStyle(includingFractionalSeconds: true))
}

/// A JSON encoder configured for the protocol (the inverse of
/// `protocolDecoder`; used by tests to prove the models are lossless).
public func protocolEncoder() -> JSONEncoder {
    let e = JSONEncoder()
    e.keyEncodingStrategy = .convertToSnakeCase
    e.dateEncodingStrategy = .iso8601
    e.outputFormatting = [.sortedKeys]
    return e
}

/// A string-valued protocol constant (a code, a status, a kind) that stays
/// open: an unknown value from a newer CLI decodes as itself instead of
/// failing, and `switch` falls to its `default`.
public protocol ProtocolValue: RawRepresentable, Codable, Hashable, Sendable,
    CustomStringConvertible, ExpressibleByStringLiteral where RawValue == String {
    init(rawValue: String)
}

extension ProtocolValue {
    public init(stringLiteral value: String) { self.init(rawValue: value) }
    public var description: String { rawValue }
}

/// A command's outcome: its `data` on success or its `error`, plus the
/// warnings it reported either way. A failed command still writes one.
public struct Envelope<Data: Codable & Sendable>: Codable, Sendable {
    public var schemaVersion: Int
    /// "result" on the last line of an `--events` stream; nil for a single
    /// document.
    public var type: String?
    public var data: Data?
    public var warnings: [Warning]
    public var error: ProtocolError?
}

extension Envelope: Equatable where Data: Equatable {}

/// A problem a command reported without failing (a warn or error log event).
public struct Warning: Codable, Sendable, Equatable {
    /// The event's stable machine code (e.g. "copy_failed", "update_failed").
    public var code: String
    public var message: String
    public var skillId: String?
}

/// A failed command's error. `code` is what a GUI switches on.
public struct ProtocolError: Codable, Sendable, Equatable, Error {
    public var code: ErrorCode
    public var message: String
    public var skillId: String?
    public var path: String?
    public var paths: [String]?
    /// The same command may succeed if run again unchanged.
    public var retryable: Bool
}

/// The stable error and warning codes (internal/protocol/errors.go).
public struct ErrorCode: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let error: ErrorCode = "error"
    public static let usage: ErrorCode = "usage"
    public static let jsonUnsupported: ErrorCode = "json_unsupported"
    public static let gitMissing: ErrorCode = "git_missing"
    public static let cancelled: ErrorCode = "cancelled"
    public static let timeout: ErrorCode = "timeout"
    public static let homeLocked: ErrorCode = "home_locked"
    public static let foreignFiles: ErrorCode = "foreign_files"
    public static let needsForce: ErrorCode = "needs_force"
    public static let needsConfirm: ErrorCode = "needs_confirm"
    public static let uninstallFailed: ErrorCode = "uninstall_failed"
    public static let sourceCollision: ErrorCode = "source_collision"
    public static let asMultiple: ErrorCode = "as_multiple"
    public static let localScopeAliased: ErrorCode = "local_scope_aliased"
    public static let commitMismatch: ErrorCode = "commit_mismatch"
    public static let notInstalled: ErrorCode = "not_installed"
    public static let updateFailed: ErrorCode = "update_failed"
    public static let updateSkipped: ErrorCode = "update_skipped"
    public static let noAgentEnabled: ErrorCode = "no_agent_enabled"
    public static let unknownAgent: ErrorCode = "unknown_agent"
    public static let managedByApp: ErrorCode = "managed_by_app"
    public static let sourceBuild: ErrorCode = "source_build"
    public static let noUpgrade: ErrorCode = "no_upgrade"
    public static let unknownKey: ErrorCode = "unknown_key"
    public static let invalidValue: ErrorCode = "invalid_value"
}

/// One core event on an `--events` stream.
public struct Event: Codable, Sendable, Equatable {
    public var schemaVersion: Int
    /// Always "event".
    public var type: String
    public var event: EventType
    public var level: EventLevel?
    /// The item's index in the batch (item_start, item_done).
    public var index: Int?
    public var skillId: String?
    /// The event's stable machine code, e.g. "update_available".
    public var code: String?
    /// The human sentence for the event.
    public var text: String?
    /// A batch's skill ids; `items[i]` is item i.
    public var items: [String]?
    /// A progress event's counts.
    public var done: Int?
    public var total: Int?
}

public struct EventType: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let log: EventType = "log"
    public static let batch: EventType = "batch"
    public static let itemStart: EventType = "item_start"
    public static let itemDone: EventType = "item_done"
    public static let progress: EventType = "progress"
}

public struct EventLevel: ProtocolValue {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let info: EventLevel = "info"
    public static let success: EventLevel = "success"
    public static let warn: EventLevel = "warn"
    public static let error: EventLevel = "error"
}

/// One line of an `--events` stream, as `SkillmClient.stream` delivers it.
public enum StreamMessage<Data: Codable & Sendable>: Sendable {
    /// A core event, as the work happens.
    case event(Event)
    /// The command's successful outcome; always the last message.
    case result(Data, warnings: [Warning])
}

/// Decodes one NDJSON line of an `--events` stream: an event, or the
/// result envelope that ends the stream.
enum StreamLine<Data: Codable & Sendable> {
    case event(Event)
    case result(Envelope<Data>)

    private struct Kind: Decodable {
        var schemaVersion: Int
        var type: String?
    }

    static func decode(_ line: Foundation.Data, decoder: JSONDecoder = protocolDecoder()) throws -> StreamLine {
        let kind = try decoder.decode(Kind.self, from: line)
        guard kind.schemaVersion == protocolSchemaVersion else {
            throw SkillmError.unsupportedSchema(kind.schemaVersion)
        }
        switch kind.type {
        case "event":
            return .event(try decoder.decode(Event.self, from: line))
        case "result":
            return .result(try decoder.decode(Envelope<Data>.self, from: line))
        default:
            throw SkillmError.malformedOutput(
                detail: "unknown stream line type \(kind.type ?? "(none)")", stderr: "", exitCode: nil)
        }
    }
}
