package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// HelmStub is a configurable helm command stub for tests.
// Use ExecFunc() to get a function suitable for helm.SetExecCommand.
type HelmStub struct {
	depBuildOutput string
	upgradeOutput  map[string]string // app → stdout
	upgradeFail    map[string]stubFailure
	listOutput     string
}

type stubFailure struct {
	exitCode int
	stderr   string
}

// NewHelmStub creates a new helm stub with sensible defaults.
func NewHelmStub() *HelmStub {
	return &HelmStub{
		upgradeOutput: make(map[string]string),
		upgradeFail:   make(map[string]stubFailure),
		listOutput:    "[]",
	}
}

// SetDepBuildOutput sets the output for "helm dependency build" commands.
func (s *HelmStub) SetDepBuildOutput(output string) {
	s.depBuildOutput = output
}

// SetListOutput sets the output for "helm list" commands.
func (s *HelmStub) SetListOutput(output string) {
	s.listOutput = output
}

// SetUpgradeOutput sets the stdout for a successful "helm upgrade/install <app> ..." command.
func (s *HelmStub) SetUpgradeOutput(app, output string) {
	s.upgradeOutput[app] = output
}

// SetUpgradeFailure sets the exit code and stderr for a failed "helm upgrade/install <app> ..." command.
func (s *HelmStub) SetUpgradeFailure(app string, exitCode int, stderr string) {
	s.upgradeFail[app] = stubFailure{exitCode: exitCode, stderr: stderr}
}

// ExecFunc returns a function suitable for helm.SetExecCommand.
// It uses the TestHelmStubHelperProcess pattern to produce output and exit codes.
func (s *HelmStub) ExecFunc() func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		var stdout, stderr, exitCode string

		// Determine which helm subcommand and extract parameters
		if len(args) >= 1 {
			switch args[0] {
			case "dependency":
				stdout = s.depBuildOutput
			case "upgrade", "install":
				// Find app name in args (the release name, first non-flag arg after subcommand)
				app := findReleaseName(args)
				if fail, ok := s.upgradeFail[app]; ok {
					exitCode = strconv.Itoa(fail.exitCode)
					stderr = fail.stderr
				} else {
					stdout = s.upgradeOutput[app]
				}
			case "list":
				stdout = s.listOutput
			}
		}

		// Use TestHelmStubHelperProcess pattern: run this test binary with env vars
		cmd := exec.Command(os.Args[0], "-test.run=TestHelmStubHelperProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_HELPER_PROCESS=1",
			"HELM_STUB_STDOUT="+stdout,
			"HELM_STUB_STDERR="+stderr,
			"HELM_STUB_EXIT="+exitCode,
		)
		return cmd
	}
}

// findReleaseName extracts the release name from helm upgrade/install args.
// For "helm upgrade <release> <chart> ..." or "helm install <release> <chart> ...",
// the release name is the first non-flag argument after the subcommand.
func findReleaseName(args []string) string {
	// args[0] is "upgrade" or "install"; look for the release name in args[1:]
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// TestHelmStubHelperProcess is the helper subprocess used by HelmStub.
// It MUST NOT be skipped — the os.Args[0] trick relies on this test function existing.
func TestHelmStubHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER_PROCESS") != "1" {
		return // not a helper process invocation; skip silently
	}

	if out := os.Getenv("HELM_STUB_STDOUT"); out != "" {
		fmt.Fprint(os.Stdout, out)
	}
	if errOut := os.Getenv("HELM_STUB_STDERR"); errOut != "" {
		fmt.Fprint(os.Stderr, errOut)
	}
	exitStr := os.Getenv("HELM_STUB_EXIT")
	if exitStr != "" {
		if code, err := strconv.Atoi(exitStr); err == nil && code != 0 {
			os.Exit(code)
		}
	}
	os.Exit(0)
}

// TestHelmStub_BasicOutput verifies that HelmStub produces configured output
// and exit codes correctly via the helper process pattern.
func TestHelmStub_BasicOutput(t *testing.T) {
	stub := NewHelmStub()
	stub.SetDepBuildOutput("Hang tight…\n")
	stub.SetUpgradeOutput("caddy", "release installed\n")

	// Test dependency build output
	cmd := stub.ExecFunc()("helm", "dependency", "build", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dependency build failed: %v", err)
	}
	if !strings.Contains(string(out), "Hang tight") {
		t.Errorf("expected 'Hang tight' in output, got: %s", out)
	}

	// Test upgrade output
	cmd = stub.ExecFunc()("helm", "upgrade", "caddy", ".")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	if !strings.Contains(string(out), "release installed") {
		t.Errorf("expected 'release installed' in output, got: %s", out)
	}

	// Test failure case
	stub.SetUpgradeFailure("failing", 1, "chart not found\n")
	cmd = stub.ExecFunc()("helm", "upgrade", "failing", ".")
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure but got success")
	}
	if !strings.Contains(string(out), "chart not found") {
		t.Errorf("expected stderr in output, got: %s", out)
	}
}
