import Foundation
import Observation

/// The Settings window: auto refresh and its interval, the enabled agents,
/// Start at login and the command-line tool. The refresh settings live in
/// `AppModel.settings`; every command goes through `AppModel`.
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
    /// The `skillm` link into the app's CLI; nil when there is none.
    public private(set) var commandLineTool: URL?

    @ObservationIgnored private let loginItem: any LoginItemService
    @ObservationIgnored private let toolDirectories: [URL]

    public init(
        app: AppModel,
        loginItem: any LoginItemService = AppLoginItem(),
        toolDirectories: [URL] = CommandLineTool.defaultDirectories()
    ) {
        self.app = app
        self.loginItem = loginItem
        self.toolDirectories = toolDirectories
    }

    /// Reads everything again: run it every time Settings opens, since the
    /// login item and the config can change outside the app.
    public func load() async {
        refreshLocalState()
        do {
            try await app.reloadSettings()
            let data: AgentsData = try await app.read(["agent", "ls"])
            agents = data.agents
            loadError = nil
        } catch is CancellationError {
        } catch {
            loadError = AppModel.failure(error).text
        }
    }

    /// Re-reads what the system holds: the login item and the CLI link.
    public func refreshLocalState() {
        loginItemState = loginItem.state
        commandLineTool = app.cliExecutable.flatMap { CommandLineTool.installedLink(to: $0, in: toolDirectories) }
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

    /// The user agreed to disable `pendingDisable`.
    @discardableResult
    public func confirmDisable() -> Task<Void, Never>? {
        guard let name = pendingDisable else { return nil }
        pendingDisable = nil
        return agentSet(["--disable", name, "--yes"])
    }

    /// Only one agent is enabled: its toggle cannot turn off.
    public func isLastEnabled(_ agent: Agent) -> Bool {
        agent.enabled && agents.filter(\.enabled).count == 1
    }

    @discardableResult
    private func agentSet(_ flags: [String]) -> Task<Void, Never>? {
        let task = app.change(.savingSettings) { [weak self] client in
            do {
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
        if task != nil { message = nil }
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

    /// Links `skillm` into `/usr/local/bin` (or `~/.local/bin`).
    public func installCommandLineTool() {
        guard let cli = app.cliExecutable else {
            message = AppModel.Notice(text: AppModel.NotReadyError().errorDescription ?? "", isError: true)
            return
        }
        do {
            let link = try CommandLineTool.install(target: cli, directories: toolDirectories)
            commandLineTool = link
            message = AppModel.Notice(text: "Linked \(SkillsModel.abbreviate(link.path)) to the app's skillm", isError: false)
        } catch {
            message = AppModel.Notice(text: error.localizedDescription, isError: true)
        }
    }
}
