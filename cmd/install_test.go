package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/manifest"
)

type installTestApp struct {
	name      string
	namespace string
	dependsOn []string
}

func setupInstallManifest(t *testing.T, apps []installTestApp) *manifest.Manifest {
	t.Helper()
	tmpDir := t.TempDir()

	var sb strings.Builder
	sb.WriteString("apiVersion: kubolt.io/v1\napps:\n")
	for _, a := range apps {
		chartDir := filepath.Join(tmpDir, "charts", a.name)
		if err := os.MkdirAll(chartDir, 0o755); err != nil {
			t.Fatalf("mkdir chart dir: %v", err)
		}
		chartYaml := fmt.Sprintf("apiVersion: v2\nname: %s\nversion: 1.0.0\n", a.name)
		if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
			t.Fatalf("write Chart.yaml: %v", err)
		}
		fmt.Fprintf(&sb, "  - name: %s\n    chartPath: charts/%s\n    namespace: %s\n",
			a.name, a.name, a.namespace)
		if len(a.dependsOn) > 0 {
			sb.WriteString("    dependsOn:\n")
			for _, d := range a.dependsOn {
				fmt.Fprintf(&sb, "      - %s\n", d)
			}
		}
	}
	manifestPath := filepath.Join(tmpDir, "kubolt.yaml")
	if err := os.WriteFile(manifestPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write kubolt.yaml: %v", err)
	}

	m, err := manifest.Load(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return m
}

type callRecorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (c *callRecorder) record(name string, args ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, append([]string{name}, args...))
}

func (c *callRecorder) snapshot() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]string, len(c.calls))
	copy(out, c.calls)
	return out
}

func TestInstall_WithDeps_CorrectOrder(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "dep1", namespace: "ns1"},
		{name: "dep2", namespace: "ns2", dependsOn: []string{"dep1"}},
		{name: "target", namespace: "ns3", dependsOn: []string{"dep2"}},
	})

	rec := &callRecorder{}
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		if name == "helm" && len(args) >= 1 && args[0] == "list" {
			return exec.Command("echo", "[]")
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := installApps(m, "target", runner); err != nil {
		t.Fatalf("installApps error: %v", err)
	}

	var installOrder []string
	for _, c := range rec.snapshot() {
		if len(c) >= 3 && c[0] == "helm" && c[1] == "install" {
			installOrder = append(installOrder, c[2])
		}
	}
	want := []string{"dep1", "dep2", "target"}
	if !equalStrings(installOrder, want) {
		t.Fatalf("install order = %v, want %v", installOrder, want)
	}
}

func TestInstall_ExistingRelease_Upgrades(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "ns1"},
	})

	rec := &callRecorder{}
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		if name == "helm" && len(args) >= 1 && args[0] == "list" {
			return exec.Command("echo", `[{"name":"target"}]`)
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := installApps(m, "target", runner); err != nil {
		t.Fatalf("installApps error: %v", err)
	}

	var sawUpgrade, sawInstall bool
	for _, c := range rec.snapshot() {
		if len(c) >= 2 && c[0] == "helm" {
			if c[1] == "upgrade" {
				sawUpgrade = true
			}
			if c[1] == "install" {
				sawInstall = true
			}
		}
	}
	if !sawUpgrade {
		t.Errorf("expected helm upgrade call; got %v", rec.snapshot())
	}
	if sawInstall {
		t.Errorf("expected NO helm install call; got %v", rec.snapshot())
	}
}

func TestInstall_ForceConflicts_Flag(t *testing.T) {
	t.Setenv("HELM_FORCE_CONFLICTS", "1")

	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "ns1"},
	})

	rec := &callRecorder{}
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		if name == "helm" && len(args) >= 1 && args[0] == "list" {
			return exec.Command("echo", "[]")
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := installApps(m, "target", runner); err != nil {
		t.Fatalf("installApps error: %v", err)
	}

	var foundFlag bool
	for _, c := range rec.snapshot() {
		if len(c) >= 2 && c[0] == "helm" && c[1] == "install" {
			for _, a := range c {
				if a == "--force-conflicts" {
					foundFlag = true
				}
			}
		}
	}
	if !foundFlag {
		t.Fatalf("expected --force-conflicts in helm install args; got %v", rec.snapshot())
	}
}

func TestInstall_DryRun(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "ns1"},
	})

	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		t.Fatalf("dry-run must not exec; got %s %v", name, args)
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{DryRun: true, Stdout: &buf, Stderr: &buf}

	if err := installApps(m, "target", runner); err != nil {
		t.Fatalf("installApps error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("expected [dry-run] in output; got %q", out)
	}
	if !strings.Contains(out, "helm install target") {
		t.Fatalf("expected dry-run helm install command in output; got %q", out)
	}
}

func TestInstall_MidChainFailure(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "dep1", namespace: "ns1"},
		{name: "dep2", namespace: "ns2", dependsOn: []string{"dep1"}},
		{name: "target", namespace: "ns3", dependsOn: []string{"dep2"}},
	})

	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) >= 1 && args[0] == "list" {
			return exec.Command("echo", "[]")
		}
		if name == "helm" && len(args) >= 3 && args[0] == "install" && args[1] == "dep2" {
			return exec.Command("false")
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	err := installApps(m, "target", runner)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "dep2") {
		t.Errorf("expected error to mention dep2; got %q", msg)
	}
	if !strings.Contains(msg, "dep1") {
		t.Errorf("expected error to list dep1 as already applied; got %q", msg)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
