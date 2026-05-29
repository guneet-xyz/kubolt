package preflight

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func withLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := lookPath
	lookPath = fn
	t.Cleanup(func() { lookPath = orig })
}

func withExecCommand(t *testing.T, fn func(string, ...string) *exec.Cmd) {
	t.Helper()
	orig := execCommand
	execCommand = fn
	t.Cleanup(func() { execCommand = orig })
}

func withEnv(t *testing.T, key, val string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if val == "" {
		os.Unsetenv(key)
	} else {
		os.Setenv(key, val)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(key, prev)
		} else {
			os.Unsetenv(key)
		}
	})
}

func TestRequireBinaries_AllPresent(t *testing.T) {
	withLookPath(t, func(name string) (string, error) { return "/usr/bin/" + name, nil })
	if err := RequireBinaries("helm", "kubectl", "ssh"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRequireBinaries_OneMissing(t *testing.T) {
	withLookPath(t, func(name string) (string, error) {
		if name == "helm" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + name, nil
	})
	err := RequireBinaries("helm", "kubectl")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "helm") {
		t.Errorf("error missing 'helm': %s", msg)
	}
	if !strings.Contains(msg, "https://helm.sh") {
		t.Errorf("error missing install URL: %s", msg)
	}
}

func TestRequireBinaries_MultipleMissing(t *testing.T) {
	withLookPath(t, func(name string) (string, error) { return "", exec.ErrNotFound })
	err := RequireBinaries("helm", "kubectl")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "helm") || !strings.Contains(msg, "kubectl") {
		t.Errorf("error missing one of the binaries: %s", msg)
	}
}

func TestRequireBinaries_UnknownBinaryFallbackHint(t *testing.T) {
	withLookPath(t, func(name string) (string, error) { return "", exec.ErrNotFound })
	err := RequireBinaries("frobnicate")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("expected fallback hint mentioning binary: %s", err.Error())
	}
}

func fakeCmdEcho(output string) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", output)
	}
}

func TestRequireObscuroAuth_KeychainPresent(t *testing.T) {
	withExecCommand(t, fakeCmdEcho("password loaded from keychain"))
	withEnv(t, "OBSCURO_PASSWORD", "")
	if err := RequireObscuroAuth(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRequireObscuroAuth_NoPasswordNoEnv(t *testing.T) {
	withExecCommand(t, fakeCmdEcho("no password configured"))
	withEnv(t, "OBSCURO_PASSWORD", "")
	err := RequireObscuroAuth()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "obscuro auth store") {
		t.Errorf("missing 'obscuro auth store': %s", msg)
	}
	if !strings.Contains(msg, "OBSCURO_PASSWORD") {
		t.Errorf("missing 'OBSCURO_PASSWORD': %s", msg)
	}
}

func TestRequireObscuroAuth_NoPasswordWithEnv(t *testing.T) {
	withExecCommand(t, fakeCmdEcho("no password configured"))
	withEnv(t, "OBSCURO_PASSWORD", "secret")
	if err := RequireObscuroAuth(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRequireSSHHost_Set(t *testing.T) {
	withEnv(t, "KUBOLT_SSH_HOST", "pax")
	host, err := RequireSSHHost()
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if host != "pax" {
		t.Errorf("expected 'pax', got %q", host)
	}
}

func TestRequireSSHHost_Unset(t *testing.T) {
	withEnv(t, "KUBOLT_SSH_HOST", "")
	_, err := RequireSSHHost()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "KUBOLT_SSH_HOST") {
		t.Errorf("missing var name: %s", err.Error())
	}
}

func TestRequirePasswordlessSSH_Success(t *testing.T) {
	withExecCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	})
	if err := RequirePasswordlessSSH("pax"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRequirePasswordlessSSH_Failure(t *testing.T) {
	withExecCommand(t, func(name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	})
	err := RequirePasswordlessSSH("pax")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "pax") {
		t.Errorf("error missing host: %s", err.Error())
	}
}
