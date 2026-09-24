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
    /// Whether the installed CLI is usable. The app has no CLI of its own:
    /// it drives the one the user installed (`SkillmBinary`).
    public enum CLIState: Equatable, Sendable {
        /// The launch checks are running.
        case starting
        case ready(version: String)
        /// No skillm is installed: "Install skillm CLI".
        case missing
        /// The CLI speaks an older API than this app supports: "Upgrade
        /// skillm CLI". `version` is "" for a skillm from before the JSON API.
        case tooOld(version: String)
        /// The CLI speaks a newer API than this app supports: "Check for app
        /// update".
        case tooNew(version: String)
        /// The CLI cannot be used otherwise: `message` says why, `fix` how
        /// to repair it.
        case failed(message: String, fix: String?)
    }

    /// Work on the CLI itself that runs outside the protocol, while the CLI
    /// cannot be used.
    public enum CLIWork: Equatable, Sendable {
        /// `install.sh`, when no CLI is installed.
        case installing
        /// A plain `skillm upgrade`, when the CLI is too old.
        case upgrading

        public var text: String {
            switch self {
            case .installing: "Installing skillm CLI…"
            case .upgrading: "Upgrading skillm CLI…"
            }
        }
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
        /// `upgrade`: "Upgrade skillm CLI to X".
        case upgradingCLI
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
    /// The CLI the launch checks found (usable or not); nil when none is
    /// installed.
    public private(set) var cliPath: URL?
    /// The version the found CLI reported; nil when unknown.
    public private(set) var cliVersion: String?
    /// `install.sh` or a plain `skillm upgrade` is running.
    public private(set) var cliWork: CLIWork?
    /// Why the last `cliWork` failed; cleared when the next starts and once
    /// the CLI is usable.
    public private(set) var cliProblem: String?
    /// The Refresh cache as `status`/`refresh` last reported it; nil until
    /// it was read once.
    public private(set) var status: StatusData?
    /// "Upgrade app and restart": the app's updater and what it found.
    public let upgrade = AppUpgrade()
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

    /// Show the red dot: the cache's Badge (a skill update or a newer CLI),
    /// or a newer app the updater found.
    public var badge: Bool { (status?.cache.badge ?? false) || upgrade.isAvailable }
    /// The release "Upgrade skillm CLI to X" offers: the Refresh cache says
    /// a newer CLI exists that `skillm upgrade` can install; nil otherwise.
    public var cliUpgrade: String? {
        guard isReady, let me = status?.cache.selfStatus, me.eligible else { return nil }
        return me.latest
    }
    /// A command that can change something is running: the items that
    /// start one are greyed out.
    public var isBusy: Bool { activity != .idle }
    /// Any skillm is running, a read, the launch checks or work on the CLI
    /// included: a quit waits for it.
    public var hasRunningCommands: Bool { operation != nil || !reads.isEmpty || cliTask != nil }
    /// The launch checks passed: commands can run. Observable, so a window
    /// restored at launch loads once it is true.
    public var isReady: Bool {
        if case .ready = cli { return true }
        return false
    }

    @ObservationIgnored private var client: SkillmClient?
    /// The environment the found CLI runs with, usable or not (a plain
    /// upgrade of a CLI too old for the protocol runs with it).
    @ObservationIgnored private var cliEnvironment: [String: String] = [:]
    @ObservationIgnored private var operation: Task<Void, Never>?
    /// The launch checks run again (`checkCLIAgain`), or work on the CLI
    /// (`cliWork`) followed by them.
    @ObservationIgnored private var cliTask: Task<Void, Never>?
    /// The CLI file the launch checks found, as it was then: when it changes
    /// (an upgrade, a reinstall or a removal in a terminal), the launch
    /// checks run again. Nil when none was found.
    @ObservationIgnored private var cliFile: CLIFileIdentity?
    /// The reads running beside `operation`.
    @ObservationIgnored private var reads: [UUID: RunningRead] = [:]
    @ObservationIgnored private var scheduler: RefreshScheduler?
    /// Goes up when a `config set` starts and when it ends: a `config get`
    /// read that overlapped one is dropped, since it may hold the old values.
    @ObservationIgnored private var settingsWrites = 0
    @ObservationIgnored private var isShuttingDown = false
    @ObservationIgnored private var hasStarted = false
    @ObservationIgnored private let makeClient: @MainActor () async throws -> SkillmClient
    @ObservationIgnored private let installScript: URL?
    @ObservationIgnored private let installEnvironment: [String: String]
    @ObservationIgnored private let checksGit: Bool
    @ObservationIgnored private let timing: RefreshScheduler.Timing
    @ObservationIgnored private let wakeCenter: NotificationCenter

    /// - Parameters:
    ///   - makeClient: finds the CLI (`SkillmBinary.locate` by default); run
    ///     at every launch check.
    ///   - checksGit: refuse to start without a usable git.
    ///   - timing: how often the scheduled refresh ticks.
    ///   - wakeCenter: where wake notifications come from (NSWorkspace's
    ///     center by default).
    ///   - installScript: what "Install skillm CLI" runs (the `install.sh`
    ///     bundled in the app by default); `installEnvironment` is added to
    ///     its environment.
    public init(
        makeClient: @escaping @MainActor () async throws -> SkillmClient = { try await SkillmClient.located() },
        checksGit: Bool = true,
        timing: RefreshScheduler.Timing = .standard,
        wakeCenter: NotificationCenter? = nil,
        installScript: URL? = Bundle.main.url(forResource: "install", withExtension: "sh"),
        installEnvironment: [String: String] = [:]
    ) {
        self.makeClient = makeClient
        self.installScript = installScript
        self.installEnvironment = installEnvironment
        self.checksGit = checksGit
        self.timing = timing
        self.wakeCenter = wakeCenter ?? NSWorkspace.shared.notificationCenter
    }

    // MARK: - Launch

    /// Runs the launch checks (finds the installed skillm, refuses one with
    /// an unknown API version, checks for git), asks the app's updater,
    /// reads the cached status, and starts the schedule. Its first tick,
    /// which reads the settings, runs now when the CLI is usable; while it is
    /// not, each tick runs the launch checks again, so a CLI installed or
    /// upgraded in a terminal is found. While it is usable, each tick first
    /// looks at the CLI's file, and runs the launch checks again when it
    /// changed.
    public func start() async {
        guard !hasStarted else { return }
        hasStarted = true
        await connectCLI()
        guard !isShuttingDown else { return }
        upgrade.tick(every: appCheckInterval)
        if isReady { await readStatus() }
        guard !isShuttingDown else { return }
        startSchedule(tickNow: isReady)
    }

    /// Starts the schedule; with `tickNow` its first tick runs now.
    private func startSchedule(tickNow: Bool = true) {
        guard scheduler == nil else { return }
        let scheduler = RefreshScheduler(timing: timing, notificationCenter: wakeCenter) { [weak self] in
            self?.tick()
        }
        self.scheduler = scheduler
        scheduler.start(tickNow: tickNow)
    }

    /// A scheduled tick: the app's updater when a check is due, then a
    /// refresh when the CLI is usable and unchanged, else the launch checks
    /// again.
    private func tick() {
        upgrade.tick(every: appCheckInterval)
        if isReady, !cliChanged { scheduledRefresh() } else { checkCLIAgain() }
    }

    /// How often the app's updater is asked: the refresh interval, and not
    /// at all while auto check is off (the Refresh item still asks). While
    /// the CLI cannot be used, its Auto check is out of reach, so the
    /// updater is asked on the last interval known, or daily.
    private var appCheckInterval: TimeInterval? {
        guard let settings else { return AppUpgrade.defaultInterval }
        if isReady, !settings.enabled { return nil }
        return TimeInterval(max(settings.intervalHours, 1)) * 3600
    }

    /// The CLI's file is no longer the one the launch checks found: it was
    /// upgraded, replaced or removed since.
    var cliChanged: Bool {
        guard let cliPath else { return false }
        return CLIFileIdentity(cliPath) != cliFile
    }

    /// The launch checks. Only run while no command runs: the client goes
    /// away until they pass.
    private func connectCLI() async {
        client = nil
        cli = .starting
        do {
            let client = try await makeClient()
            cliPath = client.executable
            cliFile = CLIFileIdentity(client.executable)
            cliEnvironment = client.environment
            cliVersion = nil
            let version = try await client.connect()
            cliVersion = version.version
            if checksGit { try await client.checkGit() }
            self.client = client
            cli = .ready(version: version.version)
            cliProblem = nil
        } catch {
            cliUnusable(error)
        }
    }

    /// Records why the CLI cannot be used.
    private func cliUnusable(_ error: any Error) {
        client = nil
        let skillmError = error as? SkillmError
        switch skillmError {
        case .binaryNotFound:
            cliPath = nil
            cliFile = nil
            cliVersion = nil
            cli = .missing
        case .incompatibleCLI(let version, _, _):
            cliVersion = version.isEmpty ? nil : version
            cli = skillmError?.isCLITooOld == true ? .tooOld(version: version) : .tooNew(version: version)
        default:
            let localized = error as? LocalizedError
            cli = .failed(
                message: localized?.errorDescription ?? error.localizedDescription,
                fix: localized?.recoverySuggestion)
        }
    }

    /// The launch checks again, then, once they pass, the status read and a
    /// scheduled refresh (as at launch).
    private func relaunchChecks() async {
        guard !isShuttingDown else { return }
        await connectCLI()
        guard isReady, !isShuttingDown else { return }
        await readStatus()
        guard !isShuttingDown else { return }
        scheduledRefresh()
    }

    /// "Check Again", Settings, every scheduled tick while the CLI cannot be
    /// used, and the end of every command: the launch checks again. Nil when
    /// the CLI is usable and its file unchanged, or while a check, a command
    /// or work on the CLI runs.
    @discardableResult
    public func checkCLIAgain() -> Task<Void, Never>? {
        guard hasStarted, !isReady || cliChanged, cli != .starting, cliWork == nil, !hasRunningCommands,
            !isShuttingDown
        else { return nil }
        return runCLITask { await $0.relaunchChecks() }
    }

    /// Runs `work` as `cliTask`.
    private func runCLITask(_ work: @escaping @MainActor (AppModel) async -> Void) -> Task<Void, Never> {
        let task = Task { @MainActor in
            await work(self)
            self.cliTask = nil
        }
        cliTask = task
        return task
    }

    // MARK: - The CLI itself

    /// "Install skillm CLI", when none is installed: runs `install.sh` (the
    /// latest release), then the launch checks again. Nil when the CLI is
    /// found, or while other work runs.
    @discardableResult
    public func installCLI() -> Task<Void, Never>? {
        guard cli == .missing else { return nil }
        guard let script = installScript else {
            cliProblem = "The install script is missing from the app"
            return nil
        }
        let env = installEnvironment
        return fixCLI(.installing) { try await CommandLineTool.runInstallScript(script, environment: env) }
    }

    /// "Upgrade skillm CLI". When the CLI is usable and the Refresh cache
    /// says a newer release exists (`cliUpgrade`), `upgrade --json` as the
    /// running command, then the version and the status read again. When
    /// the CLI is too old for the protocol, a plain `skillm upgrade`, then
    /// the launch checks again. Nil otherwise, or while other work runs.
    @discardableResult
    public func upgradeCLI() -> Task<Void, Never>? {
        if isReady {
            guard cliUpgrade != nil else { return nil }
            return perform(.upgradingCLI) { model, client in
                do {
                    let data: UpgradeData = try await client.run(["upgrade"])
                    let text =
                        data.upgraded
                        ? "Upgraded skillm CLI to \(data.to)" : "skillm CLI \(data.from) is the latest release"
                    model.notice = Notice(text: text, isError: false)
                } catch is CancellationError {
                } catch {
                    model.notice = Self.failure(error)
                }
                await model.reconnect(client)
                await model.rereadStatus()
            }
        }
        guard case .tooOld = cli, let path = cliPath else { return nil }
        let env = cliEnvironment
        return fixCLI(.upgrading) { try await CommandLineTool.runUpgrade(path, environment: env) }
    }

    /// Runs `work` on the CLI (nil while other work runs), then the launch
    /// checks again, whether it failed or not. Work that succeeded but left
    /// a CLI still too old (the latest release is older than this app
    /// supports) says so.
    private func fixCLI(_ kind: CLIWork, _ work: @escaping @MainActor () async throws -> Void) -> Task<Void, Never>? {
        guard cliWork == nil, cli != .starting, !hasRunningCommands, !isShuttingDown else { return nil }
        cliWork = kind
        cliProblem = nil
        return runCLITask { model in
            var succeeded = false
            do {
                try await work()
                succeeded = true
            } catch {
                model.cliProblem = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            }
            model.cliWork = nil
            await model.relaunchChecks()
            if succeeded, case .tooOld = model.cli {
                model.cliProblem = Self.latestTooOld
            }
        }
    }

    /// Install or upgrade worked, and the CLI is still too old.
    static let latestTooOld = "The latest skillm release is still too old for this app"

    /// The CLI changed under a usable client (an upgrade): reads its version
    /// again, and stops using it when this app no longer supports it.
    private func reconnect(_ client: SkillmClient) async {
        do {
            let version = try await client.connect()
            cliVersion = version.version
            cliFile = CLIFileIdentity(client.executable)
            cli = .ready(version: version.version)
        } catch is CancellationError {
        } catch {
            cliUnusable(error)
        }
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
                return
            } catch {
                model.notice = Self.failure(error)
            }
            // The Refresh item checks the app too, whether or not the CLI's
            // check worked.
            model.upgrade.checkNow()
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
    /// every skillm has exited, so a quit never cuts a write short. Work on
    /// the CLI (`install.sh`, a plain `skillm upgrade`) cannot be
    /// interrupted: it is waited for, up to `cliWorkLimit`.
    public func shutdown() async {
        isShuttingDown = true
        scheduler?.stop()
        scheduler = nil
        if let cliTask {
            // A task group would wait for both: whichever ends first.
            let done = Latch<Void>()
            let limit = Self.cliWorkLimit
            Task { @MainActor in
                await cliTask.value
                done.set(())
            }
            let timer = Task.detached {
                try? await Task.sleep(for: limit)
                done.set(())
            }
            await done.wait()
            timer.cancel()
        }
        let running = Array(reads.values)
        for r in running { r.cancel() }
        if let operation {
            isStopping = true
            operation.cancel()
            await operation.value
        }
        for r in running { await r.done.value }
    }

    /// The app's updater is about to quit the app to install an update and
    /// relaunch it. Always postpones: stops the schedule and interrupts the
    /// running commands, as a quit does, then calls `relaunch` once every
    /// skillm has exited. No command starts after this, so the updater's
    /// own quit never meets a running one (see `AppDelegate.quit()`),
    /// unless the update fails and `resumeAfterAbortedUpdate()` follows.
    public func postponeRelaunch(_ relaunch: @escaping @MainActor () -> Void) -> Bool {
        Task { @MainActor in
            await self.shutdown()
            relaunch()
        }
        return true
    }

    /// The updater gave up after `postponeRelaunch` stopped everything (the
    /// update failed or was cancelled) and the app goes on running: commands
    /// can run again, the schedule restarts, and the menu says why nothing
    /// was installed. Does nothing unless the model was shut down.
    public func resumeAfterAbortedUpdate() {
        guard isShuttingDown else { return }
        isShuttingDown = false
        isStopping = false
        notice = Notice(text: "The update could not be installed", isError: true)
        startSchedule()
    }

    /// How long a quit waits for work on the CLI.
    static let cliWorkLimit: Duration = .seconds(120)

    /// Returns when no command and no launch check is running (including
    /// one a finished one started after itself).
    public func waitUntilIdle() async {
        while true {
            if let cliTask {
                await cliTask.value
            } else if let operation {
                await operation.value
            } else {
                return
            }
        }
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
            // The CLI may have changed under the command (a terminal
            // upgrade, a removal): its failures then say little.
            if !self.isShuttingDown, self.cliChanged { self.checkCLIAgain() }
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
        case .refreshing: "Refreshing skills…"
        case .updating(let progress): progress.text
        case .updatingSkill(let id): "Updating \(id)…"
        case .installing(let progress): progress.text(doing: "Installing skills")
        case .uninstalling(let id): "Uninstalling \(id)…"
        case .upgradingCLI: "Upgrading skillm CLI…"
        }
    }

    /// Worth a Stop item: a check, an update or an install. A settings
    /// write is instant, and an uninstall of one skill quick.
    public var canStop: Bool {
        switch self {
        case .refreshing, .updating, .updatingSkill, .installing: true
        case .idle, .savingSettings, .uninstalling, .upgradingCLI: false
        }
    }
}
