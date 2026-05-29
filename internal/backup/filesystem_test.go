package backup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/manifest"
)

func newFsStrategy(b *Backuper) *filesystemStrategy {
	return &filesystemStrategy{b: b, remoteTsDir: "/tmp/k3s-backups/2025-01-01_120000"}
}

func TestFilesystem_Happy(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get pvc walls-data", "pv-walls")
	stub.setOutput("kubectl get pv pv-walls", "/var/lib/data/walls")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	s := newFsStrategy(b)

	target := manifest.Target{Type: manifest.TargetFilesystem, PVC: "walls-data"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	if err := s.Backup(context.Background(), app, target, "/tmp/local"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := stub.getCalls()
	wantTar := "ssh pax tar czf /tmp/k3s-backups/2025-01-01_120000/walls-data.tar.gz -C /var/lib/data/walls ."
	found := false
	for _, c := range calls {
		if c == wantTar {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tar call %q not found\ncalls:\n  %s", wantTar, strings.Join(calls, "\n  "))
	}
}

func TestFilesystem_MissingPVName(t *testing.T) {
	stub := newStub()
	stub.setFailure("kubectl get pvc", 1)

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	s := newFsStrategy(b)

	target := manifest.Target{Type: manifest.TargetFilesystem, PVC: "walls-data"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	err := s.Backup(context.Background(), app, target, "/tmp/local")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "getting PV name") {
		t.Errorf("expected error to wrap 'getting PV name', got: %v", err)
	}
}

func TestFilesystem_MissingHostPath(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get pvc walls-data", "pv-walls")
	stub.setFailure("kubectl get pv", 1)

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	s := newFsStrategy(b)

	target := manifest.Target{Type: manifest.TargetFilesystem, PVC: "walls-data"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	err := s.Backup(context.Background(), app, target, "/tmp/local")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "getting host path") {
		t.Errorf("expected error to wrap 'getting host path', got: %v", err)
	}
}

func TestFilesystem_DryRun(t *testing.T) {
	stub := newStub()
	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	b.DryRun = true
	s := newFsStrategy(b)

	target := manifest.Target{Type: manifest.TargetFilesystem, PVC: "walls-data"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	if err := s.Backup(context.Background(), app, target, "/tmp/local"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if calls := stub.getCalls(); len(calls) != 0 {
		t.Errorf("expected 0 exec calls in dry-run, got %d:\n  %s", len(calls), strings.Join(calls, "\n  "))
	}
	out := stdout.String()
	if !strings.Contains(out, "[dry-run]") {
		t.Errorf("expected stdout to contain [dry-run], got: %q", out)
	}
	if !strings.Contains(out, "tar czf") {
		t.Errorf("expected stdout to contain 'tar czf', got: %q", out)
	}
}

func TestFilesystem_CancelledContext(t *testing.T) {
	stub := newStub()
	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	s := newFsStrategy(b)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	target := manifest.Target{Type: manifest.TargetFilesystem, PVC: "walls-data"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	err := s.Backup(ctx, app, target, "/tmp/local")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected error to mention 'cancelled', got: %v", err)
	}
}
