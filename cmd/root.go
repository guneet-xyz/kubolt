package cmd

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

var rootCmd = &cobra.Command{
	Use:   "kubolt",
	Short: "Kubernetes cluster management CLI",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolP("dry-run", "", false, "Print commands without executing them")
	rootCmd.PersistentFlags().Bool("plain", false, "force plain prefixed-line output (no TUI)")
	rootCmd.PersistentFlags().Bool("tui", false, "force Bubble Tea TUI (even when not a terminal)")
	rootCmd.MarkFlagsMutuallyExclusive("plain", "tui")
}
