package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/installer"
	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/guneet-xyz/kubolt/internal/output"
	"github.com/guneet-xyz/kubolt/internal/preflight"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install [app]",
	Short: "Install an app and its dependencies (or all apps when no arg given)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runInstall,
}

func init() {
	installCmd.Flags().Int("parallelism", -1, "max concurrent installs across the dependency tree (-1 = use manifest or default 4, 1 = sequential)")
	installCmd.Flags().Bool("no-tui", false, "disable interactive TUI; use plain prefixed-line output (deprecated: use --plain)")
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	var target string
	if len(args) == 1 {
		target = args[0]
	}

	if err := preflight.RequireBinaries("helm", "kubectl", "obscuro"); err != nil {
		return err
	}
	if err := preflight.RequireObscuroAuth(); err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	parallelism, _ := cmd.Flags().GetInt("parallelism")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := manifest.Load(filepath.Join(cwd, "kubolt.yaml"))
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	if parallelism == -1 {
		if m.Parallelism > 0 {
			parallelism = m.Parallelism
		} else {
			parallelism = 4
		}
	}
	if parallelism < 1 {
		return fmt.Errorf("parallelism must be >= 1 (or -1 for auto), got %d", parallelism)
	}

	runner := &helm.Runner{
		DryRun: dryRun,
		Stdout: Stdout,
		Stderr: Stderr,
	}

	// Serial plugin install (not concurrency-safe; shared helm plugin dir).
	pluginDir := filepath.Join(m.Dir(), "plugins", "obscuro")
	if _, err := os.Stat(filepath.Join(pluginDir, "plugin.yaml")); err == nil {
		if err := helm.EnsurePlugin(runner, "obscuro", pluginDir); err != nil {
			return fmt.Errorf("ensuring obscuro plugin: %w", err)
		}
	}

	// SIGINT/SIGTERM cancellation lives at the command boundary so the sink
	// (which may run a Bubble Tea program) can observe ctx cancellation too.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	var sink output.Sink
	var bubbleSink *output.BubbleTeaSink
	switch resolveOutputMode(cmd, Stdout) {
	case OutputModeTUI:
		bubbleSink = output.NewBubbleTeaSink(Stdout)
		sink = bubbleSink
	default:
		sink = output.NewLineSink(Stdout)
	}

	if bubbleSink != nil {
		go func() { _ = bubbleSink.Run(ctx) }()
	}

	runErr := installApps(ctx, m, target, runner, sink, parallelism)

	if bubbleSink != nil {
		bubbleSink.Close()
	}

	return runErr
}

// installApps resolves install order and runs helm install/upgrade for each
// app in dependency order using the tree-based installer.Executor. When
// target is "", every app in the manifest is installed. Pure helm/manifest
// logic — no preflight, no TTY detection — so it is directly testable.
func installApps(ctx context.Context, m *manifest.Manifest, target string, runner *helm.Runner, sink output.Sink, parallelism int) error {
	if sink == nil {
		sink = output.NopSink{}
	}
	if parallelism < 1 {
		parallelism = 1
	}

	var appsToInstall []string
	var err error
	if target == "" {
		appsToInstall, err = m.InstallAllOrder()
	} else {
		appsToInstall, err = m.InstallOrder(target)
	}
	if err != nil {
		return fmt.Errorf("resolving install order: %w", err)
	}

	inSet := make(map[string]bool, len(appsToInstall))
	for _, n := range appsToInstall {
		inSet[n] = true
	}

	adj := make(map[string][]string, len(appsToInstall))
	for _, name := range appsToInstall {
		app, ok := m.AppByName(name)
		if !ok {
			return fmt.Errorf("unknown app: %q", name)
		}
		var deps []string
		for _, d := range app.DependsOn {
			if inSet[d] {
				deps = append(deps, d)
			}
		}
		adj[name] = deps
	}

	// `helm dependency build` mutates a shared chart cache and is not
	// concurrency-safe, so it must run serially before any parallel installs.
	manifestDir := m.Dir()
	for _, name := range appsToInstall {
		app, _ := m.AppByName(name)
		chartPath := filepath.Join(manifestDir, app.ChartPath)
		if hasFileDependency(chartPath) {
			if err := runner.Run(helm.BuildDependencyBuild(chartPath)); err != nil {
				return fmt.Errorf("dependency build for %s: %w", name, err)
			}
		}
	}

	jobs := make(map[string]installer.AppJob, len(appsToInstall))
	for _, name := range appsToInstall {
		name := name
		app, _ := m.AppByName(name)
		chartPath := filepath.Join(manifestDir, app.ChartPath)
		jobs[name] = installer.AppJob{
			Name: name,
			Run: func(ctx context.Context, stdout, stderr io.Writer) error {
				out, _ := runner.Capture(helm.BuildList(app.Namespace))
				exists := releaseExists(out, name)

				var valuesFiles []string
				sharedValues := filepath.Join(manifestDir, "values-shared.yaml")
				if _, e := os.Stat(sharedValues); e == nil {
					valuesFiles = append(valuesFiles, sharedValues)
				}
				chartValues := filepath.Join(chartPath, "values.yaml")
				if _, e := os.Stat(chartValues); e == nil {
					valuesFiles = append(valuesFiles, chartValues)
				}

				opts := helm.InstallOpts{
					ForceConflicts: os.Getenv("HELM_FORCE_CONFLICTS") == "1",
					TakeOwnership:  os.Getenv("HELM_TAKE_OWNERSHIP") == "1",
				}

				var helmArgs []string
				if exists {
					helmArgs = helm.BuildUpgrade(name, chartPath, app.Namespace, valuesFiles, opts)
				} else {
					helmArgs = helm.BuildInstall(name, chartPath, app.Namespace, valuesFiles, opts)
				}

				return runner.RunWith(ctx, helmArgs, stdout, stderr)
			},
		}
	}

	exec := &installer.Executor{
		Parallelism: parallelism,
		Sink:        sink,
	}
	result, runErr := exec.Run(ctx, installer.Plan{
		Nodes: adj,
		Jobs:  jobs,
	})

	if len(result.Failed) > 0 {
		return fmt.Errorf("install failed: failed=%v; succeeded=%v; skipped=%v",
			result.Failed, result.Succeeded, result.Skipped)
	}
	return runErr
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
