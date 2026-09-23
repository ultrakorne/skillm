import Foundation

/// Finds the skillm binary the app runs.
///
/// A built app carries a version-matched CLI at `Contents/Helpers/skillm`
/// (the build phase `scripts/build-cli.sh` puts it there). It lives under
/// `Contents/` so the CLI judges itself "bundled" and refuses to swap its
/// own binary, and not in `Contents/MacOS`, where the app's own executable is
/// also named skillm.
///
/// A debug build also honours `$SKILLM_BIN` (checked first, as an explicit
/// override) and falls back to the `go build` output at the repository root.
public enum SkillmBinary {
    /// The bundled CLI's path inside the app bundle.
    public static let bundledPath = "Contents/Helpers/skillm"

    /// Whether this is a debug build.
    public static var isDebugBuild: Bool {
        #if DEBUG
            return true
        #else
            return false
        #endif
    }

    /// The repository root this debug build was compiled from, if any.
    public static var debugSourceRoot: URL? {
        #if DEBUG
            // macos/SkillmKit/SkillmBinary.swift → the repository root.
            return URL(fileURLWithPath: #filePath)
                .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        #else
            return nil
        #endif
    }

    /// The paths searched, in order.
    public static func candidates(
        bundleURL: URL = Bundle.main.bundleURL,
        environment: [String: String] = ProcessInfo.processInfo.environment,
        debug: Bool = isDebugBuild,
        sourceRoot: URL? = debugSourceRoot
    ) -> [URL] {
        var urls: [URL] = []
        if debug, let bin = environment["SKILLM_BIN"], !bin.isEmpty {
            urls.append(URL(fileURLWithPath: bin))
        }
        urls.append(bundleURL.appending(path: bundledPath))
        if debug, let root = sourceRoot {
            urls.append(root.appending(path: "skillm"))
            urls.append(root.appending(path: "bin/skillm"))
        }
        return urls
    }

    /// The first candidate that is an executable file. Throws
    /// `SkillmError.binaryNotFound`.
    public static func locate(
        bundleURL: URL = Bundle.main.bundleURL,
        environment: [String: String] = ProcessInfo.processInfo.environment,
        debug: Bool = isDebugBuild,
        sourceRoot: URL? = debugSourceRoot
    ) throws -> URL {
        let urls = candidates(bundleURL: bundleURL, environment: environment, debug: debug, sourceRoot: sourceRoot)
        let fm = FileManager.default
        for url in urls {
            var isDir: ObjCBool = false
            if fm.fileExists(atPath: url.path, isDirectory: &isDir), !isDir.boolValue,
                fm.isExecutableFile(atPath: url.path)
            {
                return url
            }
        }
        throw SkillmError.binaryNotFound(searched: urls.map(\.path))
    }
}

/// The environment a skillm child runs with.
public enum ChildEnvironment {
    /// Appended to PATH when missing: a GUI app starts with a minimal PATH
    /// that lacks Homebrew's (and a hand-installed) git.
    public static let extraPath = ["/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"]

    /// `path` with every `extraPath` entry it lacks appended.
    public static func extendedPath(_ path: String?) -> String {
        var entries = (path ?? "").split(separator: ":").map(String.init).filter { !$0.isEmpty }
        for extra in extraPath where !entries.contains(extra) {
            entries.append(extra)
        }
        return entries.joined(separator: ":")
    }

    /// `base` with PATH extended.
    public static func make(from base: [String: String]) -> [String: String] {
        var env = base
        env["PATH"] = extendedPath(base["PATH"])
        return env
    }
}

/// Checks for a usable git before skillm needs one.
///
/// skillm itself only checks that `git` is on PATH (`git_missing`). On a Mac
/// without the Command Line Tools, `/usr/bin/git` still exists but is a stub
/// that fails (and pops up an installer), so that case is judged here too.
public enum GitCheck {
    /// The first executable `git` on `path`, if any.
    public static func find(onPath path: String) -> URL? {
        let fm = FileManager.default
        for dir in path.split(separator: ":") where !dir.isEmpty {
            let url = URL(fileURLWithPath: String(dir)).appending(path: "git")
            if fm.isExecutableFile(atPath: url.path) { return url }
        }
        return nil
    }

    /// Throws `SkillmError.gitMissing` (with the fix in its recovery
    /// suggestion) when no usable git is on `path`. `developerToolsInstalled`
    /// is asked only when the git found is Apple's `/usr/bin/git` stub.
    public static func check(path: String, developerToolsInstalled: () async -> Bool) async throws {
        guard let git = find(onPath: path) else {
            throw SkillmError.gitMissing(detail: "No git was found on PATH (\(path)).")
        }
        if git.path == "/usr/bin/git", !(await developerToolsInstalled()) {
            throw SkillmError.gitMissing(detail: "Apple's Command Line Tools, which provide git, are not installed.")
        }
    }

    /// Whether Apple's developer tools are installed: `xcode-select -p`
    /// succeeds only when a developer directory is set and present. It never
    /// shows the install prompt.
    public static func developerToolsInstalled() async -> Bool {
        let child = ChildProcess(
            executable: URL(fileURLWithPath: "/usr/bin/xcode-select"), arguments: ["-p"], environment: [:])
        do { try child.start() } catch { return false }
        for await _ in child.stdout {}
        return await child.waitForExit() == 0
    }
}
