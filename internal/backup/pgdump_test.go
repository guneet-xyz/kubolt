package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/manifest"
)

func newPgDumpStrategy(b *Backuper) *pgDumpStrategy {
	return &pgDumpStrategy{b: b}
}

func pgDumpResolverStubs(stub *stubRecorder) {
	stub.setOutput("kubectl get pod -n walls", "postgres-0 ")
	stub.setOutput("kubectl exec -n walls postgres-0 -- printenv PGDATABASE", "mydb")
}

func TestPgDump_Happy(t *testing.T) {
	stub := newStub()
	pgDumpResolverStubs(stub)
	stub.setOutput("kubectl exec -n walls postgres-0 -- pg_dump", "PGDUMPDATA")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	tmpDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	s := newPgDumpStrategy(b)

	target := manifest.Target{Type: manifest.TargetPgDump, PodSelector: "app=postgres"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	if err := s.Backup(context.Background(), app, target, tmpDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	finalPath := filepath.Join(tmpDir, "walls-mydb.dump")
	partialPath := finalPath + ".partial"

	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("expected final dump file at %s: %v", finalPath, err)
	}
	if string(data) != "PGDUMPDATA" {
		t.Errorf("dump content = %q, want %q", string(data), "PGDUMPDATA")
	}
	if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
		t.Errorf("expected .partial to be removed, but stat err = %v", err)
	}
}

func TestPgDump_NonzeroExit(t *testing.T) {
	stub := newStub()
	pgDumpResolverStubs(stub)
	stub.setFailure("kubectl exec -n walls postgres-0 -- pg_dump", 1)

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	tmpDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	s := newPgDumpStrategy(b)

	target := manifest.Target{Type: manifest.TargetPgDump, PodSelector: "app=postgres"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	err := s.Backup(context.Background(), app, target, tmpDir)
	if err == nil {
		t.Fatal("expected error from nonzero exit")
	}
	if !strings.Contains(err.Error(), "pg_dump") {
		t.Errorf("expected error to mention pg_dump, got: %v", err)
	}

	finalPath := filepath.Join(tmpDir, "walls-mydb.dump")
	partialPath := finalPath + ".partial"

	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Errorf("final .dump must not exist on failure, stat err = %v", err)
	}
	if _, err := os.Stat(partialPath); err != nil {
		t.Errorf("expected .partial to remain on failure, stat err = %v", err)
	}
}

func TestPgDump_Cancel(t *testing.T) {
	stub := newStub()
	pgDumpResolverStubs(stub)
	stub.setOutput("kubectl exec -n walls postgres-0 -- pg_dump", "PARTIALDATA")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	tmpDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	s := newPgDumpStrategy(b)

	target := manifest.Target{Type: manifest.TargetPgDump, PodSelector: "app=postgres"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.Backup(ctx, app, target, tmpDir)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected error to mention 'cancelled', got: %v", err)
	}

	finalPath := filepath.Join(tmpDir, "walls-mydb.dump")
	partialPath := finalPath + ".partial"

	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Errorf("final .dump must not exist on cancel, stat err = %v", err)
	}
	if _, err := os.Stat(partialPath); err != nil {
		t.Errorf("expected .partial to remain on cancel, stat err = %v", err)
	}
}

func TestPgDump_DryRun(t *testing.T) {
	stub := newStub()
	pgDumpResolverStubs(stub)

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	tmpDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	b.DryRun = true
	s := newPgDumpStrategy(b)

	target := manifest.Target{Type: manifest.TargetPgDump, PodSelector: "app=postgres"}
	app := manifest.App{Name: "walls", Namespace: "walls"}

	if err := s.Backup(context.Background(), app, target, tmpDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dumpCalls := 0
	for _, c := range stub.getCalls() {
		if strings.Contains(c, "pg_dump") {
			dumpCalls++
		}
	}
	if dumpCalls != 0 {
		t.Errorf("expected 0 pg_dump exec calls in dry-run, got %d", dumpCalls)
	}

	out := stdout.String()
	if !strings.Contains(out, "[dry-run]") {
		t.Errorf("expected [dry-run] prefix in stdout, got: %q", out)
	}
	if !strings.Contains(out, "pg_dump -Fc") {
		t.Errorf("expected stdout to mention pg_dump -Fc, got: %q", out)
	}
}
