package cmd

import (
	"bytes"
	"os"
	"testing"
)

func TestIsInteractive_NonFileWriter(t *testing.T) {
	buf := &bytes.Buffer{}
	if isInteractive(buf) {
		t.Error("expected bytes.Buffer to not be interactive")
	}
}

func TestIsInteractive_RealFile(t *testing.T) {
	// Test with actual stdout (may or may not be a TTY depending on test runner)
	// We just verify it doesn't panic
	_ = isInteractive(os.Stdout)
}
