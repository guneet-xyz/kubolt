package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/guneet-xyz/kubolt/internal/manifest"
)

// filesystemStrategy backs up a PVC by tarring its host path over SSH.
type filesystemStrategy struct {
	b           *Backuper
	remoteTsDir string
}

func init() {
	registerStrategy(manifest.TargetFilesystem, func(b *Backuper) Strategy {
		return &filesystemStrategy{b: b}
	})
}

// setRemoteTsDir sets the remote staging directory for this strategy.
// Called by the dispatcher before invoking Backup.
func (s *filesystemStrategy) setRemoteTsDir(dir string) {
	s.remoteTsDir = dir
}

// Backup tars the PVC's host path on the SSH host into remoteTsDir.
// The SCP-back-to-local and remote cleanup are handled by the dispatcher.
func (s *filesystemStrategy) Backup(ctx context.Context, app manifest.App, target manifest.Target, localTsDir string) error {
	b := s.b

	if b.DryRun {
		fmt.Fprintf(b.Stdout, "[dry-run] ssh %s tar czf %s/%s.tar.gz -C <hostpath> .\n",
			b.SSHHost, s.remoteTsDir, target.PVC)
		return nil
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("backup cancelled (signal received)")
	default:
	}

	pvName, err := b.captureCmd("kubectl", "get", "pvc", target.PVC, "-n", app.Namespace, "-o", "jsonpath={.spec.volumeName}")
	if err != nil {
		return fmt.Errorf("getting PV name for pvc %s: %w", target.PVC, err)
	}
	hostPath, err := b.captureCmd("kubectl", "get", "pv", strings.TrimSpace(string(pvName)), "-o", "jsonpath={.spec.local.path}")
	if err != nil {
		return fmt.Errorf("getting host path for pv %s: %w", strings.TrimSpace(string(pvName)), err)
	}
	tarDest := fmt.Sprintf("%s/%s.tar.gz", s.remoteTsDir, target.PVC)
	if err := b.runSSH(fmt.Sprintf("tar czf %s -C %s .", tarDest, strings.TrimSpace(string(hostPath)))); err != nil {
		return fmt.Errorf("tar for pvc %s: %w", target.PVC, err)
	}
	return nil
}
