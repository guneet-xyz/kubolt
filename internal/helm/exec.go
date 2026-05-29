package helm

import (
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
