import Foundation

/// One launched skillm process: its stdout as a stream of chunks, its stderr
/// collected (the last 64 KiB), and its exit status. Arguments go straight to
/// the executable as an array; no shell is involved.
final class ChildProcess: @unchecked Sendable {
    /// stdout, chunk by chunk; finishes at EOF.
    let stdout: AsyncStream<Data>

    private let process = Process()
    private let stdoutContinuation: AsyncStream<Data>.Continuation
    private let exit: AsyncStream<Int32>
    private let exitContinuation: AsyncStream<Int32>.Continuation
    private let lock = NSLock()
    private var stderrBuffer = Data()
    private var interrupted = false
    private static let stderrLimit = 64 * 1024

    init(executable: URL, arguments: [String], environment: [String: String]) {
        process.executableURL = executable
        process.arguments = arguments
        process.environment = environment
        process.standardInput = FileHandle.nullDevice
        (stdout, stdoutContinuation) = AsyncStream.makeStream(of: Data.self)
        (exit, exitContinuation) = AsyncStream.makeStream(of: Int32.self, bufferingPolicy: .bufferingNewest(1))
    }

    /// Starts the process. Throws `SkillmError.launchFailed`.
    func start() throws {
        let out = Pipe()
        let err = Pipe()
        process.standardOutput = out
        process.standardError = err
        let exitContinuation = self.exitContinuation
        process.terminationHandler = { p in
            exitContinuation.yield(p.terminationStatus)
            exitContinuation.finish()
        }
        do {
            try process.run()
        } catch {
            stdoutContinuation.finish()
            exitContinuation.finish()
            throw SkillmError.launchFailed(path: process.executableURL?.path ?? "?", reason: error.localizedDescription)
        }
        let stdoutContinuation = self.stdoutContinuation
        Self.drain(out.fileHandleForReading) { stdoutContinuation.yield($0) } done: { stdoutContinuation.finish() }
        Self.drain(err.fileHandleForReading) { [weak self] in self?.appendStderr($0) } done: {}
    }

    /// Reads `handle` to EOF on its own thread, so a full pipe never blocks
    /// the child and EOF is always seen. Each chunk is delivered as soon as
    /// it arrives: `read(2)` returns what the pipe holds, whereas
    /// `FileHandle.read(upToCount:)` waits for the full count or EOF, which
    /// would hold an event stream back until the command ends.
    private static func drain(
        _ handle: FileHandle, chunk: @escaping @Sendable (Data) -> Void, done: @escaping @Sendable () -> Void
    ) {
        let t = Thread {
            let fd = handle.fileDescriptor
            let size = 64 * 1024
            let buffer = UnsafeMutableRawPointer.allocate(byteCount: size, alignment: 1)
            defer { buffer.deallocate() }
            while true {
                let n = read(fd, buffer, size)
                if n > 0 {
                    chunk(Data(bytes: buffer, count: n))
                } else if n < 0, errno == EINTR {
                    continue
                } else {
                    break  // EOF, or a read error: either way nothing more comes.
                }
            }
            try? handle.close()
            done()
        }
        t.name = "skillm-pipe"
        t.start()
    }

    private func appendStderr(_ data: Data) {
        lock.lock()
        defer { lock.unlock() }
        stderrBuffer.append(data)
        if stderrBuffer.count > Self.stderrLimit {
            stderrBuffer.removeFirst(stderrBuffer.count - Self.stderrLimit)
        }
    }

    /// What the process wrote to stderr so far (the tail, trimmed).
    var stderr: String {
        lock.lock()
        defer { lock.unlock() }
        return String(decoding: stderrBuffer, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Whether `interrupt` was called.
    var wasInterrupted: Bool {
        lock.lock()
        defer { lock.unlock() }
        return interrupted
    }

    /// Asks the process to stop: SIGINT, which skillm answers by cancelling
    /// and writing a "cancelled" result. If it is still running after
    /// `grace`, SIGTERM follows.
    func interrupt(grace: Duration) {
        lock.lock()
        let first = !interrupted
        interrupted = true
        lock.unlock()
        guard first, process.isRunning else { return }
        process.interrupt()
        let process = self.process
        Task.detached {
            try? await Task.sleep(for: grace)
            if process.isRunning { process.terminate() }
        }
    }

    /// Waits for the process to exit and returns its status. Call it once.
    func waitForExit() async -> Int32 {
        for await status in exit { return status }
        return process.isRunning ? -1 : process.terminationStatus
    }
}

/// Splits a byte stream into newline-terminated lines (NDJSON).
struct LineSplitter {
    private var buffer = Data()

    /// Appends `chunk` and returns the lines it completed, without their
    /// newline; blank lines are dropped.
    mutating func append(_ chunk: Data) -> [Data] {
        buffer.append(chunk)
        var lines: [Data] = []
        while let nl = buffer.firstIndex(of: 0x0A) {
            let line = buffer[buffer.startIndex..<nl]
            buffer.removeSubrange(buffer.startIndex...nl)
            if !Self.isBlank(line) { lines.append(Data(line)) }
        }
        return lines
    }

    /// The unterminated last line, if any.
    mutating func finish() -> Data? {
        defer { buffer = Data() }
        return Self.isBlank(buffer) ? nil : buffer
    }

    private static func isBlank(_ d: Data) -> Bool {
        d.allSatisfy { $0 == 0x20 || $0 == 0x09 || $0 == 0x0D || $0 == 0x0A }
    }
}
