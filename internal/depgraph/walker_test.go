package depgraph

import (
	"errors"
	"testing"
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
