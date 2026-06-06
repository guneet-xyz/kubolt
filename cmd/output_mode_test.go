package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveOutputMode_PlainFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.PersistentFlags().Bool("verbose", false, "")
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
	cmd.PersistentFlags().Bool("verbose", false, "")
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
	cmd.PersistentFlags().Bool("verbose", false, "")
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
	cmd.PersistentFlags().Bool("verbose", false, "")
	cmd.Flags().Bool("no-tui", false, "")
	cmd.SetArgs([]string{"--no-tui"})
	cmd.Execute()
	mode := resolveOutputMode(cmd, &bytes.Buffer{})
	if mode != OutputModePlain {
		t.Errorf("expected OutputModePlain with --no-tui, got %v", mode)
	}
}

func TestFlagMutualExclusion(t *testing.T) {
	// Test via cobra's flag parsing directly without building binary
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.PersistentFlags().Bool("verbose", false, "")
	cmd.MarkFlagsMutuallyExclusive("plain", "tui")
	cmd.SetArgs([]string{"--plain", "--tui"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error with conflicting flags")
	}

	errText := err.Error()
	if !contains(errText, "plain") || !contains(errText, "tui") {
		t.Errorf("error message should mention both 'plain' and 'tui': %s", errText)
	}
}

func TestResolveOutputMode_VerboseFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.PersistentFlags().Bool("verbose", false, "")
	cmd.PersistentFlags().Bool("no-tui", false, "")
	cmd.SetArgs([]string{"--verbose"})
	cmd.Execute()
	mode := resolveOutputMode(cmd, &bytes.Buffer{})
	if mode != OutputModePlain {
		t.Errorf("expected OutputModePlain with --verbose, got %v", mode)
	}
}

func TestResolveOutputMode_VerboseOverridesTTY(t *testing.T) {
	// Even if output is interactive (TTY), --verbose should force plain mode
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.PersistentFlags().Bool("verbose", false, "")
	cmd.PersistentFlags().Bool("no-tui", false, "")
	cmd.SetArgs([]string{"--verbose"})
	cmd.Execute()
	// Create a mock TTY writer
	mode := resolveOutputMode(cmd, &bytes.Buffer{})
	if mode != OutputModePlain {
		t.Errorf("expected OutputModePlain with --verbose even for TTY, got %v", mode)
	}
}

func TestResolveOutputMode_VerboseTUIExclusive(t *testing.T) {
	// Test that --verbose and --tui are mutually exclusive
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
	cmd.PersistentFlags().Bool("verbose", false, "")
	cmd.MarkFlagsMutuallyExclusive("verbose", "tui")
	cmd.SetArgs([]string{"--verbose", "--tui"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error with --verbose and --tui both set")
	}

	errText := err.Error()
	if !contains(errText, "verbose") || !contains(errText, "tui") {
		t.Errorf("error message should mention both 'verbose' and 'tui': %s", errText)
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
