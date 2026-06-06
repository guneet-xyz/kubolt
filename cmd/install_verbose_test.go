package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/output"
)

// TestInstallVerbose_Success verifies that when the LineSink is configured
// with verbose=true, every per-app helm output line is streamed via the
// "[app] text" prefix format and NO "--- output from … ---" dump markers
// are produced on a successful run.
//
// This test runs the install pipeline end-to-end with two apps (caddy,
// registry) under a verbose LineSink. Both apps succeed; the LineSink must
// surface the helm stdout text for both apps via prefixed lines, and never
// emit failure-dump markers (verbose mode has no concept of "suppress and
// dump on failure" — every line is shown live).
func TestInstallVerbose_Success(t *testing.T) {
	apps := []installTestApp{
		{name: "caddy", namespace: "ns1"},
		{name: "registry", namespace: "ns2", dependsOn: []string{"caddy"}},
	}
	c := installCaptureCase{
		name: "verbose_streams_lines_no_dump_on_success",
		apps: apps,
		upgradeOutputs: map[string]string{
			"caddy":    "caddy-line-1\n",
			"registry": "registry-line-1\n",
		},
		wantContains: []string{
			"[caddy] caddy-line-1",
			"[registry] registry-line-1",
		},
		wantAbsent: []string{
			"--- output from",
			"--- end output ---",
		},
	}

	m := setupInstallManifest(t, c.apps)
	stub := configureStub(c)
	helm.SetExecCommand(stub.ExecFunc())
	defer helm.ResetExecCommand()

	var sinkBuf, runnerBuf bytes.Buffer
	sink := output.NewLineSink(&sinkBuf, true) // verbose=true
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

// TestInstallVerbose_Failure verifies that when the LineSink is configured
// with verbose=true and one app fails, the per-line output for BOTH apps is
// still streamed (verbose mode never suppresses output) AND the failure
// path does NOT add "--- output from … ---" dump markers (those markers
// are exclusively a non-verbose feature — verbose mode already showed
// everything live).
//
// caddy succeeds (stdout: "caddy-line-1"), registry fails (stderr:
// "registry failed text", exit code 1). The verbose LineSink must contain
// both apps' per-line output and a "[registry] FAILED in ..." footer, but
// no dump-block markers.
func TestInstallVerbose_Failure(t *testing.T) {
	apps := []installTestApp{
		{name: "caddy", namespace: "ns1"},
		{name: "registry", namespace: "ns2", dependsOn: []string{"caddy"}},
	}
	c := installCaptureCase{
		name: "verbose_streams_lines_no_dump_on_failure",
		apps: apps,
		upgradeOutputs: map[string]string{
			"caddy": "caddy-line-1\n",
		},
		upgradeFails: map[string]struct{ stderr string }{
			"registry": {stderr: "registry failed text\n"},
		},
		wantErr: true,
		wantContains: []string{
			"[caddy] caddy-line-1",
			"[registry] registry failed text",
			"[registry] FAILED",
		},
		wantAbsent: []string{
			"--- output from",
			"--- end output ---",
		},
	}

	m := setupInstallManifest(t, c.apps)
	stub := configureStub(c)
	helm.SetExecCommand(stub.ExecFunc())
	defer helm.ResetExecCommand()

	var sinkBuf, runnerBuf bytes.Buffer
	sink := output.NewLineSink(&sinkBuf, true) // verbose=true
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
