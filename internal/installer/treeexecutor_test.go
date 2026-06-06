package installer

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/guneet-xyz/kubolt/internal/output"
)

type recordingSink struct {
	mu     sync.Mutex
	events []output.Event
}

func (s *recordingSink) Emit(e output.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *recordingSink) snapshot() []output.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]output.Event, len(s.events))
	copy(out, s.events)
	return out
}

func eventKinds(events []output.Event) []output.EventKind {
	out := make([]output.EventKind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func countKind(events []output.Event, kind output.EventKind) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func successJob(name string) AppJob {
	return AppJob{
		Name: name,
		Run: func(ctx context.Context, stdout, stderr io.Writer) error {
			return nil
		},
	}
}

func failingJob(name string, err error) AppJob {
	return AppJob{
		Name: name,
		Run: func(ctx context.Context, stdout, stderr io.Writer) error {
			return err
		},
	}
}

func TestTreeExecutorRun_HappyPath(t *testing.T) {
	sink := &recordingSink{}
	plan := Plan{Nodes: map[string][]string{"a": nil, "b": nil},
		Jobs: map[string]AppJob{
			"a": successJob("a"),
			"b": successJob("b"),
		},}

	ex := Executor{Parallelism: 2, Sink: sink}
	res, err := ex.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := append([]string(nil), res.Succeeded...)
	sort.Strings(got)
	if want := []string{"a", "b"}; !equalStrings(got, want) {
		t.Fatalf("Succeeded = %v, want %v", got, want)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("Failed = %v, want empty", res.Failed)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("Skipped = %v, want empty", res.Skipped)
	}

	events := sink.snapshot()
	if got, want := countKind(events, output.TreeStart), 1; got != want {
		t.Fatalf("TreeStart count = %d, want %d", got, want)
	}
	if got, want := countKind(events, output.TreeDone), 1; got != want {
		t.Fatalf("TreeDone count = %d, want %d", got, want)
	}
	if got, want := countKind(events, output.NodeStart), 2; got != want {
		t.Fatalf("NodeStart count = %d, want %d", got, want)
	}
	if got, want := countKind(events, output.NodeDone), 2; got != want {
		t.Fatalf("NodeDone count = %d, want %d", got, want)
	}

	if events[0].Kind != output.TreeStart || events[0].Count != 2 {
		t.Fatalf("first event = %+v, want TreeStart with Count=2", events[0])
	}
	if events[len(events)-1].Kind != output.TreeDone {
		t.Fatalf("last event = %+v, want TreeDone", events[len(events)-1])
	}

	kinds := eventKinds(events)
	if kinds[0] != output.TreeStart {
		t.Fatalf("kinds[0] = %s, want TreeStart", kinds[0])
	}
	if kinds[len(kinds)-1] != output.TreeDone {
		t.Fatalf("kinds[last] = %s, want TreeDone", kinds[len(kinds)-1])
	}
}

func TestTreeExecutorRun_Serial(t *testing.T) {
	var (
		mu     sync.Mutex
		starts = make(map[string]time.Time)
		ends   = make(map[string]time.Time)
	)

	makeJob := func(name string) AppJob {
		return AppJob{
			Name: name,
			Run: func(ctx context.Context, stdout, stderr io.Writer) error {
				mu.Lock()
				starts[name] = time.Now()
				mu.Unlock()
				time.Sleep(15 * time.Millisecond)
				mu.Lock()
				ends[name] = time.Now()
				mu.Unlock()
				return nil
			},
		}
	}

	plan := Plan{Nodes: map[string][]string{
			"a": nil,
			"b": {"a"},
			"c": {"b"},
		},
		Jobs: map[string]AppJob{
			"a": makeJob("a"),
			"b": makeJob("b"),
			"c": makeJob("c"),
		},}

	ex := Executor{Parallelism: 4, Sink: &recordingSink{}}
	res, err := ex.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Succeeded) != 3 {
		t.Fatalf("Succeeded = %v, want 3 entries", res.Succeeded)
	}

	if !starts["b"].After(ends["a"]) {
		t.Fatalf("b started before a finished: bStart=%v aEnd=%v", starts["b"], ends["a"])
	}
	if !starts["c"].After(ends["b"]) {
		t.Fatalf("c started before b finished: cStart=%v bEnd=%v", starts["c"], ends["b"])
	}
}

func TestTreeExecutorRun_FailureCascade(t *testing.T) {
	sink := &recordingSink{}
	bootErr := errors.New("boom")

	plan := Plan{Nodes: map[string][]string{
			"a": nil,
			"b": {"a"},
			"c": {"b"},
		},
		Jobs: map[string]AppJob{
			"a": failingJob("a", bootErr),
			"b": successJob("b"),
			"c": successJob("c"),
		},}

	ex := Executor{Parallelism: 1, Sink: sink}
	res, err := ex.Run(context.Background(), plan)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !equalStrings(res.Failed, []string{"a"}) {
		t.Fatalf("Failed = %v, want [a]", res.Failed)
	}
	skipped := append([]string(nil), res.Skipped...)
	sort.Strings(skipped)
	if want := []string{"b", "c"}; !equalStrings(skipped, want) {
		t.Fatalf("Skipped = %v, want %v", skipped, want)
	}
	if len(res.Succeeded) != 0 {
		t.Fatalf("Succeeded = %v, want empty", res.Succeeded)
	}

	events := sink.snapshot()

	if got, want := countKind(events, output.NodeSkip), 2; got != want {
		t.Fatalf("NodeSkip count = %d, want %d", got, want)
	}

	var doneA *output.Event
	for i := range events {
		if events[i].Kind == output.NodeDone && events[i].App == "a" {
			doneA = &events[i]
			break
		}
	}
	if doneA == nil {
		t.Fatal("missing NodeDone for app a")
	}
	if doneA.Err == nil {
		t.Fatal("NodeDone.Err for app a is nil, want non-nil")
	}
}

func TestTreeExecutorRun_CtxCancel(t *testing.T) {
	makeJob := func(name string) AppJob {
		return AppJob{
			Name: name,
			Run: func(ctx context.Context, stdout, stderr io.Writer) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(50 * time.Millisecond):
					return nil
				}
			},
		}
	}

	plan := Plan{Nodes: map[string][]string{"a": nil, "b": nil, "c": nil},
		Jobs: map[string]AppJob{
			"a": makeJob("a"),
			"b": makeJob("b"),
			"c": makeJob("c"),
		},}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	ex := Executor{Parallelism: 3, Sink: &recordingSink{}}
	_, err := ex.Run(ctx, plan)
	if err == nil {
		t.Fatal("expected non-nil error from ctx cancel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestTreeExecutorRun_Parallelism(t *testing.T) {
	makeJob := func(name string) AppJob {
		return AppJob{
			Name: name,
			Run: func(ctx context.Context, stdout, stderr io.Writer) error {
				time.Sleep(20 * time.Millisecond)
				return nil
			},
		}
	}

	plan := Plan{Nodes: map[string][]string{"a": nil, "b": nil, "c": nil, "d": nil},
		Jobs: map[string]AppJob{
			"a": makeJob("a"),
			"b": makeJob("b"),
			"c": makeJob("c"),
			"d": makeJob("d"),
		},}

	ex := Executor{Parallelism: 2, Sink: &recordingSink{}}

	start := time.Now()
	res, err := ex.Run(context.Background(), plan)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Succeeded) != 4 {
		t.Fatalf("Succeeded = %v, want 4", res.Succeeded)
	}
	// Serial would be 4 * 20ms = 80ms; with parallelism=2, expect ~40ms.
	// Allow generous slack to avoid CI flake but still catch serial regression.
	if elapsed > 80*time.Millisecond {
		t.Fatalf("elapsed = %v, want <= 80ms (parallelism=2 over 4×20ms jobs)", elapsed)
	}
}

func TestTreeExecutorRun_MissingJob(t *testing.T) {
	sink := &recordingSink{}
	plan := Plan{Nodes: map[string][]string{"x": nil},
		Jobs:  map[string]AppJob{},}

	ex := Executor{Parallelism: 1, Sink: sink}
	res, err := ex.Run(context.Background(), plan)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !equalStrings(res.Failed, []string{"x"}) {
		t.Fatalf("Failed = %v, want [x]", res.Failed)
	}
	if len(res.Succeeded) != 0 {
		t.Fatalf("Succeeded = %v, want empty", res.Succeeded)
	}

	events := sink.snapshot()
	var doneX *output.Event
	for i := range events {
		if events[i].Kind == output.NodeDone && events[i].App == "x" {
			doneX = &events[i]
			break
		}
	}
	if doneX == nil {
		t.Fatal("missing NodeDone for app x")
	}
	if doneX.Err == nil {
		t.Fatal("NodeDone.Err for app x is nil, want non-nil")
	}
}

func TestTreeExecutorRun_NilSink(t *testing.T) {
	plan := Plan{Nodes: map[string][]string{"a": nil},
		Jobs: map[string]AppJob{
			"a": successJob("a"),
		},}

	ex := Executor{Parallelism: 1, Sink: nil}
	res, err := ex.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalStrings(res.Succeeded, []string{"a"}) {
		t.Fatalf("Succeeded = %v, want [a]", res.Succeeded)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
