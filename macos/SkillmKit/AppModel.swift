import AppKit
import Foundation
import Observation

/// The app's state. Views read it and call its methods; only the model
/// talks to `SkillmClient`.
///
/// One command runs at a time (`activity`). A scheduled refresh that comes
/// due while another command runs is skipped: the next tick, or the next
/// wake, runs it, and skillm decides whether a check is due anyway.
@MainActor
@Observable
public final class AppModel {
    /// Whether the bundled CLI is usable.
    public enum CLIState: Equatable, Sendable {
        case starting
        case ready(version: String)
        /// The CLI cannot be used: `message` says why, `fix` how to repair it.
        case failed(message: String, fix: String?)
    }

    /// The command running now.
    public enum Activity: Equatable, Sendable {
        case idle
        /// `refresh`; `scheduled` for the timer's `refresh --if-due`.
        case refreshing(scheduled: Bool)
        /// `update --events`, with the progress its events reported so far.
        case updating(UpdateProgress)
        /// `config set`.
        case savingSettings
    }

    /// A one-line outcome the menu shows: what an update did, or why a
    /// command failed. A notice from a command the user started stays until
    /// the next one; one from the app's own reads and ticks goes away with
    /// the next of those that succeeds.
    public struct Notice: Equatable, Sendable {
        /// Who started the command the notice is about.
        public enum Origin: Equatable, Sendable {
            /// A menu command.
            case user
            /// The launch reads or a scheduled refresh.
            case background
        }

        public var text: String
        public var isError: Bool
        public var origin: Origin

        public init(text: String, isError: Bool, origin: Origin = .user) {
            self.text = text
            self.isError = isError
            self.origin = origin
        }
    }

    public private(set) var cli: CLIState = .starting
    /// The Refresh cache as `status`/`refresh` last reported it; nil until
    /// it was read once.
    public private(set) var status: StatusData?
    /// The refresh settings from config.toml; nil until read. Read again
    /// on every scheduled tick, so a `config set` in a terminal shows.
    public private(set) var settings: RefreshSettings?
    public private(set) var activity: Activity = .idle
    /// The running command was cancelled and skillm is finishing its
    /// current write (a cancelled call returns only once skillm has exited).
    public private(set) var isStopping = false
    public private(set) var notice: Notice?

    /// Show the red dot: the cache's Badge (a skill update or a newer skillm).
    public var badge: Bool { status?.cache.badge ?? false }
    /// A command is running.
    public var isBusy: Bool { activity != .idle }

    @ObservationIgnored private var client: SkillmClient?
    @ObservationIgnored private var operation: Task<Void, Never>?
    @ObservationIgnored private var scheduler: RefreshScheduler?
    @ObservationIgnored private var isShuttingDown = false
    @ObservationIgnored private let makeClient: @MainActor () throws -> SkillmClient
    @ObservationIgnored private let checksGit: Bool
    @ObservationIgnored private let timing: RefreshScheduler.Timing
    @ObservationIgnored private let wakeCenter: NotificationCenter

    /// - Parameters:
    ///   - makeClient: finds the CLI (the bundled one by default).
    ///   - checksGit: refuse to start without a usable git.
    ///   - timing: how often the scheduled refresh ticks.
    ///   - wakeCenter: where wake notifications come from (NSWorkspace's
    ///     center by default).
    public init(
        makeClient: @escaping @MainActor () throws -> SkillmClient = { try SkillmClient.located() },
        checksGit: Bool = true,
        timing: RefreshScheduler.Timing = .standard,
        wakeCenter: NotificationCenter? = nil
    ) {
        self.makeClient = makeClient
        self.checksGit = checksGit
        self.timing = timing
        self.wakeCenter = wakeCenter ?? NSWorkspace.shared.notificationCenter
    }

    // MARK: - Launch

    /// Finds the bundled skillm, refuses one with an unknown API version,
    /// checks for git, reads the cached status, and starts the scheduled
    /// refresh (whose first tick, which reads the settings, runs now).
    public func start() async {
        guard case .starting = cli, client == nil else { return }
        do {
            let client = try makeClient()
            let version = try await client.connect()
            if checksGit { try await client.checkGit() }
            self.client = client
            cli = .ready(version: version.version)
        } catch {
            let localized = error as? LocalizedError
            cli = .failed(
                message: localized?.errorDescription ?? error.localizedDescription,
                fix: localized?.recoverySuggestion)
            return
        }
        await readStatus()
        guard !isShuttingDown else { return }
        let scheduler = RefreshScheduler(timing: timing, notificationCenter: wakeCenter) { [weak self] in
            self?.scheduledRefresh()
        }
        self.scheduler = scheduler
        scheduler.start()
    }

    // MARK: - Commands

    /// The Refresh item: `refresh`, a check now whether or not one is due.
    /// Returns the command's task, or nil when another command is running.
    @discardableResult
    public func refresh() -> Task<Void, Never>? {
        perform(.refreshing(scheduled: false)) { model, client in
            do {
                let data: RefreshData = try await client.run(["refresh"])
                model.status = data.status
            } catch is CancellationError {
                model.notice = Notice(text: "Check stopped", isError: false)
            } catch {
                model.notice = Self.failure(error)
            }
        }
    }

    /// The scheduler's tick: `config get` (the settings may have changed in
    /// a terminal), then `refresh --if-due`, which checks only when skillm
    /// says a check is due and otherwise answers the cache as it is. A tick
    /// that succeeds clears the notice an earlier background failure left.
    @discardableResult
    public func scheduledRefresh() -> Task<Void, Never>? {
        perform(.refreshing(scheduled: true), clearsNotice: false) { model, client in
            var failure: Notice?
            do {
                let config: ConfigData = try await client.run(["config", "get"])
                model.settings = config.refresh
                let data: RefreshData = try await client.run(["refresh", "--if-due"])
                model.status = data.status
            } catch is CancellationError {
                // A quit, or the Stop item.
                if !model.isShuttingDown { failure = Notice(text: "Check stopped", isError: false) }
            } catch {
                failure = Self.failure(error, origin: .background)
            }
            if let failure {
                model.notice = failure
            } else {
                model.clearBackgroundNotice()
            }
        }
    }

    /// "Update all skills": `update --events`, reporting progress as the
    /// events arrive, then the status re-read (update keeps the cache in
    /// line, so the dot clears without another check).
    @discardableResult
    public func updateAll() -> Task<Void, Never>? {
        perform(.updating(UpdateProgress())) { model, client in
            do {
                for try await message in client.stream(["update"], as: UpdateData.self) {
                    switch message {
                    case .event(let event):
                        if case .updating(var progress) = model.activity {
                            progress.apply(event)
                            model.activity = .updating(progress)
                        }
                    case .result(let data, let warnings):
                        let summary = StatusSummary.updateResult(data, warnings: warnings)
                        model.notice = Notice(text: summary.text, isError: summary.isError)
                    }
                }
            } catch is CancellationError {
                model.notice = Notice(text: "Update stopped", isError: false)
            } catch {
                model.notice = Self.failure(error)
            }
            // A failed re-read keeps the update's outcome on screen.
            await model.rereadStatus()
        }
    }

    /// The "Auto refresh" toggle: `config set refresh.enabled`. Turning it
    /// on also runs a scheduled refresh, in case one is due already.
    @discardableResult
    public func setAutoRefresh(_ enabled: Bool) -> Task<Void, Never>? {
        perform(
            .savingSettings, clearsNotice: false,
            { model, client in
                do {
                    let data: ConfigData = try await client.run([
                        "config", "set", "refresh.enabled", enabled ? "true" : "false",
                    ])
                    model.settings = data.refresh
                } catch {
                    model.notice = Self.failure(error)
                }
            },
            then: { model in
                if enabled, model.settings?.enabled == true { model.scheduledRefresh() }
            })
    }

    /// Stops the running command. skillm finishes its current write first,
    /// so the command ends a little later (`isStopping` until then).
    public func cancel() {
        guard let operation, !isStopping else { return }
        isStopping = true
        operation.cancel()
    }

    /// Stops the schedule, interrupts the running command and returns once
    /// skillm has exited, so a quit never cuts a write short.
    public func shutdown() async {
        isShuttingDown = true
        scheduler?.stop()
        scheduler = nil
        if let operation {
            isStopping = true
            operation.cancel()
            await operation.value
        }
    }

    /// Returns when no command is running (including one a finished command
    /// started after itself).
    public func waitUntilIdle() async {
        while let operation { await operation.value }
    }

    // MARK: - Helpers

    /// Runs `work` as the one running command, unless one runs already.
    private func perform(
        _ activity: Activity,
        clearsNotice: Bool = true,
        _ work: @escaping @MainActor (AppModel, SkillmClient) async -> Void,
        then: (@MainActor (AppModel) -> Void)? = nil
    ) -> Task<Void, Never>? {
        guard let client, operation == nil, !isShuttingDown else { return nil }
        if clearsNotice { notice = nil }
        self.activity = activity
        let task = Task { @MainActor in
            await work(self, client)
            self.activity = .idle
            self.isStopping = false
            self.operation = nil
            if !self.isShuttingDown { then?(self) }
        }
        operation = task
        return task
    }

    /// `status`: the cache as skillm reads it, offline. Its failure is a
    /// background notice, shown unless `keepsNotice` and a notice is up.
    private func readStatus(keepsNotice: Bool = false) async {
        guard let client else { return }
        do {
            status = try await client.run(["status"])
            clearBackgroundNotice()
        } catch {
            if !keepsNotice || notice == nil { notice = Self.failure(error, origin: .background) }
        }
    }

    /// `readStatus` in a task of its own, so it also runs after the calling
    /// command was cancelled (skillm has exited by then).
    private func rereadStatus() async {
        guard !isShuttingDown else { return }
        await Task { @MainActor in await self.readStatus(keepsNotice: true) }.value
    }

    private func clearBackgroundNotice() {
        if notice?.origin == .background { notice = nil }
    }

    /// The notice for a failed command.
    static func failure(_ error: any Error, origin: Notice.Origin = .user) -> Notice {
        if case .command(let e, let warnings) = error as? SkillmError, e.code == .updateFailed {
            let ids = warnings.filter { $0.code == ErrorCode.updateFailed.rawValue || $0.code == ErrorCode.updateSkipped.rawValue }
                .compactMap(\.skillId)
            if !ids.isEmpty {
                return Notice(text: "\(e.message): \(ids.joined(separator: ", "))", isError: true, origin: origin)
            }
        }
        let localized = error as? LocalizedError
        return Notice(text: localized?.errorDescription ?? error.localizedDescription, isError: true, origin: origin)
    }
}
