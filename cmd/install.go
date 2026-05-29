package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/guneet-xyz/kubolt/internal/preflight"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install <app>",
	Short: "Install an app and its dependencies",
	Args:  cobra.ExactArgs(1),
	RunE:  runInstall,
}

func init() {
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	target := args[0]

	if err := preflight.RequireBinaries("helm", "kubectl", "obscuro"); err != nil {
		return err
	}
	if err := preflight.RequireObscuroAuth(); err != nil {
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

	pluginDir := filepath.Join(m.Dir(), "plugins", "obscuro")
	if _, err := os.Stat(filepath.Join(pluginDir, "plugin.yaml")); err == nil {
		if err := helm.EnsurePlugin(runner, "obscuro", pluginDir); err != nil {
			return fmt.Errorf("ensuring obscuro plugin: %w", err)
		}
	}

	return installApps(m, target, runner)
}

// installApps resolves install order and runs helm install/upgrade for each
// app in dependency order. Pure helm/manifest logic — no preflight, no I/O
// outside the runner — so it is directly testable.
func installApps(m *manifest.Manifest, target string, runner *helm.Runner) error {
	order, err := m.InstallOrder(target)
	if err != nil {
		return fmt.Errorf("resolving install order: %w", err)
	}

	manifestDir := m.Dir()
	var installed []string

	for _, appName := range order {
		app, ok := m.AppByName(appName)
		if !ok {
			return fmt.Errorf("unknown app: %q", appName)
		}
		chartPath := filepath.Join(manifestDir, app.ChartPath)

		if hasFileDependency(chartPath) {
			if err := runner.Run(helm.BuildDependencyBuild(chartPath)); err != nil {
				return fmt.Errorf("dependency build for %s: %w", appName, err)
			}
		}

		out, _ := runner.Capture(helm.BuildList(app.Namespace))
		exists := releaseExists(out, appName)

		var valuesFiles []string
		sharedValues := filepath.Join(manifestDir, "values-shared.yaml")
		if _, err := os.Stat(sharedValues); err == nil {
			valuesFiles = append(valuesFiles, sharedValues)
		}
		chartValues := filepath.Join(chartPath, "values.yaml")
		if _, err := os.Stat(chartValues); err == nil {
			valuesFiles = append(valuesFiles, chartValues)
		}

		opts := helm.InstallOpts{
			ForceConflicts: os.Getenv("HELM_FORCE_CONFLICTS") == "1",
			TakeOwnership:  os.Getenv("HELM_TAKE_OWNERSHIP") == "1",
		}

		var helmArgs []string
		if exists {
			helmArgs = helm.BuildUpgrade(appName, chartPath, app.Namespace, valuesFiles, opts)
		} else {
			helmArgs = helm.BuildInstall(appName, chartPath, app.Namespace, valuesFiles, opts)
		}

		if err := runner.Run(helmArgs); err != nil {
			return fmt.Errorf("failed at %s; deps already applied: %v: %w", appName, installed, err)
		}
		installed = append(installed, appName)
	}
	return nil
}

// hasFileDependency reports whether Chart.yaml declares a file:// repository
// dependency that requires `helm dependency build` before install.
func hasFileDependency(chartPath string) bool {
	data, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, "repository: file://") ||
		strings.Contains(s, `repository: "file://`) ||
		strings.Contains(s, "repository: 'file://")
}

type helmListEntry struct {
	Name string `json:"name"`
}

func releaseExists(data []byte, name string) bool {
	if len(data) == 0 {
		return false
	}
	var releases []helmListEntry
	if err := json.Unmarshal(data, &releases); err != nil {
		return false
	}
	for _, r := range releases {
		if r.Name == name {
			return true
		}
	}
	return false
}
