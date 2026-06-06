package cmd

import (
	"io"

	"github.com/spf13/cobra"
)

// OutputMode controls how install/uninstall/backup render progress.
type OutputMode int

const (
	OutputModeAuto  OutputMode = iota // auto-detect via isInteractive
	OutputModePlain                   // force plain prefixed-line output
	OutputModeTUI                     // force Bubble Tea TUI
)

func (m OutputMode) String() string {
	switch m {
	case OutputModePlain:
		return "plain"
	case OutputModeTUI:
		return "tui"
	default:
		return "auto"
	}
}

// resolveOutputMode returns the effective output mode.
// --plain and --tui are mutually exclusive on rootCmd.
// --verbose overrides to OutputModePlain (no TUI).
// Falls back to TTY auto-detection for auto mode.
func resolveOutputMode(cmd *cobra.Command, w io.Writer) OutputMode {
	verbose, _ := cmd.Flags().GetBool("verbose")
	if verbose {
		return OutputModePlain
	}
	plain, _ := cmd.Flags().GetBool("plain")
	tui, _ := cmd.Flags().GetBool("tui")
	// Also honour legacy --no-tui on install
	noTUI, _ := cmd.Flags().GetBool("no-tui")
	if plain || noTUI {
		return OutputModePlain
	}
	if tui {
		return OutputModeTUI
	}
	if isInteractive(w) {
		return OutputModeTUI
	}
	return OutputModePlain
}
