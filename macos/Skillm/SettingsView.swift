import AppKit
import SkillmKit
import SwiftUI

/// The Settings window: Start at login, auto refresh and its interval, the
/// enabled agents and the command-line tool.
struct SettingsView: View {
    @Bindable var settings: SettingsModel

    private var app: AppModel { settings.app }

    var body: some View {
        Form {
            generalSection
            refreshSection
            agentsSection
            toolSection
            if let message = settings.message {
                Section { NoticeText(notice: message) }
            }
        }
        .formStyle(.grouped)
        .frame(width: 480, height: 620)
        // Read again every time Settings opens (the menu activates the app
        // first) and whenever the app comes back to the front: the user may
        // have changed the login item in System Settings, or the config in a
        // terminal, meanwhile.
        .task { await settings.load() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await settings.load() }
        }
        .confirmationDialog(
            "Disable \(settings.pendingDisable ?? "")?",
            isPresented: Binding(
                get: { settings.pendingDisable != nil },
                set: { if !$0 { settings.pendingDisable = nil } }),
            titleVisibility: .visible
        ) {
            Button("Disable", role: .destructive) { settings.confirmDisable() }
            Button("Cancel", role: .cancel) { settings.pendingDisable = nil }
        } message: {
            Text("Its links to your skills are removed everywhere. The skills stay installed for the other agents.")
        }
    }

    // MARK: - General

    private var generalSection: some View {
        Section("General") {
            Toggle(
                "Start at login",
                isOn: Binding(
                    get: { settings.loginItemState != .disabled },
                    set: { settings.setStartAtLogin($0) }))
            if settings.loginItemState == .requiresApproval {
                HStack {
                    Label("Allow skillm in Login Items to start it at login.", systemImage: "exclamationmark.triangle")
                        .font(.callout)
                    Spacer()
                    Button("Open Login Items") { settings.openLoginItemsSettings() }
                }
            }
        }
    }

    // MARK: - Refresh

    private var refreshSection: some View {
        Section {
            Toggle(
                "Check for updates automatically",
                isOn: Binding(
                    get: { app.settings?.enabled ?? false },
                    set: { settings.setAutoRefresh($0) })
            )
            .disabled(app.isBusy || app.settings == nil)
            Picker(
                "Check every",
                selection: Binding(
                    get: { app.settings?.intervalHours ?? 24 },
                    set: { settings.setInterval(hours: $0) })
            ) {
                ForEach(intervalChoices, id: \.self) { hours in
                    Text(Self.intervalText(hours)).tag(hours)
                }
            }
            .disabled(app.isBusy || app.settings?.enabled != true)
        } header: {
            Text("Updates")
        } footer: {
            if let error = settings.loadError {
                NoticeText(notice: .init(text: error, isError: true))
            }
        }
    }

    /// The offered intervals, plus the configured one if it is not among
    /// them (set with `skillm config set`).
    private var intervalChoices: [Int] {
        var choices = SettingsModel.intervalChoices
        if let current = app.settings?.intervalHours, !choices.contains(current) {
            choices.append(current)
            choices.sort()
        }
        return choices
    }

    static func intervalText(_ hours: Int) -> String {
        switch hours {
        case 1: "hour"
        case 24: "day"
        case 168: "week"
        case let h where h % 24 == 0: "\(h / 24) days"
        default: "\(hours) hours"
        }
    }

    // MARK: - Agents

    private var agentsSection: some View {
        Section {
            ForEach(settings.agents) { agent in
                Toggle(
                    isOn: Binding(
                        get: { agent.enabled },
                        set: { settings.setAgent(agent.name, enabled: $0) })
                ) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(agent.name)
                        Text(folders(agent)).font(.callout).foregroundStyle(.secondary)
                    }
                }
                .disabled(app.isBusy || settings.isLastEnabled(agent))
            }
        } header: {
            Text("Agents")
        } footer: {
            Text("Installed skills are linked into every enabled agent's folders. Add agents in ~/.skillm/config.toml.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
    }

    private func folders(_ agent: Agent) -> String {
        [agent.global, agent.local].compactMap { $0 }.joined(separator: " · ")
    }

    // MARK: - Command-line tool

    private var toolSection: some View {
        Section {
            HStack {
                if let link = settings.commandLineTool {
                    Text("Installed at \(SkillsModel.abbreviate(link.path))")
                } else {
                    Text("Not installed")
                }
                Spacer()
                if settings.commandLineTool == nil {
                    Button("Install") { settings.installCommandLineTool() }
                        .disabled(app.cliExecutable == nil)
                }
            }
        } header: {
            Text("Command-line tool")
        } footer: {
            Text("Links skillm into /usr/local/bin, or ~/.local/bin when that is not writable, so a terminal runs the app's skillm.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
    }
}
