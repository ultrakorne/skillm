import Foundation

/// "Install command-line tool": a `skillm` symlink into the app's Bundled
/// CLI, so a terminal runs the same skillm as the app. It stays a link into
/// the bundle, so the CLI still judges itself bundled and leaves upgrades to
/// the app, and it follows the app when Sparkle replaces the bundle.
public enum CommandLineTool {
    public enum Failure: Error, Equatable, LocalizedError {
        /// Something that is not a link into a skillm app is there already.
        case occupied(String)
        case failed(String, reason: String)

        public var errorDescription: String? {
            switch self {
            case .occupied(let path):
                "\(path) already exists and is not a link to this app; remove it first to use the app's skillm."
            case .failed(let path, let reason):
                "Could not create \(path): \(reason)"
            }
        }
    }

    /// Where the link goes, in order: the first folder that exists and can
    /// be written, else the last one, created.
    public static func defaultDirectories(home: String = NSHomeDirectory()) -> [URL] {
        [URL(fileURLWithPath: "/usr/local/bin"), URL(fileURLWithPath: home).appending(path: ".local/bin")]
    }

    /// The link to `target` in one of `directories`, if there is one.
    public static func installedLink(to target: URL, in directories: [URL]) -> URL? {
        directories.map { $0.appending(path: "skillm") }.first { points($0, to: target) }
    }

    /// Links `skillm` in the first usable directory to `target` and returns
    /// the link. An existing link to `target` is kept; a link into another
    /// skillm app (an older copy, or one that was moved) or a dangling one
    /// is replaced; anything else is left alone and refused.
    public static func install(target: URL, directories: [URL]) throws -> URL {
        if let existing = installedLink(to: target, in: directories) { return existing }
        let fm = FileManager.default
        guard let dir = directories.first(where: { fm.isWritableFile(atPath: $0.path) }) ?? directories.last else {
            throw Failure.failed("skillm", reason: "no folder to put it in")
        }
        let link = dir.appending(path: "skillm")
        do {
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        } catch {
            throw Failure.failed(link.path, reason: error.localizedDescription)
        }
        if let resolved = destination(of: link) {
            guard !fm.fileExists(atPath: resolved.path) || isBundledCLI(resolved.path) else {
                throw Failure.occupied(link.path)
            }
            do {
                try fm.removeItem(at: link)
            } catch {
                throw Failure.failed(link.path, reason: error.localizedDescription)
            }
        } else if (try? link.checkResourceIsReachable()) == true {
            throw Failure.occupied(link.path)
        }
        do {
            try fm.createSymbolicLink(atPath: link.path, withDestinationPath: target.path)
        } catch {
            throw Failure.failed(link.path, reason: error.localizedDescription)
        }
        return link
    }

    /// `link` is a symlink whose destination is `target`.
    static func points(_ link: URL, to target: URL) -> Bool {
        destination(of: link)?.path == target.standardizedFileURL.path
    }

    /// Where the symlink `link` points, made absolute; nil when `link` is
    /// not a symlink.
    static func destination(of link: URL) -> URL? {
        guard let dest = try? FileManager.default.destinationOfSymbolicLink(atPath: link.path) else { return nil }
        let url = dest.hasPrefix("/")
            ? URL(fileURLWithPath: dest) : link.deletingLastPathComponent().appending(path: dest)
        return url.standardizedFileURL
    }

    /// A path to the CLI inside some skillm app bundle.
    static func isBundledCLI(_ path: String) -> Bool {
        path.hasSuffix(".app/" + SkillmBinary.bundledPath)
    }
}
