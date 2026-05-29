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
	if err := preflight.RequireBinaries("kubectl", "ssh", "scp", "tar"); err != nil {
		return err
	}
	host, err := preflight.RequireSSHHost()
	if err != nil {
		return err
	}
	if err := preflight.RequirePasswordlessSSH(host); err != nil {
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

	return runBackupCore(m, args, backupDir, host, dryRun)
}

// runBackupCore is the testable core: selects apps, validates, runs BackupApps.
// Selection rules:
//   - No app names provided: pick every app with a non-nil Backup spec.
//   - Names provided: each name must resolve to an app AND that app must have
//     a Backup spec. Otherwise an error is returned before any execution.
func runBackupCore(m *manifest.Manifest, appNames []string, dir, sshHost string, dryRun bool) error {
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
				return fmt.Errorf("unknown app: %q", name)
			}
			if app.Backup == nil {
				return fmt.Errorf("app %q has no backup configuration", name)
			}
			selected = append(selected, *app)
		}
	}

	b := &backup.Backuper{
		SSHHost:   sshHost,
		RemoteTmp: "/tmp/kubolt-backups",
		DryRun:    dryRun,
		Stdout:    Stdout,
		Stderr:    Stderr,
	}
	return b.BackupApps(selected, dir)
}
