package output

import (
	"bytes"
	"sync"
)

// NodeLineWriter is a per-node line-buffering io.Writer that emits NodeLine events into a Sink.
type NodeLineWriter struct {
	sink    Sink
	app     string
	parents []string
	stream  string

	mu  sync.Mutex
	buf bytes.Buffer
}

// NewNodeLineWriter returns a new line-buffering writer for a node.
func NewNodeLineWriter(sink Sink, app string, parents []string, stream string) *NodeLineWriter {
	return &NodeLineWriter{sink: sink, app: app, parents: parents, stream: stream}
}

// Write appends p to the buffer, splits on newlines, and emits complete lines as NodeLine events.
// Lock is released before calling sink.Emit to avoid deadlock.
func (w *NodeLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf.Write(p)
	var lines []string
	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := string(data[:idx])
		next := make([]byte, len(data)-idx-1)
		copy(next, data[idx+1:])
		w.buf.Reset()
		w.buf.Write(next)
		lines = append(lines, line)
	}
	w.mu.Unlock()

	for _, line := range lines {
		w.sink.Emit(Event{
			Kind:    NodeLine,
			App:     w.app,
			Parents: w.parents,
			Stream:  w.stream,
			Text:    line,
		})
	}
	return len(p), nil
}

// Flush emits any buffered partial line as a NodeLine event.
func (w *NodeLineWriter) Flush() {
	w.mu.Lock()
	if w.buf.Len() == 0 {
		w.mu.Unlock()
		return
	}
	text := w.buf.String()
	w.buf.Reset()
	w.mu.Unlock()

	w.sink.Emit(Event{
		Kind:    NodeLine,
		App:     w.app,
		Parents: w.parents,
		Stream:  w.stream,
		Text:    text,
	})
}
