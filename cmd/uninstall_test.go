package cmd

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/helm"
)

func stubHelm(listOutputs map[string]string, rec *callRecorder) {
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		if name == "helm" && len(args) >= 4 && args[0] == "list" && args[1] == "-n" {
			ns := args[2]
			if out, ok := listOutputs[ns]; ok {
				return exec.Command("echo", out)
			}
			return exec.Command("echo", "[]")
		}
		return exec.Command("true")
	})
}

func helmCallCount(calls [][]string, sub string) int {
	n := 0
	for _, c := range calls {
		if len(c) >= 2 && c[0] == "helm" && c[1] == sub {
			n++
		}
	}
	return n
}

func findHelmCall(calls [][]string, sub string) []string {
	for _, c := range calls {
		if len(c) >= 2 && c[0] == "helm" && c[1] == sub {
			return c
		}
	}
	return nil
}

func TestUninstall_NoDependents(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "target"},
	})

	rec := &callRecorder{}
	stubHelm(nil, rec)
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := uninstallApp(m, "target", runner); err != nil {
		t.Fatalf("uninstallApp: %v", err)
	}

	calls := rec.snapshot()
	if helmCallCount(calls, "uninstall") != 1 {
		t.Fatalf("expected exactly 1 helm uninstall call, got %d (calls=%v)",
			helmCallCount(calls, "uninstall"), calls)
	}
	got := findHelmCall(calls, "uninstall")
	want := []string{"helm", "uninstall", "target", "-n", "target"}
	if !equalSlice(got, want) {
		t.Fatalf("uninstall args = %v, want %v", got, want)
	}
}

func TestUninstall_InstalledDependent_Blocks(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "ns-target"},
		{name: "consumer", namespace: "ns-consumer", dependsOn: []string{"target"}},
	})

	rec := &callRecorder{}
	stubHelm(map[string]string{
		"ns-consumer": `[{"name":"consumer"}]`,
	}, rec)
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	err := uninstallApp(m, "target", runner)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "consumer") {
		t.Fatalf("error should mention dependent name 'consumer', got: %v", err)
	}

	calls := rec.snapshot()
	if n := helmCallCount(calls, "uninstall"); n != 0 {
		t.Fatalf("expected 0 helm uninstall calls when blocked, got %d", n)
	}
	if n := helmCallCount(calls, "install"); n != 0 {
		t.Fatalf("expected 0 helm install calls, got %d", n)
	}
	if n := helmCallCount(calls, "upgrade"); n != 0 {
		t.Fatalf("expected 0 helm upgrade calls, got %d", n)
	}
}

func TestUninstall_UninstalledDependent_Allows(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "ns-target"},
		{name: "consumer", namespace: "ns-consumer", dependsOn: []string{"target"}},
	})

	rec := &callRecorder{}
	stubHelm(map[string]string{
		"ns-consumer": `[]`,
	}, rec)
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := uninstallApp(m, "target", runner); err != nil {
		t.Fatalf("uninstallApp: %v", err)
	}

	calls := rec.snapshot()
	if n := helmCallCount(calls, "uninstall"); n != 1 {
		t.Fatalf("expected 1 helm uninstall, got %d (calls=%v)", n, calls)
	}
}

func TestUninstall_NoExtraFlags(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "ns-target"},
	})

	rec := &callRecorder{}
	stubHelm(nil, rec)
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := uninstallApp(m, "target", runner); err != nil {
		t.Fatalf("uninstallApp: %v", err)
	}

	got := findHelmCall(rec.snapshot(), "uninstall")
	if got == nil {
		t.Fatalf("no helm uninstall call recorded")
	}
	forbidden := []string{"--cascade", "--delete-pvc", "--keep-history", "--force"}
	for _, arg := range got {
		for _, f := range forbidden {
			if arg == f || strings.HasPrefix(arg, f+"=") {
				t.Fatalf("uninstall args contain forbidden flag %q: %v", f, got)
			}
		}
	}
	want := []string{"helm", "uninstall", "target", "-n", "ns-target"}
	if !equalSlice(got, want) {
		t.Fatalf("uninstall args = %v, want exactly %v", got, want)
	}
}

func TestUninstall_DryRun(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "ns-target"},
	})

	rec := &callRecorder{}
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{DryRun: true, Stdout: &buf, Stderr: &buf}

	if err := uninstallApp(m, "target", runner); err != nil {
		t.Fatalf("uninstallApp: %v", err)
	}

	if n := len(rec.snapshot()); n != 0 {
		t.Fatalf("expected 0 exec calls in dry-run, got %d", n)
	}
	out := buf.String()
	if !strings.Contains(out, "helm uninstall target -n ns-target") {
		t.Fatalf("dry-run output should contain uninstall command, got: %q", out)
	}
}

func equalSlice(a, b []string) bool {
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
