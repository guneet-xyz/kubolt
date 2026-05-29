package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guneet-xyz/kubolt/internal/depgraph"
	"gopkg.in/yaml.v3"
)

const SupportedAPIVersion = "kubolt.io/v1"

var ErrUnsupportedAPIVersion = errors.New("unsupported apiVersion")

// Load reads, defaults, and validates a kubolt manifest from path.
func Load(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening manifest: %w", err)
	}
	defer f.Close()

	var m Manifest
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		// Check if it's a legacy pvcs field error and make it actionable
		errStr := err.Error()
		if strings.Contains(errStr, "pvcs") {
			return nil, fmt.Errorf("parsing manifest: backup.pvcs is no longer supported; use backup.targets: [{type: filesystem, pvc: <name>}] or [{type: pg_dump, podSelector: <selector>}]: %w", err)
		}
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	m.dir = filepath.Dir(path)
	m.ApplyDefaults()

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ApplyDefaults fills in default values for optional fields.
func (m *Manifest) ApplyDefaults() {
	t := true
	for i := range m.Apps {
		if m.Apps[i].Backup != nil && m.Apps[i].Backup.ScaleDeployments == nil {
			m.Apps[i].Backup.ScaleDeployments = &t
		}
	}
}

// Validate checks the manifest for structural and semantic errors.
// Fails fast on the first error.
func (m *Manifest) Validate() error {
	if m.APIVersion != SupportedAPIVersion {
		return fmt.Errorf("%w: got %q, want %q", ErrUnsupportedAPIVersion, m.APIVersion, SupportedAPIVersion)
	}

	names := make(map[string]bool, len(m.Apps))
	for _, app := range m.Apps {
		if app.Name == "" {
			return fmt.Errorf("app has empty name field")
		}
		if app.ChartPath == "" {
			return fmt.Errorf("app %q has empty chartPath field", app.Name)
		}
		if app.Namespace == "" {
			return fmt.Errorf("app %q has empty namespace field", app.Name)
		}
		if names[app.Name] {
			return fmt.Errorf("duplicate app name: %q", app.Name)
		}
		names[app.Name] = true
	}

	for _, app := range m.Apps {
		for _, dep := range app.DependsOn {
			if !names[dep] {
				return fmt.Errorf("app %q dependsOn unknown app %q", app.Name, dep)
			}
		}
	}

	adj := make(map[string][]string, len(m.Apps))
	for _, app := range m.Apps {
		adj[app.Name] = app.DependsOn
	}
	if _, err := depgraph.TopoSort(adj); err != nil {
		return fmt.Errorf("dependsOn: %w", err)
	}

	for _, app := range m.Apps {
		chartYaml := filepath.Join(m.dir, app.ChartPath, "Chart.yaml")
		if _, err := os.Stat(chartYaml); err != nil {
			return fmt.Errorf("app %q chartPath: Chart.yaml not found at %s", app.Name, chartYaml)
		}
	}

	for _, app := range m.Apps {
		if app.Backup != nil {
			if len(app.Backup.Targets) == 0 {
				return fmt.Errorf("app %q: backup.targets must be non-empty", app.Name)
			}
			for i, t := range app.Backup.Targets {
				switch t.Type {
				case TargetFilesystem:
					if t.PVC == "" {
						return fmt.Errorf("app %q: backup.targets[%d]: type filesystem requires pvc", app.Name, i)
					}
					if t.PodSelector != "" {
						return fmt.Errorf("app %q: backup.targets[%d]: type filesystem must not set podSelector", app.Name, i)
					}
				case TargetPgDump:
					if t.PodSelector == "" {
						return fmt.Errorf("app %q: backup.targets[%d]: type pg_dump requires podSelector", app.Name, i)
					}
					if t.PVC != "" {
						return fmt.Errorf("app %q: backup.targets[%d]: type pg_dump must not set pvc", app.Name, i)
					}
				default:
					return fmt.Errorf("app %q: backup.targets[%d]: unknown type %q (must be filesystem or pg_dump)", app.Name, i, t.Type)
				}
			}
		}
	}

	return nil
}

// AppByName returns a pointer to the app with the given name, or false.
func (m *Manifest) AppByName(name string) (*App, bool) {
	for i := range m.Apps {
		if m.Apps[i].Name == name {
			return &m.Apps[i], true
		}
	}
	return nil, false
}

// InstallOrder returns target and its transitive dependencies in install order.
func (m *Manifest) InstallOrder(target string) ([]string, error) {
	if _, ok := m.AppByName(target); !ok {
		return nil, fmt.Errorf("unknown app: %q", target)
	}
	adj := make(map[string][]string, len(m.Apps))
	for _, app := range m.Apps {
		adj[app.Name] = app.DependsOn
	}
	order, err := depgraph.TopoSort(adj)
	if err != nil {
		return nil, err
	}
	needed := transitiveDeps(adj, target)
	needed[target] = true
	result := make([]string, 0, len(needed))
	for _, name := range order {
		if needed[name] {
			result = append(result, name)
		}
	}
	return result, nil
}

// InstallAllOrder returns every app in the manifest in dependency order.
func (m *Manifest) InstallAllOrder() ([]string, error) {
	adj := make(map[string][]string, len(m.Apps))
	for _, app := range m.Apps {
		adj[app.Name] = app.DependsOn
	}
	return depgraph.TopoSort(adj)
}

func transitiveDeps(adj map[string][]string, target string) map[string]bool {
	visited := make(map[string]bool)
	var walk func(n string)
	walk = func(n string) {
		for _, dep := range adj[n] {
			if !visited[dep] {
				visited[dep] = true
				walk(dep)
			}
		}
	}
	walk(target)
	return visited
}

// Dependents returns apps that directly depend on target.
func (m *Manifest) Dependents(target string) []string {
	var result []string
	for _, app := range m.Apps {
		for _, dep := range app.DependsOn {
			if dep == target {
				result = append(result, app.Name)
				break
			}
		}
	}
	return result
}

// Dir returns the directory containing the loaded manifest file.
func (m *Manifest) Dir() string {
	return m.dir
}
