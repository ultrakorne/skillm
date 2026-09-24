import AppKit
import SkillmKit
import SwiftUI

/// The View skills window: every installed skill, where it is installed and
/// for which agents, with Reveal in Finder, Update and Uninstall per skill.
struct SkillsView: View {
    @Bindable var skills: SkillsModel
    @Environment(\.openWindow) private var openWindow
    @State private var selection: String?

    private var app: AppModel { skills.app }

    var body: some View {
        VStack(spacing: 0) {
            table
            Divider()
            footer
        }
        .frame(minWidth: 640, minHeight: 280)
        .toolbar {
            ToolbarItemGroup {
                Button {
                    Task { await skills.load() }
                } label: {
                    Label("Reload", systemImage: "arrow.clockwise")
                }
                .help("List the installed skills again")
                Button {
                    openWindow(id: WindowID.addSkill)
                } label: {
                    Label("Add Skill", systemImage: "plus")
                }
                .help("Install skills from a repository or a folder")
            }
        }
        // Lists on open, again once the CLI is ready (a window macOS
        // restores at launch opens before), and after anything changed what
        // is installed.
        .task(id: ListKey(installsVersion: app.installsVersion, ready: app.isReady)) { await skills.load() }
        .sheet(item: $skills.pendingUninstall) { question in
            UninstallSheet(question: question, busy: app.isBusy) {
                skills.confirmUninstall()
            } cancel: {
                skills.pendingUninstall = nil
            }
        }
    }

    private var table: some View {
        Table(skills.skills, selection: $selection) {
            TableColumn("Skill") { skill in
                Text(skill.id).fontWeight(.medium)
            }
            .width(min: 120, ideal: 160)
            TableColumn("Source") { skill in
                Text(skill.sourceLabel + (skill.ref.map { " @ \($0)" } ?? ""))
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .help(skill.sourceLabel)
            }
            .width(min: 140, ideal: 240)
            TableColumn("Kind") { skill in
                Text(skill.kind.rawValue).foregroundStyle(.secondary)
            }
            .width(44)
            TableColumn("Installed") { skill in
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(Array(skill.installs.enumerated()), id: \.offset) { _, install in
                        Text(SkillsModel.placeText(install))
                            + Text("  " + SkillsModel.agentsText(install)).foregroundStyle(.secondary)
                    }
                }
                .help(skill.installs.map(\.path).joined(separator: "\n"))
            }
            .width(min: 160, ideal: 260)
            TableColumn("") { skill in
                actions(skill)
            }
            .width(32)
        }
        .contextMenu(forSelectionType: String.self) { ids in
            if let id = ids.first, let skill = skills.skills.first(where: { $0.id == id }) {
                menuItems(skill)
            }
        }
        .overlay {
            if skills.loaded, skills.skills.isEmpty {
                ContentUnavailableView(
                    "No skills installed", systemImage: "books.vertical",
                    description: Text("Add a skill from a repository or a folder."))
            } else if !skills.loaded, let error = skills.loadError {
                ContentUnavailableView(
                    "Could not list the skills", systemImage: "exclamationmark.triangle",
                    description: Text(error))
            } else if !skills.loaded {
                ProgressView()
            }
        }
    }

    private func actions(_ skill: ListedSkill) -> some View {
        Menu {
            menuItems(skill)
        } label: {
            Image(systemName: "ellipsis.circle")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Reveal, update or uninstall \(skill.id)")
    }

    @ViewBuilder private func menuItems(_ skill: ListedSkill) -> some View {
        ForEach(Array(skill.installs.enumerated()), id: \.offset) { _, install in
            Button("Reveal in Finder: " + SkillsModel.placeText(install)) { reveal(install) }
        }
        Divider()
        Button("Update") { skills.update(skill.id) }
            .disabled(app.isBusy)
        Button("Uninstall…", role: .destructive) { skills.askUninstall(skill) }
            .disabled(app.isBusy)
    }

    private var footer: some View {
        HStack(spacing: 8) {
            if let activity = app.activity.text {
                ProgressView().controlSize(.small)
                Text(activity)
                if app.activity.canStop {
                    Button(app.isStopping ? "Stopping…" : "Stop") { app.cancel() }
                        .disabled(app.isStopping)
                }
            } else if let message = skills.message {
                NoticeText(notice: message)
            } else if skills.loaded, let error = skills.loadError {
                NoticeText(notice: .init(text: error, isError: true))
            } else {
                Text(countText).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var countText: String {
        skills.skills.count == 1 ? "1 skill" : "\(skills.skills.count) skills"
    }

    /// Shows the install's copy in Finder, or its folder when the copy is
    /// gone.
    private func reveal(_ install: SkillInstall) {
        let copy = URL(fileURLWithPath: install.path)
        if FileManager.default.fileExists(atPath: copy.path) {
            NSWorkspace.shared.activateFileViewerSelecting([copy])
        } else {
            NSWorkspace.shared.open(copy.deletingLastPathComponent())
        }
    }
}

/// The Uninstall confirmation: it names what is deleted, including the
/// committed copies in projects.
struct UninstallSheet: View {
    let question: SkillsModel.UninstallQuestion
    let busy: Bool
    let confirm: () -> Void
    let cancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Uninstall \(question.id)?").font(.headline)
            if let reason = question.reason {
                Label(reason, systemImage: "exclamationmark.triangle")
            }
            Text("It is removed for every agent, everywhere it is installed:")
            VStack(alignment: .leading, spacing: 4) {
                if question.global {
                    Text("• the global copy in ~/.agents/skills")
                }
                ForEach(question.roots, id: \.self) { root in
                    Text("• the committed copy in \(SkillsModel.abbreviate(root))")
                }
            }
            if !question.roots.isEmpty {
                Text("Project copies are files in those repositories; commit their removal there.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            if question.force {
                Text("Entries another tool created are in the way. Uninstall Anyway leaves them in place and removes the rest.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            HStack {
                if busy {
                    Text("Waiting for the running command…").foregroundStyle(.secondary)
                }
                Spacer()
                Button("Cancel", role: .cancel, action: cancel)
                    .keyboardShortcut(.cancelAction)
                Button(question.force ? "Uninstall Anyway" : "Uninstall", role: .destructive, action: confirm)
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy)
            }
        }
        .padding(20)
        .frame(width: 440)
    }
}

/// What the Skills window lists again on.
private struct ListKey: Equatable {
    var installsVersion: Int
    var ready: Bool
}

/// A notice line: an error with its icon, or plain text.
struct NoticeText: View {
    let notice: AppModel.Notice

    var body: some View {
        if notice.isError {
            Label(notice.text, systemImage: "xmark.octagon").foregroundStyle(.red)
                .textSelection(.enabled)
        } else {
            Text(notice.text).textSelection(.enabled)
        }
    }
}
