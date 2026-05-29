package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/guneet-xyz/kubolt/internal/manifest"
)

// execCommand is the package-level seam for testing (mirrors internal/helm pattern).
var execCommand = exec.Command

// SetExecCommand overrides the exec.Command function (for testing).
func SetExecCommand(fn func(string, ...string) *exec.Cmd) { execCommand = fn }

// ResetExecCommand restores the default exec.Command.
func ResetExecCommand() { execCommand = exec.Command }

// Backuper orchestrates PVC backups via ssh/scp/kubectl.
type Backuper struct {
	SSHHost   string
	RemoteTmp string
	DryRun    bool
	Stdout    io.Writer
	Stderr    io.Writer

	// nowFn is used to generate the timestamp directory. Defaults to time.Now.
	// Exposed for deterministic tests.
	nowFn func() time.Time
}

// BackupApps backs up PVCs for the given apps into localDir/<timestamp>/.
func (b *Backuper) BackupApps(apps []manifest.App, localDir string) error {
	now := b.nowFn
	if now == nil {
		now = time.Now
	}
	ts := now().Format("2006-01-02_150405")
	localTs := fmt.Sprintf("%s/%s", localDir, ts)
	remoteTsDir := fmt.Sprintf("%s/%s", b.RemoteTmp, ts)

	if b.DryRun {
		fmt.Fprintf(b.Stdout, "[dry-run] mkdir -p %s\n", localTs)
		fmt.Fprintf(b.Stdout, "[dry-run] ssh %s mkdir -p %s\n", b.SSHHost, remoteTsDir)
		for _, app := range apps {
			if app.Backup == nil {
				continue
			}
			for _, pvc := range app.Backup.PVCs {
				fmt.Fprintf(b.Stdout, "[dry-run] ssh %s tar czf %s/%s.tar.gz ...\n", b.SSHHost, remoteTsDir, pvc)
			}
		}
		fmt.Fprintf(b.Stdout, "[dry-run] scp -r %s:%s/* %s/\n", b.SSHHost, remoteTsDir, localTs)
		return nil
	}

	// Create local timestamp dir
	if err := os.MkdirAll(localTs, 0755); err != nil {
		return fmt.Errorf("creating local dir: %w", err)
	}

	// Create remote timestamp dir
	if err := b.runSSH(fmt.Sprintf("mkdir -p %s", remoteTsDir)); err != nil {
		return fmt.Errorf("creating remote dir: %w", err)
	}

	// Signal handler for cleanup
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

	for _, app := range apps {
		if app.Backup == nil {
			continue
		}
		if err := b.backupApp(ctx, app, remoteTsDir); err != nil {
			return err
		}
	}

	// SCP from remote to local
	scpSrc := fmt.Sprintf("%s:%s/", b.SSHHost, remoteTsDir)
	if err := b.runCmd("scp", "-r", scpSrc, localTs+"/"); err != nil {
		return fmt.Errorf("scp from remote: %w", err)
	}

	// Cleanup remote
	if err := b.runSSH(fmt.Sprintf("rm -rf %s", remoteTsDir)); err != nil {
		fmt.Fprintf(b.Stderr, "warning: failed to clean remote tmp: %v\n", err)
	}

	fmt.Fprintf(b.Stdout, "==> Backup complete: %s\n", localTs)
	return nil
}

func (b *Backuper) backupApp(ctx context.Context, app manifest.App, remoteTsDir string) error {
	scaleDown := app.Backup.ScaleDeployments == nil || *app.Backup.ScaleDeployments

	// Save replica counts and scale down
	replicas := map[string]string{}
	if scaleDown {
		// Get deployments
		out, err := b.captureCmd("kubectl", "get", "deploy", "-n", app.Namespace, "-o", "jsonpath={range .items[*]}{.metadata.name}={.spec.replicas} {end}")
		if err != nil {
			return fmt.Errorf("listing deployments for %s: %w", app.Name, err)
		}
		// Parse "name=N name2=N2 ..."
		for _, part := range strings.Fields(string(out)) {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				replicas[kv[0]] = kv[1]
			}
		}

		// Scale each to 0 (sorted for deterministic order)
		names := sortedKeys(replicas)
		for _, name := range names {
			if err := b.runCmd("kubectl", "scale", "deploy", name, "-n", app.Namespace, "--replicas=0"); err != nil {
				return fmt.Errorf("scaling down %s/%s: %w", app.Namespace, name, err)
			}
		}

		// Wait for pods to delete
		if err := b.runCmd("kubectl", "wait", "--for=delete", "pod", "--all", "-n", app.Namespace, "--timeout=120s"); err != nil {
			return fmt.Errorf("scale-down timeout for %s: %w", app.Name, err)
		}

		// Defer restore replicas
		defer func() {
			for _, name := range sortedKeys(replicas) {
				count := replicas[name]
				_ = b.runCmd("kubectl", "scale", "deploy", name, "-n", app.Namespace, fmt.Sprintf("--replicas=%s", count))
			}
		}()
	}

	// Check for context cancellation (SIGINT)
	select {
	case <-ctx.Done():
		return fmt.Errorf("backup cancelled (signal received)")
	default:
	}

	// Tar each PVC
	for _, pvc := range app.Backup.PVCs {
		// Get PV host path
		pvName, err := b.captureCmd("kubectl", "get", "pvc", pvc, "-n", app.Namespace, "-o", "jsonpath={.spec.volumeName}")
		if err != nil {
			return fmt.Errorf("getting PV name for pvc %s: %w", pvc, err)
		}
		hostPath, err := b.captureCmd("kubectl", "get", "pv", strings.TrimSpace(string(pvName)), "-o", "jsonpath={.spec.local.path}")
		if err != nil {
			return fmt.Errorf("getting host path for pv %s: %w", string(pvName), err)
		}
		tarDest := fmt.Sprintf("%s/%s.tar.gz", remoteTsDir, pvc)
		if err := b.runSSH(fmt.Sprintf("tar czf %s -C %s .", tarDest, strings.TrimSpace(string(hostPath)))); err != nil {
			return fmt.Errorf("tar for pvc %s: %w", pvc, err)
		}
	}

	return nil
}

func (b *Backuper) runSSH(cmd string) error {
	return b.runCmd("ssh", b.SSHHost, cmd)
}

func (b *Backuper) runCmd(name string, args ...string) error {
	cmd := execCommand(name, args...)
	cmd.Stdout = b.Stdout
	cmd.Stderr = b.Stderr
	return cmd.Run()
}

func (b *Backuper) captureCmd(name string, args ...string) ([]byte, error) {
	cmd := execCommand(name, args...)
	cmd.Stderr = b.Stderr
	return cmd.Output()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort to avoid pulling in "sort" (cheap, small maps)
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
