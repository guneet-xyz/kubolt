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

	"github.com/guneet-xyz/kubolt/internal/backup"
	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/guneet-xyz/kubolt/internal/output"
	"github.com/guneet-xyz/kubolt/internal/preflight"
)

type backupTestApp struct {
	name      string
	namespace string
	targets   []string
}

func setupBackupManifest(t *testing.T, apps []backupTestApp) *manifest.Manifest {
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
		if len(a.targets) > 0 {
			sb.WriteString("    backup:\n      targets:\n")
			for _, p := range a.targets {
				fmt.Fprintf(&sb, "        - type: filesystem\n          pvc: %s\n", p)
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

type backupCallRecorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (c *backupCallRecorder) record(name string, args ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, append([]string{name}, args...))
}

func (c *backupCallRecorder) snapshot() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]string, len(c.calls))
	copy(out, c.calls)
	return out
}

func (c *backupCallRecorder) joined() string {
	var sb strings.Builder
	for _, call := range c.snapshot() {
		sb.WriteString(strings.Join(call, " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func stubBackupExec(rec *backupCallRecorder) {
	backup.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		return exec.Command("true")
	})
}

func TestBackup_NoArgs_PicksAll(t *testing.T) {
	m := setupBackupManifest(t, []backupTestApp{
		{name: "a1", namespace: "ns1", targets: []string{"pvc-a1"}},
		{name: "a2", namespace: "ns2"},
		{name: "a3", namespace: "ns3", targets: []string{"pvc-a3"}},
	})

	rec := &backupCallRecorder{}
	stubBackupExec(rec)
	defer backup.ResetExecCommand()

	if err := runBackupCore(m, nil, t.TempDir(), "testhost", false); err != nil {
		t.Fatalf("runBackupCore: %v", err)
	}

	out := rec.joined()
	if !strings.Contains(out, "pvc-a1") {
		t.Fatalf("expected backup of pvc-a1, got:\n%s", out)
	}
	if !strings.Contains(out, "pvc-a3") {
		t.Fatalf("expected backup of pvc-a3, got:\n%s", out)
	}
	if strings.Contains(out, "pvc-a2") {
		t.Fatalf("did not expect pvc-a2 (no backup spec) in output:\n%s", out)
	}
}

func TestBackup_ExplicitApps(t *testing.T) {
	m := setupBackupManifest(t, []backupTestApp{
		{name: "a1", namespace: "ns1", targets: []string{"pvc-a1"}},
		{name: "a2", namespace: "ns2", targets: []string{"pvc-a2"}},
		{name: "a3", namespace: "ns3", targets: []string{"pvc-a3"}},
	})

	rec := &backupCallRecorder{}
	stubBackupExec(rec)
	defer backup.ResetExecCommand()

	if err := runBackupCore(m, []string{"a2"}, t.TempDir(), "testhost", false); err != nil {
		t.Fatalf("runBackupCore: %v", err)
	}

	out := rec.joined()
	if !strings.Contains(out, "pvc-a2") {
		t.Fatalf("expected backup of a2's pvc 'pvc-a2', got:\n%s", out)
	}
	if strings.Contains(out, "pvc-a1") || strings.Contains(out, "pvc-a3") {
		t.Fatalf("unexpected backup of non-selected apps:\n%s", out)
	}
}

func TestBackup_InvalidAppName(t *testing.T) {
	m := setupBackupManifest(t, []backupTestApp{
		{name: "a1", namespace: "ns1", targets: []string{"pvc-a1"}},
	})

	rec := &backupCallRecorder{}
	stubBackupExec(rec)
	defer backup.ResetExecCommand()

	err := runBackupCore(m, []string{"does-not-exist"}, t.TempDir(), "testhost", false)
	if err == nil {
		t.Fatalf("expected error for unknown app")
	}
	if !strings.Contains(err.Error(), "unknown app") {
		t.Fatalf("expected 'unknown app' in error, got: %v", err)
	}
	if len(rec.snapshot()) != 0 {
		t.Fatalf("expected no exec calls before validation failure, got %d", len(rec.snapshot()))
	}
}

func TestBackup_AppWithoutBackupSpec(t *testing.T) {
	m := setupBackupManifest(t, []backupTestApp{
		{name: "a1", namespace: "ns1", targets: []string{"pvc-a1"}},
		{name: "a2", namespace: "ns2"},
	})

	rec := &backupCallRecorder{}
	stubBackupExec(rec)
	defer backup.ResetExecCommand()

	err := runBackupCore(m, []string{"a2"}, t.TempDir(), "testhost", false)
	if err == nil {
		t.Fatalf("expected error for app without backup spec")
	}
	if !strings.Contains(err.Error(), "no backup configuration") {
		t.Fatalf("expected 'no backup configuration' in error, got: %v", err)
	}
	if len(rec.snapshot()) != 0 {
		t.Fatalf("expected no exec calls before validation failure, got %d", len(rec.snapshot()))
	}
}

func TestBackup_SSHHostUnset(t *testing.T) {
	orig, hadOrig := os.LookupEnv("KUBOLT_SSH_HOST")
	if err := os.Unsetenv("KUBOLT_SSH_HOST"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	t.Cleanup(func() {
		if hadOrig {
			os.Setenv("KUBOLT_SSH_HOST", orig)
		}
	})

	host, err := preflight.RequireSSHHost()
	if err == nil {
		t.Fatalf("expected error when KUBOLT_SSH_HOST is unset, got host %q", host)
	}
	if !strings.Contains(err.Error(), "KUBOLT_SSH_HOST") {
		t.Fatalf("expected KUBOLT_SSH_HOST in error, got: %v", err)
	}
}

func TestBackup_DryRun(t *testing.T) {
	m := setupBackupManifest(t, []backupTestApp{
		{name: "a1", namespace: "ns1", targets: []string{"pvc-a1"}},
	})

	rec := &backupCallRecorder{}
	stubBackupExec(rec)
	defer backup.ResetExecCommand()

	var buf bytes.Buffer
	origStdout := Stdout
	Stdout = &buf
	t.Cleanup(func() { Stdout = origStdout })

	if err := runBackupCore(m, nil, t.TempDir(), "testhost", true); err != nil {
		t.Fatalf("runBackupCore: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "[dry-run]") {
		t.Fatalf("expected '[dry-run]' lines in output, got:\n%s", out)
	}
	if len(rec.snapshot()) != 0 {
		t.Fatalf("expected no real exec calls in dry-run mode, got %d", len(rec.snapshot()))
	}
}

func TestBackupWithSink_PlainMode(t *testing.T) {
	m := setupBackupManifest(t, []backupTestApp{
		{name: "a1", namespace: "ns1", targets: []string{"pvc-a1"}},
	})

	rec := &backupCallRecorder{}
	stubBackupExec(rec)
	defer backup.ResetExecCommand()

	selected, err := selectApps(m, nil)
	if err != nil {
		t.Fatalf("selectApps: %v", err)
	}

	sink := &recordSink{}
	if _, err := runBackupWithHost(selected, t.TempDir(), "testhost", false, sink); err != nil {
		t.Fatalf("runBackupWithHost: %v", err)
	}

	events := sink.snapshot()
	if len(events) == 0 {
		t.Fatalf("expected events, got none")
	}

	if events[0].Kind != output.TreeStart {
		t.Fatalf("event[0] kind = %s, want TreeStart", events[0].Kind)
	}
	if events[0].Count != 1 {
		t.Fatalf("TreeStart.Count = %d, want 1", events[0].Count)
	}
	if events[len(events)-1].Kind != output.TreeDone {
		t.Fatalf("last event kind = %s, want TreeDone", events[len(events)-1].Kind)
	}

	wantInOrder := []output.EventKind{
		output.TreeStart,
		output.NodeStart,
		output.NodeLine,
		output.NodeDone,
		output.TreeDone,
	}
	idx := 0
	for _, e := range events {
		if idx < len(wantInOrder) && e.Kind == wantInOrder[idx] {
			idx++
		}
	}
	if idx != len(wantInOrder) {
		var got []string
		for _, e := range events {
			got = append(got, e.Kind.String())
		}
		t.Fatalf("missing expected event sequence; got events: %v", got)
	}

	sawCopying := false
	for _, e := range events {
		if e.Kind == output.NodeLine && e.Stage == "copying" {
			sawCopying = true
			break
		}
	}
	if !sawCopying {
		t.Fatalf("expected NodeLine with Stage=copying, did not find it")
	}
}

func TestBackupWithSink_NilSink(t *testing.T) {
	m := setupBackupManifest(t, []backupTestApp{
		{name: "a1", namespace: "ns1", targets: []string{"pvc-a1"}},
	})

	rec := &backupCallRecorder{}
	stubBackupExec(rec)
	defer backup.ResetExecCommand()

	selected, err := selectApps(m, nil)
	if err != nil {
		t.Fatalf("selectApps: %v", err)
	}

	if _, err := runBackupWithHost(selected, t.TempDir(), "testhost", false, output.NopSink{}); err != nil {
		t.Fatalf("runBackupWithHost with NopSink: %v", err)
	}
}
