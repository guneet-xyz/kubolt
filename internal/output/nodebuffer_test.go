package output

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestNodeBuffer_BasicWriteAndRead(t *testing.T) {
	tests := []struct {
		name      string
		capBytes  int
		writes    []string
		wantBytes string
		wantTrunc int64
	}{
		{
			name:      "single line under cap",
			capBytes:  1024,
			writes:    []string{"hello"},
			wantBytes: "hello\n",
			wantTrunc: 0,
		},
		{
			name:      "multiple lines total under cap",
			capBytes:  1024,
			writes:    []string{"alpha", "beta", "gamma"},
			wantBytes: "alpha\nbeta\ngamma\n",
			wantTrunc: 0,
		},
		{
			name:      "empty line",
			capBytes:  1024,
			writes:    []string{""},
			wantBytes: "\n",
			wantTrunc: 0,
		},
		{
			name:      "exact cap fit",
			capBytes:  6,
			writes:    []string{"hello"},
			wantBytes: "hello\n",
			wantTrunc: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := NewNodeBuffer(tc.capBytes)
			for _, w := range tc.writes {
				b.WriteLine(w)
			}
			got := b.Bytes()
			if string(got) != tc.wantBytes {
				t.Errorf("Bytes() = %q, want %q", string(got), tc.wantBytes)
			}
			if gotTrunc := b.TruncatedBytes(); gotTrunc != tc.wantTrunc {
				t.Errorf("TruncatedBytes() = %d, want %d", gotTrunc, tc.wantTrunc)
			}
		})
	}
}

func TestNodeBuffer_HeadTruncation(t *testing.T) {
	b := NewNodeBuffer(20)
	b.WriteLine("0123456789") // 11 bytes
	b.WriteLine("abcdefghij") // 11 bytes; total 22, exceeds cap of 20

	got := b.Bytes()
	if len(got) > 20 {
		t.Fatalf("buffer length %d exceeds cap 20", len(got))
	}

	// The most recent line "abcdefghij\n" must remain readable.
	if !bytes.Contains(got, []byte("abcdefghij\n")) {
		t.Errorf("expected most recent line preserved; got %q", string(got))
	}

	if trunc := b.TruncatedBytes(); trunc <= 0 {
		t.Errorf("TruncatedBytes() = %d, want > 0", trunc)
	}

	// The earliest bytes must have been elided.
	if bytes.HasPrefix(got, []byte("0123456789")) {
		t.Errorf("expected head bytes to be elided; got %q", string(got))
	}
}

func TestNodeBuffer_MultipleTruncations(t *testing.T) {
	b := NewNodeBuffer(10)
	for range 100 {
		b.WriteLine("xxxxx") // 6 bytes per write; many will trigger truncation
	}
	got := b.Bytes()
	if len(got) > 10 {
		t.Fatalf("buffer length %d exceeds cap 10", len(got))
	}
	if trunc := b.TruncatedBytes(); trunc <= 0 {
		t.Errorf("TruncatedBytes() = %d, want > 0", trunc)
	}
	// After many truncations, total truncated should be roughly (100 * 6) - cap.
	expectedMin := int64(100*6) - int64(10) - 6 // allow small slack
	if trunc := b.TruncatedBytes(); trunc < expectedMin {
		t.Errorf("TruncatedBytes() = %d, want >= %d", trunc, expectedMin)
	}
}

func TestNodeBuffer_Reset(t *testing.T) {
	b := NewNodeBuffer(4)
	b.WriteLine("aaaaaaaaaa") // exceeds cap, will set truncated
	if b.TruncatedBytes() == 0 {
		t.Fatal("precondition: expected truncation before reset")
	}
	if len(b.Bytes()) == 0 {
		t.Fatal("precondition: expected non-empty buffer before reset")
	}

	b.Reset()

	if got := b.Bytes(); len(got) != 0 {
		t.Errorf("Bytes() after Reset = %q, want empty", string(got))
	}
	if got := b.TruncatedBytes(); got != 0 {
		t.Errorf("TruncatedBytes() after Reset = %d, want 0", got)
	}

	// Buffer is reusable after reset.
	b.WriteLine("hi")
	if got := b.Bytes(); string(got) != "hi\n" {
		t.Errorf("Bytes() after Reset+WriteLine = %q, want %q", string(got), "hi\n")
	}
}

func TestNodeBuffer_DefaultCap(t *testing.T) {
	tests := []struct {
		name     string
		capBytes int
	}{
		{"zero uses default", 0},
		{"negative uses default", -1},
		{"large negative uses default", -9999},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := NewNodeBuffer(tc.capBytes)
			if b.cap != 1<<20 {
				t.Errorf("cap = %d, want %d", b.cap, 1<<20)
			}
		})
	}
}

func TestNodeBuffer_CustomCapPreserved(t *testing.T) {
	b := NewNodeBuffer(512)
	if b.cap != 512 {
		t.Errorf("cap = %d, want 512", b.cap)
	}
}

func TestNodeBuffer_ConcurrentWriters(t *testing.T) {
	const (
		writers       = 16
		writesPerGoro = 200
	)

	b := NewNodeBuffer(4096)
	var wg sync.WaitGroup
	wg.Add(writers)

	for i := range writers {
		go func(id int) {
			defer wg.Done()
			line := strings.Repeat("g", 8) // ASCII; safely UTF-8
			for range writesPerGoro {
				b.WriteLine(line)
			}
		}(i)
	}
	wg.Wait()

	got := b.Bytes()
	if !utf8.Valid(got) {
		t.Errorf("Bytes() returned invalid UTF-8 (torn writes detected)")
	}
	if len(got) > 4096 {
		t.Errorf("buffer length %d exceeds cap 4096", len(got))
	}
	// Total bytes written = writers * writesPerGoro * 9 = 28800; cap = 4096
	// So truncation must have occurred.
	if trunc := b.TruncatedBytes(); trunc <= 0 {
		t.Errorf("TruncatedBytes() = %d, want > 0", trunc)
	}
}

func TestNodeBuffer_BytesIsSnapshot(t *testing.T) {
	b := NewNodeBuffer(1024)
	b.WriteLine("first")
	snap := b.Bytes()
	b.WriteLine("second")

	// The earlier snapshot must be unchanged by subsequent writes.
	if string(snap) != "first\n" {
		t.Errorf("snapshot mutated: got %q, want %q", string(snap), "first\n")
	}
	// Mutating the snapshot must not affect the buffer.
	if len(snap) > 0 {
		snap[0] = 'X'
	}
	if got := b.Bytes(); !bytes.HasPrefix(got, []byte("first\n")) {
		t.Errorf("internal buffer mutated by snapshot edit: got %q", string(got))
	}
}
