package output

import (
	"testing"
)

func TestEventKindString(t *testing.T) {
	tests := []struct {
		name       string
		kind       EventKind
		wantNonNil bool
		wantUnique bool
	}{
		{
			name:       "WaveStart",
			kind:       WaveStart,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "WaveEnd",
			kind:       WaveEnd,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "AppStart",
			kind:       AppStart,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "AppLine",
			kind:       AppLine,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "AppDone",
			kind:       AppDone,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "AppSkip",
			kind:       AppSkip,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "AllDone",
			kind:       AllDone,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "NodeReady",
			kind:       NodeReady,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "NodeStart",
			kind:       NodeStart,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "NodeLine",
			kind:       NodeLine,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "NodeDone",
			kind:       NodeDone,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "NodeSkip",
			kind:       NodeSkip,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "TreeStart",
			kind:       TreeStart,
			wantNonNil: true,
			wantUnique: true,
		},
		{
			name:       "TreeDone",
			kind:       TreeDone,
			wantNonNil: true,
			wantUnique: true,
		},
	}

	// Track strings to verify uniqueness
	seen := make(map[string]bool)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.kind.String()

			// Check non-empty
			if tt.wantNonNil && got == "" {
				t.Errorf("String() returned empty string, want non-empty")
			}

			// Check uniqueness
			if tt.wantUnique && seen[got] {
				t.Errorf("String() returned %q, which was already seen (not unique)", got)
			}
			seen[got] = true
		})
	}
}
