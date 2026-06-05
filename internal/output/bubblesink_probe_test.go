package output

import (
	"testing"

	_ "charm.land/bubbletea/v2"
	_ "charm.land/bubbles/v2"
)

// TestBubbleSinkProbeImports verifies that charm.land/bubbletea/v2 and charm.land/bubbles/v2
// dependencies are available and can be imported successfully.
func TestBubbleSinkProbeImports(t *testing.T) {
	// This test is a compile-time import lock. If it compiles and runs, both packages are available.
}
