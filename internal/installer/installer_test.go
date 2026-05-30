package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/guneet-xyz/kubolt/internal/output"
)

type recorder struct {
	mu     sync.Mutex
	order  []string
	starts map[string]time.Time
	ends   map[string]time.Time
}

func newRecorder() *recorder {
	return &recorder{starts: map[string]time.Time{}, ends: map[string]time.Time{}}
}

func (r *recorder) record(name string, sleep time.Duration, err error) func(ctx context.Context, stdout, stderr io.Writer) error {
	return func(ctx context.Context, stdout, stderr io.Writer) error {
		r.mu.Lock()
		r.order = append(r.order, name)
		r.starts[name] = time.Now()
		r.mu.Unlock()

		if sleep > 0 {
			select {
			case <-time.After(sleep):
			case <-ctx.Done():
				r.mu.Lock()
				r.ends[name] = time.Now()
				r.mu.Unlock()
				return ctx.Err()
			}
		}

		r.mu.Lock()
		r.ends[name] = time.Now()
		r.mu.Unlock()
		return err
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func equalSorted(a, b []string) bool {
	a = sortedCopy(a)
	b = sortedCopy(b)
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

func TestExecutor_SingleApp(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	plan := Plan{
		Waves: [][]string{{"a"}},
		Jobs: map[string]AppJob{
			"a": {Name: "a", Run: r.record("a", 0, nil)},
		},
		Dependents: map[string][]string{},
	}
	ex := &Executor{Parallelism: 1, Sink: output.NopSink{}}
	res, err := ex.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalSorted(res.Succeeded, []string{"a"}) {
		t.Fatalf("succeeded=%v", res.Succeeded)
	}
	if len(res.Failed) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("failed=%v skipped=%v", res.Failed, res.Skipped)
	}
}

func diamondPlan(r *recorder, failA bool) Plan {
	var aErr error
	if failA {
		aErr = errors.New("boom")
	}
	reverse := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
	}
	waves := [][]string{{"a"}, {"b", "c"}, {"d"}}
	return Plan{
		Waves: waves,
		Jobs: map[string]AppJob{
			"a": {Name: "a", Run: r.record("a", 0, aErr)},
			"b": {Name: "b", Run: r.record("b", 0, nil)},
			"c": {Name: "c", Run: r.record("c", 0, nil)},
			"d": {Name: "d", Run: r.record("d", 0, nil)},
		},
		Dependents: BuildDependents(waves, reverse),
	}
}

func TestExecutor_Diamond_AllSucceed(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	plan := diamondPlan(r, false)
	ex := &Executor{Parallelism: 4, Sink: output.NopSink{}}
	res, err := ex.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !equalSorted(res.Succeeded, []string{"a", "b", "c", "d"}) {
		t.Fatalf("succeeded=%v", res.Succeeded)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.starts["b"].Before(r.ends["a"]) || r.starts["c"].Before(r.ends["a"]) {
		t.Fatalf("b/c started before a finished")
	}
	if r.starts["d"].Before(r.ends["b"]) || r.starts["d"].Before(r.ends["c"]) {
		t.Fatalf("d started before b/c finished")
	}
}

func TestExecutor_Diamond_RootFails_LeafSkipped(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	plan := diamondPlan(r, true)
	ex := &Executor{Parallelism: 4, Sink: output.NopSink{}}
	res, err := ex.Run(context.Background(), plan)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !equalSorted(res.Failed, []string{"a"}) {
		t.Fatalf("failed=%v", res.Failed)
	}
	if !equalSorted(res.Skipped, []string{"b", "c", "d"}) {
		t.Fatalf("skipped=%v", res.Skipped)
	}
	if len(res.Succeeded) != 0 {
		t.Fatalf("succeeded=%v", res.Succeeded)
	}
}

func TestExecutor_ParallelSpeedup(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	plan := Plan{
		Waves: [][]string{{"a", "b", "c", "d"}},
		Jobs: map[string]AppJob{
			"a": {Name: "a", Run: r.record("a", 150*time.Millisecond, nil)},
			"b": {Name: "b", Run: r.record("b", 150*time.Millisecond, nil)},
			"c": {Name: "c", Run: r.record("c", 150*time.Millisecond, nil)},
			"d": {Name: "d", Run: r.record("d", 150*time.Millisecond, nil)},
		},
		Dependents: map[string][]string{},
	}
	ex := &Executor{Parallelism: 4, Sink: output.NopSink{}}
	start := time.Now()
	res, err := ex.Run(context.Background(), plan)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Succeeded) != 4 {
		t.Fatalf("succeeded=%v", res.Succeeded)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("expected parallel speedup, got %v", elapsed)
	}
}

func TestExecutor_SequentialBranch(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	plan := Plan{
		Waves: [][]string{{"a", "b", "c"}},
		Jobs: map[string]AppJob{
			"a": {Name: "a", Run: r.record("a", 50*time.Millisecond, nil)},
			"b": {Name: "b", Run: r.record("b", 50*time.Millisecond, nil)},
			"c": {Name: "c", Run: r.record("c", 50*time.Millisecond, nil)},
		},
		Dependents: map[string][]string{},
	}
	ex := &Executor{Parallelism: 1, Sink: output.NopSink{}}
	start := time.Now()
	_, err := ex.Run(context.Background(), plan)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 140*time.Millisecond {
		t.Fatalf("expected sequential timing (>140ms), got %v", elapsed)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.order) != 3 {
		t.Fatalf("order=%v", r.order)
	}
}

func TestExecutor_Parallelism0_NotDeadlock(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	plan := Plan{
		Waves: [][]string{{"a", "b", "c"}},
		Jobs: map[string]AppJob{
			"a": {Name: "a", Run: r.record("a", 0, nil)},
			"b": {Name: "b", Run: r.record("b", 0, nil)},
			"c": {Name: "c", Run: r.record("c", 0, nil)},
		},
		Dependents: map[string][]string{},
	}
	ex := &Executor{Parallelism: 0, Sink: output.NopSink{}}
	done := make(chan struct{})
	go func() {
		_, _ = ex.Run(context.Background(), plan)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock with Parallelism=0")
	}
}

func TestExecutor_CtxCancel(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	plan := Plan{
		Waves: [][]string{{"a", "b"}},
		Jobs: map[string]AppJob{
			"a": {Name: "a", Run: r.record("a", 10*time.Second, nil)},
			"b": {Name: "b", Run: r.record("b", 10*time.Second, nil)},
		},
		Dependents: map[string][]string{},
	}
	ex := &Executor{Parallelism: 2, Sink: output.NopSink{}}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	done := make(chan error, 1)
	go func() {
		_, err := ex.Run(ctx, plan)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from cancelled ctx")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestExecutor_BestEffort_PartialWave(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	plan := Plan{
		Waves: [][]string{{"a", "b"}},
		Jobs: map[string]AppJob{
			"a": {Name: "a", Run: r.record("a", 0, errors.New("boom"))},
			"b": {Name: "b", Run: r.record("b", 50*time.Millisecond, nil)},
		},
		Dependents: map[string][]string{},
	}
	ex := &Executor{Parallelism: 2, Sink: output.NopSink{}}
	res, err := ex.Run(context.Background(), plan)
	if err == nil {
		t.Fatal("expected error")
	}
	if !equalSorted(res.Failed, []string{"a"}) {
		t.Fatalf("failed=%v", res.Failed)
	}
	if !equalSorted(res.Succeeded, []string{"b"}) {
		t.Fatalf("succeeded=%v", res.Succeeded)
	}
}

func TestBuildDependents_Diamond(t *testing.T) {
	t.Parallel()
	waves := [][]string{{"a"}, {"b", "c"}, {"d"}}
	reverse := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
	}
	got := BuildDependents(waves, reverse)
	cases := map[string][]string{
		"a": {"b", "c", "d"},
		"b": {"d"},
		"c": {"d"},
		"d": {},
	}
	for name, want := range cases {
		if !equalSorted(got[name], want) {
			t.Errorf("dependents[%q]=%v want %v", name, got[name], want)
		}
	}
}

type captureSink struct {
	mu     sync.Mutex
	events []output.Event
}

func (c *captureSink) Emit(e output.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func TestExecutor_EmitsLines(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	plan := Plan{
		Waves: [][]string{{"a"}},
		Jobs: map[string]AppJob{
			"a": {Name: "a", Run: func(ctx context.Context, stdout, stderr io.Writer) error {
				fmt.Fprintln(stdout, "hello")
				fmt.Fprintln(stderr, "world")
				return nil
			}},
		},
		Dependents: map[string][]string{},
	}
	ex := &Executor{Parallelism: 1, Sink: sink}
	if _, err := ex.Run(context.Background(), plan); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var sawHello, sawWorld bool
	for _, e := range sink.events {
		if e.Kind == output.AppLine && e.Text == "hello" && e.Stream == "stdout" {
			sawHello = true
		}
		if e.Kind == output.AppLine && e.Text == "world" && e.Stream == "stderr" {
			sawWorld = true
		}
	}
	if !sawHello || !sawWorld {
		t.Fatalf("missing lines; events=%+v", sink.events)
	}
}
