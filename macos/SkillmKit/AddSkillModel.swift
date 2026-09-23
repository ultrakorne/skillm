import Foundation
import Observation

/// The Add Skill window: a Source (and optional ref) → `source inspect` →
/// the skills to install → Global or a project → `install --events`.
/// Every command goes through `AppModel`.
@MainActor
@Observable
public final class AddSkillModel {
    /// Where the skills are installed.
    public enum Target: Equatable, Sendable {
        case global
        /// A project folder (Local scope), passed as `--project`.
        case project(URL)
    }

    /// Files skillm did not create are in the way of the install.
    public struct ForeignFilesQuestion: Identifiable, Equatable, Sendable {
        /// The canonical copies that would be overwritten.
        public var paths: [String]
        /// Agent links another tool holds, which only `takeOver` replaces.
        public var links: [String]
        public var id: [String] { paths + links }
    }

    /// The user's answer to `ForeignFilesQuestion`.
    public enum ForeignFilesAnswer: String, Sendable, CaseIterable {
        /// `--yes`: overwrite the canonical copies listed.
        case overwrite = "--yes"
        /// `--force`: also take over the agent links another tool holds.
        case takeOver = "--force"
        /// `--skip-foreign`: install the others, leave these skills out.
        case skip = "--skip-foreign"
    }

    public let app: AppModel
    /// A git URL, `owner/repo`, or an absolute local folder.
    public var source = ""
    /// A branch or tag (git only); empty means the default branch.
    public var ref = ""
    /// What `source inspect` found; nil until it answered. An install uses
    /// its Source, ref and commit, not the fields as edited since.
    public private(set) var inspection: InspectData?
    /// The chosen skill ids.
    public var selected: Set<String> = []
    public var target: Target = .global
    public private(set) var isInspecting = false
    /// The outcome of the last inspect or install.
    public private(set) var message: AppModel.Notice?
    /// The last install's result.
    public private(set) var installed: InstallData?
    /// The foreign-files question waiting for the user's answer.
    public var foreignFiles: ForeignFilesQuestion?

    @ObservationIgnored private var inspectTask: Task<Void, Never>?
    /// What the running install found, acted on once it is done.
    @ObservationIgnored private var followUp: FollowUp?

    private enum FollowUp {
        case ask(ForeignFilesQuestion)
        case reinspect
    }

    public init(app: AppModel) {
        self.app = app
    }

    /// The chosen skills, in the order `source inspect` listed them.
    public var selectedIDs: [String] {
        inspection?.skills.map(\.id).filter(selected.contains) ?? []
    }

    /// The install can run: skills chosen and no command running.
    public var canInstall: Bool {
        inspection != nil && !selectedIDs.isEmpty && !app.isBusy && !isInspecting
    }

    // MARK: - Inspect

    /// `source inspect`: reads the Source (a git one at `ref`, or its
    /// default branch) and lists its skills. The choice is kept for the
    /// skills still there.
    @discardableResult
    public func inspect() -> Task<Void, Never>? {
        let src: String
        switch Self.normalizedSource(source) {
        case .success(let s): src = s
        case .failure(let problem):
            message = AppModel.Notice(text: problem.text, isError: true)
            return nil
        }
        return startInspect(src, ref: ref.trimmingCharacters(in: .whitespacesAndNewlines), notice: nil)
    }

    /// Runs `source inspect` for `src` at `ref` (empty: the default
    /// branch), showing `notice` meanwhile.
    @discardableResult
    private func startInspect(_ src: String, ref: String, notice: AppModel.Notice?) -> Task<Void, Never> {
        var args = ["source", "inspect"]
        if !ref.isEmpty { args += ["--ref", ref] }
        args += ["--", src]

        inspectTask?.cancel()
        message = notice
        installed = nil
        foreignFiles = nil
        let task = Task { [weak self] in
            guard let self else { return }
            isInspecting = true
            do {
                let data: InspectData = try await app.read(args)
                guard !Task.isCancelled else { return }
                let keep = selected.intersection(data.skills.map(\.id))
                inspection = data
                selected = data.skills.count == 1 ? [data.skills[0].id] : keep
                if data.skills.isEmpty {
                    message = AppModel.Notice(text: "No skills were found in \(data.source)", isError: true)
                }
            } catch is CancellationError {
                return
            } catch {
                guard !Task.isCancelled else { return }
                inspection = nil
                message = AppModel.failure(error)
            }
            isInspecting = false
        }
        inspectTask = task
        return task
    }

    /// Stops a running inspect.
    public func cancelInspect() {
        inspectTask?.cancel()
        inspectTask = nil
        isInspecting = false
    }

    // MARK: - Install

    /// `install --events` of the chosen skills from the inspected Source,
    /// at the inspected commit, into `target`. `answer` is the reply to a
    /// foreign-files question.
    @discardableResult
    public func install(answer: ForeignFilesAnswer? = nil) -> Task<Void, Never>? {
        guard let inspection, !selectedIDs.isEmpty else { return nil }
        let args = Self.installArguments(inspection, ids: selectedIDs, target: target, answer: answer)
        followUp = nil
        let task = app.change(
            .installing(UpdateProgress()),
            { [weak self] client in
                guard let self else { return }
                do {
                    for try await message in client.stream(args, as: InstallData.self) {
                        switch message {
                        case .event(let event):
                            if case .installing(var progress) = app.activity {
                                progress.apply(event)
                                app.activity = .installing(progress)
                            }
                        case .result(let data, let warnings):
                            installed = data
                            self.message = Self.installSummary(data, warnings: warnings)
                        }
                    }
                } catch let error as SkillmError {
                    switch error.protocolError?.code {
                    case .foreignFiles?:
                        guard case .command(let e, let warnings) = error else { break }
                        followUp = .ask(
                            ForeignFilesQuestion(
                                paths: e.paths ?? [],
                                links: warnings.filter { $0.code == "link_refused" }.map(\.message)))
                    case .commitMismatch?:
                        self.message = AppModel.Notice(
                            text: "The source changed since it was read. Check the skills and install again.",
                            isError: true)
                        followUp = .reinspect
                    default:
                        self.message = AppModel.failure(error)
                    }
                } catch is CancellationError {
                    self.message = AppModel.Notice(text: "Install stopped", isError: false)
                } catch {
                    self.message = AppModel.failure(error)
                }
            },
            then: { [weak self] in
                guard let self, let next = followUp else { return }
                followUp = nil
                switch next {
                case .ask(let question): foreignFiles = question
                case .reinspect: reinspect()
                }
            })
        if task != nil {
            message = nil
            installed = nil
            foreignFiles = nil
        }
        return task
    }

    /// Inspects the Source again after it moved, keeping the message that
    /// says so.
    private func reinspect() {
        guard let inspection else { return }
        startInspect(inspection.source, ref: inspection.ref ?? "", notice: message)
    }

    // MARK: - Words and arguments

    /// A problem with the Source field.
    public struct SourceProblem: Error, Equatable {
        public var text: String
    }

    /// The Source as skillm gets it: trimmed, `~` expanded. A relative
    /// local path is refused, since the app has no working directory to
    /// resolve it against.
    public static func normalizedSource(
        _ text: String, home: String = NSHomeDirectory()
    ) -> Result<String, SourceProblem> {
        let s = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return .failure(SourceProblem(text: "Enter a git URL, owner/repo, or a folder.")) }
        if s == "~" { return .success(home) }
        if s.hasPrefix("~/") { return .success(home + "/" + s.dropFirst(2)) }
        if s == "." || s == ".." || s.hasPrefix("./") || s.hasPrefix("../") {
            return .failure(SourceProblem(text: "Use the folder's full path (or Choose…)."))
        }
        return .success(s)
    }

    static func installArguments(
        _ inspection: InspectData, ids: [String], target: Target, answer: ForeignFilesAnswer?
    ) -> [String] {
        var args = ["install"]
        if inspection.kind == .git, let commit = inspection.commit {
            if let ref = inspection.ref { args += ["--ref", ref] }
            args += ["--commit", commit]
        }
        switch target {
        case .global: args.append("--global")
        case .project(let url): args += ["--project", url.path]
        }
        if let answer { args.append(answer.rawValue) }
        return args + ["--", inspection.source] + ids
    }

    static func installSummary(_ data: InstallData, warnings: [Warning]) -> AppModel.Notice {
        let done = data.skills.filter { $0.action != .skipped }.map(\.id)
        let skipped = data.skills.filter { $0.action == .skipped }.map(\.id)
        let place = data.scope == .global ? "globally" : "in " + SkillsModel.abbreviate(data.root ?? "the project")
        var parts: [String] = []
        if !done.isEmpty { parts.append("Installed \(done.joined(separator: ", ")) \(place)") }
        if !skipped.isEmpty { parts.append("skipped \(skipped.joined(separator: ", "))") }
        if parts.isEmpty { parts.append("Nothing was installed") }
        let other = warnings.filter { $0.code != "install_blocked" }.map(\.message)
        var text = parts.joined(separator: "; ")
        text = text.prefix(1).uppercased() + text.dropFirst()
        if !other.isEmpty { text += ". " + other.joined(separator: "; ") }
        return AppModel.Notice(text: text, isError: !other.isEmpty)
    }
}
