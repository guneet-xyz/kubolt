package output

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestLineSink_BasicOutput(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: AppStart, App: "app1"})
	sink.Emit(Event{Kind: AppLine, App: "app1", Text: "hello\nworld\n"})
	sink.Emit(Event{Kind: AppDone, App: "app1"})

	output := w.String()

	if !strings.Contains(output, "[app1] starting") {
		t.Errorf("expected app1 start marker, got: %q", output)
	}
	if !strings.Contains(output, "[app1] hello") {
		t.Errorf("expected app1 hello line, got: %q", output)
	}
	if !strings.Contains(output, "[app1] world") {
		t.Errorf("expected app1 world line, got: %q", output)
	}
	if !strings.Contains(output, "[app1] OK in") {
		t.Errorf("expected app1 OK marker, got: %q", output)
	}
}

func TestLineSink_NoTearing(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	var wg sync.WaitGroup
	numGoroutines := 10
	linesPerGoroutine := 100

	wg.Add(numGoroutines)
	for g := 0; g < numGoroutines; g++ {
		go func(g int) {
			defer wg.Done()
			appName := fmt.Sprintf("app%d", g%2)
			for i := 0; i < linesPerGoroutine; i++ {
				sink.Emit(Event{
					Kind: AppLine,
					App:  appName,
					Text: fmt.Sprintf("line %d\n", i),
				})
			}
		}(g)
	}
	wg.Wait()

	output := w.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "[app0]") && !strings.HasPrefix(line, "[app1]") {
			t.Fatalf("line does not start with valid app prefix: %q", line)
		}
		if strings.Contains(line, "[app0][app1]") || strings.Contains(line, "[app1][app0]") {
			t.Fatalf("torn line detected: %q", line)
		}
	}
}

func TestLineSink_NoANSI(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: TreeStart, Count: 1})
	sink.Emit(Event{Kind: NodeStart, App: "testapp"})
	sink.Emit(Event{Kind: NodeLine, App: "testapp", Text: "output line\n"})
	sink.Emit(Event{Kind: NodeDone, App: "testapp"})
	sink.Emit(Event{Kind: TreeDone})

	output := w.Bytes()

	if bytes.Contains(output, []byte("\x1b[")) {
		t.Errorf("ANSI escape code found in output: %q", output)
	}
}

func TestLineSink_PartialLine_FlushedOnDone(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: AppStart, App: "app1"})
	sink.Emit(Event{Kind: AppLine, App: "app1", Text: "partial line without newline"})
	sink.Emit(Event{Kind: AppDone, App: "app1"})

	output := w.String()

	if !strings.Contains(output, "[app1] partial line without newline") {
		t.Errorf("expected partial line to be flushed on AppDone, got: %q", output)
	}
}

func TestLineSink_SkippedApp(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{
		Kind:   AppSkip,
		App:    "skipped-app",
		Reason: "dependency failed",
	})

	output := w.String()

	if !strings.Contains(output, "[skipped-app] SKIPPED") {
		t.Errorf("expected SKIPPED marker, got: %q", output)
	}
	if !strings.Contains(output, "dependency failed") {
		t.Errorf("expected reason in output, got: %q", output)
	}
}

func TestLineSink_AppFailure(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: AppStart, App: "failing-app"})
	sink.Emit(Event{Kind: AppLine, App: "failing-app", Text: "error occurred\n"})
	sink.Emit(Event{
		Kind: AppDone,
		App:  "failing-app",
		Err:  errors.New("helm install failed"),
	})

	output := w.String()

	if !strings.Contains(output, "[failing-app] FAILED") {
		t.Errorf("expected FAILED marker, got: %q", output)
	}
	if !strings.Contains(output, "helm install failed") {
		t.Errorf("expected error message in output, got: %q", output)
	}
}

func TestLineSink_AllDone(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: AllDone})

	output := w.String()

	if !strings.Contains(output, "=== Installation complete ===") {
		t.Errorf("expected completion marker, got: %q", output)
	}
}

func TestLineSink_ConcurrentEmit(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			sink.Emit(Event{Kind: AppLine, App: "appA", Text: fmt.Sprintf("A%d\n", i)})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			sink.Emit(Event{Kind: AppLine, App: "appB", Text: fmt.Sprintf("B%d\n", i)})
		}
	}()

	wg.Wait()

	output := w.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	appACount := 0
	appBCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "[appA]") {
			appACount++
		} else if strings.HasPrefix(line, "[appB]") {
			appBCount++
		}
	}

	if appACount != 50 {
		t.Errorf("expected 50 appA lines, got %d", appACount)
	}
	if appBCount != 50 {
		t.Errorf("expected 50 appB lines, got %d", appBCount)
	}
}

func TestLineSink_EmptyApp(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: AppStart, App: "silent-app"})
	sink.Emit(Event{Kind: AppDone, App: "silent-app"})

	output := w.String()

	if !strings.Contains(output, "[silent-app] starting") {
		t.Errorf("expected app start, got: %q", output)
	}
	if !strings.Contains(output, "[silent-app] OK") {
		t.Errorf("expected app OK, got: %q", output)
	}
}

func TestLineSink_TreeFraming(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: TreeStart, Count: 3})
	sink.Emit(Event{Kind: TreeDone})

	output := w.String()

	if !strings.Contains(output, "=== Starting (3 apps) ===") {
		t.Errorf("expected tree start marker, got: %q", output)
	}
	if !strings.Contains(output, "=== Complete (succeeded=0 failed=0 skipped=0) ===") {
		t.Errorf("expected tree done marker, got: %q", output)
	}
}

func TestLineSink_TreeDone_CountsAggregated(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: TreeStart, Count: 4})

	sink.Emit(Event{Kind: NodeStart, App: "ok1"})
	sink.Emit(Event{Kind: NodeDone, App: "ok1"})

	sink.Emit(Event{Kind: NodeStart, App: "ok2"})
	sink.Emit(Event{Kind: NodeDone, App: "ok2"})

	sink.Emit(Event{Kind: NodeStart, App: "bad"})
	sink.Emit(Event{Kind: NodeDone, App: "bad", Err: errors.New("boom")})

	sink.Emit(Event{Kind: NodeSkip, App: "skip", Reason: "dep failed"})

	sink.Emit(Event{Kind: TreeDone})

	output := w.String()

	if !strings.Contains(output, "=== Complete (succeeded=2 failed=1 skipped=1) ===") {
		t.Errorf("expected aggregated counts, got: %q", output)
	}
}

func TestLineSink_NodeStartDone_OK(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: NodeStart, App: "x"})
	sink.Emit(Event{Kind: NodeDone, App: "x"})

	output := w.String()

	if !strings.Contains(output, "[x] starting") {
		t.Errorf("expected [x] starting, got: %q", output)
	}
	if !strings.Contains(output, "[x] OK in") {
		t.Errorf("expected [x] OK in marker, got: %q", output)
	}
}

func TestLineSink_NodeDone_Failed(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: NodeStart, App: "x"})
	sink.Emit(Event{Kind: NodeDone, App: "x", Err: errors.New("boom")})

	output := w.String()

	if !strings.Contains(output, "[x] FAILED in") {
		t.Errorf("expected [x] FAILED in marker, got: %q", output)
	}
	if !strings.Contains(output, "boom") {
		t.Errorf("expected error message in output, got: %q", output)
	}
}

func TestLineSink_NodeSkip(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: NodeSkip, App: "x", Reason: "dep failed"})

	output := w.String()

	if !strings.Contains(output, "[x] SKIPPED: dep failed") {
		t.Errorf("expected [x] SKIPPED line, got: %q", output)
	}
}

func TestLineSink_NodeLineWithStage(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: NodeStart, App: "x"})
	sink.Emit(Event{Kind: NodeLine, App: "x", Text: "step1\n", Stage: "scaling-down"})

	output := w.String()

	if !strings.Contains(output, "[x] [scaling-down] step1") {
		t.Errorf("expected stage-prefixed line, got: %q", output)
	}
}

func TestLineSink_PartialLineBuffering(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	sink.Emit(Event{Kind: NodeStart, App: "x"})
	sink.Emit(Event{Kind: NodeLine, App: "x", Text: "hel"})

	if strings.Contains(w.String(), "[x] hel") {
		t.Errorf("partial chunk should not be flushed yet, got: %q", w.String())
	}

	sink.Emit(Event{Kind: NodeLine, App: "x", Text: "lo\n"})

	output := w.String()

	if !strings.Contains(output, "[x] hello") {
		t.Errorf("expected joined [x] hello line, got: %q", output)
	}
	if strings.Contains(output, "[x] hel\n") {
		t.Errorf("partial chunk should not have produced its own line, got: %q", output)
	}
}
