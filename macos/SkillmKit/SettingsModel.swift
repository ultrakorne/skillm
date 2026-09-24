import Foundation
import Observation

/// The Settings window: auto refresh and its interval, the enabled agents,
/// Start at login and the command-line tool (the installed CLI the app
/// drives). The refresh settings live in `AppModel.settings`; every command
/// goes through `AppModel`.
@MainActor
@Observable
public final class SettingsModel {
    /// The refresh intervals offered, in hours (skillm takes 1 to 720).
    public static let intervalChoices = [1, 3, 6, 12, 24, 48, 168]

    public let app: AppModel
    /// Every defined agent (`agent ls`), sorted by name.
    public private(set) var agents: [Agent] = []
    /// Why the settings or agents could not be read.
    public private(set) var loadError: String?
    /// The outcome of the last change made here.
    public private(set) var message: AppModel.Notice?
    /// The agent waiting for the user to confirm disabling it.
    public var pendingDisable: String?
    public private(set) var loginItemState: LoginItemState = .disabled
    /// The skillm the app found and drives (usable or not); nil when none is
    /// installed.
    public var commandLineTool: URL? { app.cliPath }
    /// The version it reported; nil when unknown.
    public var commandLineToolVersion: String? { app.cliVersion }
    /// `install.sh` is running.
    public var isInstallingTool: Bool { app.cliWork == .installing }

    @ObservationIgnored private let loginItem: any LoginItemService
    /// Goes up when an `agent set` starts and when it ends: an `agent ls`
    /// read that overlapped one is dropped, since it may hold the old state.
    @ObservationIgnored private var agentWrites = 0

    public init(app: AppModel, loginItem: any LoginItemService = AppLoginItem()) {
        self.app = app
        self.loginItem = loginItem
    }

    /// Reads everything again: run it every time Settings opens, since the
    /// login item and the config can change outside the app.
    public func load() async {
        refreshLocalState()
        do {
            try await app.reloadSettings()
            let writes = agentWrites
            let data: AgentsData = try await app.read(["agent", "ls"])
            if writes == agentWrites { agents = data.agents }
            loadError = nil
        } catch is CancellationError {
        } catch {
            loadError = AppModel.failure(error).text
        }
    }

    /// Re-reads what the system holds: the login item. While the CLI
    /// cannot be used, the launch checks run again too, so a skillm
    /// installed in a terminal shows.
    public func refreshLocalState() {
        loginItemState = loginItem.state
        app.checkCLIAgain()
    }

    // MARK: - Agents

    /// An agent's toggle. Enabling runs at once; disabling removes its
    /// links, so it asks first (`pendingDisable`).
    public func setAgent(_ name: String, enabled: Bool) {
        if enabled {
            agentSet(["--enable", name])
        } else {
            pendingDisable = name
        }
    }

    /// The user agreed to disable `pendingDisable`. nil when another
    /// command is running; the question then stays.
    @discardableResult
    public func confirmDisable() -> Task<Void, Never>? {
        guard let name = pendingDisable else { return nil }
        let task = agentSet(["--disable", name, "--yes"])
        if task != nil { pendingDisable = nil }
        return task
    }

    /// Only one agent is enabled: its toggle cannot turn off.
    public func isLastEnabled(_ agent: Agent) -> Bool {
        agent.enabled && agents.filter(\.enabled).count == 1
    }

    @discardableResult
    private func agentSet(_ flags: [String]) -> Task<Void, Never>? {
        let task = app.change(.savingSettings) { [weak self] client in
            do {
                defer { self?.agentWrites += 1 }
                let data: AgentsSetData = try await client.run(["agent", "set"] + flags)
                guard let self else { return }
                for i in agents.indices { agents[i].enabled = data.enabled.contains(agents[i].name) }
                let warnings = data.changes.flatMap(\.warnings)
                if !warnings.isEmpty {
                    message = AppModel.Notice(
                        text: "Some links were left as they were: " + warnings.joined(separator: "; "), isError: true)
                }
            } catch {
                self?.message = AppModel.failure(error)
            }
        }
        if task != nil {
            agentWrites += 1
            message = nil
        }
        return task
    }

    // MARK: - Refresh

    /// The auto refresh toggle.
    @discardableResult
    public func setAutoRefresh(_ enabled: Bool) -> Task<Void, Never>? {
        saveSetting("refresh.enabled", enabled ? "true" : "false", checksAfter: enabled)
    }

    /// The refresh interval picker. A shorter interval applies at once, so
    /// a scheduled refresh follows.
    @discardableResult
    public func setInterval(hours: Int) -> Task<Void, Never>? {
        saveSetting("refresh.interval_hours", String(hours), checksAfter: true)
    }

    private func saveSetting(_ key: String, _ value: String, checksAfter: Bool) -> Task<Void, Never>? {
        let task = app.saveSetting(key, value, checksAfter: checksAfter) { [weak self] in self?.message = $0 }
        if task != nil { message = nil }
        return task
    }

    // MARK: - Start at login

    /// The Start at login checkbox. It shows the system's state, read
    /// again right after.
    public func setStartAtLogin(_ on: Bool) {
        do {
            if on { try loginItem.register() } else { try loginItem.unregister() }
            message = nil
        } catch {
            message = AppModel.Notice(
                text: "Could not \(on ? "turn on" : "turn off") Start at login: \(error.localizedDescription)",
                isError: true)
        }
        loginItemState = loginItem.state
    }

    public func openLoginItemsSettings() {
        loginItem.openSystemSettings()
    }

    // MARK: - Command-line tool

    /// "Install": runs the bundled `install.sh` (the latest release), then
    /// the launch checks again (`AppModel.installCLI`). Its failure is
    /// `AppModel.cliProblem`.
    @discardableResult
    public func installCommandLineTool() -> Task<Void, Never>? {
        app.installCLI()
    }
}
