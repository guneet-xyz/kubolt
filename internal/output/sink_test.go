package output

import (
	"bytes"
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

func TestNewLineSink_ReturnsSink(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	if sink == nil {
		t.Fatal("NewLineSink returned nil")
	}

	var _ = sink
}
