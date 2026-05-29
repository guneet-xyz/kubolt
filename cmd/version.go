package cmd

import (
	"fmt"

	"github.com/guneet-xyz/kubolt/internal/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the current version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(Stdout, "kubolt %s\n", version.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
