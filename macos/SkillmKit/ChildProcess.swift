import Foundation

/// One launched skillm process: its stdout as a stream of chunks, its stderr
/// collected (the last 64 KiB), and its exit status. Arguments go straight to
/// the executable as an array; no shell is involved.
///
/// Waiting for the exit does not depend on the waiting task: a cancelled
/// caller still waits until the child has really exited, so whatever the
/// child was writing (the Registry, `status.json`) is finished when the
/// caller goes on. Read `stdout` from a task that is not cancelled (an
/// `AsyncStream` ends as soon as its reader's task is cancelled).
final class ChildProcess: @unchecked Sendable {
    /// stdout, chunk by chunk; finishes at EOF.
    let stdout: AsyncStream<Data>

    private let process = Process()
    private let stdoutContinuation: AsyncStream<Data>.Continuation
    private let exited = Latch<Int32>()
    private let stderrClosed = Latch<Void>()
    private let lock = NSLock()
    private var stderrBuffer = Data()
    private var interrupted = false
    private static let stderrLimit = 64 * 1024
    /// How long `waitForExit` waits, after the exit, for stderr's EOF: a
    /// grandchild that inherited the pipe must not hold the caller forever.
    private static let stderrDrainLimit: DispatchTimeInterval = .seconds(2)

    init(executable: URL, arguments: [String], environment: [String: String]) {
        process.executableURL = executable
        process.arguments = arguments
        process.environment = environment
        process.standardInput = FileHandle.nullDevice
        (stdout, stdoutContinuation) = AsyncStream.makeStream(of: Data.self)
    }

    /// Starts the process. Throws `SkillmError.launchFailed`.
    func start() throws {
        let out = Pipe()
        let err = Pipe()
        process.standardOutput = out
        process.standardError = err
        let exited = self.exited
        process.terminationHandler = { p in exited.set(p.terminationStatus) }
        do {
            try process.run()
        } catch {
            stdoutContinuation.finish()
            exited.set(-1)
            stderrClosed.set(())
            throw SkillmError.launchFailed(path: process.executableURL?.path ?? "?", reason: error.localizedDescription)
        }
        let stdoutContinuation = self.stdoutContinuation
        Self.drain(out.fileHandleForReading) { stdoutContinuation.yield($0) } done: { stdoutContinuation.finish() }
        // Held strongly until EOF, when the drain thread ends.
        let stderrClosed = self.stderrClosed
        Self.drain(err.fileHandleForReading) { self.appendStderr($0) } done: { stderrClosed.set(()) }
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

    /// What the process wrote to stderr (the tail, trimmed): all of it once
    /// `waitForExit` has returned.
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
    /// `grace`, SIGTERM follows. skillm does not catch SIGTERM, so that
    /// kills it on the spot: keep `grace` long, a guard against a hung
    /// child rather than part of an ordinary cancel.
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

    /// Kills the process (SIGKILL), for a child that is not skillm and
    /// ignores the other signals (an interactive shell).
    func kill() {
        guard process.isRunning else { return }
        Darwin.kill(process.processIdentifier, SIGKILL)
    }

    /// Waits until the process has exited and its stderr is read to the end,
    /// then returns its exit status. The wait goes on even if the calling
    /// task is cancelled.
    func waitForExit() async -> Int32 {
        let status = await exited.wait()
        let stderrClosed = self.stderrClosed
        DispatchQueue.global().asyncAfter(deadline: .now() + Self.stderrDrainLimit) { stderrClosed.set(()) }
        await stderrClosed.wait()
        return status
    }
}

/// A value set once. Waiting for it does not depend on the waiting task's
/// cancellation (unlike iterating an `AsyncStream`).
final class Latch<Value: Sendable>: @unchecked Sendable {
    private let lock = NSLock()
    private var value: Value?
    private var waiters: [CheckedContinuation<Value, Never>] = []

    /// Sets the value and wakes every waiter; later calls do nothing.
    func set(_ newValue: Value) {
        lock.lock()
        guard value == nil else {
            lock.unlock()
            return
        }
        value = newValue
        let waiting = waiters
        waiters = []
        lock.unlock()
        for w in waiting { w.resume(returning: newValue) }
    }

    /// The value, once it is set.
    func wait() async -> Value {
        await withCheckedContinuation { (c: CheckedContinuation<Value, Never>) in
            lock.lock()
            if let value {
                lock.unlock()
                c.resume(returning: value)
            } else {
                waiters.append(c)
                lock.unlock()
            }
        }
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
