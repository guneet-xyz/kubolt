package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeChartYamls creates an empty Chart.yaml under dir/<chartPath> for every app.
func writeChartYamls(t *testing.T, dir string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(filepath.Join(full, "Chart.yaml"), []byte("apiVersion: v2\nname: x\nversion: 0.1.0\n"), 0o644); err != nil {
			t.Fatalf("write Chart.yaml: %v", err)
		}
	}
}

// stageManifest copies a testdata yaml file into a temp dir and creates the
// referenced chartPath directories (each with a stub Chart.yaml).
func stageManifest(t *testing.T, testdataName string, chartPaths ...string) string {
	t.Helper()
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", testdataName))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	dst := filepath.Join(dir, "kubolt.yaml")
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	writeChartYamls(t, dir, chartPaths...)
	return dst
}

func TestLoad_Valid(t *testing.T) {
	path := stageManifest(t, "valid.yaml", "apps/caddy", "apps/registry", "apps/walls")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.APIVersion != "kubolt.io/v1" {
		t.Errorf("APIVersion = %q", m.APIVersion)
	}
	if len(m.Apps) != 3 {
		t.Fatalf("apps = %d, want 3", len(m.Apps))
	}
	walls, ok := m.AppByName("walls")
	if !ok {
		t.Fatal("walls not found")
	}
	if walls.Backup == nil {
		t.Fatal("walls.Backup nil")
	}
	if len(walls.Backup.Targets) == 0 {
		t.Fatal("walls.Backup.Targets empty")
	}
	if walls.Backup.ScaleDeployments == nil || *walls.Backup.ScaleDeployments != true {
		t.Errorf("ScaleDeployments default = %v, want *true", walls.Backup.ScaleDeployments)
	}
	if m.Dir() == "" {
		t.Error("Dir empty")
	}
}

func TestLoad_UnknownAPIVersion(t *testing.T) {
	path := stageManifest(t, "unknown_apiversion.yaml", "apps/caddy")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "apiVersion") {
		t.Errorf("error %q missing 'apiVersion'", err)
	}
}

func TestLoad_MissingField(t *testing.T) {
	path := stageManifest(t, "missing_field.yaml", "apps/caddy")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error %q missing 'name'", err)
	}
}

func TestLoad_DuplicateName(t *testing.T) {
	path := stageManifest(t, "duplicate_name.yaml", "apps/caddy", "apps/caddy2")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), "caddy") {
		t.Errorf("error %q missing 'duplicate' and 'caddy'", err)
	}
}

func TestLoad_Cycle(t *testing.T) {
	path := stageManifest(t, "cycle.yaml", "apps/a", "apps/b")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "dependsOn") && !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q missing 'dependsOn' or 'cycle'", err)
	}
}

func TestLoad_UnknownDep(t *testing.T) {
	path := stageManifest(t, "unknown_dep.yaml", "apps/walls")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %q missing 'nonexistent'", err)
	}
}

func TestLoad_UnknownField(t *testing.T) {
	path := stageManifest(t, "unknown_field.yaml", "apps/caddy")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cluster") {
		t.Errorf("error %q missing 'cluster'", err)
	}
}

func TestLoad_MissingChart(t *testing.T) {
	path := stageManifest(t, "missing_chart.yaml")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "chartPath") && !strings.Contains(err.Error(), "Chart.yaml") {
		t.Errorf("error %q missing 'chartPath'/'Chart.yaml'", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q missing 'ghost'", err)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestApplyDefaults_RespectsExplicitFalse(t *testing.T) {
	f := false
	m := &Manifest{
		APIVersion: SupportedAPIVersion,
		Apps: []App{{
			Name:      "x",
			ChartPath: "x",
			Namespace: "x",
			Backup: &BackupSpec{
				Targets:          []Target{{Type: TargetFilesystem, PVC: "p"}},
				ScaleDeployments: &f,
			},
		}},
	}
	m.ApplyDefaults()
	if m.Apps[0].Backup.ScaleDeployments == nil || *m.Apps[0].Backup.ScaleDeployments != false {
		t.Errorf("explicit false overwritten: %v", m.Apps[0].Backup.ScaleDeployments)
	}
}

func TestValidate_EmptyChartPath(t *testing.T) {
	m := &Manifest{
		APIVersion: SupportedAPIVersion,
		Apps:       []App{{Name: "a", ChartPath: "", Namespace: "a"}},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "chartPath") {
		t.Errorf("err = %v", err)
	}
}

func TestValidate_EmptyNamespace(t *testing.T) {
	m := &Manifest{
		APIVersion: SupportedAPIVersion,
		Apps:       []App{{Name: "a", ChartPath: "x", Namespace: ""}},
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("err = %v", err)
	}
}

func TestValidate_EmptyBackupTargets(t *testing.T) {
	dir := t.TempDir()
	writeChartYamls(t, dir, "x")
	m := &Manifest{
		APIVersion: SupportedAPIVersion,
		Apps: []App{{
			Name: "a", ChartPath: "x", Namespace: "a",
			Backup: &BackupSpec{Targets: []Target{}},
		}},
		dir: dir,
	}
	err := m.Validate()
	if err == nil || !strings.Contains(err.Error(), "targets") {
		t.Errorf("err = %v", err)
	}
}

func TestInstallOrder(t *testing.T) {
	path := stageManifest(t, "valid.yaml", "apps/caddy", "apps/registry", "apps/walls")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	order, err := m.InstallOrder("walls")
	if err != nil {
		t.Fatalf("InstallOrder: %v", err)
	}
	if order[len(order)-1] != "walls" {
		t.Errorf("walls not last: %v", order)
	}
	if len(order) != 3 {
		t.Errorf("len=%d want 3 (%v)", len(order), order)
	}
	caddyIdx, regIdx, wallsIdx := -1, -1, -1
	for i, n := range order {
		switch n {
		case "caddy":
			caddyIdx = i
		case "registry":
			regIdx = i
		case "walls":
			wallsIdx = i
		}
	}
	if caddyIdx >= wallsIdx || regIdx >= wallsIdx {
		t.Errorf("deps not before walls: %v", order)
	}

	order, err = m.InstallOrder("caddy")
	if err != nil {
		t.Fatalf("InstallOrder caddy: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"caddy"}) {
		t.Errorf("caddy order = %v", order)
	}

	if _, err := m.InstallOrder("nope"); err == nil {
		t.Error("expected unknown app error")
	}
}

func TestDependents(t *testing.T) {
	path := stageManifest(t, "valid.yaml", "apps/caddy", "apps/registry", "apps/walls")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dep := m.Dependents("caddy")
	if !reflect.DeepEqual(dep, []string{"walls"}) {
		t.Errorf("Dependents(caddy) = %v", dep)
	}
	if got := m.Dependents("walls"); len(got) != 0 {
		t.Errorf("Dependents(walls) = %v, want empty", got)
	}
}

func TestAppByName(t *testing.T) {
	path := stageManifest(t, "valid.yaml", "apps/caddy", "apps/registry", "apps/walls")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if a, ok := m.AppByName("caddy"); !ok || a.Name != "caddy" {
		t.Errorf("AppByName(caddy) = %v, %v", a, ok)
	}
	if _, ok := m.AppByName("nope"); ok {
		t.Error("expected !ok for nope")
	}
}

func TestLoad_BackupValidation(t *testing.T) {
	cases := []struct {
		name      string
		fixture   string
		charts    []string
		errSubstr string
	}{
		{
			name:      "valid_mixed_targets",
			fixture:   "valid_mixed_targets.yaml",
			charts:    []string{"apps/walls"},
			errSubstr: "",
		},
		{
			name:      "legacy_pvcs_rejected",
			fixture:   "legacy_pvcs.yaml",
			charts:    []string{"apps/walls"},
			errSubstr: "pvcs",
		},
		{
			name:      "legacy_pvcs_mentions_targets",
			fixture:   "legacy_pvcs.yaml",
			charts:    []string{"apps/walls"},
			errSubstr: "targets",
		},
		{
			name:      "pgdump_no_selector",
			fixture:   "pgdump_no_selector.yaml",
			charts:    []string{"apps/walls"},
			errSubstr: "podSelector",
		},
		{
			name:      "filesystem_no_pvc",
			fixture:   "filesystem_no_pvc.yaml",
			charts:    []string{"apps/walls"},
			errSubstr: "pvc",
		},
		{
			name:      "empty_targets",
			fixture:   "",
			charts:    []string{"apps/walls"},
			errSubstr: "targets",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			if tc.fixture != "" {
				path = stageManifest(t, tc.fixture, tc.charts...)
			} else {
				dir := t.TempDir()
				writeChartYamls(t, dir, tc.charts...)
				manifestYAML := `apiVersion: kubolt.io/v1
apps:
  - name: walls
    chartPath: ./apps/walls
    namespace: walls
    backup:
      targets: []
`
				dst := filepath.Join(dir, "kubolt.yaml")
				if err := os.WriteFile(dst, []byte(manifestYAML), 0o644); err != nil {
					t.Fatalf("write manifest: %v", err)
				}
				path = dst
			}

			_, err := Load(path)
			if tc.errSubstr == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q missing %q", err, tc.errSubstr)
				}
			}
		})
	}
}

