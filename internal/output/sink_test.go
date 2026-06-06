package output

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func TestNopSink(t *testing.T) {
	sink := NopSink{}

	eventKinds := []EventKind{
		AppStart,
		AppLine,
		AppDone,
		AppSkip,
		AllDone,
	}

	for _, kind := range eventKinds {
		sink.Emit(Event{Kind: kind})
	}
}

// TestNopSink_AllEventKinds is a table-driven test that confirms NopSink
// absorbs every EventKind constant without panicking, races, or side effects.
// Covers NodeLine/NodeDone flow added by T5/T6.
func TestNopSink_AllEventKinds(t *testing.T) {
	sink := NopSink{}

	tests := []struct {
		name  string
		event Event
	}{
		{
			name:  "AppStart",
			event: Event{Kind: AppStart, App: "myapp"},
		},
		{
			name:  "AppLine",
			event: Event{Kind: AppLine, App: "myapp", Text: "installing..."},
		},
		{
			name:  "AppDone success",
			event: Event{Kind: AppDone, App: "myapp", Err: nil},
		},
		{
			name:  "AppDone failure",
			event: Event{Kind: AppDone, App: "myapp", Err: errors.New("helm failed")},
		},
		{
			name:  "AppSkip",
			event: Event{Kind: AppSkip, App: "myapp", Reason: "dependency failed"},
		},
		{
			name:  "AllDone",
			event: Event{Kind: AllDone},
		},
		{
			name:  "NodeReady",
			event: Event{Kind: NodeReady, App: "myapp"},
		},
		{
			name:  "NodeStart",
			event: Event{Kind: NodeStart, App: "myapp"},
		},
		{
			name:  "NodeLine short",
			event: Event{Kind: NodeLine, App: "myapp", Text: "output line"},
		},
		{
			name:  "NodeLine long",
			event: Event{Kind: NodeLine, App: "myapp", Text: "this is a very long line of output that should not cause any side effects or allocations in the nop sink implementation " + string(make([]byte, 1000))},
		},
		{
			name:  "NodeDone success",
			event: Event{Kind: NodeDone, App: "myapp", Err: nil},
		},
		{
			name:  "NodeDone failure",
			event: Event{Kind: NodeDone, App: "myapp", Err: errors.New("boom")},
		},
		{
			name:  "NodeSkip",
			event: Event{Kind: NodeSkip, App: "myapp", Reason: "parent failed"},
		},
		{
			name:  "TreeStart",
			event: Event{Kind: TreeStart, Count: 5},
		},
		{
			name:  "TreeDone",
			event: Event{Kind: TreeDone},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic, no data race, no side effects.
			sink.Emit(tt.event)
		})
	}
}

// TestNopSink_Concurrent confirms NopSink is safe under concurrent emission
// from multiple goroutines (no data races, no panics).
func TestNopSink_Concurrent(t *testing.T) {
	sink := NopSink{}
	const goroutines = 10
	const eventsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				sink.Emit(Event{
					Kind: NodeLine,
					App:  "concurrent-test",
					Text: "output from goroutine",
				})
			}
		}(i)
	}

	wg.Wait()
	// If we get here without panicking or a race detector complaint, test passes.
}

func TestNewLineSink_ReturnsSink(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w, false)

	if sink == nil {
		t.Fatal("NewLineSink returned nil")
	}

	var _ = sink
}
