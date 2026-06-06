package output

import (
	"fmt"
	"io"
)

// EventKind describes what happened.
type EventKind int

const (
	AppStart EventKind = iota
	AppLine
	AppDone
	AppSkip
	AllDone
	// Tree-based vocabulary
	NodeReady // node is ready to start (deps completed)
	NodeStart // node execution has started
	NodeLine  // a line of output from the node's helm subprocess
	NodeDone  // node execution completed (check Err for failure)
	NodeSkip  // node skipped due to dep failure (check Reason)
	TreeStart // entire tree execution starting (Count field = total count)
	TreeDone  // entire tree execution finished
)

// Event carries data about an install lifecycle event.
type Event struct {
	Kind    EventKind
	Count   int      // node count for TreeStart
	App     string   // app name
	Stream  string   // "stdout" or "stderr"
	Text    string   // line content (AppLine only)
	Err     error    // AppDone failure (nil = success)
	Reason  string   // AppSkip human-readable reason
	Parents []string // parent names for tree rendering; nil = root or flat-list app
	Stage   string   // backup stage: "scaling-down", "copying", "scaling-up", "restoring"; empty otherwise
}

// String returns the name of the EventKind.
func (k EventKind) String() string {
	switch k {
	case AppStart:
		return "AppStart"
	case AppLine:
		return "AppLine"
	case AppDone:
		return "AppDone"
	case AppSkip:
		return "AppSkip"
	case AllDone:
		return "AllDone"
	case NodeReady:
		return "NodeReady"
	case NodeStart:
		return "NodeStart"
	case NodeLine:
		return "NodeLine"
	case NodeDone:
		return "NodeDone"
	case NodeSkip:
		return "NodeSkip"
	case TreeStart:
		return "TreeStart"
	case TreeDone:
		return "TreeDone"
	default:
		return fmt.Sprintf("EventKind(%d)", int(k))
	}
}

// Sink receives install progress events.
type Sink interface {
	Emit(Event)
}

// NopSink discards all events.
type NopSink struct{}

func (NopSink) Emit(Event) {}

// NewLineSink returns a Sink that writes prefixed lines to w.
// Real implementation is in linesink.go.
func NewLineSink(w io.Writer) Sink {
	return newLineSinkImpl(w)
}
