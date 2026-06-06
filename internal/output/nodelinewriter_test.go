package output

import (
	"fmt"
	"sync"
	"testing"
)

// recordSink captures all emitted events for testing.
type recordSink struct {
	mu     sync.Mutex
	events []Event
}

func newRecordSink() *recordSink {
	return &recordSink{events: []Event{}}
}

func (rs *recordSink) Emit(e Event) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.events = append(rs.events, e)
}

func (rs *recordSink) Events() []Event {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	// Return a copy to avoid races
	out := make([]Event, len(rs.events))
	copy(out, rs.events)
	return out
}

// TestNodeLineWriter_SingleLineWrite tests writing a single line ending in \n.
func TestNodeLineWriter_SingleLineWrite(t *testing.T) {
	sink := newRecordSink()
	w := NewNodeLineWriter(sink, "app1", []string{"parent1"}, "stdout")

	n, err := w.Write([]byte("hello\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 6 {
		t.Errorf("expected 6 bytes written, got %d", n)
	}

	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Kind != NodeLine {
		t.Errorf("expected NodeLine, got %v", e.Kind)
	}
	if e.App != "app1" {
		t.Errorf("expected app=app1, got %q", e.App)
	}
	if len(e.Parents) != 1 || e.Parents[0] != "parent1" {
		t.Errorf("expected parents=[parent1], got %v", e.Parents)
	}
	if e.Stream != "stdout" {
		t.Errorf("expected stream=stdout, got %q", e.Stream)
	}
	if e.Text != "hello" {
		t.Errorf("expected text=hello, got %q", e.Text)
	}
}

// TestNodeLineWriter_MultiLineWrite tests writing multiple complete lines in one buffer.
func TestNodeLineWriter_MultiLineWrite(t *testing.T) {
	sink := newRecordSink()
	w := NewNodeLineWriter(sink, "app2", nil, "stderr")

	n, err := w.Write([]byte("line1\nline2\nline3\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 18 {
		t.Errorf("expected 18 bytes written, got %d", n)
	}

	events := sink.Events()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	expectedLines := []string{"line1", "line2", "line3"}
	for i, expected := range expectedLines {
		if events[i].Text != expected {
			t.Errorf("event %d: expected text=%q, got %q", i, expected, events[i].Text)
		}
		if events[i].App != "app2" {
			t.Errorf("event %d: expected app=app2, got %q", i, events[i].App)
		}
		if events[i].Stream != "stderr" {
			t.Errorf("event %d: expected stream=stderr, got %q", i, events[i].Stream)
		}
		if events[i].Parents != nil {
			t.Errorf("event %d: expected parents=nil, got %v", i, events[i].Parents)
		}
	}
}

// TestNodeLineWriter_PartialLineNoFlush tests buffering a partial line (no \n).
func TestNodeLineWriter_PartialLineNoFlush(t *testing.T) {
	sink := newRecordSink()
	w := NewNodeLineWriter(sink, "app3", []string{"p1", "p2"}, "stdout")

	// Write partial line (no newline)
	n, err := w.Write([]byte("incomplete"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 10 {
		t.Errorf("expected 10 bytes written, got %d", n)
	}

	// No events emitted yet
	events := sink.Events()
	if len(events) != 0 {
		t.Errorf("expected 0 events (partial line), got %d", len(events))
	}

	// Append to the partial line and complete it
	n, err = w.Write([]byte(" line\n"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 6 {
		t.Errorf("expected 6 bytes written, got %d", n)
	}

	events = sink.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event after completing line, got %d", len(events))
	}

	if events[0].Text != "incomplete line" {
		t.Errorf("expected text=incomplete line, got %q", events[0].Text)
	}
}

// TestNodeLineWriter_FlushEmitsPartialLine tests Flush emits trailing partial line.
func TestNodeLineWriter_FlushEmitsPartialLine(t *testing.T) {
	sink := newRecordSink()
	w := NewNodeLineWriter(sink, "app4", nil, "stdout")

	// Write partial line
	_, err := w.Write([]byte("trailing"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// No events yet
	events := sink.Events()
	if len(events) != 0 {
		t.Errorf("expected 0 events before flush, got %d", len(events))
	}

	// Flush should emit the partial line
	w.Flush()

	events = sink.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event after flush, got %d", len(events))
	}

	if events[0].Text != "trailing" {
		t.Errorf("expected text=trailing, got %q", events[0].Text)
	}
	if events[0].Kind != NodeLine {
		t.Errorf("expected NodeLine, got %v", events[0].Kind)
	}
}

// TestNodeLineWriter_FlushEmptyBuffer tests Flush with empty buffer is no-op.
func TestNodeLineWriter_FlushEmptyBuffer(t *testing.T) {
	sink := newRecordSink()
	w := NewNodeLineWriter(sink, "app5", nil, "stdout")

	// Flush empty buffer
	w.Flush()

	events := sink.Events()
	if len(events) != 0 {
		t.Errorf("expected 0 events after flush on empty buffer, got %d", len(events))
	}
}

// TestNodeLineWriter_MultipleWritersDistinctApps tests two writers with different apps.
func TestNodeLineWriter_MultipleWritersDistinctApps(t *testing.T) {
	sink := newRecordSink()
	w1 := NewNodeLineWriter(sink, "appA", []string{"pA"}, "stdout")
	w2 := NewNodeLineWriter(sink, "appB", []string{"pB"}, "stderr")

	_, _ = w1.Write([]byte("from A\n"))
	_, _ = w2.Write([]byte("from B\n"))

	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].App != "appA" || events[0].Text != "from A" {
		t.Errorf("event 0: expected appA/from A, got %q/%q", events[0].App, events[0].Text)
	}
	if events[0].Parents[0] != "pA" {
		t.Errorf("event 0: expected parents=[pA], got %v", events[0].Parents)
	}

	if events[1].App != "appB" || events[1].Text != "from B" {
		t.Errorf("event 1: expected appB/from B, got %q/%q", events[1].App, events[1].Text)
	}
	if events[1].Stream != "stderr" {
		t.Errorf("event 1: expected stderr, got %q", events[1].Stream)
	}
}

// TestNodeLineWriter_ConcurrentWrites tests concurrent writes from multiple goroutines.
func TestNodeLineWriter_ConcurrentWrites(t *testing.T) {
	sink := newRecordSink()
	w := NewNodeLineWriter(sink, "concurrent", nil, "stdout")

	var wg sync.WaitGroup
	numGoroutines := 10
	linesPerGoroutine := 50

	wg.Add(numGoroutines)
	for g := 0; g < numGoroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < linesPerGoroutine; i++ {
				line := fmt.Sprintf("goroutine %d line %d\n", g, i)
				_, err := w.Write([]byte(line))
				if err != nil {
					t.Errorf("Write failed: %v", err)
				}
			}
		}(g)
	}

	wg.Wait()

	// All lines should be complete (ending in \n), so all should emit
	events := sink.Events()
	expected := numGoroutines * linesPerGoroutine
	if len(events) != expected {
		t.Errorf("expected %d events, got %d", expected, len(events))
	}

	// Verify all events are NodeLine kind
	for i, e := range events {
		if e.Kind != NodeLine {
			t.Errorf("event %d: expected NodeLine, got %v", i, e.Kind)
		}
	}
}

// TestNodeLineWriter_MixedCompleteAndPartial tests mix of complete and partial lines.
func TestNodeLineWriter_MixedCompleteAndPartial(t *testing.T) {
	sink := newRecordSink()
	w := NewNodeLineWriter(sink, "mixed", nil, "stdout")

	// Write: complete + partial
	_, _ = w.Write([]byte("complete1\nincomplete"))
	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event after first write, got %d", len(events))
	}
	if events[0].Text != "complete1" {
		t.Errorf("expected text=complete1, got %q", events[0].Text)
	}

	// Append and complete
	_, _ = w.Write([]byte("line\n"))
	events = sink.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[1].Text != "incompleteline" {
		t.Errorf("expected text=incompleteline, got %q", events[1].Text)
	}

	// Flush empty (should be no-op now)
	w.Flush()
	events = sink.Events()
	if len(events) != 2 {
		t.Errorf("expected 2 events after flush, got %d", len(events))
	}
}
