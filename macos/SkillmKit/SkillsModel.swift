import Foundation
import Observation

/// The View skills window: what `list` reports, and Update and Uninstall
/// for one skill. Every command goes through `AppModel`.
@MainActor
@Observable
public final class SkillsModel {
    /// The Uninstall confirmation: what will be deleted, and how the
    /// command runs once the user agrees.
    public struct UninstallQuestion: Identifiable, Equatable, Sendable {
        /// The skill.
        public var id: String
        /// It has a Global copy.
        public var global: Bool
        /// The projects whose committed copies are deleted, passed back as
        /// `--confirmed-root`.
        public var roots: [String]
        /// Another tool's entry is in the way: run with `--force`.
        public var force: Bool
        /// Why the question is asked again, when it is.
        public var reason: String?

        public init(id: String, global: Bool, roots: [String], force: Bool = false, reason: String? = nil) {
            self.id = id
            self.global = global
            self.roots = roots
            self.force = force
            self.reason = reason
        }
    }

    public let app: AppModel
    public private(set) var skills: [ListedSkill] = []
    /// `list` answered at least once.
    public private(set) var loaded = false
    public private(set) var isLoading = false
    /// Why the last `list` failed.
    public private(set) var loadError: String?
    /// The outcome of the last Update or Uninstall.
    public private(set) var message: AppModel.Notice?
    /// The uninstall waiting for the user's answer (the confirm sheet).
    public var pendingUninstall: UninstallQuestion?

    /// The latest `load`; an older one that finishes later is dropped.
    @ObservationIgnored private var loadGeneration = 0
    /// A question to ask again once the uninstall that raised it is done.
    @ObservationIgnored private var askAgain: UninstallQuestion?

    public init(app: AppModel) {
        self.app = app
    }

    /// `list`: every installed skill and where it is installed.
    public func load() async {
        loadGeneration += 1
        let generation = loadGeneration
        isLoading = true
        defer { if generation == loadGeneration { isLoading = false } }
        do {
            let data: ListData = try await app.read(["list"])
            guard generation == loadGeneration else { return }
            skills = data.skills
            loaded = true
            loadError = nil
        } catch is CancellationError {
            // The window closed, or a newer load replaced this one.
        } catch {
            guard generation == loadGeneration else { return }
            loadError = AppModel.failure(error).text
        }
    }

    // MARK: - Update

    /// `update <id> --events`: the skill's new Revision (or its copies
    /// re-synced from a local source).
    @discardableResult
    public func update(_ id: String) -> Task<Void, Never>? {
        let task = app.change(.updatingSkill(id)) { [weak self] client in
            do {
                for try await message in client.stream(["update", "--", id], as: UpdateData.self) {
                    if case .result(let data, let warnings) = message {
                        let summary = StatusSummary.skillUpdateResult(id, data, warnings: warnings)
                        self?.message = AppModel.Notice(text: summary.text, isError: summary.isError)
                    }
                }
            } catch is CancellationError {
                self?.message = AppModel.Notice(text: "Update stopped", isError: false)
            } catch {
                self?.message = AppModel.failure(error)
            }
        }
        if task != nil { message = nil }
        return task
    }

    // MARK: - Uninstall

    /// Opens the confirmation for `skill`, naming the projects whose
    /// committed copies would be deleted.
    public func askUninstall(_ skill: ListedSkill) {
        pendingUninstall = UninstallQuestion(
            id: skill.id,
            global: skill.installs.contains { $0.scope == .global && $0.recorded },
            roots: Self.projectRoots(skill))
    }

    /// The user agreed: `uninstall --yes`, with the projects the question
    /// named. If skillm needs the question asked again (another project got
    /// a copy meanwhile, or another tool's entry is in the way),
    /// `pendingUninstall` holds the new one once the command is done.
    @discardableResult
    public func confirmUninstall() -> Task<Void, Never>? {
        guard let question = pendingUninstall else { return nil }
        let args = Self.uninstallArguments(question)
        askAgain = nil
        let task = app.change(
            .uninstalling(question.id),
            { [weak self] client in
                do {
                    let data: UninstallData = try await client.run(args)
                    self?.message = Self.uninstallSummary(question.id, data)
                } catch let error as SkillmError {
                    switch error.protocolError?.code {
                    case .needsConfirm?:
                        var again = question
                        again.roots = error.protocolError?.paths ?? []
                        again.reason = "Another project got a copy of \(question.id) in the meantime."
                        self?.askAgain = again
                    case .needsForce?:
                        var again = question
                        again.force = true
                        again.reason = error.protocolError?.message
                        self?.askAgain = again
                    default:
                        self?.message = AppModel.failure(error)
                    }
                } catch is CancellationError {
                    self?.message = AppModel.Notice(text: "Uninstall stopped", isError: false)
                } catch {
                    self?.message = AppModel.failure(error)
                }
            },
            then: { [weak self] in
                guard let self else { return }
                if let again = askAgain {
                    askAgain = nil
                    pendingUninstall = again
                }
            })
        if task != nil {
            pendingUninstall = nil
            message = nil
        }
        return task
    }

    // MARK: - Words and arguments

    /// The recorded Local installs' project roots, the ones an uninstall
    /// deletes committed copies from.
    public static func projectRoots(_ skill: ListedSkill) -> [String] {
        var roots: [String] = []
        for install in skill.installs where install.scope == .local && install.recorded {
            if let root = install.root, !roots.contains(root) { roots.append(root) }
        }
        return roots
    }

    static func uninstallArguments(_ q: UninstallQuestion) -> [String] {
        var args = ["uninstall", "--yes"]
        if q.force { args.append("--force") }
        if q.roots.isEmpty {
            args.append("--confirmed-root=")
        } else {
            for root in q.roots { args += ["--confirmed-root", root] }
        }
        return args + ["--", q.id]
    }

    static func uninstallSummary(_ id: String, _ data: UninstallData) -> AppModel.Notice {
        guard let skill = data.skills.first(where: { $0.id == id }) else {
            return AppModel.Notice(text: "\(id) was no longer installed", isError: false)
        }
        if skill.warnings.isEmpty {
            return AppModel.Notice(text: "Uninstalled \(id)", isError: false)
        }
        return AppModel.Notice(
            text: "Uninstalled \(id), but left in place: " + skill.warnings.joined(separator: "; "), isError: true)
    }

    /// Where an install is, in words: "Global", or its project with `~`
    /// for the home folder; "(missing)" when its copy is gone, "(no agent)"
    /// when no agent reads it.
    public static func placeText(_ install: SkillInstall, home: String = NSHomeDirectory()) -> String {
        var text: String
        if install.scope == .global {
            text = "Global"
        } else {
            text = abbreviate(install.root ?? install.path, home: home)
        }
        if !install.exists {
            text += " (missing)"
        } else if install.agents.isEmpty {
            text += " (no agent)"
        }
        return text
    }

    /// `path` with the home folder written `~`.
    public static func abbreviate(_ path: String, home: String = NSHomeDirectory()) -> String {
        guard !home.isEmpty else { return path }
        if path == home { return "~" }
        let prefix = home.hasSuffix("/") ? home : home + "/"
        return path.hasPrefix(prefix) ? "~/" + path.dropFirst(prefix.count) : path
    }
}
