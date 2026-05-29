package manifest

import (
	"bytes"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRoundTrip(t *testing.T) {
	// Read sample.yaml
	data, err := os.ReadFile("testdata/sample.yaml")
	if err != nil {
		t.Fatalf("failed to read sample.yaml: %v", err)
	}

	// Unmarshal to struct
	var m1 Manifest
	err = yaml.Unmarshal(data, &m1)
	if err != nil {
		t.Fatalf("failed to unmarshal sample.yaml: %v", err)
	}

	// Marshal back to YAML
	marshaled, err := yaml.Marshal(&m1)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}

	// Unmarshal again
	var m2 Manifest
	err = yaml.Unmarshal(marshaled, &m2)
	if err != nil {
		t.Fatalf("failed to unmarshal marshaled YAML: %v", err)
	}

	// Deep equality check
	if len(m1.Apps) != len(m2.Apps) {
		t.Errorf("app count mismatch: %d vs %d", len(m1.Apps), len(m2.Apps))
	}

	for i, app1 := range m1.Apps {
		app2 := m2.Apps[i]
		if app1.Name != app2.Name {
			t.Errorf("app[%d].Name mismatch: %q vs %q", i, app1.Name, app2.Name)
		}
		if app1.ChartPath != app2.ChartPath {
			t.Errorf("app[%d].ChartPath mismatch: %q vs %q", i, app1.ChartPath, app2.ChartPath)
		}
		if app1.Namespace != app2.Namespace {
			t.Errorf("app[%d].Namespace mismatch: %q vs %q", i, app1.Namespace, app2.Namespace)
		}
		if len(app1.DependsOn) != len(app2.DependsOn) {
			t.Errorf("app[%d].DependsOn length mismatch: %d vs %d", i, len(app1.DependsOn), len(app2.DependsOn))
		}
		for j, dep := range app1.DependsOn {
			if dep != app2.DependsOn[j] {
				t.Errorf("app[%d].DependsOn[%d] mismatch: %q vs %q", i, j, dep, app2.DependsOn[j])
			}
		}

		// Check backup spec
		if (app1.Backup == nil) != (app2.Backup == nil) {
			t.Errorf("app[%d].Backup nil mismatch", i)
		}
		if app1.Backup != nil && app2.Backup != nil {
			if len(app1.Backup.Targets) != len(app2.Backup.Targets) {
				t.Errorf("app[%d].Backup.Targets length mismatch: %d vs %d", i, len(app1.Backup.Targets), len(app2.Backup.Targets))
			}
			for j, target := range app1.Backup.Targets {
				if target.Type != app2.Backup.Targets[j].Type {
					t.Errorf("app[%d].Backup.Targets[%d].Type mismatch: %q vs %q", i, j, target.Type, app2.Backup.Targets[j].Type)
				}
				if target.PVC != app2.Backup.Targets[j].PVC {
					t.Errorf("app[%d].Backup.Targets[%d].PVC mismatch: %q vs %q", i, j, target.PVC, app2.Backup.Targets[j].PVC)
				}
				if target.PodSelector != app2.Backup.Targets[j].PodSelector {
					t.Errorf("app[%d].Backup.Targets[%d].PodSelector mismatch: %q vs %q", i, j, target.PodSelector, app2.Backup.Targets[j].PodSelector)
				}
			}
			if (app1.Backup.ScaleDeployments == nil) != (app2.Backup.ScaleDeployments == nil) {
				t.Errorf("app[%d].Backup.ScaleDeployments nil mismatch", i)
			}
			if app1.Backup.ScaleDeployments != nil && app2.Backup.ScaleDeployments != nil &&
				*app1.Backup.ScaleDeployments != *app2.Backup.ScaleDeployments {
				t.Errorf("app[%d].Backup.ScaleDeployments mismatch: %v vs %v", i, *app1.Backup.ScaleDeployments, *app2.Backup.ScaleDeployments)
			}
		}
	}
}

func TestStrictParse(t *testing.T) {
	// YAML with unknown top-level field
	yamlWithUnknown := `apiVersion: kubolt.io/v1
cluster: foo
apps: []
`

	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlWithUnknown)))
	decoder.KnownFields(true)

	var m Manifest
	err := decoder.Decode(&m)
	if err == nil {
		t.Error("expected error for unknown field 'cluster', but got nil")
	}
}

func TestTargetZeroValue(t *testing.T) {
	target := Target{}
	if target.Type != "" {
		t.Errorf("expected zero-value Type to be empty string, got %q", target.Type)
	}
	if target.PVC != "" {
		t.Errorf("expected zero-value PVC to be empty string, got %q", target.PVC)
	}
	if target.PodSelector != "" {
		t.Errorf("expected zero-value PodSelector to be empty string, got %q", target.PodSelector)
	}
}

func TestBackupSpecWithTargets(t *testing.T) {
	spec := BackupSpec{
		Targets: []Target{
			{
				Type:        TargetFilesystem,
				PVC:         "data-pvc",
				PodSelector: "",
			},
			{
				Type:        TargetPgDump,
				PVC:         "",
				PodSelector: "app=postgres",
			},
		},
	}

	if len(spec.Targets) != 2 {
		t.Errorf("expected 2 targets, got %d", len(spec.Targets))
	}

	if spec.Targets[0].Type != TargetFilesystem {
		t.Errorf("expected first target type to be %q, got %q", TargetFilesystem, spec.Targets[0].Type)
	}
	if spec.Targets[0].PVC != "data-pvc" {
		t.Errorf("expected first target PVC to be %q, got %q", "data-pvc", spec.Targets[0].PVC)
	}

	if spec.Targets[1].Type != TargetPgDump {
		t.Errorf("expected second target type to be %q, got %q", TargetPgDump, spec.Targets[1].Type)
	}
	if spec.Targets[1].PodSelector != "app=postgres" {
		t.Errorf("expected second target PodSelector to be %q, got %q", "app=postgres", spec.Targets[1].PodSelector)
	}
}
