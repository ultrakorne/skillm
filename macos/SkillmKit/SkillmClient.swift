import Foundation

/// Runs `skillm … --json` and decodes its answer. It is the app's only way
/// to reach skillm: views go through `AppModel`, which goes through this.
///
/// - `run` returns a command's data, or throws its protocol error.
/// - `stream` runs with `--events` and delivers each event, then the result.
///
/// Cancelling the calling task sends SIGINT; skillm then stops and answers
/// "cancelled" (SIGTERM follows if it has not exited after `interruptGrace`).
public struct SkillmClient: Sendable {
    /// The API versions this app understands (`skillm version --json`).
    public static let supportedAPIVersions: Set<Int> = [1]

    /// The skillm binary.
    public let executable: URL
    /// The child's environment: the app's, with PATH extended.
    public let environment: [String: String]
    /// A Home override (`--home`); nil means skillm's default (~/.skillm).
    public let home: String?
    /// How long to wait after SIGINT before sending SIGTERM.
    public var interruptGrace: Duration = .seconds(5)

    public init(
        executable: URL,
        environment: [String: String] = ProcessInfo.processInfo.environment,
        home: String? = nil
    ) {
        self.executable = executable
        self.environment = ChildEnvironment.make(from: environment)
        self.home = home
    }

    /// A client for the binary `SkillmBinary.locate` finds.
    public static func located(
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) throws -> SkillmClient {
        SkillmClient(executable: try SkillmBinary.locate(environment: environment), environment: environment)
    }

    // MARK: - Setup checks

    /// Runs `skillm version --json` and refuses a CLI whose API version this
    /// app does not know. Run it first: it works without git.
    @discardableResult
    public func connect() async throws -> VersionData {
        let v: VersionData = try await run(["version"])
        guard Self.supportedAPIVersions.contains(v.apiVersion) else {
            throw SkillmError.incompatibleCLI(
                version: v.version, apiVersion: v.apiVersion, supported: Self.supportedAPIVersions.sorted())
        }
        return v
    }

    /// Throws `SkillmError.gitMissing` (with its fix) when the child would
    /// find no usable git.
    public func checkGit() async throws {
        try await GitCheck.check(
            path: environment["PATH"] ?? "", developerToolsInstalled: GitCheck.developerToolsInstalled)
    }

    // MARK: - Commands

    /// Runs `skillm <args> --json` and returns its data. A failed command
    /// throws `SkillmError.command` (or `.gitMissing`); a run cancelled by
    /// the calling task throws `CancellationError`.
    public func run<T: Codable & Sendable>(_ args: [String], as type: T.Type = T.self) async throws -> T {
        let env = try await runEnvelope(args, as: type)
        return try Self.unwrap(env)
    }

    /// Runs `skillm <args> --json` and returns its envelope as written,
    /// failed or not (for callers that need the warnings of a success). It
    /// throws only when there is no envelope to return.
    public func runEnvelope<T: Codable & Sendable>(_ args: [String], as type: T.Type = T.self) async throws
        -> Envelope<T>
    {
        try Task.checkCancellation()
        let child = ChildProcess(executable: executable, arguments: arguments(args, events: false), environment: environment)
        try child.start()
        let grace = interruptGrace
        return try await withTaskCancellationHandler {
            var output = Data()
            for await chunk in child.stdout { output.append(chunk) }
            let status = await child.waitForExit()
            let env: Envelope<T>
            do {
                env = try Self.decodeDocument(output, as: T.self)
            } catch let error as SkillmError {
                if child.wasInterrupted { throw CancellationError() }
                throw Self.withProcessContext(error, stderr: child.stderr, status: status)
            }
            if child.wasInterrupted, env.error?.code == .cancelled { throw CancellationError() }
            return env
        } onCancel: {
            child.interrupt(grace: grace)
        }
    }

    /// Runs `skillm <args> --json --events` and delivers each event as it
    /// happens, then `.result` as the last message. A failed command ends
    /// the stream by throwing, as `run` does. Ending the iteration early (or
    /// cancelling its task) interrupts skillm with SIGINT; a cancelled task's
    /// loop then simply ends without a `.result`, as any `AsyncSequence` does,
    /// so a caller checks `Task.isCancelled` rather than catching an error.
    public func stream<T: Codable & Sendable>(_ args: [String], as type: T.Type = T.self)
        -> AsyncThrowingStream<StreamMessage<T>, any Error>
    {
        let (stream, continuation) = AsyncThrowingStream.makeStream(of: StreamMessage<T>.self)
        let child = ChildProcess(executable: executable, arguments: arguments(args, events: true), environment: environment)
        let grace = interruptGrace
        continuation.onTermination = { reason in
            if case .cancelled = reason { child.interrupt(grace: grace) }
        }
        do {
            try child.start()
        } catch {
            continuation.finish(throwing: error)
            return stream
        }
        Task.detached {
            var splitter = LineSplitter()
            var result: Envelope<T>?
            var failure: (any Error)?
            func handle(_ line: Data) {
                guard failure == nil, result == nil else { return }
                do {
                    switch try StreamLine<T>.decode(line) {
                    case .event(let ev): continuation.yield(.event(ev))
                    case .result(let env): result = env
                    }
                } catch let e as SkillmError {
                    failure = e
                } catch {
                    failure = SkillmError.malformedOutput(
                        detail: "not an event line: \(String(decoding: line.prefix(200), as: UTF8.self))",
                        stderr: "", exitCode: nil)
                }
            }
            // Read to EOF even after a failure, so the child never blocks on
            // a full pipe.
            for await chunk in child.stdout {
                for line in splitter.append(chunk) { handle(line) }
            }
            if let last = splitter.finish() { handle(last) }
            let status = await child.waitForExit()

            if child.wasInterrupted, failure != nil || result == nil || result?.error?.code == .cancelled {
                continuation.finish(throwing: CancellationError())
                return
            }
            if let failure {
                continuation.finish(throwing: Self.withProcessContext(failure, stderr: child.stderr, status: status))
                return
            }
            guard let result else {
                continuation.finish(
                    throwing: SkillmError.malformedOutput(
                        detail: "the event stream ended without a result", stderr: child.stderr, exitCode: status))
                return
            }
            do {
                let data = try Self.unwrap(result)
                continuation.yield(.result(data, warnings: result.warnings))
                continuation.finish()
            } catch {
                continuation.finish(throwing: error)
            }
        }
        return stream
    }

    // MARK: - Helpers

    /// The full argument list: `args`, then `--json` (and `--events`) and
    /// `--home` unless `args` already has them.
    func arguments(_ args: [String], events: Bool) -> [String] {
        var out = args
        if !out.contains("--json") { out.append("--json") }
        if events, !out.contains("--events") { out.append("--events") }
        if let home, !out.contains(where: { $0 == "--home" || $0.hasPrefix("--home=") }) {
            out += ["--home", home]
        }
        return out
    }

    static func decodeDocument<T: Codable & Sendable>(_ data: Data, as: T.Type) throws -> Envelope<T> {
        let decoder = protocolDecoder()
        let trimmed = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw SkillmError.malformedOutput(detail: "no output", stderr: "", exitCode: nil)
        }
        let schema: SchemaProbe
        do {
            schema = try decoder.decode(SchemaProbe.self, from: data)
        } catch {
            throw SkillmError.malformedOutput(
                detail: "not a JSON document: \(String(trimmed.prefix(200)))", stderr: "", exitCode: nil)
        }
        guard schema.schemaVersion == protocolSchemaVersion else {
            throw SkillmError.unsupportedSchema(schema.schemaVersion)
        }
        do {
            return try decoder.decode(Envelope<T>.self, from: data)
        } catch {
            throw SkillmError.malformedOutput(
                detail: "unexpected \(T.self) document: \(error)", stderr: "", exitCode: nil)
        }
    }

    /// Reads only a document's schema_version.
    private struct SchemaProbe: Decodable { var schemaVersion: Int }

    /// The data of a successful envelope, or its error thrown.
    static func unwrap<T>(_ env: Envelope<T>) throws -> T {
        if let e = env.error {
            if e.code == .gitMissing { throw SkillmError.gitMissing(detail: e.message) }
            throw SkillmError.command(e, warnings: env.warnings)
        }
        guard let data = env.data else {
            throw SkillmError.malformedOutput(detail: "a result with neither data nor error", stderr: "", exitCode: nil)
        }
        return data
    }

    /// Adds the child's stderr and exit status to a malformed-output error.
    static func withProcessContext(_ error: any Error, stderr: String, status: Int32) -> any Error {
        if case .malformedOutput(let detail, _, _) = error as? SkillmError {
            return SkillmError.malformedOutput(detail: detail, stderr: stderr, exitCode: status)
        }
        return error
    }
}
