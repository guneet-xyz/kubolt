// Package output: NodeBuffer is a thread-safe, bounded per-node capture buffer
// that retains the most recent bytes when a configurable cap is exceeded.
// Older bytes are elided from the head; the elided byte count is tracked so
// callers can report truncation to the user (e.g. "[... N bytes elided ...]").
package output

import "sync"

// defaultNodeBufferCap is the default capacity (1 MiB) used when a non-positive
// cap is passed to NewNodeBuffer.
const defaultNodeBufferCap = 1 << 20

// NodeBuffer is a bounded, line-aware capture buffer that retains the most
// recent bytes written via WriteLine. When the buffer exceeds its cap, the
// oldest bytes are elided from the head and the elided count is recorded.
//
// All methods are safe for concurrent use by multiple goroutines.
type NodeBuffer struct {
	mu        sync.Mutex
	buf       []byte
	cap       int   // max bytes; default 1 MiB
	truncated int64 // total bytes elided due to cap
}

// NewNodeBuffer returns a NodeBuffer with the given capacity in bytes. If
// capBytes is non-positive, a default cap of 1 MiB is used.
func NewNodeBuffer(capBytes int) *NodeBuffer {
	if capBytes <= 0 {
		capBytes = defaultNodeBufferCap
	}
	return &NodeBuffer{cap: capBytes}
}

// WriteLine appends text plus a trailing newline to the buffer. If the buffer
// would exceed its cap after the append, the oldest bytes are elided from the
// head so that only the most recent cap bytes remain. Elided byte counts
// accumulate in the truncation counter, observable via TruncatedBytes.
func (b *NodeBuffer) WriteLine(text string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = append(b.buf, text...)
	b.buf = append(b.buf, '\n')

	if len(b.buf) > b.cap {
		excess := len(b.buf) - b.cap
		b.truncated += int64(excess)
		b.buf = append(b.buf[:0], b.buf[excess:]...)
	}
}

// Bytes returns a snapshot copy of the buffer contents. The returned slice is
// independent of the internal buffer, so the caller may use it freely without
// holding any lock.
func (b *NodeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	return out
}

// TruncatedBytes returns the total number of bytes that have been elided from
// the head of the buffer due to the capacity cap.
func (b *NodeBuffer) TruncatedBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.truncated
}

// Reset clears the buffer contents and resets the truncation counter.
func (b *NodeBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = b.buf[:0]
	b.truncated = 0
}
