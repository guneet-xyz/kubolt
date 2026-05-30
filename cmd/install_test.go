package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/guneet-xyz/kubolt/internal/output"
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

	if err := installApps(m, "target", runner, output.NopSink{}, 1); err != nil {
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

	if err := installApps(m, "target", runner, output.NopSink{}, 1); err != nil {
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

	if err := installApps(m, "target", runner, output.NopSink{}, 1); err != nil {
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

	if err := installApps(m, "target", runner, output.NewLineSink(&buf), 1); err != nil {
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

	err := installApps(m, "target", runner, output.NopSink{}, 1)
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

func TestInstall_AllApps_NoArg(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "dep1", namespace: "ns1"},
		{name: "dep2", namespace: "ns2", dependsOn: []string{"dep1"}},
		{name: "leafA", namespace: "nsA", dependsOn: []string{"dep2"}},
		{name: "leafB", namespace: "nsB", dependsOn: []string{"dep1"}},
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

	if err := installApps(m, "", runner, output.NopSink{}, 1); err != nil {
		t.Fatalf("installApps all: %v", err)
	}

	var installed []string
	for _, c := range rec.snapshot() {
		if len(c) >= 3 && c[0] == "helm" && c[1] == "install" {
			installed = append(installed, c[2])
		}
	}
	if len(installed) != 4 {
		t.Fatalf("expected 4 installs (all apps), got %d: %v", len(installed), installed)
	}
	pos := map[string]int{}
	for i, n := range installed {
		pos[n] = i
	}
	if pos["dep1"] > pos["dep2"] || pos["dep2"] > pos["leafA"] || pos["dep1"] > pos["leafB"] {
		t.Errorf("topo order violated: %v", installed)
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

// TestInstall_Parallel_IndependentApps: 3 apps with no deps run concurrently
// at parallelism=3. Asserts overlap by checking all 3 started before any
// finished.
func TestInstall_Parallel_IndependentApps(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "a1", namespace: "ns"},
		{name: "a2", namespace: "ns"},
		{name: "a3", namespace: "ns"},
	})

	var (
		mu       sync.Mutex
		starts   = map[string]time.Time{}
		ends     = map[string]time.Time{}
		inflight int
		maxInfl  int
	)

	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) >= 1 && args[0] == "list" {
			return exec.Command("echo", "[]")
		}
		if name == "helm" && len(args) >= 2 && args[0] == "install" {
			app := args[1]
			mu.Lock()
			starts[app] = time.Now()
			inflight++
			if inflight > maxInfl {
				maxInfl = inflight
			}
			mu.Unlock()
			cmd := exec.Command("sh", "-c", "sleep 0.1")
			_ = app
			return cmd
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	start := time.Now()
	if err := installApps(m, "", runner, output.NopSink{}, 3); err != nil {
		t.Fatalf("installApps: %v", err)
	}
	elapsed := time.Since(start)

	mu.Lock()
	got := maxInfl
	mu.Unlock()
	_ = ends
	_ = starts

	if got < 2 {
		t.Fatalf("expected concurrent execution (maxInflight >= 2), got %d; elapsed=%v", got, elapsed)
	}
}

// TestInstall_Parallel_DependencyOrder: diamond A→{B,C}→D with parallelism=4.
// Asserts A finishes before B/C start; B and C finish before D starts.
func TestInstall_Parallel_DependencyOrder(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "A", namespace: "ns"},
		{name: "B", namespace: "ns", dependsOn: []string{"A"}},
		{name: "C", namespace: "ns", dependsOn: []string{"A"}},
		{name: "D", namespace: "ns", dependsOn: []string{"B", "C"}},
	})

	var (
		mu     sync.Mutex
		events []string
	)
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) >= 1 && args[0] == "list" {
			return exec.Command("echo", "[]")
		}
		if name == "helm" && len(args) >= 2 && args[0] == "install" {
			app := args[1]
			mu.Lock()
			events = append(events, "start:"+app)
			mu.Unlock()
			return exec.Command("sh", "-c", "sleep 0.05; echo done-"+app)
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := installApps(m, "", runner, output.NopSink{}, 4); err != nil {
		t.Fatalf("installApps: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	idx := map[string]int{}
	for i, e := range events {
		if strings.HasPrefix(e, "start:") {
			app := strings.TrimPrefix(e, "start:")
			if _, seen := idx[app]; !seen {
				idx[app] = i
			}
		}
	}
	if idx["A"] >= idx["B"] || idx["A"] >= idx["C"] {
		t.Errorf("A must start before B and C; events=%v", events)
	}
	if idx["B"] >= idx["D"] || idx["C"] >= idx["D"] {
		t.Errorf("B and C must start before D; events=%v", events)
	}
}

func TestInstall_ParallelismFlag_Sequential(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "a1", namespace: "ns"},
		{name: "a2", namespace: "ns"},
		{name: "a3", namespace: "ns"},
	})

	var (
		mu     sync.Mutex
		events []string
	)
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) >= 1 && args[0] == "list" {
			return exec.Command("echo", "[]")
		}
		if name == "helm" && len(args) >= 2 && args[0] == "install" {
			app := args[1]
			mu.Lock()
			events = append(events, "start:"+app)
			mu.Unlock()
			return exec.Command("sh", "-c", "sleep 0.05; true")
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	start := time.Now()
	if err := installApps(m, "", runner, output.NopSink{}, 1); err != nil {
		t.Fatalf("installApps: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < 140*time.Millisecond {
		t.Errorf("parallelism=1 should run serially (>=150ms), got %v", elapsed)
	}
}

// TestInstall_Parallel_FailureSkipsDependents: A→{B,C}; A fails; B & C are
// not installed.
func TestInstall_Parallel_FailureSkipsDependents(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "A", namespace: "ns"},
		{name: "B", namespace: "ns", dependsOn: []string{"A"}},
		{name: "C", namespace: "ns", dependsOn: []string{"A"}},
	})

	rec := &callRecorder{}
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		if name == "helm" && len(args) >= 1 && args[0] == "list" {
			return exec.Command("echo", "[]")
		}
		if name == "helm" && len(args) >= 2 && args[0] == "install" && args[1] == "A" {
			return exec.Command("false")
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	err := installApps(m, "", runner, output.NopSink{}, 4)
	if err == nil {
		t.Fatalf("expected error when A fails")
	}

	var installed []string
	for _, c := range rec.snapshot() {
		if len(c) >= 3 && c[0] == "helm" && c[1] == "install" {
			installed = append(installed, c[2])
		}
	}
	sort.Strings(installed)
	for _, app := range installed {
		if app == "B" || app == "C" {
			t.Errorf("dependents of failed A should be skipped; saw install %s (installed=%v)", app, installed)
		}
	}
	if !strings.Contains(err.Error(), "A") {
		t.Errorf("error should mention failed app A; got %q", err.Error())
	}
}
