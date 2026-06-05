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
	// Test via cobra's flag parsing directly without building binary
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().Bool("plain", false, "")
	cmd.PersistentFlags().Bool("tui", false, "")
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

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
