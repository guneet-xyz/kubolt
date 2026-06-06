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
	mu        sync.Mutex
	w         io.Writer
	verbose   bool
	appStart  map[string]time.Time   // track elapsed per app
	appBuf    map[string][]byte      // partial-line buffer per app (legacy AppLine path / verbose NodeLine)
	buffers   map[string]*NodeBuffer // per-node capture buffer; dumped on NodeDone failure
	succeeded int
	failed    int
	skipped   int
}

// NewLineSink returns a Sink that writes prefixed lines to w.
// Lines are buffered per app and flushed atomically to prevent tearing.
func newLineSinkImpl(w io.Writer, verbose bool) *LineSink {
	return &LineSink{
		w:        w,
		verbose:  verbose,
		appStart: make(map[string]time.Time),
		appBuf:   make(map[string][]byte),
		buffers:  make(map[string]*NodeBuffer),
	}
}

// Emit writes an event to the sink in a thread-safe manner.
func (s *LineSink) Emit(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch e.Kind {
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

	case AllDone:
		fmt.Fprintf(s.w, "=== Installation complete ===\n")

	case TreeStart:
		// e.Count holds the total app count
		fmt.Fprintf(s.w, "=== Starting (%d apps) ===\n", e.Count)

	case NodeStart:
		s.appStart[e.App] = time.Now()
		if s.buffers[e.App] == nil {
			s.buffers[e.App] = NewNodeBuffer(0)
		}
		fmt.Fprintf(s.w, "[%s] starting\n", e.App)

	case NodeLine:
		if s.verbose {
			s.appBuf[e.App] = append(s.appBuf[e.App], []byte(e.Text)...)
			if e.Stage != "" {
				s.flushLinesWithStage(e.App, e.Stage)
			} else {
				s.flushLines(e.App)
			}
		} else {
			if s.buffers[e.App] == nil {
				s.buffers[e.App] = NewNodeBuffer(0)
			}
			s.buffers[e.App].WriteLine(e.Text)
		}

	case NodeDone:
		if len(s.appBuf[e.App]) > 0 {
			fmt.Fprintf(s.w, "[%s] %s\n", e.App, string(s.appBuf[e.App]))
			delete(s.appBuf, e.App)
		}
		if e.Err != nil && !s.verbose {
			if nb := s.buffers[e.App]; nb != nil {
				fmt.Fprintf(s.w, "--- output from %s ---\n", e.App)
				if t := nb.TruncatedBytes(); t > 0 {
					fmt.Fprintf(s.w, "[... %d bytes elided due to 1 MiB cap ...]\n", t)
				}
				_, _ = s.w.Write(nb.Bytes())
				fmt.Fprintf(s.w, "--- end output ---\n")
			}
		}
		delete(s.buffers, e.App)
		elapsed := time.Since(s.appStart[e.App]).Truncate(time.Millisecond)
		if e.Err != nil {
			fmt.Fprintf(s.w, "[%s] FAILED in %s: %v\n", e.App, elapsed, e.Err)
			s.failed++
		} else {
			fmt.Fprintf(s.w, "[%s] OK in %s\n", e.App, elapsed)
			s.succeeded++
		}
		delete(s.appStart, e.App)

	case NodeSkip:
		delete(s.buffers, e.App)
		fmt.Fprintf(s.w, "[%s] SKIPPED: %s\n", e.App, e.Reason)
		s.skipped++

	case TreeDone:
		fmt.Fprintf(s.w, "=== Complete (succeeded=%d failed=%d skipped=%d) ===\n",
			s.succeeded, s.failed, s.skipped)
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

// flushLinesWithStage flushes complete lines with a stage prefix:
// "[app] [stage] line". Must be called with mu held.
func (s *LineSink) flushLinesWithStage(app, stage string) {
	buf := s.appBuf[app]
	for {
		idx := bytes.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}
		line := string(buf[:idx])
		buf = buf[idx+1:]
		fmt.Fprintf(s.w, "[%s] [%s] %s\n", app, stage, line)
	}
	s.appBuf[app] = buf
}
