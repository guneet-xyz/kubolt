package depgraph

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestWalkerContract(t *testing.T) {
	cases := []struct {
		name    string
		nodes   map[string][]string
		wantErr error
	}{
		{
			name:    "unknown_dep",
			nodes:   map[string][]string{"a": {"b"}},
			wantErr: ErrUnknownDep,
		},
		{
			name:    "cycle",
			nodes:   map[string][]string{"a": {"b"}, "b": {"a"}},
			wantErr: ErrCycle,
		},
		{
			name:    "empty_graph",
			nodes:   map[string][]string{},
			wantErr: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := NewWalker(tc.nodes)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.wantErr)
				}
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected error wrapping %v, got %v", tc.wantErr, err)
				}
				if w != nil {
					t.Errorf("expected nil Walker on error, got %+v", w)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if w == nil {
				t.Fatal("expected non-nil Walker")
			}
			ch := w.Walk(t.Context())
			for r := range ch {
				t.Errorf("empty graph should emit no Ready, got %+v", r)
			}
		})
	}

	t.Run("walk_emits_ready", func(t *testing.T) {
		nodes := map[string][]string{"a": {}}
		w, err := NewWalker(nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ch := w.Walk(t.Context())
		r, ok := <-ch
		if !ok {
			t.Fatalf("expected Ready emitted for zero-dep node, got closed channel (Walk not implemented)")
		}
		if r.Name != "a" {
			t.Errorf("expected Ready{Name:\"a\"}, got %+v", r)
		}
		w.Done(r.Name, nil)
	})
}

func TestWalkerLinearChain(t *testing.T) {
	nodes := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"b"},
	}
	w, err := NewWalker(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ch := w.Walk(t.Context())

	r := mustRecv(t, ch, "first emission")
	if r.Name != "a" {
		t.Fatalf("expected first=a, got %s", r.Name)
	}
	if r.DepFailures != nil {
		t.Errorf("expected nil DepFailures for a, got %v", r.DepFailures)
	}

	mustEmpty(t, ch, "before Done(a)")
	w.Done("a", nil)

	r = mustRecv(t, ch, "after Done(a)")
	if r.Name != "b" {
		t.Fatalf("expected b after Done(a), got %s", r.Name)
	}

	mustEmpty(t, ch, "before Done(b)")
	w.Done("b", nil)

	r = mustRecv(t, ch, "after Done(b)")
	if r.Name != "c" {
		t.Fatalf("expected c after Done(b), got %s", r.Name)
	}

	w.Done("c", nil)
	mustClosed(t, ch)
}

func TestWalkerDiamond(t *testing.T) {
	nodes := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"a"},
		"d": {"b", "c"},
	}
	w, err := NewWalker(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ch := w.Walk(t.Context())

	a := mustRecv(t, ch, "root")
	if a.Name != "a" {
		t.Fatalf("expected root=a, got %s", a.Name)
	}

	w.Done("a", nil)

	first := mustRecv(t, ch, "first dependent of a")
	second := mustRecv(t, ch, "second dependent of a")
	got := []string{first.Name, second.Name}
	sort.Strings(got)
	if !slices.Equal(got, []string{"b", "c"}) {
		t.Fatalf("expected {b,c} after Done(a), got %v", got)
	}

	w.Done(first.Name, nil)
	mustEmpty(t, ch, "after only one of (b,c) done — d still has 1 unmet dep")

	w.Done(second.Name, nil)
	d := mustRecv(t, ch, "d after both b and c done")
	if d.Name != "d" {
		t.Fatalf("expected d, got %s", d.Name)
	}
	if d.DepFailures != nil {
		t.Errorf("expected nil DepFailures for d, got %v", d.DepFailures)
	}

	w.Done("d", nil)
	mustClosed(t, ch)
}

func TestWalkerParallelLeaves(t *testing.T) {
	nodes := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"a"},
		"d": {"a"},
		"e": {"a"},
	}
	w, err := NewWalker(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ch := w.Walk(t.Context())

	a := mustRecv(t, ch, "root")
	if a.Name != "a" {
		t.Fatalf("expected root=a, got %s", a.Name)
	}
	w.Done("a", nil)

	got := []string{}
	for range 4 {
		got = append(got, mustRecv(t, ch, "leaf").Name)
	}
	sort.Strings(got)
	if !slices.Equal(got, []string{"b", "c", "d", "e"}) {
		t.Fatalf("expected {b,c,d,e} after Done(a), got %v", got)
	}

	for _, n := range got {
		w.Done(n, nil)
	}
	mustClosed(t, ch)
}

func TestWalkerFailureCascade(t *testing.T) {
	nodes := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"b"},
		"d": {"c"},
		"e": {},
	}
	w, err := NewWalker(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ch := w.Walk(t.Context())

	first := mustRecv(t, ch, "first root")
	second := mustRecv(t, ch, "second root")
	got := []string{first.Name, second.Name}
	sort.Strings(got)
	if !slices.Equal(got, []string{"a", "e"}) {
		t.Fatalf("expected roots {a,e}, got %v", got)
	}
	for _, r := range []Ready{first, second} {
		if r.DepFailures != nil {
			t.Errorf("root %s should have nil DepFailures, got %v", r.Name, r.DepFailures)
		}
	}

	w.Done("a", errors.New("boom"))

	cascaded := map[string][]string{}
	for range 3 {
		r := mustRecv(t, ch, "cascade emission")
		if r.DepFailures == nil {
			t.Errorf("cascaded %s should have non-nil DepFailures", r.Name)
		}
		cascaded[r.Name] = r.DepFailures
	}

	wantNames := []string{"b", "c", "d"}
	for _, n := range wantNames {
		if _, ok := cascaded[n]; !ok {
			t.Errorf("expected cascade emission for %s, got %v", n, cascaded)
		}
	}

	if got := cascaded["b"]; !slices.Equal(got, []string{"a"}) {
		t.Errorf("b.DepFailures: expected [a], got %v", got)
	}
	if got := cascaded["c"]; !slices.Equal(got, []string{"b"}) {
		t.Errorf("c.DepFailures: expected [b], got %v", got)
	}
	if got := cascaded["d"]; !slices.Equal(got, []string{"c"}) {
		t.Errorf("d.DepFailures: expected [c], got %v", got)
	}

	w.Done("e", nil)
	mustClosed(t, ch)
}

func TestWalkerCtxCancel(t *testing.T) {
	nodes := map[string][]string{
		"a": {},
		"b": {"a"},
		"c": {"b"},
	}
	w, err := NewWalker(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	ch := w.Walk(ctx)

	r := mustRecv(t, ch, "first emission")
	if r.Name != "a" {
		t.Fatalf("expected a, got %s", r.Name)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after ctx cancel")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel did not close within 100ms after ctx cancel")
	}
}

func TestWalkerStressRace(t *testing.T) {
	nodes := map[string][]string{"root": {}}
	leaves := []string{"l0", "l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9"}
	for _, l := range leaves {
		nodes[l] = []string{"root"}
	}
	w, err := NewWalker(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ch := w.Walk(t.Context())

	const workers = 5
	var (
		mu       sync.Mutex
		received []string
		wg       sync.WaitGroup
	)
	for range workers {
		wg.Go(func() {
			for r := range ch {
				mu.Lock()
				received = append(received, r.Name)
				mu.Unlock()
				w.Done(r.Name, nil)
			}
		})
	}
	wg.Wait()

	sort.Strings(received)
	expected := append([]string{"root"}, leaves...)
	sort.Strings(expected)
	if !slices.Equal(received, expected) {
		t.Errorf("expected %v, got %v", expected, received)
	}
}

func mustRecv(t *testing.T, ch <-chan Ready, label string) Ready {
	t.Helper()
	select {
	case r, ok := <-ch:
		if !ok {
			t.Fatalf("[%s] channel closed unexpectedly", label)
		}
		return r
	case <-time.After(time.Second):
		t.Fatalf("[%s] timed out waiting for Ready", label)
	}
	return Ready{}
}

func mustEmpty(t *testing.T, ch <-chan Ready, label string) {
	t.Helper()
	select {
	case r, ok := <-ch:
		if ok {
			t.Fatalf("[%s] expected channel empty, got %+v", label, r)
		}
		t.Fatalf("[%s] channel closed unexpectedly", label)
	default:
	}
}

func mustClosed(t *testing.T, ch <-chan Ready) {
	t.Helper()
	select {
	case r, ok := <-ch:
		if ok {
			t.Fatalf("expected closed channel, got %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("channel did not close within 1s")
	}
}
