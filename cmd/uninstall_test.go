package cmd

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/output"
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

type recordSink struct {
	mu     sync.Mutex
	events []output.Event
}

func (r *recordSink) Emit(e output.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordSink) snapshot() []output.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]output.Event, len(r.events))
	copy(out, r.events)
	return out
}

func TestUninstallWithSink_BlockedByDependent(t *testing.T) {
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

	sink := &recordSink{}
	err := uninstallAppWithSink(context.Background(), m, "target", runner, sink)
	if err == nil {
		t.Fatalf("expected error when blocked by dependent, got nil")
	}
	if !strings.Contains(err.Error(), "consumer") {
		t.Fatalf("error should mention dependent name 'consumer', got: %v", err)
	}

	for _, e := range sink.snapshot() {
		switch e.Kind {
		case output.TreeStart, output.NodeReady, output.NodeStart, output.NodeDone, output.TreeDone:
			t.Fatalf("blocked uninstall must not emit tree-framing events, got %s", e.Kind)
		}
	}

	calls := rec.snapshot()
	if n := helmCallCount(calls, "uninstall"); n != 0 {
		t.Fatalf("expected 0 helm uninstall calls when blocked, got %d", n)
	}
}

func TestUninstallWithSink_PlainSuccess(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "foo", namespace: "ns-foo"},
	})

	rec := &callRecorder{}
	stubHelm(nil, rec)
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	sink := &recordSink{}
	if err := uninstallAppWithSink(context.Background(), m, "foo", runner, sink); err != nil {
		t.Fatalf("uninstallAppWithSink: %v", err)
	}

	events := sink.snapshot()
	wantSeq := []output.EventKind{
		output.TreeStart,
		output.NodeReady,
		output.NodeStart,
		output.NodeDone,
		output.TreeDone,
	}
	gotSeq := make([]output.EventKind, 0, len(events))
	for _, e := range events {
		switch e.Kind {
		case output.TreeStart, output.NodeReady, output.NodeStart, output.NodeDone, output.TreeDone:
			gotSeq = append(gotSeq, e.Kind)
		}
	}
	if len(gotSeq) != len(wantSeq) {
		t.Fatalf("event sequence length = %d, want %d (got=%v)", len(gotSeq), len(wantSeq), gotSeq)
	}
	for i := range wantSeq {
		if gotSeq[i] != wantSeq[i] {
			t.Fatalf("event[%d] = %s, want %s (full=%v)", i, gotSeq[i], wantSeq[i], gotSeq)
		}
	}

	last := events[len(events)-1]
	if last.Kind != output.TreeDone {
		t.Fatalf("final event = %s, want TreeDone", last.Kind)
	}
}

func TestUninstallWithSink_NilSink(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "foo", namespace: "ns-foo"},
	})

	rec := &callRecorder{}
	stubHelm(nil, rec)
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := uninstallAppWithSink(context.Background(), m, "foo", runner, nil); err != nil {
		t.Fatalf("uninstallAppWithSink with nil sink: %v", err)
	}
}

// TestUninstallWithSink_HelmOutputRoutesToSink guards the TUI corruption fix:
// `helm uninstall` output MUST be routed to the sink as NodeLine events (not
// directly to runner.Stdout / os.Stdout). If a future refactor collapses
// runner.RunWith back to runner.Run for uninstall, an active Bubble Tea TUI
// would be corrupted by helm's "release ... uninstalled" message writing into
// the same terminal.
func TestUninstallWithSink_HelmOutputRoutesToSink(t *testing.T) {
	m := setupInstallManifest(t, []installTestApp{
		{name: "target", namespace: "ns-target"},
	})

	const helmMsg = `release "target" uninstalled`
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) >= 1 && args[0] == "uninstall" {
			return exec.Command("echo", helmMsg)
		}
		return exec.Command("echo", "[]")
	})
	defer helm.ResetExecCommand()

	var stdoutSpy bytes.Buffer
	runner := &helm.Runner{Stdout: &stdoutSpy, Stderr: &stdoutSpy}

	sink := &recordSink{}
	if err := uninstallAppWithSink(context.Background(), m, "target", runner, sink); err != nil {
		t.Fatalf("uninstallAppWithSink: %v", err)
	}

	// Helm output must NOT leak to runner.Stdout (which would corrupt an
	// active TUI). The runner's stdout/stderr should be untouched during
	// the helm uninstall call because RunWith routes through per-call
	// writers.
	if got := stdoutSpy.String(); strings.Contains(got, "uninstalled") {
		t.Errorf("helm uninstall output leaked to runner.Stdout (would corrupt active TUI); got %q", got)
	}

	// Sink must have received NodeLine events for the helm output, tagged
	// with the app name.
	events := sink.snapshot()
	var sawNodeLine bool
	for _, e := range events {
		if e.Kind != output.NodeLine {
			continue
		}
		if e.App != "target" {
			t.Errorf("NodeLine event has App=%q, want %q", e.App, "target")
		}
		if e.Stream != "stdout" && e.Stream != "stderr" {
			t.Errorf("NodeLine event has Stream=%q, want stdout or stderr", e.Stream)
		}
		if strings.Contains(e.Text, "uninstalled") {
			sawNodeLine = true
		}
	}
	if !sawNodeLine {
		t.Errorf("expected NodeLine event containing 'uninstalled' from helm output; got events=%v", events)
	}
}
