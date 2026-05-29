package helm

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestRunner_DryRun_SkipsExec(t *testing.T) {
	called := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command(name, args...)
	}
	defer func() { execCommand = exec.Command }()

	var buf bytes.Buffer
	r := &Runner{DryRun: true, Stdout: &buf, Stderr: &buf}
	err := r.Run([]string{"helm", "install", "caddy"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("execCommand should not be called in dry-run mode")
	}
	if !strings.Contains(buf.String(), "[dry-run]") {
		t.Errorf("expected [dry-run] prefix in output, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "helm install caddy") {
		t.Errorf("expected command in output, got: %q", buf.String())
	}
}

func TestRunner_Run_RealExec(t *testing.T) {
	var buf bytes.Buffer
	r := &Runner{DryRun: false, Stdout: &buf, Stderr: &buf}
	err := r.Run([]string{"echo", "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected 'hello' in output, got: %q", buf.String())
	}
}

func TestRunner_Capture_DryRun(t *testing.T) {
	called := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command(name, args...)
	}
	defer func() { execCommand = exec.Command }()

	var buf bytes.Buffer
	r := &Runner{DryRun: true, Stdout: &buf, Stderr: &buf}
	out, err := r.Capture([]string{"helm", "list", "-n", "caddy", "-o", "json"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("execCommand should not be called in dry-run mode")
	}
	if out != nil {
		t.Errorf("expected nil output in dry-run, got: %v", out)
	}
}

func TestRunner_Capture_RealExec(t *testing.T) {
	var buf bytes.Buffer
	r := &Runner{DryRun: false, Stdout: &buf, Stderr: &buf}
	out, err := r.Capture([]string{"echo", "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Errorf("expected 'hello' in captured output, got: %q", string(out))
	}
}

func TestRunner_Run_ErrorWrapped(t *testing.T) {
	var buf bytes.Buffer
	r := &Runner{DryRun: false, Stdout: &buf, Stderr: &buf}
	err := r.Run([]string{"false"})
	if err == nil {
		t.Fatal("expected error from 'false' command")
	}
	if !strings.Contains(err.Error(), "false") {
		t.Errorf("expected command name in error, got: %q", err.Error())
	}
}

func TestRunner_Run_EmptyArgs(t *testing.T) {
	r := &Runner{}
	if err := r.Run(nil); err == nil {
		t.Error("expected error for empty args")
	}
}

func TestRunner_Capture_EmptyArgs(t *testing.T) {
	r := &Runner{}
	if _, err := r.Capture(nil); err == nil {
		t.Error("expected error for empty args")
	}
}

func TestRunner_Capture_ErrorWrapped(t *testing.T) {
	var buf bytes.Buffer
	r := &Runner{DryRun: false, Stdout: &buf, Stderr: &buf}
	_, err := r.Capture([]string{"false"})
	if err == nil {
		t.Fatal("expected error from 'false' command")
	}
	if !strings.Contains(err.Error(), "false") {
		t.Errorf("expected command name in error, got: %q", err.Error())
	}
}
