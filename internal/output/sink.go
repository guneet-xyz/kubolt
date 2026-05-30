package output

import "io"

// EventKind describes what happened.
type EventKind int

const (
	WaveStart EventKind = iota
	WaveEnd
	AppStart
	AppLine
	AppDone
	AppSkip
	AllDone
)

// Event carries data about an install lifecycle event.
type Event struct {
	Kind   EventKind
	Wave   int    // wave index (0-based)
	App    string // app name
	Stream string // "stdout" or "stderr"
	Text   string // line content (AppLine only)
	Err    error  // AppDone failure (nil = success)
	Reason string // AppSkip human-readable reason
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
