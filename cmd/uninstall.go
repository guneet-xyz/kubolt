package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/guneet-xyz/kubolt/internal/output"
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
	verbose, _ := cmd.Flags().GetBool("verbose")
	switch resolveOutputMode(cmd, Stdout) {
	case OutputModeTUI:
		bubbleSink = output.NewBubbleTeaSink(Stdout)
		sink = bubbleSink
	default:
		sink = output.NewLineSink(Stdout, verbose)
	}

	if bubbleSink != nil {
		go func() { _ = bubbleSink.Run(ctx) }()
	}

	runErr := uninstallAppWithSink(ctx, m, target, runner, sink)

	if bubbleSink != nil {
		bubbleSink.Close()
	}

	return runErr
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

// uninstallAppWithSink is the sink-aware version of uninstallApp. It performs
// the same dependent-blocking check and helm uninstall, but wraps the helm run
// with tree-vocabulary sink events so a TUI or line sink can render progress.
//
// The blocking check fires before any tree events are emitted, so a blocked
// uninstall produces no TreeStart / NodeStart noise.
func uninstallAppWithSink(ctx context.Context, m *manifest.Manifest, target string, runner *helm.Runner, sink output.Sink) error {
	if sink == nil {
		sink = output.NopSink{}
	}

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

	// Emit tree framing — uninstall is a single-node tree.
	sink.Emit(output.Event{Kind: output.TreeStart, Count: 1})
	sink.Emit(output.Event{Kind: output.NodeReady, App: target})
	sink.Emit(output.Event{Kind: output.NodeStart, App: target})

	stdout := output.NewNodeLineWriter(sink, target, nil, "stdout")
	stderr := output.NewNodeLineWriter(sink, target, nil, "stderr")

	err := runner.RunWith(ctx, helm.BuildUninstall(target, app.Namespace), stdout, stderr)
	stdout.Flush()
	stderr.Flush()

	sink.Emit(output.Event{Kind: output.NodeDone, App: target, Err: err})
	sink.Emit(output.Event{Kind: output.TreeDone})

	return err
}
