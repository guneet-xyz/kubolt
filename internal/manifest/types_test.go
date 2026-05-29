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
			if len(app1.Backup.PVCs) != len(app2.Backup.PVCs) {
				t.Errorf("app[%d].Backup.PVCs length mismatch: %d vs %d", i, len(app1.Backup.PVCs), len(app2.Backup.PVCs))
			}
			for j, pvc := range app1.Backup.PVCs {
				if pvc != app2.Backup.PVCs[j] {
					t.Errorf("app[%d].Backup.PVCs[%d] mismatch: %q vs %q", i, j, pvc, app2.Backup.PVCs[j])
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
