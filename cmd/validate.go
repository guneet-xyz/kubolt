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

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate all apps render with `helm template`",
	Args:  cobra.NoArgs,
	RunE:  runValidate,
}

func init() {
	rootCmd.AddCommand(validateCmd)
}

func runValidate(cmd *cobra.Command, args []string) error {
	if err := preflight.RequireBinaries("helm"); err != nil {
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

	return validateApps(m, runner, m.Dir())
}

// validateApps templates every app in the manifest, returning a non-nil error
// if any chart fails to render. Library charts are skipped. Charts with a
// file:// dependency get a `helm dependency build` before templating.
func validateApps(m *manifest.Manifest, runner *helm.Runner, manifestDir string) error {
	var failed []string
	total := 0
	ok := 0

	for _, app := range m.Apps {
		chartPath := filepath.Join(manifestDir, app.ChartPath)

		if isLibraryChart(chartPath) {
			fmt.Fprintf(Stdout, "==> skip %s (library chart)\n", app.Name)
			continue
		}

		total++

		if hasFileDependency(chartPath) {
			if err := runner.Run(helm.BuildDependencyBuild(chartPath)); err != nil {
				fmt.Fprintf(Stderr, "==> %s: dependency build failed: %v\n", app.Name, err)
				failed = append(failed, app.Name)
				continue
			}
		}

		var valuesFiles []string
		sharedValues := filepath.Join(manifestDir, "values-shared.yaml")
		if _, err := os.Stat(sharedValues); err == nil {
			valuesFiles = append(valuesFiles, sharedValues)
		}
		chartValues := filepath.Join(chartPath, "values.yaml")
		if _, err := os.Stat(chartValues); err == nil {
			valuesFiles = append(valuesFiles, chartValues)
		}

		args := helm.BuildTemplate(app.Name, chartPath, app.Namespace, valuesFiles)

		// Capture and discard stdout; only exit code matters.
		if _, err := runner.Capture(args); err != nil {
			fmt.Fprintf(Stderr, "==> %s: template failed: %v\n", app.Name, err)
			failed = append(failed, app.Name)
			continue
		}

		ok++
	}

	fmt.Fprintf(Stdout, "%d/%d charts OK\n", ok, total)

	if len(failed) > 0 {
		return fmt.Errorf("validation failed for: %s", strings.Join(failed, ", "))
	}
	return nil
}

// isLibraryChart reports whether the chart at chartPath has `type: library`
// in its Chart.yaml.
func isLibraryChart(chartPath string) bool {
	data, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "type: library" ||
			trimmed == `type: "library"` ||
			trimmed == "type: 'library'" {
			return true
		}
	}
	return false
}
