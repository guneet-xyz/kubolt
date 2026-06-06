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
	// Stdout/Stderr are used ONLY for pre-sink writes (e.g. dry-run preview)
	// or post-Close writes drained by the caller (e.g. cleanup warnings,
	// "Backup complete" message). While a Sink is active and per-app writers
	// are bound, all subprocess output is routed through NodeLineWriter or
	// io.Discard — never to these writers — to avoid corrupting a TUI.
	Stdout io.Writer
	Stderr io.Writer

	// nowFn is used to generate the timestamp directory. Defaults to time.Now.
	// Exposed for deterministic tests.
	nowFn func() time.Time

	// Sink receives progress events per app. If nil, no events are emitted.
	Sink output.Sink

	// LocalDir is set on successful completion of BackupApps to the resolved
	// timestamped output directory (e.g. "./backups/2026-01-02_150405").
	// Callers drain this after closing the sink to print a summary line
	// without corrupting an active TUI.
	LocalDir string

	// Warnings collects non-fatal warnings produced during BackupApps while
	// a sink is active (which can't safely write to stderr without corrupting
	// the TUI). Callers drain this after closing the sink and emit each
	// entry to stderr. When no sink is active, warnings are written directly
	// to b.Stderr at the moment they occur.
	Warnings []string

	// currentStdout/currentStderr are per-app subprocess writers, set by
	// BackupApps before each app and cleared after. When set, runCmd /
	// captureCmd route subprocess output here (typically NodeLineWriter
	// instances bound to the active app). When nil, outStdout/outStderr
	// fall back to b.Stdout/b.Stderr — or to io.Discard when a Sink is
	// active to prevent TUI corruption from cluster-level commands.
	currentStdout io.Writer
	currentStderr io.Writer
}

// emit forwards an event to the configured Sink, no-op when Sink is nil.
func (b *Backuper) emit(e output.Event) {
	if b.Sink != nil {
		b.Sink.Emit(e)
	}
}

// outStdout returns the writer to use for subprocess stdout.
// Priority: per-app writer > io.Discard (when sink active, cluster-level call) > b.Stdout.
func (b *Backuper) outStdout() io.Writer {
	if b.currentStdout != nil {
		return b.currentStdout
	}
	if b.Sink != nil {
		// A sink owns the terminal; cluster-level subprocess output (e.g.
		// scp, top-level mkdir) is dropped to avoid TUI corruption.
		return io.Discard
	}
	return b.Stdout
}

// outStderr is the stderr counterpart of outStdout.
func (b *Backuper) outStderr() io.Writer {
	if b.currentStderr != nil {
		return b.currentStderr
	}
	if b.Sink != nil {
		return io.Discard
	}
	return b.Stderr
}

// addWarning records a non-fatal warning. With a sink active, the warning is
// buffered for the caller to drain post-Close; otherwise it is written to
// b.Stderr immediately.
func (b *Backuper) addWarning(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if b.Sink != nil {
		b.Warnings = append(b.Warnings, msg)
		return
	}
	fmt.Fprintln(b.Stderr, msg)
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
	// dry-run is gated to LineSink/NopSink in cmd/backup.go (see runBackup),
	// so direct b.Stdout writes here cannot corrupt a TUI alt-screen.
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

		var stdoutFlush, stderrFlush func()
		if b.Sink != nil {
			nlwOut := output.NewNodeLineWriter(b.Sink, app.Name, nil, "stdout")
			nlwErr := output.NewNodeLineWriter(b.Sink, app.Name, nil, "stderr")
			b.currentStdout, b.currentStderr = nlwOut, nlwErr
			stdoutFlush, stderrFlush = nlwOut.Flush, nlwErr.Flush
		}

		err := b.backupAppTargets(ctx, app, localTs, remoteTsDir)

		if stdoutFlush != nil {
			stdoutFlush()
		}
		if stderrFlush != nil {
			stderrFlush()
		}
		b.currentStdout, b.currentStderr = nil, nil

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
			b.addWarning("warning: failed to clean remote tmp: %v", err)
		}
	}

	b.LocalDir = localTs
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
	cmd.Stdout = b.outStdout()
	cmd.Stderr = b.outStderr()
	return cmd.Run()
}

func (b *Backuper) captureCmd(name string, args ...string) ([]byte, error) {
	cmd := execCommand(name, args...)
	cmd.Stderr = b.outStderr()
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
