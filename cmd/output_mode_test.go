package cmd

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveOutputMode_PlainFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.PersistentFlags().Bool("no-tui", false, "")
	cmd.SetArgs([]string{"--plain"})
	cmd.Execute()
	mode := resolveOutputMode(cmd, &bytes.Buffer{})
	if mode != OutputModePlain {
		t.Errorf("expected OutputModePlain, got %v", mode)
	}
}

func TestResolveOutputMode_TUIFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.PersistentFlags().Bool("no-tui", false, "")
	cmd.SetArgs([]string{"--tui"})
	cmd.Execute()
	mode := resolveOutputMode(cmd, &bytes.Buffer{})
	if mode != OutputModeTUI {
		t.Errorf("expected OutputModeTUI, got %v", mode)
	}
}

func TestResolveOutputMode_DefaultNonTTY(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.PersistentFlags().Bool("no-tui", false, "")
	cmd.SetArgs([]string{})
	cmd.Execute()
	mode := resolveOutputMode(cmd, &bytes.Buffer{})
	if mode != OutputModePlain {
		t.Errorf("expected OutputModePlain for non-TTY, got %v", mode)
	}
}

func TestResolveOutputMode_NoTUILegacy(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.Flags().Bool("no-tui", false, "")
	cmd.SetArgs([]string{"--no-tui"})
	cmd.Execute()
	mode := resolveOutputMode(cmd, &bytes.Buffer{})
	if mode != OutputModePlain {
		t.Errorf("expected OutputModePlain with --no-tui, got %v", mode)
	}
}

func TestFlagMutualExclusion(t *testing.T) {
	// Build the binary and test mutual exclusion at runtime
	t.Run("mutual_exclusion_error", func(t *testing.T) {
		out, err := exec.Command("go", "build", "-o", "/tmp/kubolt-t5", ".").CombinedOutput()
		if err != nil {
			t.Fatalf("build failed: %v\n%s", err, out)
		}

		// Run with both flags and capture output
		cmd := exec.Command("/tmp/kubolt-t5", "--plain", "--tui", "install", "--dry-run")
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("expected non-zero exit with conflicting flags")
		}

		// Check that error mentions both flags
		errText := string(output)
		if !contains(errText, "plain") || !contains(errText, "tui") {
			t.Errorf("error message should mention both 'plain' and 'tui': %s", errText)
		}
	})
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
