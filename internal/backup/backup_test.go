package backup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/guneet-xyz/kubolt/internal/output"
)

type stubResponse struct {
	stdout   string
	exitCode int
}

type stubRecorder struct {
	mu       sync.Mutex
	calls    []string
	matchers map[string]stubResponse
	beforeFn func(name string, args []string)
}

func newStub() *stubRecorder {
	return &stubRecorder{matchers: map[string]stubResponse{}}
}

func (s *stubRecorder) setOutput(prefix, stdout string) {
	s.matchers[prefix] = stubResponse{stdout: stdout}
}

func (s *stubRecorder) setFailure(prefix string, exitCode int) {
	s.matchers[prefix] = stubResponse{exitCode: exitCode}
}

func (s *stubRecorder) record(name string, args []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
}

func (s *stubRecorder) getCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *stubRecorder) lookup(full string) stubResponse {
	var best string
	var bestResp stubResponse
	for prefix, resp := range s.matchers {
		if strings.HasPrefix(full, prefix) && len(prefix) > len(best) {
			best = prefix
			bestResp = resp
		}
	}
	return bestResp
}

func (s *stubRecorder) execFn() func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		full := strings.TrimSpace(name + " " + strings.Join(args, " "))
		s.record(name, args)
		if s.beforeFn != nil {
			s.beforeFn(name, args)
		}
		resp := s.lookup(full)
		helper := []string{"-test.run=TestHelperProcess", "--", fmt.Sprintf("%d", resp.exitCode), resp.stdout}
		cmd := exec.Command(os.Args[0], helper...)
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
		return cmd
	}
}

// TestHelperProcess is a fake subprocess used by exec stubs.
// It reads exit code and stdout from os.Args and writes them out.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" && i+2 < len(args) {
			exitCode := args[i+1]
			stdout := args[i+2]
			fmt.Fprint(os.Stdout, stdout)
			if exitCode != "0" {
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(0)
}

func boolPtr(b bool) *bool { return &b }

func newBackuper(stdout, stderr *bytes.Buffer) *Backuper {
	return &Backuper{
		SSHHost:   "pax",
		RemoteTmp: "/tmp/k3s-backups",
		Stdout:    stdout,
		Stderr:    stderr,
	}
}

func fsTarget(pvc string) manifest.Target {
	return manifest.Target{Type: manifest.TargetFilesystem, PVC: pvc}
}

func pgTarget(selector string) manifest.Target {
	return manifest.Target{Type: manifest.TargetPgDump, PodSelector: selector}
}

func TestBackup_HappyPath(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get deploy", "web=2 ")
	stub.setOutput("kubectl get pvc caddy-data", "pv-caddy")
	stub.setOutput("kubectl get pv pv-caddy", "/var/lib/data/caddy")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	dir := t.TempDir()

	apps := []manifest.App{{
		Name:      "caddy",
		Namespace: "caddy",
		Backup: &manifest.BackupSpec{
			Targets:          []manifest.Target{fsTarget("caddy-data")},
			ScaleDeployments: boolPtr(true),
		},
	}}

	if err := b.BackupApps(apps, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := stub.getCalls()
	wantOrder := []string{
		"ssh pax mkdir -p",
		"kubectl get deploy -n caddy",
		"kubectl scale deploy web -n caddy --replicas=0",
		"kubectl wait --for=delete pod --all -n caddy",
		"kubectl get pvc caddy-data -n caddy",
		"kubectl get pv pv-caddy",
		"ssh pax tar czf /tmp/k3s-backups",
		"kubectl scale deploy web -n caddy --replicas=2",
		"scp -r pax:/tmp/k3s-backups",
		"ssh pax rm -rf /tmp/k3s-backups",
	}

	idx := 0
	for _, want := range wantOrder {
		found := false
		for ; idx < len(calls); idx++ {
			if strings.HasPrefix(calls[idx], want) {
				found = true
				idx++
				break
			}
		}
		if !found {
			t.Errorf("expected call starting with %q not found in order\ncalls:\n  %s", want, strings.Join(calls, "\n  "))
		}
	}
}

func TestBackup_TarFailure_RestoresReplicas(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get deploy", "web=3 ")
	stub.setOutput("kubectl get pvc data", "pv-data")
	stub.setOutput("kubectl get pv pv-data", "/srv/data")
	stub.setFailure("ssh pax tar", 1)

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	dir := t.TempDir()

	apps := []manifest.App{{
		Name:      "app",
		Namespace: "app",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{fsTarget("data")},
		},
	}}

	err := b.BackupApps(apps, dir)
	if err == nil {
		t.Fatal("expected error from tar failure")
	}

	calls := stub.getCalls()
	tarIdx, restoreIdx := -1, -1
	for i, c := range calls {
		if strings.HasPrefix(c, "ssh pax tar") && tarIdx < 0 {
			tarIdx = i
		}
		if strings.HasPrefix(c, "kubectl scale deploy web -n app --replicas=3") {
			restoreIdx = i
		}
	}
	if tarIdx < 0 {
		t.Fatalf("tar call not recorded\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
	if restoreIdx < 0 {
		t.Fatalf("replica restore not called\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
	if restoreIdx < tarIdx {
		t.Errorf("restore (idx=%d) should occur after tar failure (idx=%d)", restoreIdx, tarIdx)
	}
}

func TestBackup_SIGINT_TriggersRestore(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get deploy", "web=1 ")

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)

	app := manifest.App{
		Name:      "app",
		Namespace: "app",
		Backup: &manifest.BackupSpec{
			Targets:          []manifest.Target{fsTarget("data")},
			ScaleDeployments: boolPtr(true),
		},
	}

	err := b.backupAppTargets(cancelledCtx, app, "/tmp/local/ts", "/tmp/k3s-backups/ts")
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancellation error, got: %v", err)
	}

	calls := stub.getCalls()
	foundRestore := false
	for _, c := range calls {
		if strings.HasPrefix(c, "kubectl scale deploy web -n app --replicas=1") {
			foundRestore = true
		}
	}
	if !foundRestore {
		t.Errorf("expected replica restore after SIGINT-style cancel\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
}

func TestBackup_ScaleDeploymentsFalse(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get pvc data", "pv-data")
	stub.setOutput("kubectl get pv pv-data", "/srv/data")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	dir := t.TempDir()

	apps := []manifest.App{{
		Name:      "app",
		Namespace: "app",
		Backup: &manifest.BackupSpec{
			Targets:          []manifest.Target{fsTarget("data")},
			ScaleDeployments: boolPtr(false),
		},
	}}

	if err := b.BackupApps(apps, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := stub.getCalls()
	hasTar := false
	for _, c := range calls {
		if strings.HasPrefix(c, "kubectl scale") {
			t.Errorf("unexpected kubectl scale call: %s", c)
		}
		if strings.HasPrefix(c, "kubectl wait") {
			t.Errorf("unexpected kubectl wait call: %s", c)
		}
		if strings.HasPrefix(c, "ssh pax tar") {
			hasTar = true
		}
	}
	if !hasTar {
		t.Errorf("expected tar call\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
}

func TestBackup_NilBackup_Skipped(t *testing.T) {
	stub := newStub()

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	dir := t.TempDir()

	apps := []manifest.App{
		{Name: "no-backup", Namespace: "x", Backup: nil},
	}

	if err := b.BackupApps(apps, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, c := range stub.getCalls() {
		if strings.HasPrefix(c, "kubectl") {
			t.Errorf("no kubectl calls expected for nil-backup app: %s", c)
		}
		if strings.HasPrefix(c, "ssh pax tar") {
			t.Errorf("no tar calls expected for nil-backup app: %s", c)
		}
	}
}

func TestBackup_DryRun(t *testing.T) {
	called := false
	SetExecCommand(func(name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command(name, args...)
	})
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	b.DryRun = true

	apps := []manifest.App{{
		Name:      "caddy",
		Namespace: "caddy",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{fsTarget("caddy-data")},
		},
	}}

	if err := b.BackupApps(apps, t.TempDir()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("execCommand should not be invoked in dry-run mode")
	}
	out := stdout.String()
	if !strings.Contains(out, "[dry-run]") {
		t.Errorf("expected [dry-run] in output, got: %q", out)
	}
	if !strings.Contains(out, "tar czf") {
		t.Errorf("expected tar czf in dry-run output, got: %q", out)
	}
	if !strings.Contains(out, "scp -r pax:") {
		t.Errorf("expected scp line in dry-run output, got: %q", out)
	}
}

func TestBackup_PgDumpOnly_NoSSH(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get pod -n db", "postgres-0 ")
	stub.setOutput("kubectl exec -n db postgres-0 -- printenv PGDATABASE", "mydb")
	stub.setOutput(pgDumpExecPrefix("db", "postgres-0"), "DUMPBYTES")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	dir := t.TempDir()

	apps := []manifest.App{{
		Name:      "pg",
		Namespace: "db",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{pgTarget("app=postgres")},
		},
	}}

	if err := b.BackupApps(apps, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := stub.getCalls()
	pgDumpCalled := false
	for _, c := range calls {
		if strings.HasPrefix(c, "ssh ") {
			t.Errorf("no ssh calls expected for pg_dump-only backup: %s", c)
		}
		if strings.HasPrefix(c, "scp ") {
			t.Errorf("no scp calls expected for pg_dump-only backup: %s", c)
		}
		if strings.HasPrefix(c, "kubectl scale") {
			t.Errorf("no scale calls expected for pg_dump-only backup: %s", c)
		}
		if strings.HasPrefix(c, "kubectl wait") {
			t.Errorf("no wait calls expected for pg_dump-only backup: %s", c)
		}
		if strings.HasPrefix(c, pgDumpExecPrefix("db", "postgres-0")) {
			pgDumpCalled = true
		}
	}
	if !pgDumpCalled {
		t.Fatalf("expected pg_dump exec call\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one timestamp dir, got %d", len(entries))
	}
	dumpPath := filepath.Join(dir, entries[0].Name(), "pg-mydb.dump")
	data, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("reading dump file %s: %v", dumpPath, err)
	}
	if string(data) != "DUMPBYTES" {
		t.Errorf("dump contents: got %q, want %q", string(data), "DUMPBYTES")
	}
}

func TestBackup_FilesystemAndPgDump_Mixed(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get deploy", "web=2 ")
	stub.setOutput("kubectl get pvc data", "pv-data")
	stub.setOutput("kubectl get pv pv-data", "/srv/data")
	stub.setOutput("kubectl get pod -n app", "postgres-0 ")
	stub.setOutput("kubectl exec -n app postgres-0 -- printenv PGDATABASE", "appdb")
	stub.setOutput(pgDumpExecPrefix("app", "postgres-0"), "MIX")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	dir := t.TempDir()

	apps := []manifest.App{{
		Name:      "app",
		Namespace: "app",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{
				fsTarget("data"),
				pgTarget("app=postgres"),
			},
		},
	}}

	if err := b.BackupApps(apps, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := stub.getCalls()
	scaleDownCount := 0
	scpCount := 0
	tarCalled := false
	pgDumpCalled := false
	for _, c := range calls {
		if strings.HasPrefix(c, "kubectl scale deploy web -n app --replicas=0") {
			scaleDownCount++
		}
		if strings.HasPrefix(c, "scp -r pax:") {
			scpCount++
		}
		if strings.HasPrefix(c, "ssh pax tar") {
			tarCalled = true
		}
		if strings.HasPrefix(c, pgDumpExecPrefix("app", "postgres-0")) {
			pgDumpCalled = true
		}
	}
	if scaleDownCount != 1 {
		t.Errorf("expected exactly 1 scale-down call, got %d\ncalls:\n  %s", scaleDownCount, strings.Join(calls, "\n  "))
	}
	if scpCount != 1 {
		t.Errorf("expected exactly 1 scp-back call, got %d\ncalls:\n  %s", scpCount, strings.Join(calls, "\n  "))
	}
	if !tarCalled {
		t.Errorf("expected tar call\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
	if !pgDumpCalled {
		t.Errorf("expected pg_dump call\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
}

type sinkRecorder struct {
	mu     sync.Mutex
	events []output.Event
}

func (s *sinkRecorder) Emit(e output.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *sinkRecorder) snapshot() []output.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]output.Event, len(s.events))
	copy(out, s.events)
	return out
}

func TestBackupApps_SinkEvents(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get pod -n db", "postgres-0 ")
	stub.setOutput("kubectl exec -n db postgres-0 -- printenv PGDATABASE", "mydb")
	stub.setOutput(pgDumpExecPrefix("db", "postgres-0"), "DUMPBYTES")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	rec := &sinkRecorder{}
	b := newBackuper(&stdout, &stderr)
	b.Sink = rec

	apps := []manifest.App{{
		Name:      "pg",
		Namespace: "db",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{pgTarget("app=postgres")},
		},
	}}

	if err := b.BackupApps(apps, t.TempDir()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := rec.snapshot()
	wantKinds := []output.EventKind{
		output.TreeStart,
		output.NodeStart,
		output.NodeLine,
		output.NodeDone,
		output.TreeDone,
	}
	if len(events) != len(wantKinds) {
		t.Fatalf("expected %d events, got %d: %+v", len(wantKinds), len(events), events)
	}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("event[%d] kind: got %s, want %s", i, events[i].Kind, want)
		}
	}

	if got := events[0].Count; got != 1 {
		t.Errorf("TreeStart.Count: got %d, want 1", got)
	}
	if got := events[1].App; got != "pg" {
		t.Errorf("NodeStart.App: got %q, want %q", got, "pg")
	}
	if got := events[2].App; got != "pg" {
		t.Errorf("NodeLine.App: got %q, want %q", got, "pg")
	}
	if got := events[2].Stage; got != "copying" {
		t.Errorf("NodeLine.Stage: got %q, want %q", got, "copying")
	}
	if got := events[3].App; got != "pg" {
		t.Errorf("NodeDone.App: got %q, want %q", got, "pg")
	}
	if events[3].Err != nil {
		t.Errorf("NodeDone.Err: got %v, want nil", events[3].Err)
	}
}

func TestBackupApps_SinkNilSafe(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get pod -n db", "postgres-0 ")
	stub.setOutput("kubectl exec -n db postgres-0 -- printenv PGDATABASE", "mydb")
	stub.setOutput(pgDumpExecPrefix("db", "postgres-0"), "DUMPBYTES")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	if b.Sink != nil {
		t.Fatalf("sink should default to nil")
	}

	apps := []manifest.App{{
		Name:      "pg",
		Namespace: "db",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{pgTarget("app=postgres")},
		},
	}}

	if err := b.BackupApps(apps, t.TempDir()); err != nil {
		t.Fatalf("unexpected error with nil sink: %v", err)
	}
}
