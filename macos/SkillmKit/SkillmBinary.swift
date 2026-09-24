import Foundation

/// Finds the skillm CLI the app runs: the one the user installed. The app
/// carries no CLI of its own; the CLI and the app are released separately
/// and `api_version` keeps them compatible (`SkillmClient.connect`).
///
/// Looked for, in order: `$SKILLM_BIN` (debug builds only, an explicit
/// override), the folders `install.sh` and Homebrew put it in
/// (`installDirectories`), then the user's login shell (`command -v skillm`,
/// bounded by a timeout), for a skillm installed anywhere else on the PATH a
/// terminal sees. The app looks again at every launch check, so a CLI
/// installed or upgraded in a terminal meanwhile is picked up.
public enum SkillmBinary {
    /// Whether this is a debug build.
    public static var isDebugBuild: Bool {
        #if DEBUG
            return true
        #else
            return false
        #endif
    }

    /// Where `install.sh` puts skillm (`/usr/local/bin`, else
    /// `~/.local/bin`), then Homebrew's folder.
    public static func installDirectories(home: String = NSHomeDirectory()) -> [URL] {
        [
            URL(fileURLWithPath: "/usr/local/bin"),
            URL(fileURLWithPath: home).appending(path: ".local/bin"),
            URL(fileURLWithPath: "/opt/homebrew/bin"),
        ]
    }

    /// The paths looked at before the login shell is asked, in order.
    public static func candidates(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        debug: Bool = isDebugBuild,
        directories: [URL] = installDirectories()
    ) -> [URL] {
        var urls: [URL] = []
        if debug, let bin = environment["SKILLM_BIN"], !bin.isEmpty {
            urls.append(URL(fileURLWithPath: bin))
        }
        urls += directories.map { $0.appending(path: "skillm") }
        return urls
    }

    /// Whether `url` is an executable file (a symlink counts when it
    /// resolves to one).
    static func isExecutableFile(_ url: URL) -> Bool {
        let path = url.resolvingSymlinksInPath().path
        var isDir: ObjCBool = false
        return FileManager.default.fileExists(atPath: path, isDirectory: &isDir) && !isDir.boolValue
            && FileManager.default.isExecutableFile(atPath: path)
    }

    /// The first candidate that is an executable file, else the one the
    /// login shell finds (`loginShell` nil skips that). Throws
    /// `SkillmError.binaryNotFound` naming where it looked.
    public static func locate(
        environment: [String: String] = ProcessInfo.processInfo.environment,
        debug: Bool = isDebugBuild,
        directories: [URL] = installDirectories(),
        loginShell: LoginShell? = LoginShell(environment: ProcessInfo.processInfo.environment)
    ) async throws -> URL {
        let urls = candidates(environment: environment, debug: debug, directories: directories)
        if let found = urls.first(where: isExecutableFile) { return found }
        var searched = urls.map(\.path)
        if let loginShell {
            if let found = await loginShell.find("skillm") { return found }
            searched.append("the PATH of \(loginShell.shell.lastPathComponent)")
        }
        throw SkillmError.binaryNotFound(searched: searched)
    }
}

/// Asks the user's login shell where a command is (`command -v`): a GUI app
/// starts with a minimal PATH, while a terminal's PATH comes from the shell's
/// startup files. The shell runs as a login, interactive shell, so both kinds
/// of startup file (`.zprofile` and `.zshrc`, for zsh) are read, with no
/// terminal and stdin closed. A shell that does not answer within `timeout`
/// is stopped and counts as finding nothing.
public struct LoginShell: Sendable {
    /// The shell run.
    public var shell: URL
    public var timeout: Duration
    /// The shell's environment.
    public var environment: [String: String]

    public init(shell: URL, timeout: Duration = .seconds(5), environment: [String: String] = [:]) {
        self.shell = shell
        self.timeout = timeout
        self.environment = environment
    }

    /// The user's shell: `$SHELL`, else the account's shell, else zsh.
    public init(environment: [String: String], timeout: Duration = .seconds(5)) {
        var path = environment["SHELL"] ?? ""
        if path.isEmpty, let entry = getpwuid(getuid()), let shell = entry.pointee.pw_shell {
            path = String(cString: shell)
        }
        if path.isEmpty { path = "/bin/zsh" }
        self.init(shell: URL(fileURLWithPath: path), timeout: timeout, environment: environment)
    }

    /// The executable `command -v <name>` names, or nil when the shell
    /// names none, names something that is not an executable file, fails to
    /// start or takes longer than `timeout`. Startup files may print their
    /// own lines: the answer is the last line that is an absolute path.
    public func find(_ name: String) async -> URL? {
        let child = ChildProcess(
            executable: shell, arguments: ["-l", "-i", "-c", "command -v \(name)"], environment: environment)
        do { try child.start() } catch { return nil }
        let output = OutputBuffer()
        let answered = await withTaskGroup(of: Bool.self) { group in
            group.addTask {
                for await chunk in child.stdout { output.append(chunk) }
                return !Task.isCancelled
            }
            // A background process the startup files left running may hold
            // stdout open after the shell has exited.
            group.addTask {
                _ = await child.waitForExit()
                return !Task.isCancelled
            }
            // An interactive shell ignores SIGINT and SIGTERM: SIGKILL.
            group.addTask {
                do { try await Task.sleep(for: timeout) } catch { return false }
                child.kill()
                return false
            }
            let first = await group.next() ?? false
            group.cancelAll()
            return first
        }
        guard answered else { return nil }
        let lines = String(decoding: output.data, as: UTF8.self).split(whereSeparator: \.isNewline)
        guard let path = lines.map({ $0.trimmingCharacters(in: .whitespaces) }).last(where: { $0.hasPrefix("/") })
        else { return nil }
        let url = URL(fileURLWithPath: path)
        return SkillmBinary.isExecutableFile(url) ? url : nil
    }
}

/// Bytes collected from another task.
private final class OutputBuffer: @unchecked Sendable {
    private let lock = NSLock()
    private var buffer = Data()

    func append(_ chunk: Data) {
        lock.lock()
        defer { lock.unlock() }
        buffer.append(chunk)
    }

    var data: Data {
        lock.lock()
        defer { lock.unlock() }
        return buffer
    }
}

/// The environment a skillm child runs with.
public enum ChildEnvironment {
    /// Put ahead of PATH when missing, in this order, as a login shell with
    /// Homebrew set up would have them: a GUI app starts with the minimal
    /// `/usr/bin:/bin:/usr/sbin:/sbin`, where Apple's `/usr/bin/git` (a stub
    /// without the Command Line Tools) would always win over Homebrew's git.
    public static let preferredPath = ["/opt/homebrew/bin", "/usr/local/bin"]
    /// Appended to PATH when missing.
    public static let fallbackPath = ["/usr/bin"]

    /// `path` with the `preferredPath` entries it lacks put in front and the
    /// `fallbackPath` entries it lacks appended.
    public static func extendedPath(_ path: String?) -> String {
        let entries = (path ?? "").split(separator: ":").map(String.init).filter { !$0.isEmpty }
        let front = preferredPath.filter { !entries.contains($0) }
        let back = fallbackPath.filter { !entries.contains($0) }
        return (front + entries + back).joined(separator: ":")
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
