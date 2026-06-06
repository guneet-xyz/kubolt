package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/output"
)

type installCaptureCase struct {
	name           string
	apps           []installTestApp
	upgradeOutputs map[string]string
	upgradeFails   map[string]struct{ stderr string }
	wantErr        bool
	wantContains   []string
	wantAbsent     []string
}

func twoAppCases() []installCaptureCase {
	apps := []installTestApp{
		{name: "caddy", namespace: "ns1"},
		{name: "registry", namespace: "ns2", dependsOn: []string{"caddy"}},
	}
	return []installCaptureCase{
		{
			name: "success_suppresses_all_helm_output",
			apps: apps,
			upgradeOutputs: map[string]string{
				"caddy":    "caddy-line-1\n",
				"registry": "registry-line-1\n",
			},
			wantAbsent: []string{
				"--- output from",
				"--- end output ---",
				"caddy-line-1",
				"registry-line-1",
			},
		},
		{
			name: "failure_dumps_only_failing_app",
			apps: apps,
			upgradeOutputs: map[string]string{
				"caddy": "caddy-line-1\n",
			},
			upgradeFails: map[string]struct{ stderr string }{
				"registry": {stderr: "registry failed text\n"},
			},
			wantErr: true,
			wantContains: []string{
				"--- output from registry ---",
				"registry failed text",
				"--- end output ---",
			},
			wantAbsent: []string{
				"--- output from caddy ---",
				"caddy-line-1",
			},
		},
	}
}

func configureStub(c installCaptureCase) *HelmStub {
	stub := NewHelmStub()
	for app, out := range c.upgradeOutputs {
		stub.SetUpgradeOutput(app, out)
	}
	for app, fail := range c.upgradeFails {
		stub.SetUpgradeFailure(app, 1, fail.stderr)
	}
	return stub
}

func assertOutput(t *testing.T, out string, c installCaptureCase) {
	t.Helper()
	for _, want := range c.wantContains {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q; got:\n%s", want, out)
		}
	}
	for _, absent := range c.wantAbsent {
		if strings.Contains(out, absent) {
			t.Errorf("expected output to NOT contain %q; got:\n%s", absent, out)
		}
	}
}

// TestInstallCapture_LineSink_Success: two apps both succeed under the
// suppress-and-dump policy. The LineSink writer must contain tree-summary
// lines but NO per-line "[app] text" output and NO "--- output from … ---"
// dump markers.
func TestInstallCapture_LineSink_Success(t *testing.T) {
	cases := twoAppCases()
	c := cases[0]

	m := setupInstallManifest(t, c.apps)
	stub := configureStub(c)
	helm.SetExecCommand(stub.ExecFunc())
	defer helm.ResetExecCommand()

	var sinkBuf, runnerBuf bytes.Buffer
	sink := output.NewLineSink(&sinkBuf, false)
	runner := &helm.Runner{Stdout: &runnerBuf, Stderr: &runnerBuf}

	err := installApps(context.Background(), m, "", runner, sink, 2, nil)
	if err != nil {
		t.Fatalf("installApps error: %v", err)
	}

	out := sinkBuf.String()
	if !strings.Contains(out, "=== Starting (2 apps) ===") {
		t.Errorf("expected tree-summary header in output; got:\n%s", out)
	}
	if !strings.Contains(out, "=== Complete (succeeded=2 failed=0 skipped=0) ===") {
		t.Errorf("expected tree-summary footer in output; got:\n%s", out)
	}
	assertOutput(t, out, c)
}

// TestInstallCapture_LineSink_Failure: registry fails after caddy succeeds.
// The LineSink writer must contain a "--- output from registry ---" block
// with the captured stderr text, and must NOT contain a dump for the
// succeeding caddy.
func TestInstallCapture_LineSink_Failure(t *testing.T) {
	cases := twoAppCases()
	c := cases[1]

	m := setupInstallManifest(t, c.apps)
	stub := configureStub(c)
	helm.SetExecCommand(stub.ExecFunc())
	defer helm.ResetExecCommand()

	var sinkBuf, runnerBuf bytes.Buffer
	sink := output.NewLineSink(&sinkBuf, false)
	runner := &helm.Runner{Stdout: &runnerBuf, Stderr: &runnerBuf}

	err := installApps(context.Background(), m, "", runner, sink, 2, nil)
	if !c.wantErr {
		t.Fatalf("test case %q misconfigured: wantErr should be true", c.name)
	}
	if err == nil {
		t.Fatalf("expected installApps to fail, got nil")
	}

	assertOutput(t, sinkBuf.String(), c)
}

// TestInstallCapture_BubbleTeaSink_Success: two apps succeed under a
// BubbleTeaSink backed by a bytes.Buffer (non-TTY mode). After Close()
// returns the buffer must contain NO failure-dump markers.
func TestInstallCapture_BubbleTeaSink_Success(t *testing.T) {
	cases := twoAppCases()
	c := cases[0]

	m := setupInstallManifest(t, c.apps)
	stub := configureStub(c)
	helm.SetExecCommand(stub.ExecFunc())
	defer helm.ResetExecCommand()

	var sinkBuf, runnerBuf bytes.Buffer
	bubbleSink := output.NewBubbleTeaSink(&sinkBuf)
	runner := &helm.Runner{Stdout: &runnerBuf, Stderr: &runnerBuf}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- bubbleSink.Run(ctx) }()

	runErr := installApps(ctx, m, "", runner, bubbleSink, 2, nil)
	bubbleSink.Close()
	<-runDone

	if runErr != nil {
		t.Fatalf("installApps error: %v", runErr)
	}

	assertOutput(t, sinkBuf.String(), c)
}

// TestInstallCapture_BubbleTeaSink_Failure: registry fails after caddy
// succeeds. After Close() returns the buffer must contain a
// "--- output from registry ---" block with the captured stderr text, and
// must NOT contain a dump for the succeeding caddy.
func TestInstallCapture_BubbleTeaSink_Failure(t *testing.T) {
	cases := twoAppCases()
	c := cases[1]

	m := setupInstallManifest(t, c.apps)
	stub := configureStub(c)
	helm.SetExecCommand(stub.ExecFunc())
	defer helm.ResetExecCommand()

	var sinkBuf, runnerBuf bytes.Buffer
	bubbleSink := output.NewBubbleTeaSink(&sinkBuf)
	runner := &helm.Runner{Stdout: &runnerBuf, Stderr: &runnerBuf}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- bubbleSink.Run(ctx) }()

	runErr := installApps(ctx, m, "", runner, bubbleSink, 2, nil)
	bubbleSink.Close()
	<-runDone

	if !c.wantErr {
		t.Fatalf("test case %q misconfigured: wantErr should be true", c.name)
	}
	if runErr == nil {
		t.Fatalf("expected installApps to fail, got nil")
	}

	assertOutput(t, sinkBuf.String(), c)
}
