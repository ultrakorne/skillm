import AppKit
import Foundation
import Observation

/// The app's state. Views read it and call its methods; only the model
/// talks to `SkillmClient`.
///
/// One command that can change something runs at a time (`activity`). A
/// scheduled refresh that comes due while another command runs is skipped:
/// the next tick, or the next wake, runs it, and skillm decides whether a
/// check is due anyway. Commands that only read (`list`, `agent ls`,
/// `source inspect`) run beside it (`read`), so the windows can show what
/// is installed while a check runs.
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
        /// `update <id> --events`, from the Skills window.
        case updatingSkill(String)
        /// `install --events`, from the Add Skill window.
        case installing(UpdateProgress)
        /// `uninstall`, from the Skills window.
        case uninstalling(String)
        /// `config set` or `agent set`.
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
    public internal(set) var activity: Activity = .idle
    /// The running command was cancelled and skillm is finishing its
    /// current write (a cancelled call returns only once skillm has exited).
    public private(set) var isStopping = false
    public private(set) var notice: Notice?
    /// Goes up after every command that may have changed what is installed
    /// (install, uninstall, update, agent set), so an open Skills window
    /// lists again.
    public private(set) var installsVersion = 0

    /// Show the red dot: the cache's Badge (a skill update or a newer skillm).
    public var badge: Bool { status?.cache.badge ?? false }
    /// A command that can change something is running: the items that
    /// start one are greyed out.
    public var isBusy: Bool { activity != .idle }
    /// Any skillm is running, a read included: a quit waits for it.
    public var hasRunningCommands: Bool { operation != nil || !reads.isEmpty }
    /// The skillm the app runs; nil until the launch checks passed.
    public var cliExecutable: URL? { client?.executable }
    /// The launch checks passed: commands can run. Observable, unlike
    /// `cliExecutable`, so a window restored at launch loads once it is true.
    public var isReady: Bool {
        if case .ready = cli { return true }
        return false
    }

    @ObservationIgnored private var client: SkillmClient?
    @ObservationIgnored private var operation: Task<Void, Never>?
    /// The reads running beside `operation`.
    @ObservationIgnored private var reads: [UUID: RunningRead] = [:]
    @ObservationIgnored private var scheduler: RefreshScheduler?
    /// Goes up when a `config set` starts and when it ends: a `config get`
    /// read that overlapped one is dropped, since it may hold the old values.
    @ObservationIgnored private var settingsWrites = 0
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
            model.installsVersion += 1
        }
    }

    /// The "Auto refresh" toggle: `config set refresh.enabled`. Turning it
    /// on also runs a scheduled refresh, in case one is due already.
    @discardableResult
    public func setAutoRefresh(_ enabled: Bool) -> Task<Void, Never>? {
        saveSetting("refresh.enabled", enabled ? "true" : "false", checksAfter: enabled)
    }

    /// The refresh interval in Settings: `config set refresh.interval_hours`.
    /// A shorter interval applies at once, so a scheduled refresh follows.
    @discardableResult
    public func setRefreshInterval(hours: Int) -> Task<Void, Never>? {
        saveSetting("refresh.interval_hours", String(hours), checksAfter: true)
    }

    /// `config set <key> <value>`; with `checksAfter`, a scheduled refresh
    /// follows when auto refresh is on. A failure goes to `report`, or is
    /// the menu's notice.
    func saveSetting(
        _ key: String, _ value: String, checksAfter: Bool, report: (@MainActor (Notice) -> Void)? = nil
    ) -> Task<Void, Never>? {
        var saved = false
        let task = perform(
            .savingSettings, clearsNotice: false,
            { model, client in
                do {
                    let data: ConfigData = try await client.run(["config", "set", key, value])
                    model.settings = data.refresh
                    saved = true
                } catch {
                    if let report { report(Self.failure(error)) } else { model.notice = Self.failure(error) }
                }
                model.settingsWrites += 1
            },
            then: { model in
                if saved, checksAfter, model.settings?.enabled == true { model.scheduledRefresh() }
            })
        if task != nil { settingsWrites += 1 }
        return task
    }

    /// Reads the settings again (`config get`), for the Settings window.
    /// A read that overlapped a `config set` keeps what the write left.
    public func reloadSettings() async throws {
        let writes = settingsWrites
        let config: ConfigData = try await read(["config", "get"])
        guard writes == settingsWrites else { return }
        settings = config.refresh
    }

    /// Stops the running command. skillm finishes its current write first,
    /// so the command ends a little later (`isStopping` until then).
    public func cancel() {
        guard let operation, !isStopping else { return }
        isStopping = true
        operation.cancel()
    }

    /// Stops the schedule, interrupts the running commands and returns once
    /// every skillm has exited, so a quit never cuts a write short.
    public func shutdown() async {
        isShuttingDown = true
        scheduler?.stop()
        scheduler = nil
        let running = Array(reads.values)
        for r in running { r.cancel() }
        if let operation {
            isStopping = true
            operation.cancel()
            await operation.value
        }
        for r in running { await r.done.value }
    }

    /// Returns when no command is running (including one a finished command
    /// started after itself).
    public func waitUntilIdle() async {
        while let operation { await operation.value }
    }

    // MARK: - For the windows

    /// The launch checks have not passed (or failed): nothing can run.
    public struct NotReadyError: LocalizedError, Equatable {
        public var errorDescription: String? { "skillm is not ready yet." }
    }

    /// Runs a command that only reads (`list`, `agent ls`, `source
    /// inspect`, `config get`) beside the running one, and returns its data.
    /// Cancelling the calling task interrupts skillm; a quit interrupts it
    /// and waits for it too.
    func read<T: Codable & Sendable>(_ args: [String], as type: T.Type = T.self) async throws -> T {
        guard let client, !isShuttingDown else { throw NotReadyError() }
        let task = Task.detached { try await client.run(args, as: type) }
        let id = UUID()
        reads[id] = RunningRead(cancel: { task.cancel() }, done: Task { _ = await task.result })
        defer { reads[id] = nil }
        return try await withTaskCancellationHandler {
            try await task.value
        } onCancel: {
            task.cancel()
        }
    }

    /// Runs `work`, a command that changes what is installed, as the one
    /// running command (nil when one runs already), then reads the status
    /// again and bumps `installsVersion`. The window's own state reports
    /// the outcome; the menu's notice is left alone.
    func change(
        _ activity: Activity,
        _ work: @escaping @MainActor (SkillmClient) async -> Void,
        then: (@MainActor () -> Void)? = nil
    ) -> Task<Void, Never>? {
        perform(
            activity, clearsNotice: false,
            { model, client in
                await work(client)
                await model.rereadStatus()
                model.installsVersion += 1
            },
            then: { _ in then?() })
    }

    /// A read running beside the one command.
    private struct RunningRead {
        let cancel: @Sendable () -> Void
        let done: Task<Void, Never>
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

extension AppModel.Activity {
    /// The running command, in words; nil when there is nothing to say.
    public var text: String? {
        switch self {
        case .idle, .savingSettings: nil
        case .refreshing: "Checking for updates…"
        case .updating(let progress): progress.text
        case .updatingSkill(let id): "Updating \(id)…"
        case .installing(let progress): progress.text(doing: "Installing skills")
        case .uninstalling(let id): "Uninstalling \(id)…"
        }
    }

    /// Worth a Stop item: a check, an update or an install. A settings
    /// write is instant, and an uninstall of one skill quick.
    public var canStop: Bool {
        switch self {
        case .refreshing, .updating, .updatingSkill, .installing: true
        case .idle, .savingSettings, .uninstalling: false
        }
    }
}
