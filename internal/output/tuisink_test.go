package output

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestTUISink_StateTransitions(t *testing.T) {
	var buf bytes.Buffer
	sink := NewTUISink(&buf)

	sink.Emit(Event{Kind: AppStart, App: "app1"})
	sink.Emit(Event{Kind: AppLine, App: "app1", Stream: "stdout", Text: "installing chart"})
	sink.Emit(Event{Kind: AppDone, App: "app1"})
	sink.Emit(Event{Kind: AllDone})
	sink.Close()

	out := buf.String()
	if !strings.Contains(out, "app1") {
		t.Fatalf("expected output to include app name, got %q", out)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("expected output to include ok status, got %q", out)
	}
}

func TestTUISink_NoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	sink := NewTUISink(&buf)

	sink.Emit(Event{Kind: AppStart, App: "app1"})
	sink.Emit(Event{Kind: AppLine, App: "app1", Stream: "stdout", Text: "plain output"})
	sink.Emit(Event{Kind: AppDone, App: "app1"})
	sink.Emit(Event{Kind: AllDone})
	sink.Close()

	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("expected no ANSI escape codes, got %q", buf.String())
	}
}

func TestTUISink_ConcurrentEmit(t *testing.T) {
	var buf bytes.Buffer
	sink := NewTUISink(&buf)

	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			app := fmt.Sprintf("app-%d", worker)
			sink.Emit(Event{Kind: AppStart, App: app})
			for i := range 50 {
				sink.Emit(Event{Kind: AppLine, App: app, Stream: "stdout", Text: fmt.Sprintf("line-%d", i)})
			}
			sink.Emit(Event{Kind: AppDone, App: app})
		}(worker)
	}

	wg.Wait()
	sink.Emit(Event{Kind: AllDone})
	sink.Close()
}

func TestTUISink_StderrVisibleOnFailure(t *testing.T) {
	var buf bytes.Buffer
	sink := NewTUISink(&buf)

	sink.Emit(Event{Kind: AppStart, App: "failing-app"})
	sink.Emit(Event{Kind: AppLine, App: "failing-app", Stream: "stderr", Text: "helm stderr detail"})
	sink.Emit(Event{Kind: AppDone, App: "failing-app", Err: errors.New("boom")})
	sink.Emit(Event{Kind: AllDone})
	sink.Close()

	if !strings.Contains(buf.String(), "helm stderr detail") {
		t.Fatalf("expected stderr in final output, got %q", buf.String())
	}
}

func TestTUISink_SkippedState(t *testing.T) {
	var buf bytes.Buffer
	sink := NewTUISink(&buf)

	sink.Emit(Event{Kind: AppSkip, App: "skipped-app", Reason: "dependency failed"})
	sink.Emit(Event{Kind: AllDone})
	sink.Close()

	out := buf.String()
	if !strings.Contains(out, "skipped-app") || !strings.Contains(out, "skipped") {
		t.Fatalf("expected skipped state in output, got %q", out)
	}
}
