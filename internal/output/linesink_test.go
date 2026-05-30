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

	sink.Emit(Event{Kind: WaveStart, Wave: 0})
	sink.Emit(Event{Kind: AppStart, App: "app1"})
	sink.Emit(Event{Kind: AppLine, App: "app1", Text: "hello\nworld\n"})
	sink.Emit(Event{Kind: AppDone, App: "app1"})
	sink.Emit(Event{Kind: WaveEnd, Wave: 0})

	output := w.String()

	if !strings.Contains(output, "=== Wave 1 starting ===") {
		t.Errorf("expected Wave 1 start marker, got: %q", output)
	}
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
	if !strings.Contains(output, "=== Wave 1 done ===") {
		t.Errorf("expected Wave 1 end marker, got: %q", output)
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

	sink.Emit(Event{Kind: WaveStart, Wave: 0})
	sink.Emit(Event{Kind: AppStart, App: "testapp"})
	sink.Emit(Event{Kind: AppLine, App: "testapp", Text: "output line\n"})
	sink.Emit(Event{Kind: AppDone, App: "testapp"})
	sink.Emit(Event{Kind: AllDone})

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

func TestLineSink_ConcurrentWaveEmit(t *testing.T) {
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

func TestLineSink_MultipleWaves(t *testing.T) {
	w := &bytes.Buffer{}
	sink := NewLineSink(w)

	for wave := 0; wave < 3; wave++ {
		sink.Emit(Event{Kind: WaveStart, Wave: wave})
		sink.Emit(Event{Kind: AppStart, App: "app1"})
		sink.Emit(Event{Kind: AppDone, App: "app1"})
		sink.Emit(Event{Kind: WaveEnd, Wave: wave})
	}

	output := w.String()

	if strings.Count(output, "=== Wave 1 starting ===") != 1 {
		t.Errorf("expected 1 Wave 1 start, got: %q", output)
	}
	if strings.Count(output, "=== Wave 2 starting ===") != 1 {
		t.Errorf("expected 1 Wave 2 start, got: %q", output)
	}
	if strings.Count(output, "=== Wave 3 starting ===") != 1 {
		t.Errorf("expected 1 Wave 3 start, got: %q", output)
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
