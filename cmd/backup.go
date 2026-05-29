package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/guneet-xyz/kubolt/internal/backup"
	"github.com/guneet-xyz/kubolt/internal/manifest"
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
	if anyFilesystemTarget(selected) {
		if err := preflight.RequireBinaries("ssh", "scp", "tar"); err != nil {
			return err
		}
		host, err := preflight.RequireSSHHost()
		if err != nil {
			return err
		}
		if err := preflight.RequirePasswordlessSSH(host); err != nil {
			return err
		}
		return runBackupWithHost(selected, backupDir, host, dryRun)
	}

	return runBackupWithHost(selected, backupDir, "", dryRun)
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
func runBackupWithHost(selected []manifest.App, dir, sshHost string, dryRun bool) error {
	b := &backup.Backuper{
		SSHHost:   sshHost,
		RemoteTmp: "/tmp/kubolt-backups",
		DryRun:    dryRun,
		Stdout:    Stdout,
		Stderr:    Stderr,
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
	return runBackupWithHost(selected, dir, sshHost, dryRun)
}
