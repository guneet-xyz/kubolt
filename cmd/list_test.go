package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guneet-xyz/kubolt/internal/helm"
)

type testApp struct {
	name, namespace string
}

func setupTestManifest(t *testing.T, apps []testApp) string {
	t.Helper()
	tmpDir := t.TempDir()

	var sb strings.Builder
	sb.WriteString("apiVersion: kubolt.io/v1\napps:\n")
	for _, a := range apps {
		chartDir := filepath.Join(tmpDir, "charts", a.name)
		if err := os.MkdirAll(chartDir, 0o755); err != nil {
			t.Fatalf("mkdir chart dir: %v", err)
		}
		chartYaml := fmt.Sprintf("apiVersion: v2\nname: %s\nversion: 1.0.0\n", a.name)
		if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYaml), 0o644); err != nil {
			t.Fatalf("write Chart.yaml: %v", err)
		}
		fmt.Fprintf(&sb, "  - name: %s\n    chartPath: charts/%s\n    namespace: %s\n", a.name, a.name, a.namespace)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "kubolt.yaml"), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write kubolt.yaml: %v", err)
	}
	return tmpDir
}

func normalizeTable(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		out = append(out, strings.Join(fields, " "))
	}
	return strings.Join(out, "\n")
}

func TestList_MixedStates(t *testing.T) {
	tmpDir := setupTestManifest(t, []testApp{
		{name: "caddy", namespace: "caddy"},
		{name: "walls", namespace: "walls"},
		{name: "registry", namespace: "registry"},
	})

	helm.SetExecCommand(func(name string, args ...string) *exec.Cmd {
		if name == "helm" && len(args) >= 2 && args[0] == "list" {
			ns := ""
			for i, a := range args {
				if a == "-n" && i+1 < len(args) {
					ns = args[i+1]
				}
			}
			switch ns {
			case "caddy":
				return exec.Command("echo", `[{"name":"caddy","namespace":"caddy","status":"deployed","chart":"caddy-1.0.0","app_version":"2.7.5"}]`)
			case "registry":
				return exec.Command("echo", `[{"name":"registry","namespace":"registry","status":"deployed","chart":"registry-1.0.0","app_version":"2.8.3"}]`)
			default:
				return exec.Command("echo", "[]")
			}
		}
		return exec.Command("true")
	})
	defer helm.ResetExecCommand()

	var buf bytes.Buffer
	Stdout = &buf
	defer func() { Stdout = os.Stdout }()

	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origDir)

	if err := runList(listCmd, nil); err != nil {
		t.Fatalf("runList error: %v", err)
	}

	golden, err := os.ReadFile(filepath.Join(origDir, "testdata", "list-golden.txt"))
	if err != nil {
		t.Fatalf("reading golden file: %v", err)
	}

	got := normalizeTable(buf.String())
	want := normalizeTable(string(golden))
	if got != want {
		t.Errorf("list output mismatch:\ngot:\n%s\n\nwant:\n%s", buf.String(), string(golden))
	}
}
