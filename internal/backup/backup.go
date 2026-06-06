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
	"github.com/guneet-xyz/kubolt/internal/output"
)

// execCommand is the package-level seam for testing (mirrors internal/helm pattern).
var execCommand = exec.Command

// SetExecCommand overrides the exec.Command function (for testing).
func SetExecCommand(fn func(string, ...string) *exec.Cmd) { execCommand = fn }

// ResetExecCommand restores the default exec.Command.
func ResetExecCommand() { execCommand = exec.Command }

// Backuper orchestrates per-target backups via ssh/scp/kubectl.
type Backuper struct {
	SSHHost   string
	RemoteTmp string
	DryRun    bool
	Stdout    io.Writer
	Stderr    io.Writer

	// nowFn is used to generate the timestamp directory. Defaults to time.Now.
	// Exposed for deterministic tests.
	nowFn func() time.Time

	// Sink receives progress events per app. If nil, no events are emitted.
	Sink output.Sink
}

// emit forwards an event to the configured Sink, no-op when Sink is nil.
func (b *Backuper) emit(e output.Event) {
	if b.Sink != nil {
		b.Sink.Emit(e)
	}
}

// BackupApps backs up all configured targets for the given apps into
// localDir/<timestamp>/. Dispatches each target to its registered Strategy.
func (b *Backuper) BackupApps(apps []manifest.App, localDir string) error {
	now := b.nowFn
	if now == nil {
		now = time.Now
	}
	ts := now().Format("2006-01-02_150405")
	localTs := fmt.Sprintf("%s/%s", localDir, ts)
	remoteTsDir := fmt.Sprintf("%s/%s", b.RemoteTmp, ts)

	// Determine if any app has filesystem targets (needs SSH staging).
	needsRemote := false
	for _, app := range apps {
		if app.Backup == nil {
			continue
		}
		for _, t := range app.Backup.Targets {
			if t.Type == manifest.TargetFilesystem {
				needsRemote = true
				break
			}
		}
		if needsRemote {
			break
		}
	}

	b.emit(output.Event{Kind: output.TreeStart, Count: len(apps)})
	defer b.emit(output.Event{Kind: output.TreeDone})

	// Dry-run: print plans for all targets across all apps.
	if b.DryRun {
		fmt.Fprintf(b.Stdout, "[dry-run] mkdir -p %s\n", localTs)
		if needsRemote {
			fmt.Fprintf(b.Stdout, "[dry-run] ssh %s mkdir -p %s\n", b.SSHHost, remoteTsDir)
		}
		for _, app := range apps {
			if app.Backup == nil {
				continue
			}
			for _, target := range app.Backup.Targets {
				strat, err := b.resolveStrategy(target.Type)
				if err != nil {
					return err
				}
				if fs, ok := strat.(*filesystemStrategy); ok {
					fs.setRemoteTsDir(remoteTsDir)
				}
				if err := strat.Backup(context.Background(), app, target, localTs); err != nil {
					return err
				}
			}
		}
		if needsRemote {
			fmt.Fprintf(b.Stdout, "[dry-run] scp -r %s:%s/* %s/\n", b.SSHHost, remoteTsDir, localTs)
		}
		return nil
	}

	// Create local timestamp dir.
	if err := os.MkdirAll(localTs, 0755); err != nil {
		return fmt.Errorf("creating local dir: %w", err)
	}

	// Create remote staging dir only if needed.
	if needsRemote {
		if err := b.runSSH(fmt.Sprintf("mkdir -p %s", remoteTsDir)); err != nil {
			return fmt.Errorf("creating remote dir: %w", err)
		}
	}

	// Signal handler for cleanup.
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

	// Per-app loop.
	for _, app := range apps {
		if app.Backup == nil {
			continue
		}
		b.emit(output.Event{Kind: output.NodeStart, App: app.Name})
		err := b.backupAppTargets(ctx, app, localTs, remoteTsDir)
		b.emit(output.Event{Kind: output.NodeDone, App: app.Name, Err: err})
		if err != nil {
			return err
		}
	}

	// SCP all filesystem outputs back and clean remote.
	if needsRemote {
		scpSrc := fmt.Sprintf("%s:%s/", b.SSHHost, remoteTsDir)
		if err := b.runCmd("scp", "-r", scpSrc, localTs+"/"); err != nil {
			return fmt.Errorf("scp from remote: %w", err)
		}
		if err := b.runSSH(fmt.Sprintf("rm -rf %s", remoteTsDir)); err != nil {
			fmt.Fprintf(b.Stderr, "warning: failed to clean remote tmp: %v\n", err)
		}
	}

	fmt.Fprintf(b.Stdout, "==> Backup complete: %s\n", localTs)
	return nil
}

// backupAppTargets handles scale-down (if needed) and runs each target.
func (b *Backuper) backupAppTargets(ctx context.Context, app manifest.App, localTs, remoteTsDir string) error {
	// Determine if this app has any filesystem targets → scale-down is needed.
	hasFilesystem := false
	for _, t := range app.Backup.Targets {
		if t.Type == manifest.TargetFilesystem {
			hasFilesystem = true
			break
		}
	}

	scaleDown := hasFilesystem && (app.Backup.ScaleDeployments == nil || *app.Backup.ScaleDeployments)

	replicas := map[string]string{}
	if scaleDown {
		b.emit(output.Event{Kind: output.NodeLine, App: app.Name, Stage: "scaling-down"})
		out, err := b.captureCmd("kubectl", "get", "deploy", "-n", app.Namespace, "-o",
			"jsonpath={range .items[*]}{.metadata.name}={.spec.replicas} {end}")
		if err != nil {
			return fmt.Errorf("listing deployments for %s: %w", app.Name, err)
		}
		for _, part := range strings.Fields(string(out)) {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				replicas[kv[0]] = kv[1]
			}
		}
		for _, name := range sortedKeys(replicas) {
			if err := b.runCmd("kubectl", "scale", "deploy", name, "-n", app.Namespace, "--replicas=0"); err != nil {
				return fmt.Errorf("scaling down %s/%s: %w", app.Namespace, name, err)
			}
		}
		if err := b.runCmd("kubectl", "wait", "--for=delete", "pod", "--all", "-n", app.Namespace, "--timeout=120s"); err != nil {
			return fmt.Errorf("scale-down timeout for %s: %w", app.Name, err)
		}
		defer func() {
			b.emit(output.Event{Kind: output.NodeLine, App: app.Name, Stage: "scaling-up"})
			for _, name := range sortedKeys(replicas) {
				count := replicas[name]
				_ = b.runCmd("kubectl", "scale", "deploy", name, "-n", app.Namespace, fmt.Sprintf("--replicas=%s", count))
			}
		}()
	}

	// Check for cancellation after scale-down, before backup work.
	select {
	case <-ctx.Done():
		return fmt.Errorf("backup cancelled (signal received)")
	default:
	}

	// Run each target.
	b.emit(output.Event{Kind: output.NodeLine, App: app.Name, Stage: "copying"})
	for _, target := range app.Backup.Targets {
		strat, err := b.resolveStrategy(target.Type)
		if err != nil {
			return err
		}
		if fs, ok := strat.(*filesystemStrategy); ok {
			fs.setRemoteTsDir(remoteTsDir)
		}
		if err := strat.Backup(ctx, app, target, localTs); err != nil {
			return err
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

func (b *Backuper) captureCmdQuiet(name string, args ...string) ([]byte, error) {
	cmd := execCommand(name, args...)
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
