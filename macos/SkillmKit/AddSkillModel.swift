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

    /// One install as it was asked: the inspection, the skills and the
    /// target the form held when it started. A follow-up question retries
    /// exactly this, whatever the form holds by then.
    public struct InstallRequest: Equatable, Sendable {
        public var inspection: InspectData
        public var ids: [String]
        public var target: Target

        public init(inspection: InspectData, ids: [String], target: Target) {
            self.inspection = inspection
            self.ids = ids
            self.target = target
        }
    }

    /// Files skillm did not create are at the canonical copies' places, so
    /// nothing was installed (`foreign_files`).
    public struct ForeignFilesQuestion: Identifiable, Equatable, Sendable {
        /// The refused install; an answer retries it with one flag.
        public var request: InstallRequest
        /// The canonical copies that would be overwritten.
        public var paths: [String]
        public var id: [String] { paths }
    }

    /// The user's answer to `ForeignFilesQuestion`.
    public enum ForeignFilesAnswer: String, Sendable, CaseIterable {
        /// `--yes`: overwrite the canonical copies listed (never the agent
        /// links, which the question did not list).
        case overwrite = "--yes"
        /// `--skip-foreign`: install the others, leave these skills out.
        case skip = "--skip-foreign"
    }

    /// The install landed, but another tool holds some of the agent link
    /// paths, which skillm left alone (`link_refused` warnings). skillm
    /// reports these only once the copies are in place, never alongside
    /// `foreign_files`.
    public struct RefusedLinksQuestion: Identifiable, Equatable, Sendable {
        /// The install again, narrowed to the skills whose links were
        /// refused; taking over retries it with `--force`.
        public var request: InstallRequest
        /// skillm's words for each refused link.
        public var links: [String]
        public var id: [String] { links }
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
    /// The refused-links question waiting for the user's answer.
    public var refusedLinks: RefusedLinksQuestion?

    @ObservationIgnored private var inspectTask: Task<Void, Never>?
    /// What the running install found, acted on once it is done.
    @ObservationIgnored private var followUp: FollowUp?
    /// Goes up with every inspect: an install's question about an older
    /// inspection is dropped.
    @ObservationIgnored private var inspectGeneration = 0

    private enum FollowUp {
        case foreignFiles(ForeignFilesQuestion)
        case refusedLinks(RefusedLinksQuestion)
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

    /// An install is running: the form keeps what it was started with.
    public var isInstalling: Bool {
        if case .installing = app.activity { return true }
        return false
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
        inspectGeneration += 1
        message = notice
        installed = nil
        foreignFiles = nil
        refusedLinks = nil
        followUp = nil
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
    /// at the inspected commit, into `target`.
    @discardableResult
    public func install() -> Task<Void, Never>? {
        guard let inspection, !selectedIDs.isEmpty else { return nil }
        return run(InstallRequest(inspection: inspection, ids: selectedIDs, target: target), flag: nil)
    }

    /// Answers `question`: retries the install it was about with `answer`.
    /// nil when another command is running; the question then stays.
    @discardableResult
    public func answer(_ question: ForeignFilesQuestion, with answer: ForeignFilesAnswer) -> Task<Void, Never>? {
        run(question.request, flag: answer.rawValue)
    }

    /// Takes over the links `question` lists: the same install of those
    /// skills again, with `--force`. nil when another command is running;
    /// the question then stays.
    @discardableResult
    public func takeOverLinks(_ question: RefusedLinksQuestion) -> Task<Void, Never>? {
        run(question.request, flag: "--force")
    }

    private func run(_ request: InstallRequest, flag: String?) -> Task<Void, Never>? {
        let args = Self.installArguments(request, flag: flag)
        let generation = inspectGeneration
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
                            if let question = Self.refusedLinks(request, warnings: warnings) {
                                followUp = .refusedLinks(question)
                            }
                        }
                    }
                } catch let error as SkillmError {
                    switch error.protocolError?.code {
                    case .foreignFiles?:
                        followUp = .foreignFiles(
                            ForeignFilesQuestion(request: request, paths: error.protocolError?.paths ?? []))
                    case .commitMismatch?:
                        self.message = AppModel.Notice(
                            text: "The source changed since it was read. Check the skills and install again.",
                            isError: true)
                        followUp = .reinspect
                    default:
                        self.message = AppModel.failure(error)
                        // Skills that landed before the failure may have
                        // had links refused.
                        if case .command(_, let warnings) = error,
                            let question = Self.refusedLinks(request, warnings: warnings)
                        {
                            followUp = .refusedLinks(question)
                        }
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
                // Read Skills ran meanwhile: the question is about a form
                // that is gone.
                guard generation == inspectGeneration else { return }
                switch next {
                case .foreignFiles(let question): foreignFiles = question
                case .refusedLinks(let question): refusedLinks = question
                case .reinspect: reinspect()
                }
            })
        if task != nil {
            message = nil
            installed = nil
            foreignFiles = nil
            refusedLinks = nil
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

    /// `request` as `install` arguments, with `flag` (`--yes`,
    /// `--skip-foreign` or `--force`) when it answers a question.
    static func installArguments(_ request: InstallRequest, flag: String?) -> [String] {
        let inspection = request.inspection
        var args = ["install"]
        if inspection.kind == .git, let commit = inspection.commit {
            if let ref = inspection.ref { args += ["--ref", ref] }
            args += ["--commit", commit]
        }
        switch request.target {
        case .global: args.append("--global")
        case .project(let url): args += ["--project", url.path]
        }
        if let flag { args.append(flag) }
        return args + ["--", inspection.source] + request.ids
    }

    /// The take-over question for an install whose warnings include
    /// `link_refused`: `request` narrowed to those skills, so `--force`
    /// cannot also overwrite a canonical copy the install left out. nil
    /// when no link was refused. (`link_failed` is an I/O failure that
    /// `--force` does not fix.)
    static func refusedLinks(_ request: InstallRequest, warnings: [Warning]) -> RefusedLinksQuestion? {
        let refused = warnings.filter { $0.code == "link_refused" && $0.skillId != nil }
        let ids = request.ids.filter { id in refused.contains { $0.skillId == id } }
        guard !ids.isEmpty else { return nil }
        var narrowed = request
        narrowed.ids = ids
        return RefusedLinksQuestion(
            request: narrowed, links: refused.filter { ids.contains($0.skillId ?? "") }.map(\.message))
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
