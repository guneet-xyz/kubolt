package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/guneet-xyz/kubolt/internal/preflight"
	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall <app>",
	Short: "Uninstall an app (blocked if other installed apps depend on it)",
	Args:  cobra.ExactArgs(1),
	RunE:  runUninstall,
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}

func runUninstall(cmd *cobra.Command, args []string) error {
	target := args[0]

	if err := preflight.RequireBinaries("helm", "kubectl"); err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := manifest.Load(filepath.Join(cwd, "kubolt.yaml"))
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	runner := &helm.Runner{
		DryRun: dryRun,
		Stdout: Stdout,
		Stderr: Stderr,
	}

	return uninstallApp(m, target, runner)
}

// uninstallApp removes target via `helm uninstall`. If any installed app still
// depends on target, the uninstall is blocked and no helm calls are made
// beyond the dependent existence checks. Pure helm/manifest logic — no
// preflight, no I/O outside the runner — so it is directly testable.
func uninstallApp(m *manifest.Manifest, target string, runner *helm.Runner) error {
	app, ok := m.AppByName(target)
	if !ok {
		return fmt.Errorf("unknown app: %q", target)
	}

	dependents := m.Dependents(target)
	var blocking []string
	for _, depName := range dependents {
		depApp, ok := m.AppByName(depName)
		if !ok {
			continue
		}
		out, err := runner.Capture(helm.BuildList(depApp.Namespace))
		if err != nil {
			return fmt.Errorf("checking dependent %s: %w", depName, err)
		}
		if releaseExists(out, depName) {
			blocking = append(blocking, depName)
		}
	}

	if len(blocking) > 0 {
		return fmt.Errorf("cannot uninstall %s: still required by installed apps: %s",
			target, strings.Join(blocking, ", "))
	}

	return runner.Run(helm.BuildUninstall(target, app.Namespace))
}
