import AppKit
import SkillmKit
import SwiftUI

/// The Add Skill window: a repository (or folder) and an optional ref →
/// read its skills → choose some → Global or a project → install.
struct AddSkillView: View {
    @Bindable var add: AddSkillModel

    private var app: AppModel { add.app }

    var body: some View {
        Form {
            // The form keeps what a running install was started with.
            Group {
                sourceSection
                if let inspection = add.inspection {
                    skillsSection(inspection)
                    targetSection
                }
            }
            .disabled(add.isInstalling)
            if let message = add.message {
                Section { NoticeText(notice: message) }
            }
        }
        .formStyle(.grouped)
        .safeAreaInset(edge: .bottom) { bottomBar }
        .frame(minWidth: 460, minHeight: 300)
        // A question closes once its retry has started; while another
        // command runs it stays, with its buttons greyed out.
        .sheet(item: $add.foreignFiles) { question in
            ForeignFilesSheet(question: question, busy: app.isBusy) { answer in
                if let answer {
                    add.answer(question, with: answer)
                } else {
                    add.foreignFiles = nil
                }
            }
        }
        .sheet(item: $add.refusedLinks) { question in
            RefusedLinksSheet(question: question, busy: app.isBusy) {
                add.takeOverLinks(question)
            } cancel: {
                add.refusedLinks = nil
            }
        }
    }

    // MARK: - Source

    private var sourceSection: some View {
        Section {
            HStack {
                TextField("Repository", text: $add.source, prompt: Text("https://github.com/owner/repo, owner/repo or a folder"))
                    .onSubmit { add.inspect() }
                Button("Choose…") { chooseSourceFolder() }
                    .help("Use a skill folder on this Mac")
            }
            TextField("Branch or tag", text: $add.ref, prompt: Text("default branch"))
                .onSubmit { add.inspect() }
            HStack {
                Spacer()
                if add.isInspecting {
                    ProgressView().controlSize(.small)
                    Button("Cancel") { add.cancelInspect() }
                }
                Button("Read Skills") { add.inspect() }
                    .disabled(add.isInspecting || add.source.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        } header: {
            Text("Source")
        } footer: {
            Text("A git repository may hold many skills; you choose which to install next.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: - Skills

    private func skillsSection(_ inspection: InspectData) -> some View {
        Section {
            ForEach(inspection.skills) { skill in
                Toggle(isOn: selectedBinding(skill.id)) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(skill.name.isEmpty ? skill.id : skill.name)
                        if !skill.description.isEmpty {
                            Text(skill.description).font(.callout).foregroundStyle(.secondary)
                        }
                    }
                }
                .toggleStyle(.checkbox)
            }
            if inspection.skills.count > 1 {
                HStack {
                    Button("Select All") { add.selected = Set(inspection.skills.map(\.id)) }
                    Button("Select None") { add.selected = [] }
                }
            }
        } header: {
            Text("Skills in \(inspection.source)" + Self.revisionText(inspection))
                .lineLimit(1)
                .truncationMode(.middle)
        }
    }

    private func selectedBinding(_ id: String) -> Binding<Bool> {
        Binding(
            get: { add.selected.contains(id) },
            set: { on in
                if on { add.selected.insert(id) } else { add.selected.remove(id) }
            })
    }

    /// " (main @ 9fceb02)" for a git Source.
    static func revisionText(_ inspection: InspectData) -> String {
        guard let commit = inspection.commit else { return "" }
        let ref = inspection.ref.map { "\($0) @ " } ?? ""
        return " (\(ref)\(commit.prefix(7)))"
    }

    // MARK: - Target

    private var targetSection: some View {
        Section("Install for") {
            Picker("Scope", selection: scopeBinding) {
                Text("Every project (Global)").tag(0)
                Text("One project").tag(1)
            }
            .pickerStyle(.radioGroup)
            .labelsHidden()
            if case .project(let url) = add.target {
                HStack {
                    Text(SkillsModel.abbreviate(url.path))
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .help(url.path)
                    Spacer()
                    Button("Change…") { chooseProject() }
                }
            }
        }
    }

    private var scopeBinding: Binding<Int> {
        Binding(
            get: {
                if case .project = add.target { return 1 }
                return 0
            },
            set: { choice in
                if choice == 0 {
                    add.target = .global
                } else {
                    chooseProject()
                }
            })
    }

    // MARK: - Install

    private var bottomBar: some View {
        HStack(spacing: 8) {
            if case .installing = app.activity, let text = app.activity.text {
                ProgressView().controlSize(.small)
                Text(text)
                Button(app.isStopping ? "Stopping…" : "Stop") { app.cancel() }
                    .disabled(app.isStopping)
            } else if app.isBusy, let text = app.activity.text {
                Text("Waiting: \(text)").foregroundStyle(.secondary)
            }
            Spacer()
            Button("Install") { add.install() }
                .keyboardShortcut(.defaultAction)
                .disabled(!add.canInstall)
        }
        .padding(12)
        .background(.bar)
    }

    // MARK: - Panels

    private func chooseSourceFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.message = "Choose a skill folder, or a folder of skills"
        panel.prompt = "Choose"
        if panel.runModal() == .OK, let url = panel.url {
            add.source = url.path
            add.ref = ""
            add.inspect()
        }
    }

    private func chooseProject() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.canCreateDirectories = false
        panel.message = "Choose the project to install the skills into"
        panel.prompt = "Choose Project"
        if case .project(let url) = add.target { panel.directoryURL = url }
        if panel.runModal() == .OK, let url = panel.url {
            add.target = .project(url)
        }
    }
}

/// Files skillm did not create are at the canonical copies' places:
/// overwrite them, skip those skills, or cancel.
struct ForeignFilesSheet: View {
    let question: AddSkillModel.ForeignFilesQuestion
    /// Another command is running: the answers wait for it.
    let busy: Bool
    /// nil: cancel.
    let answer: (AddSkillModel.ForeignFilesAnswer?) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Files skillm did not create are in the way").font(.headline)
            Text("Installing overwrites:")
            PathList(items: question.paths.map { SkillsModel.abbreviate($0) })
            Text("Skip installs the other skills and leaves these as they are.")
                .font(.callout)
                .foregroundStyle(.secondary)
            HStack {
                if busy {
                    Text("Waiting for the running command…").foregroundStyle(.secondary)
                }
                Spacer()
                Button("Cancel", role: .cancel) { answer(nil) }
                    .keyboardShortcut(.cancelAction)
                Button("Skip These") { answer(.skip) }
                    .disabled(busy)
                Button("Overwrite", role: .destructive) { answer(.overwrite) }
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy)
            }
        }
        .padding(20)
        .frame(width: 520)
    }
}

/// The skills were installed, but another tool holds some agent link
/// paths, which skillm left alone: take them over, or leave them.
struct RefusedLinksSheet: View {
    let question: AddSkillModel.RefusedLinksQuestion
    /// Another command is running: Take Over waits for it.
    let busy: Bool
    let takeOver: () -> Void
    let cancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Another tool holds some agent links").font(.headline)
            Text("skillm installed \(question.request.ids.joined(separator: ", ")) but left these alone:")
            PathList(items: question.links)
            Text("Those agents do not see the skill until skillm's link replaces what is there.")
                .font(.callout)
                .foregroundStyle(.secondary)
            HStack {
                if busy {
                    Text("Waiting for the running command…").foregroundStyle(.secondary)
                }
                Spacer()
                Button("Leave Them", role: .cancel, action: cancel)
                    .keyboardShortcut(.cancelAction)
                Button("Take Over Links", role: .destructive, action: takeOver)
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy)
            }
        }
        .padding(20)
        .frame(width: 520)
    }
}

/// A short scrolling list of paths or messages, selectable.
private struct PathList: View {
    let items: [String]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 2) {
                ForEach(items, id: \.self) { Text($0).font(.callout.monospaced()).textSelection(.enabled) }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .frame(maxHeight: 120)
    }
}
