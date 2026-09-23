import Foundation

/// How far `update --events` (or `install --events`) has got, folded from
/// its events: a `batch` names the skills it fetches, each `item_done` is one
/// more done, and a `progress` event sets the counts outright.
public struct UpdateProgress: Equatable, Sendable {
    /// The skill ids of the current batch; `items[i]` is item i.
    public private(set) var items: [String] = []
    public private(set) var done = 0
    public private(set) var total = 0
    /// The skills started and not yet done, in the order they started.
    public private(set) var running: [String] = []

    public init() {}

    public mutating func apply(_ event: Event) {
        switch event.event {
        case .batch:
            items = event.items ?? []
            total = items.count
            done = 0
            running = []
        case .itemStart:
            if let id = id(of: event), !running.contains(id) { running.append(id) }
        case .itemDone:
            if let id = id(of: event) { running.removeAll { $0 == id } }
            done += 1
            total = max(total, done)
        case .progress:
            if let d = event.done { done = d }
            if let t = event.total { total = t }
        default:
            break
        }
    }

    /// "Updating skills… 1 of 3", or without counts before the first batch.
    public var text: String { text(doing: "Updating skills") }

    /// "<doing>… 1 of 3", or without counts before the first batch. The
    /// events of `install --events` fold the same way.
    public func text(doing: String) -> String {
        total > 0 ? "\(doing)… \(done) of \(total)" : "\(doing)…"
    }

    private func id(of event: Event) -> String? {
        if let id = event.skillId { return id }
        if let i = event.index, items.indices.contains(i) { return items[i] }
        return nil
    }
}
