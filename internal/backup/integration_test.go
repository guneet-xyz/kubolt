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

func containsCall(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestIntegration_Mixed(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get pod -n walls -l app=pg", "postgres-0 ")
	stub.setOutput("kubectl exec -n walls postgres-0 -- printenv PGDATABASE", "mydb")
	stub.setOutput(pgDumpExecPrefix("walls", "postgres-0"), "DUMPBYTES")
	stub.setOutput("kubectl get deploy -n walls", "web=1 ")
	stub.setOutput("kubectl get pvc data -n walls", "pv-data")
	stub.setOutput("kubectl get pv pv-data", "/data")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	dir := t.TempDir()

	apps := []manifest.App{{
		Name:      "walls",
		Namespace: "walls",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{
				fsTarget("data"),
				pgTarget("app=pg"),
			},
			ScaleDeployments: boolPtr(true),
		},
	}}

	if err := b.BackupApps(apps, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := stub.getCalls()

	if !containsCall(calls, "ssh pax tar czf") {
		t.Errorf("expected filesystem ssh tar call\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
	if !containsCall(calls, pgDumpExecPrefix("walls", "postgres-0")) {
		t.Errorf("expected pg_dump exec call\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
	if !containsCall(calls, "scp -r") {
		t.Errorf("expected scp-back call\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading tempdir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected timestamp subdir to be created")
	}
	localTs := filepath.Join(dir, entries[0].Name())

	matches, err := filepath.Glob(filepath.Join(localTs, "walls-*.dump"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		fileEntries, _ := os.ReadDir(localTs)
		var names []string
		for _, e := range fileEntries {
			names = append(names, e.Name())
		}
		t.Errorf("expected walls-*.dump file in %s, found: %v", localTs, names)
	}
}

func TestIntegration_SIGINT(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get deploy -n app", "web=2 ")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)

	app := manifest.App{
		Name:      "app",
		Namespace: "app",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{fsTarget("data")},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := b.backupAppTargets(ctx, app, t.TempDir(), "/tmp/remote")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("expected 'cancelled' in error, got: %v", err)
	}

	calls := stub.getCalls()
	if !containsCall(calls, "kubectl scale deploy web -n app --replicas=2") {
		t.Errorf("expected replica restore after cancellation\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
}

func TestIntegration_MultiPodFail(t *testing.T) {
	stub := newStub()
	stub.setOutput("kubectl get pod", "postgres-0 postgres-1 postgres-2 ")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	dir := t.TempDir()

	apps := []manifest.App{{
		Name:      "walls",
		Namespace: "walls",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{pgTarget("app=pg")},
		},
	}}

	err := b.BackupApps(apps, dir)
	if err == nil {
		t.Fatal("expected error from multi-pod selector")
	}
	msg := err.Error()
	for _, name := range []string{"postgres-0", "postgres-1", "postgres-2", "narrow"} {
		if !strings.Contains(msg, name) {
			t.Errorf("expected error message to contain %q, got: %v", name, err)
		}
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		sub := filepath.Join(dir, e.Name())
		matches, _ := filepath.Glob(filepath.Join(sub, "*.dump"))
		if len(matches) > 0 {
			t.Errorf("expected no .dump files, found: %v", matches)
		}
	}
}

func TestIntegration_DryRun(t *testing.T) {
	stub := newStub()
	// pg_dump's resolvePod/resolveDatabase run BEFORE the DryRun early-return
	// in pgDumpStrategy.Backup, so these stubs are required even in dry-run.
	stub.setOutput("kubectl get pod -n walls", "pg-pod ")
	stub.setOutput("kubectl exec -n walls pg-pod -- printenv PGDATABASE", "testdb")

	SetExecCommand(stub.execFn())
	defer ResetExecCommand()

	var stdout, stderr bytes.Buffer
	b := newBackuper(&stdout, &stderr)
	b.DryRun = true
	dir := t.TempDir()

	apps := []manifest.App{{
		Name:      "walls",
		Namespace: "walls",
		Backup: &manifest.BackupSpec{
			Targets: []manifest.Target{
				fsTarget("data"),
				pgTarget("app=pg"),
			},
		},
	}}

	if err := b.BackupApps(apps, dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := stub.getCalls()
	if containsCall(calls, pgDumpExecPrefix("walls", "pg-pod")) {
		t.Errorf("dry-run must not invoke pg_dump\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}
	if containsCall(calls, "ssh pax tar") {
		t.Errorf("dry-run must not invoke ssh tar\ncalls:\n  %s", strings.Join(calls, "\n  "))
	}

	out := stdout.String()
	if !strings.Contains(out, "[dry-run]") {
		t.Errorf("expected [dry-run] prefix in stdout, got: %s", out)
	}
	if !strings.Contains(out, "pg_dump -Fc") {
		t.Errorf("expected pg_dump plan line in stdout, got: %s", out)
	}
	if !strings.Contains(out, "tar czf") {
		t.Errorf("expected tar czf plan line in stdout, got: %s", out)
	}
}
