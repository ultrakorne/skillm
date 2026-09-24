import Foundation

/// Runs `skillm … --json` and decodes its answer. It is the app's only way
/// to reach skillm: views go through `AppModel`, which goes through this.
///
/// - `run` returns a command's data, or throws its protocol error.
/// - `stream` runs with `--events` and delivers each event, then the result.
///
/// Cancelling the calling task sends SIGINT; skillm then stops and answers
/// "cancelled". Either way a call returns (or a stream finishes) only once
/// skillm has exited, so it no longer holds Home's lock and a follow-up
/// `status` reads what the command left. SIGTERM follows only if skillm is
/// still running after `interruptGrace`, a guard against a hung child.
public struct SkillmClient: Sendable {
    /// The API versions this app understands (`skillm version --json`).
    public static let supportedAPIVersions: Set<Int> = [1]

    /// The skillm binary.
    public let executable: URL
    /// The child's environment: the app's, with PATH extended.
    public let environment: [String: String]
    /// A Home override (`--home`); nil means skillm's default (~/.skillm).
    public let home: String?
    /// How long to wait after SIGINT before sending SIGTERM. skillm does not
    /// catch SIGTERM, so it dies on the spot and may leave a write half done
    /// (copies the Registry does not record): keep this long enough for any
    /// cancelled command to finish its current write.
    public var interruptGrace: Duration = .seconds(60)

    public init(
        executable: URL,
        environment: [String: String] = ProcessInfo.processInfo.environment,
        home: String? = nil
    ) {
        self.executable = executable
        self.environment = ChildEnvironment.make(from: environment)
        self.home = home
    }

    /// A client for the CLI `SkillmBinary.locate` finds.
    public static func located(
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) async throws -> SkillmClient {
        SkillmClient(executable: try await SkillmBinary.locate(environment: environment), environment: environment)
    }

    // MARK: - Setup checks

    /// Runs `skillm version --json` and refuses a CLI whose API version this
    /// app does not know. Run it first: it works without git.
    ///
    /// A skillm from before the JSON API has no `version` command and no
    /// `--json` flag: it answers with a usage error on stderr and nothing on
    /// stdout, and is refused as API version 0 (older than any this app
    /// supports).
    @discardableResult
    public func connect() async throws -> VersionData {
        let v: VersionData
        do {
            v = try await run(["version"])
        } catch SkillmError.malformedOutput(let detail, let stderr, let status)
            where detail == "no output" && status != 0 && Self.isUsageError(stderr)
        {
            throw SkillmError.incompatibleCLI(version: "", apiVersion: 0, supported: Self.supportedAPIVersions.sorted())
        }
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
    /// the calling task throws `CancellationError` once skillm has exited.
    public func run<T: Codable & Sendable>(_ args: [String], as type: T.Type = T.self) async throws -> T {
        let env = try await runEnvelope(args, as: type)
        if env.error?.code == .cancelled { throw CancellationError() }
        return try Self.unwrap(env)
    }

    /// Runs `skillm <args> --json` and returns its envelope as written,
    /// failed or not (for callers that need the warnings of a success, or
    /// of a cancelled command: cancelling the calling task returns skillm's
    /// "cancelled" envelope). It throws only when there is no envelope to
    /// return: `CancellationError` when a cancelled skillm wrote none.
    public func runEnvelope<T: Codable & Sendable>(_ args: [String], as type: T.Type = T.self) async throws
        -> Envelope<T>
    {
        try Task.checkCancellation()
        let child = ChildProcess(executable: executable, arguments: arguments(args, events: false), environment: environment)
        try child.start()
        let grace = interruptGrace
        // Read in a task of its own: the caller's cancellation must not cut
        // the read short, only interrupt skillm, whose answer is then read
        // to the end.
        let collector = Task.detached { () -> (Data, Int32) in
            var output = Data()
            for await chunk in child.stdout { output.append(chunk) }
            return (output, await child.waitForExit())
        }
        let (output, status) = await withTaskCancellationHandler {
            await collector.value
        } onCancel: {
            child.interrupt(grace: grace)
        }
        do {
            return try Self.decodeDocument(output, as: T.self)
        } catch let error as SkillmError {
            if child.wasInterrupted { throw CancellationError() }
            throw Self.withProcessContext(error, stderr: child.stderr, status: status)
        }
    }

    /// Runs `skillm <args> --json --events` and delivers each event as it
    /// happens, then `.result` as the last message. A failed command ends
    /// the stream by throwing, as `run` does.
    ///
    /// Cancelling the consumer's task interrupts skillm with SIGINT, but the
    /// stream goes on delivering until skillm has exited and then throws
    /// `CancellationError`, so the loop ends only when skillm is done.
    /// Ending the iteration early (`break`) also interrupts skillm, without
    /// waiting for it.
    public func stream<T: Codable & Sendable>(_ args: [String], as type: T.Type = T.self) -> SkillmStream<T> {
        let child = ChildProcess(executable: executable, arguments: arguments(args, events: true), environment: environment)
        let grace = interruptGrace
        let channel = SkillmStream<T>.Channel(interrupt: { child.interrupt(grace: grace) })
        do {
            try child.start()
        } catch {
            channel.finish(throwing: error)
            return SkillmStream(channel: channel)
        }
        Task.detached {
            var splitter = LineSplitter()
            var result: Envelope<T>?
            var failure: (any Error)?
            func handle(_ line: Data) {
                guard failure == nil, result == nil else { return }
                do {
                    switch try StreamLine<T>.decode(line) {
                    case .event(let ev): channel.yield(.event(ev))
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
                channel.finish(throwing: CancellationError())
                return
            }
            if let failure {
                channel.finish(throwing: Self.withProcessContext(failure, stderr: child.stderr, status: status))
                return
            }
            guard let result else {
                channel.finish(
                    throwing: SkillmError.malformedOutput(
                        detail: "the event stream ended without a result", stderr: child.stderr, exitCode: status))
                return
            }
            do {
                let data = try Self.unwrap(result)
                channel.yield(.result(data, warnings: result.warnings))
                channel.finish()
            } catch {
                channel.finish(throwing: error)
            }
        }
        return SkillmStream(channel: channel)
    }

    // MARK: - Helpers

    /// The full argument list: `args` with `--json` (and `--events`) and
    /// `--home` added unless `args` already has them. They go before a `--`
    /// separator, after which skillm reads every argument as positional.
    func arguments(_ args: [String], events: Bool) -> [String] {
        let split = args.firstIndex(of: "--") ?? args.endIndex
        let flags = args[..<split]
        var added: [String] = []
        if !flags.contains("--json") { added.append("--json") }
        if events, !flags.contains("--events") { added.append("--events") }
        if let home, !flags.contains(where: { $0 == "--home" || $0.hasPrefix("--home=") }) {
            added += ["--home", home]
        }
        return Array(flags) + added + args[split...]
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

    /// Whether stderr is a usage error of a skillm without JSON mode
    /// ("Unknown command", "Unknown flag").
    static func isUsageError(_ stderr: String) -> Bool {
        let text = stderr.lowercased()
        return text.contains("unknown command") || text.contains("unknown flag")
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
