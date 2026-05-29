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

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/manifest"
)

type validateTestApp struct {
	name      string
	namespace string
	chartYaml string
}

func setupValidateManifest(t *testing.T, apps []validateTestApp) *manifest.Manifest {
	t.Helper()
	tmpDir := t.TempDir()

	var sb strings.Builder
	sb.WriteString("apiVersion: kubolt.io/v1\napps:\n")
	for _, a := range apps {
		chartDir := filepath.Join(tmpDir, "charts", a.name)
		if err := os.MkdirAll(chartDir, 0o755); err != nil {
			t.Fatalf("mkdir chart dir: %v", err)
		}
		chartYaml := a.chartYaml
		if chartYaml == "" {
			chartYaml = fmt.Sprintf("apiVersion: v2\nname: %s\nversion: 1.0.0\n", a.name)
		}
		if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
			t.Fatalf("write Chart.yaml: %v", err)
		}
		fmt.Fprintf(&sb, "  - name: %s\n    chartPath: charts/%s\n    namespace: %s\n",
			a.name, a.name, a.namespace)
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

type validateCallRecorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (c *validateCallRecorder) record(name string, args ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, append([]string{name}, args...))
}

func (c *validateCallRecorder) snapshot() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]string, len(c.calls))
	copy(out, c.calls)
	return out
}

func TestValidate_AllPass(t *testing.T) {
	m := setupValidateManifest(t, []validateTestApp{
		{name: "app1", namespace: "ns1"},
		{name: "app2", namespace: "ns2"},
		{name: "app3", namespace: "ns3"},
	})

	rec := &validateCallRecorder{}
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	prevStdout := Stdout
	Stdout = &buf
	defer func() { Stdout = prevStdout }()

	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := validateApps(m, runner, m.Dir()); err != nil {
		t.Fatalf("validateApps error: %v", err)
	}

	var templates int
	for _, c := range rec.snapshot() {
		if len(c) >= 2 && c[0] == "helm" && c[1] == "template" {
			templates++
		}
	}
	if templates != 3 {
		t.Errorf("expected 3 helm template calls; got %d (%v)", templates, rec.snapshot())
	}

	if !strings.Contains(buf.String(), "3/3 charts OK") {
		t.Errorf("expected summary '3/3 charts OK' in output; got: %s", buf.String())
	}
}

func TestValidate_OneChartFails(t *testing.T) {
	m := setupValidateManifest(t, []validateTestApp{
		{name: "app1", namespace: "ns1"},
		{name: "bad", namespace: "ns2"},
		{name: "app3", namespace: "ns3"},
	})

	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) >= 3 && args[0] == "template" && args[1] == "bad" {
			return exec.Command("false")
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	prevStdout := Stdout
	prevStderr := Stderr
	Stdout = &buf
	Stderr = &buf
	defer func() { Stdout = prevStdout; Stderr = prevStderr }()

	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	err := validateApps(m, runner, m.Dir())
	if err == nil {
		t.Fatalf("expected error from validateApps; got nil")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("expected error to name failed chart 'bad'; got: %v", err)
	}
	if !strings.Contains(buf.String(), "2/3 charts OK") {
		t.Errorf("expected summary '2/3 charts OK' in output; got: %s", buf.String())
	}
}

func TestValidate_SkipsLibraryChart(t *testing.T) {
	m := setupValidateManifest(t, []validateTestApp{
		{name: "app1", namespace: "ns1"},
		{
			name:      "libchart",
			namespace: "ns2",
			chartYaml: "apiVersion: v2\nname: libchart\nversion: 1.0.0\ntype: library\n",
		},
		{name: "app3", namespace: "ns3"},
	})

	rec := &validateCallRecorder{}
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	prevStdout := Stdout
	Stdout = &buf
	defer func() { Stdout = prevStdout }()

	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := validateApps(m, runner, m.Dir()); err != nil {
		t.Fatalf("validateApps error: %v", err)
	}

	var templatedNames []string
	for _, c := range rec.snapshot() {
		if len(c) >= 3 && c[0] == "helm" && c[1] == "template" {
			templatedNames = append(templatedNames, c[2])
		}
	}
	for _, n := range templatedNames {
		if n == "libchart" {
			t.Errorf("helm template called for library chart 'libchart'; should be skipped")
		}
	}
	if len(templatedNames) != 2 {
		t.Errorf("expected 2 helm template calls (skipping library); got %d (%v)", len(templatedNames), templatedNames)
	}
	if !strings.Contains(buf.String(), "skip libchart (library chart)") {
		t.Errorf("expected skip message for libchart; got: %s", buf.String())
	}
}

func TestValidate_FileDep_TriggersDependencyBuild(t *testing.T) {
	m := setupValidateManifest(t, []validateTestApp{
		{
			name:      "withdep",
			namespace: "ns1",
			chartYaml: "apiVersion: v2\nname: withdep\nversion: 1.0.0\ndependencies:\n  - name: sub\n    version: 1.0.0\n    repository: file://../sub\n",
		},
	})

	rec := &validateCallRecorder{}
	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		rec.record(name, args...)
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	prevStdout := Stdout
	Stdout = &buf
	defer func() { Stdout = prevStdout }()

	runner := &helm.Runner{Stdout: &buf, Stderr: &buf}

	if err := validateApps(m, runner, m.Dir()); err != nil {
		t.Fatalf("validateApps error: %v", err)
	}

	calls := rec.snapshot()
	depBuildIdx := -1
	templateIdx := -1
	for i, c := range calls {
		if len(c) >= 3 && c[0] == "helm" && c[1] == "dependency" && c[2] == "build" {
			depBuildIdx = i
		}
		if len(c) >= 2 && c[0] == "helm" && c[1] == "template" {
			templateIdx = i
		}
	}
	if depBuildIdx == -1 {
		t.Fatalf("expected helm dependency build call; got %v", calls)
	}
	if templateIdx == -1 {
		t.Fatalf("expected helm template call; got %v", calls)
	}
	if depBuildIdx > templateIdx {
		t.Errorf("expected helm dependency build BEFORE helm template; got dep=%d template=%d", depBuildIdx, templateIdx)
	}
}
