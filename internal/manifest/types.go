package manifest

// Manifest is the top-level structure of a kubolt.yaml file.
type Manifest struct {
	APIVersion string `yaml:"apiVersion"` // must be "kubolt.io/v1"
	Apps       []App  `yaml:"apps"`

	// dir is the directory containing the loaded manifest file.
	// Unexported so yaml.v3 ignores it. Set by Load().
	dir string
}

// App describes a single Helm chart managed by kubolt.
type App struct {
	Name      string      `yaml:"name"`
	ChartPath string      `yaml:"chartPath"` // relative to manifest dir
	Namespace string      `yaml:"namespace"`
	DependsOn []string    `yaml:"dependsOn,omitempty"`
	Backup    *BackupSpec `yaml:"backup,omitempty"`
}

// TargetType is the type of backup target.
type TargetType string

// Target backup types.
const (
	TargetFilesystem TargetType = "filesystem"
	TargetPgDump     TargetType = "pg_dump"
)

// Target describes a single backup target.
type Target struct {
	Type        TargetType `yaml:"type"`
	PVC         string     `yaml:"pvc,omitempty"`
	PodSelector string     `yaml:"podSelector,omitempty"`
}

// BackupSpec describes how to back up an app's persistent data.
type BackupSpec struct {
	Targets          []Target `yaml:"targets"`
	ScaleDeployments *bool    `yaml:"scaleDeployments,omitempty"` // default true, applied by parser
}
