package helm

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnsurePlugin_AlreadyPresent(t *testing.T) {
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) > 0 && args[0] == "plugin" && args[1] == "list" {
			return exec.Command("echo", "NAME\tVERSION\tDESCRIPTION\nobscuro\t1.0.0\tSecrets post-renderer")
		}
		t.Errorf("unexpected exec call: %s %v", name, args)
		return exec.Command("false")
	}
	defer func() { execCommand = exec.Command }()

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "plugin.yaml"), []byte("name: obscuro\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	r := &Runner{DryRun: false, Stdout: &buf, Stderr: &buf}
	err := EnsurePlugin(r, "obscuro", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("Installing")) {
		t.Error("plugin install should not be called when already present")
	}
}

func TestEnsurePlugin_Missing_InstallsCalled(t *testing.T) {
	installCalled := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) > 0 && args[0] == "plugin" && args[1] == "list" {
			return exec.Command("echo", "NAME\tVERSION\tDESCRIPTION")
		}
		if name == "helm" && len(args) > 0 && args[0] == "plugin" && args[1] == "install" {
			installCalled = true
			return exec.Command("true")
		}
		t.Errorf("unexpected exec call: %s %v", name, args)
		return exec.Command("false")
	}
	defer func() { execCommand = exec.Command }()

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "plugin.yaml"), []byte("name: obscuro\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	r := &Runner{DryRun: false, Stdout: &buf, Stderr: &buf}
	err := EnsurePlugin(r, "obscuro", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !installCalled {
		t.Error("expected plugin install to be called when plugin is missing")
	}
}

func TestEnsurePlugin_MissingPluginYaml(t *testing.T) {
	tmpDir := t.TempDir()

	var buf bytes.Buffer
	r := &Runner{DryRun: false, Stdout: &buf, Stderr: &buf}
	err := EnsurePlugin(r, "obscuro", tmpDir)
	if err == nil {
		t.Fatal("expected error when plugin.yaml is missing")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("plugin.yaml")) {
		t.Errorf("error should mention plugin.yaml, got: %v", err)
	}
}
