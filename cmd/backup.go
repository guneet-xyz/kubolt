package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/guneet-xyz/kubolt/internal/backup"
	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/guneet-xyz/kubolt/internal/output"
	"github.com/guneet-xyz/kubolt/internal/preflight"
	"github.com/spf13/cobra"
)

var backupCmd = &cobra.Command{
	Use:   "backup [app ...]",
	Short: "Back up PVCs for one or more apps",
	RunE:  runBackup,
}

var backupDir string

func init() {
	backupCmd.Flags().StringVar(&backupDir, "dir", "./backups", "Local directory to store backups")
	rootCmd.AddCommand(backupCmd)
}

func runBackup(cmd *cobra.Command, args []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	m, err := manifest.Load(filepath.Join(cwd, "kubolt.yaml"))
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	// Select apps first so we can determine which binaries are needed.
	selected, err := selectApps(m, args)
	if err != nil {
		return err
	}

	// kubectl is always required.
	if err := preflight.RequireBinaries("kubectl"); err != nil {
		return err
	}

	// ssh/scp/tar only needed when at least one filesystem target is present.
	var sshHost string
	if anyFilesystemTarget(selected) {
		if err := preflight.RequireBinaries("ssh", "scp", "tar"); err != nil {
			return err
		}
		host, hostErr := preflight.RequireSSHHost()
		if hostErr != nil {
			return hostErr
		}
		if err := preflight.RequirePasswordlessSSH(host); err != nil {
			return err
		}
		sshHost = host
	}

	// Sink selection. BackupApps owns its own ctx + SIGINT handler internally,
	// so we use context.Background() here. TreeDone (emitted via defer in
	// BackupApps) causes BubbleSink to self-quit; Close() below guarantees
	// cleanup even if TreeDone is never received.
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
		go func() { _ = bubbleSink.Run(context.Background()) }()
	}

	runErr := runBackupWithHost(selected, backupDir, sshHost, dryRun, sink)

	if bubbleSink != nil {
		bubbleSink.Close()
	}

	return runErr
}

// selectApps selects apps based on provided names or all apps with backup specs.
// Selection rules:
//   - No app names provided: pick every app with a non-nil Backup spec.
//   - Names provided: each name must resolve to an app AND that app must have
//     a Backup spec. Otherwise an error is returned before any execution.
func selectApps(m *manifest.Manifest, appNames []string) ([]manifest.App, error) {
	var selected []manifest.App

	if len(appNames) == 0 {
		for _, app := range m.Apps {
			if app.Backup != nil {
				selected = append(selected, app)
			}
		}
	} else {
		for _, name := range appNames {
			app, ok := m.AppByName(name)
			if !ok {
				return nil, fmt.Errorf("unknown app: %q", name)
			}
			if app.Backup == nil {
				return nil, fmt.Errorf("app %q has no backup configuration", name)
			}
			selected = append(selected, *app)
		}
	}

	return selected, nil
}

// anyFilesystemTarget reports whether any selected app has a filesystem backup target.
func anyFilesystemTarget(apps []manifest.App) bool {
	for _, app := range apps {
		if app.Backup == nil {
			continue
		}
		for _, t := range app.Backup.Targets {
			if t.Type == manifest.TargetFilesystem {
				return true
			}
		}
	}
	return false
}

// runBackupWithHost creates a Backuper and runs BackupApps.
func runBackupWithHost(selected []manifest.App, dir, sshHost string, dryRun bool, sink output.Sink) error {
	b := &backup.Backuper{
		SSHHost:   sshHost,
		RemoteTmp: "/tmp/kubolt-backups",
		DryRun:    dryRun,
		Stdout:    Stdout,
		Stderr:    Stderr,
		Sink:      sink,
	}
	return b.BackupApps(selected, dir)
}

// runBackupCore is the testable core: selects apps, validates, runs BackupApps.
// Kept for backward compatibility with tests.
// Selection rules:
//   - No app names provided: pick every app with a non-nil Backup spec.
//   - Names provided: each name must resolve to an app AND that app must have
//     a Backup spec. Otherwise an error is returned before any execution.
func runBackupCore(m *manifest.Manifest, appNames []string, dir, sshHost string, dryRun bool) error {
	selected, err := selectApps(m, appNames)
	if err != nil {
		return err
	}
	return runBackupWithHost(selected, dir, sshHost, dryRun, output.NopSink{})
}
