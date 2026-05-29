package helm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsurePlugin ensures the named helm plugin is installed, installing it
// from srcDir (a local directory containing plugin.yaml) if absent.
func EnsurePlugin(r *Runner, name, srcDir string) error {
	pluginYaml := filepath.Join(srcDir, "plugin.yaml")
	if _, err := os.Stat(pluginYaml); err != nil {
		return fmt.Errorf("missing plugin manifest at %s/plugin.yaml", srcDir)
	}

	out, err := r.Capture(BuildPluginList())
	if err != nil {
		return fmt.Errorf("listing helm plugins: %w", err)
	}

	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return nil
		}
	}

	fmt.Fprintf(r.Stdout, "==> Installing helm plugin: %s\n", name)
	return r.Run(BuildPluginInstall(srcDir))
}
