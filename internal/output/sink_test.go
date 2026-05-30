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

func TestNewTUISink_ReturnsSink(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewTUISink(w)

	if sink == nil {
		t.Fatal("NewTUISink returned nil")
	}

	var _ = sink
}
