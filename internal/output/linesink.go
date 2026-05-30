package output

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"
)

// LineSink writes human-readable prefixed output to an io.Writer.
// Safe for concurrent use. No ANSI escape codes by default.
type LineSink struct {
	mu       sync.Mutex
	w        io.Writer
	appStart map[string]time.Time // track elapsed per app
	appBuf   map[string][]byte    // partial-line buffer per app
}

// NewLineSink returns a Sink that writes prefixed lines to w.
// Lines are buffered per app and flushed atomically to prevent tearing.
func newLineSinkImpl(w io.Writer) *LineSink {
	return &LineSink{
		w:        w,
		appStart: make(map[string]time.Time),
		appBuf:   make(map[string][]byte),
	}
}

// Emit writes an event to the sink in a thread-safe manner.
func (s *LineSink) Emit(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch e.Kind {
	case WaveStart:
		fmt.Fprintf(s.w, "=== Wave %d starting ===\n", e.Wave+1)

	case AppStart:
		s.appStart[e.App] = time.Now()
		fmt.Fprintf(s.w, "[%s] starting\n", e.App)

	case AppLine:
		// Buffer partial lines per app; flush complete lines
		s.appBuf[e.App] = append(s.appBuf[e.App], []byte(e.Text)...)
		s.flushLines(e.App)

	case AppDone:
		// Flush any remaining buffered content
		if len(s.appBuf[e.App]) > 0 {
			fmt.Fprintf(s.w, "[%s] %s\n", e.App, string(s.appBuf[e.App]))
			delete(s.appBuf, e.App)
		}
		elapsed := time.Since(s.appStart[e.App]).Truncate(time.Millisecond)
		if e.Err != nil {
			fmt.Fprintf(s.w, "[%s] FAILED in %s: %v\n", e.App, elapsed, e.Err)
		} else {
			fmt.Fprintf(s.w, "[%s] OK in %s\n", e.App, elapsed)
		}
		delete(s.appStart, e.App)

	case AppSkip:
		fmt.Fprintf(s.w, "[%s] SKIPPED: %s\n", e.App, e.Reason)

	case WaveEnd:
		fmt.Fprintf(s.w, "=== Wave %d done ===\n", e.Wave+1)

	case AllDone:
		fmt.Fprintf(s.w, "=== Installation complete ===\n")
	}
}

// flushLines writes all complete lines (terminated by \n) in appBuf[app].
// Partial last chunk stays in the buffer. Must be called with mu held.
func (s *LineSink) flushLines(app string) {
	buf := s.appBuf[app]
	for {
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}
		line := string(buf[:idx])
		buf = buf[idx+1:]
		fmt.Fprintf(s.w, "[%s] %s\n", app, line)
	}
	s.appBuf[app] = buf
}
