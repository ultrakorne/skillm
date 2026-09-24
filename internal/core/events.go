package core

import "sync"

// Reporter receives an operation's progress as Events. Core serializes its
// calls, so an implementation never sees two Events at once, but the calls
// may come from different goroutines.
type Reporter interface {
	Event(Event)
}

// EventType says what an Event describes. The values are stable strings so a
// protocol layer can emit them verbatim.
type EventType string

const (
	// EventLog is a standalone message, e.g. a warning about one skill.
	EventLog EventType = "log"
	// EventBatch announces the items an operation is about to process, in
	// order: Items[i] is the skill id of the item with Index i. It comes
	// before any ItemStart, so a renderer can lay out every row up front.
	EventBatch EventType = "batch"
	// EventItemStart marks the start of the work on item Index.
	EventItemStart EventType = "item_start"
	// EventItemDone marks the end of the work on item Index; Level, Code and
	// Text carry its outcome. An item interrupted by cancellation gets none.
	EventItemDone EventType = "item_done"
	// EventProgress reports Done out of Total units of work.
	EventProgress EventType = "progress"
)

// Level is an Event's severity.
type Level string

const (
	LevelInfo    Level = "info"
	LevelSuccess Level = "success"
	LevelWarn    Level = "warn"
	LevelError   Level = "error"
)

// Event is one progress notification from an operation. Which fields are set
// depends on Type.
type Event struct {
	Type  EventType
	Level Level
	// Index is the item index for ItemStart and ItemDone.
	Index int
	// Skill is the skill id, when the event concerns one.
	Skill string
	// Code is a stable machine code, e.g. "update_available".
	Code string
	// Text is the human sentence for the event. It is what the CLI prints,
	// unless the operation says otherwise (Check's code "error" names the
	// cause where the CLI keeps its historic "untracked" line).
	Text string
	// Items lists the skill ids of an EventBatch.
	Items []string
	// Done and Total are an EventProgress's counts.
	Done, Total int
}

// NopReporter discards every Event.
type NopReporter struct{}

// Event implements Reporter.
func (NopReporter) Event(Event) {}

// serialized wraps rep so concurrent workers can report through it: each
// Event call runs under one mutex. A nil rep becomes a NopReporter.
func serialized(rep Reporter) Reporter {
	if rep == nil {
		return NopReporter{}
	}
	return &lockedReporter{rep: rep}
}

type lockedReporter struct {
	mu  sync.Mutex
	rep Reporter
}

func (r *lockedReporter) Event(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rep.Event(ev)
}
