// Package preflight provides explicit, composable checks that kubolt commands
// run before invoking external tools. Each Require* function returns a
// human-readable error with remediation guidance; nothing here calls os.Exit
// or runs from init().
package preflight

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Test seams — overridden in unit tests so we never shell out for real.
var (
	lookPath    = exec.LookPath
	execCommand = exec.Command
)

// installHints maps binary names to install instructions surfaced when
// RequireBinaries cannot find the binary on PATH.
var installHints = map[string]string{
	"helm":    "Install Helm v4+: https://helm.sh/docs/intro/install/",
	"kubectl": "Install kubectl: https://kubernetes.io/docs/tasks/tools/",
	"obscuro": "Install obscuro: https://github.com/guneet-xyz/kubolt or https://github.com/janklabs/obscuro",
	"ssh":     "Install OpenSSH client",
	"scp":     "Install OpenSSH client",
	"tar":     "Install tar (usually pre-installed)",
}

// RequireBinaries checks that all named binaries are on PATH. The returned
// error lists every missing binary together with an install hint so users do
// not need to re-run after each fix.
func RequireBinaries(names ...string) error {
	var missing []string
	for _, name := range names {
		if _, err := lookPath(name); err != nil {
			hint := installHints[name]
			if hint == "" {
				hint = fmt.Sprintf("ensure %q is installed and on PATH", name)
			}
			missing = append(missing, fmt.Sprintf("  %s: %s", name, hint))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required binaries:\n%s\n\nCurrent PATH: %s",
			strings.Join(missing, "\n"), os.Getenv("PATH"))
	}
	return nil
}

// RequireObscuroAuth checks that obscuro has a non-interactive password
// source available. The wording matches clusters/pax/deploy.sh's
// ensure_obscuro_ready so users see the same message in both code paths.
func RequireObscuroAuth() error {
	cmd := execCommand("obscuro", "auth", "status")
	out, _ := cmd.CombinedOutput()
	status := string(out)

	if strings.Contains(status, "no password") && os.Getenv("OBSCURO_PASSWORD") == "" {
		return fmt.Errorf(`obscuro has no non-interactive password source.

Helm runs the post-renderer as a subprocess with no TTY, so 'obscuro inject'
cannot prompt for the master password. Provide one of:

  1. Store it in the OS keychain (recommended, one-time):
       obscuro auth store

  2. Export it for this shell:
       export OBSCURO_PASSWORD='...'`)
	}
	return nil
}

// RequireSSHHost reads KUBOLT_SSH_HOST from the environment and returns it.
func RequireSSHHost() (string, error) {
	host := os.Getenv("KUBOLT_SSH_HOST")
	if host == "" {
		return "", fmt.Errorf("KUBOLT_SSH_HOST is not set\n\nSet it to your cluster's SSH alias or hostname, e.g.:\n  export KUBOLT_SSH_HOST=pax")
	}
	return host, nil
}

// RequirePasswordlessSSH verifies that passwordless SSH to host works by
// running `ssh -o BatchMode=yes <host> true`.
func RequirePasswordlessSSH(host string) error {
	cmd := execCommand("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, "true")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("passwordless SSH to %q failed: %w\n\nEnsure SSH key-based auth is configured:\n  ssh-copy-id %s", host, err, host)
	}
	return nil
}
