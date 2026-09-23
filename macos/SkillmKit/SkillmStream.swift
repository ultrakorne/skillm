import Foundation

/// The messages of `SkillmClient.stream`: each event as it happens, then
/// `.result` as the last message. Iterate it once.
///
/// Unlike an `AsyncThrowingStream`, whose iteration ends the moment the
/// consumer's task is cancelled, this sequence keeps delivering after a
/// cancel (which interrupts skillm) until skillm has exited, and then throws
/// `CancellationError`. So a cancelled consumer's loop ends only when skillm
/// is done and no longer holds Home's lock. Dropping the iterator early
/// (`break`) interrupts skillm without waiting for it.
public struct SkillmStream<T: Codable & Sendable>: AsyncSequence, Sendable {
    public typealias Element = StreamMessage<T>

    private let channel: Channel
    /// Interrupts skillm when the last copy of the stream and its iterator
    /// is gone before the end.
    private let consumer: Consumer

    init(channel: Channel) {
        self.channel = channel
        consumer = Consumer(channel: channel)
    }

    public func makeAsyncIterator() -> Iterator {
        Iterator(channel: channel, consumer: consumer)
    }

    public struct Iterator: AsyncIteratorProtocol {
        fileprivate let channel: Channel
        fileprivate let consumer: Consumer

        public mutating func next() async throws -> StreamMessage<T>? {
            try await channel.next()
        }
    }

    /// Holds the messages between the reader of skillm's output and the
    /// consumer.
    final class Channel: @unchecked Sendable {
        private enum End {
            case finished
            case failed(any Error)
        }

        private let lock = NSLock()
        private var buffer: [StreamMessage<T>] = []
        private var end: End?
        private var waiter: CheckedContinuation<StreamMessage<T>?, any Error>?
        private let interruptChild: @Sendable () -> Void

        init(interrupt: @escaping @Sendable () -> Void) {
            interruptChild = interrupt
        }

        /// Queues a message for the consumer.
        func yield(_ message: StreamMessage<T>) {
            lock.lock()
            if let w = waiter {
                waiter = nil
                lock.unlock()
                w.resume(returning: message)
            } else {
                buffer.append(message)
                lock.unlock()
            }
        }

        /// Ends the stream after the queued messages, with `error` if given.
        func finish(throwing error: (any Error)? = nil) {
            lock.lock()
            guard end == nil else {
                lock.unlock()
                return
            }
            end = error.map { .failed($0) } ?? .finished
            let w = waiter
            waiter = nil
            let delivery = w.map { _ in takeEnd() }
            lock.unlock()
            if let w, let delivery { w.resume(with: delivery) }
        }

        /// The next message, nil at the end. Cancelling the calling task
        /// interrupts skillm but does not end the wait.
        func next() async throws -> StreamMessage<T>? {
            try await withTaskCancellationHandler {
                try await withCheckedThrowingContinuation { (c: CheckedContinuation<StreamMessage<T>?, any Error>) in
                    lock.lock()
                    if !buffer.isEmpty {
                        let m = buffer.removeFirst()
                        lock.unlock()
                        c.resume(returning: m)
                    } else if end != nil {
                        let delivery = takeEnd()
                        lock.unlock()
                        c.resume(with: delivery)
                    } else {
                        waiter = c
                        lock.unlock()
                    }
                }
            } onCancel: {
                interrupt()
            }
        }

        /// Interrupts skillm unless the stream has ended.
        func interrupt() {
            lock.lock()
            let running = end == nil
            lock.unlock()
            if running { interruptChild() }
        }

        /// The end to deliver: an error is thrown once, then nil follows.
        /// Call with the lock held.
        private func takeEnd() -> Result<StreamMessage<T>?, any Error> {
            if case .failed(let e) = end {
                end = .finished
                return .failure(e)
            }
            return .success(nil)
        }
    }

    /// Its deinit is the "consumer went away" signal.
    fileprivate final class Consumer: Sendable {
        let channel: Channel
        init(channel: Channel) { self.channel = channel }
        deinit { channel.interrupt() }
    }
}
