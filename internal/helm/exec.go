package helm

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// execCommand is the test seam for exec.Command.
// Override in tests to stub subprocess execution.
var execCommand = exec.Command

// SetExecCommand overrides the exec.Command function (for testing).
func SetExecCommand(fn func(string, ...string) *exec.Cmd) {
	execCommand = fn
}

// ResetExecCommand restores the default exec.Command.
func ResetExecCommand() {
	execCommand = exec.Command
}

// Runner executes helm/kubectl/ssh commands.
type Runner struct {
	DryRun bool
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes the given command args.
// In dry-run mode, prints "[dry-run] <cmd>" to Stdout and returns nil without executing.
func (r *Runner) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("empty command args")
	}
	if r.DryRun {
		fmt.Fprintf(r.Stdout, "[dry-run] %s\n", strings.Join(args, " "))
		return nil
	}
	cmd := execCommand(args[0], args[1:]...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running %s: %w", args[0], err)
	}
	return nil
}

// RunWith executes the given command args with context and per-call stdout/stderr writers.
// In dry-run mode, prints "[dry-run] <cmd>" to the provided stdout and returns nil without executing.
// The context can be used to cancel the command; if ctx is canceled while the process is running,
// the process will be killed and ctx.Err() will be returned.
func (r *Runner) RunWith(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("empty command args")
	}
	if r.DryRun {
		fmt.Fprintf(stdout, "[dry-run] %s\n", strings.Join(args, " "))
		return nil
	}
	cmd := execCommand(args[0], args[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", args[0], err)
	}
	// Wait for completion or context cancellation
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("running %s: %w", args[0], err)
		}
		return nil
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done // drain the done channel to allow goroutine to complete
		return ctx.Err()
	}
}

// Capture executes the command and returns its combined stdout+stderr output.
// In dry-run mode, returns empty bytes and nil without executing.
func (r *Runner) Capture(args []string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("empty command args")
	}
	if r.DryRun {
		fmt.Fprintf(r.Stdout, "[dry-run] %s\n", strings.Join(args, " "))
		return nil, nil
	}
	cmd := execCommand(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("running %s: %w", args[0], err)
	}
	return out, nil
}
