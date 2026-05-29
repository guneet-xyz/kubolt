package helm

// InstallOpts controls optional helm install/upgrade flags.
type InstallOpts struct {
	ForceConflicts bool
	TakeOwnership  bool
}

// BuildInstall returns args for: helm install <release> <chartDir> -n <ns> --create-namespace
// -f <valuesFile>... --rollback-on-failure --wait [--force-conflicts] [--take-ownership]
// --post-renderer obscuro --post-renderer-args inject
func BuildInstall(release, chartDir, namespace string, valuesFiles []string, opts InstallOpts) []string {
	args := []string{"helm", "install", release, chartDir, "-n", namespace, "--create-namespace"}
	for _, f := range valuesFiles {
		args = append(args, "-f", f)
	}
	args = append(args, "--rollback-on-failure", "--wait")
	if opts.ForceConflicts {
		args = append(args, "--force-conflicts")
	}
	if opts.TakeOwnership {
		args = append(args, "--take-ownership")
	}
	args = append(args, "--post-renderer", "obscuro", "--post-renderer-args", "inject")
	return args
}

// BuildUpgrade returns args for: helm upgrade <release> <chartDir> -n <ns>
// (same flags as install but no --create-namespace)
func BuildUpgrade(release, chartDir, namespace string, valuesFiles []string, opts InstallOpts) []string {
	args := []string{"helm", "upgrade", release, chartDir, "-n", namespace}
	for _, f := range valuesFiles {
		args = append(args, "-f", f)
	}
	args = append(args, "--rollback-on-failure", "--wait")
	if opts.ForceConflicts {
		args = append(args, "--force-conflicts")
	}
	if opts.TakeOwnership {
		args = append(args, "--take-ownership")
	}
	args = append(args, "--post-renderer", "obscuro", "--post-renderer-args", "inject")
	return args
}

// BuildUninstall returns args for: helm uninstall <release> -n <namespace>
// IMPORTANT: This function MUST return exactly 5 elements. No extra flags allowed.
func BuildUninstall(release, namespace string) []string {
	return []string{"helm", "uninstall", release, "-n", namespace}
}

// BuildDependencyBuild returns args for: helm dependency build <chartDir>
func BuildDependencyBuild(chartDir string) []string {
	return []string{"helm", "dependency", "build", chartDir}
}

// BuildTemplate returns args for: helm template <release> <chartDir> -n <ns> -f <valuesFile>...
func BuildTemplate(release, chartDir, namespace string, valuesFiles []string) []string {
	args := []string{"helm", "template", release, chartDir, "-n", namespace}
	for _, f := range valuesFiles {
		args = append(args, "-f", f)
	}
	return args
}

// BuildList returns args for: helm list -n <namespace> -o json
func BuildList(namespace string) []string {
	return []string{"helm", "list", "-n", namespace, "-o", "json"}
}

// BuildPluginList returns args for: helm plugin list
func BuildPluginList() []string {
	return []string{"helm", "plugin", "list"}
}

// BuildPluginInstall returns args for: helm plugin install <srcDir>
func BuildPluginInstall(srcDir string) []string {
	return []string{"helm", "plugin", "install", srcDir}
}
