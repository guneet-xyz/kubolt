package output

import (
	"bytes"
	"testing"
)

func TestNopSink(t *testing.T) {
	sink := NopSink{}

	eventKinds := []EventKind{
		WaveStart,
		WaveEnd,
		AppStart,
		AppLine,
		AppDone,
		AppSkip,
		AllDone,
	}

	for _, kind := range eventKinds {
		// Should not panic
		sink.Emit(Event{Kind: kind})
	}
}

func TestNewLineSink_ReturnsSink(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	if sink == nil {
		t.Fatal("NewLineSink returned nil")
	}

	// Should be a valid Sink
	var _ Sink = sink
}

func TestNewTUISink_ReturnsSink(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewTUISink(w)

	if sink == nil {
		t.Fatal("NewTUISink returned nil")
	}

	// Should be a valid Sink
	var _ Sink = sink
}
