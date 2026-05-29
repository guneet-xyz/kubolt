package helm

import (
	"slices"
	"testing"
)

func TestBuildInstall(t *testing.T) {
	release := "my-release"
	chartDir := "/path/to/chart"
	namespace := "default"
	valuesFiles := []string{"/path/to/values1.yaml", "/path/to/values2.yaml"}
	opts := InstallOpts{ForceConflicts: true, TakeOwnership: false}

	result := BuildInstall(release, chartDir, namespace, valuesFiles, opts)

	expected := []string{
		"helm", "install", "my-release", "/path/to/chart",
		"-n", "default", "--create-namespace",
		"-f", "/path/to/values1.yaml",
		"-f", "/path/to/values2.yaml",
		"--rollback-on-failure", "--wait",
		"--force-conflicts",
		"--post-renderer", "obscuro", "--post-renderer-args", "inject",
	}

	if !slices.Equal(result, expected) {
		t.Errorf("BuildInstall mismatch\nGot:      %v\nExpected: %v", result, expected)
	}
}

func TestBuildUpgrade(t *testing.T) {
	release := "my-release"
	chartDir := "/path/to/chart"
	namespace := "default"
	valuesFiles := []string{"/path/to/values.yaml"}
	opts := InstallOpts{ForceConflicts: false, TakeOwnership: true}

	result := BuildUpgrade(release, chartDir, namespace, valuesFiles, opts)

	// Verify no --create-namespace
	if slices.Contains(result, "--create-namespace") {
		t.Errorf("BuildUpgrade should not contain --create-namespace, got: %v", result)
	}

	// Verify basic structure
	if result[0] != "helm" || result[1] != "upgrade" {
		t.Errorf("BuildUpgrade should start with 'helm upgrade', got: %v", result[:2])
	}

	// Verify --take-ownership is present
	if !slices.Contains(result, "--take-ownership") {
		t.Errorf("BuildUpgrade should contain --take-ownership, got: %v", result)
	}
}

func TestUninstallHasNoExtraFlags(t *testing.T) {
	result := BuildUninstall("walls", "walls")

	// CRITICAL GUARDRAIL: Must be exactly 5 elements
	if len(result) != 5 {
		t.Errorf("BuildUninstall must return exactly 5 elements, got %d: %v", len(result), result)
	}

	// CRITICAL GUARDRAIL: Must not contain --cascade
	if slices.Contains(result, "--cascade") {
		t.Errorf("BuildUninstall must not contain --cascade, got: %v", result)
	}

	// Verify exact content
	expected := []string{"helm", "uninstall", "walls", "-n", "walls"}
	if !slices.Equal(result, expected) {
		t.Errorf("BuildUninstall mismatch\nGot:      %v\nExpected: %v", result, expected)
	}
}

func TestBuildDependencyBuild(t *testing.T) {
	chartDir := "/path/to/chart"
	result := BuildDependencyBuild(chartDir)

	expected := []string{"helm", "dependency", "build", "/path/to/chart"}
	if !slices.Equal(result, expected) {
		t.Errorf("BuildDependencyBuild mismatch\nGot:      %v\nExpected: %v", result, expected)
	}
}

func TestBuildTemplate(t *testing.T) {
	release := "my-release"
	chartDir := "/path/to/chart"
	namespace := "default"
	valuesFiles := []string{"/path/to/values1.yaml", "/path/to/values2.yaml"}

	result := BuildTemplate(release, chartDir, namespace, valuesFiles)

	expected := []string{
		"helm", "template", "my-release", "/path/to/chart",
		"-n", "default",
		"-f", "/path/to/values1.yaml",
		"-f", "/path/to/values2.yaml",
	}

	if !slices.Equal(result, expected) {
		t.Errorf("BuildTemplate mismatch\nGot:      %v\nExpected: %v", result, expected)
	}
}

func TestBuildList(t *testing.T) {
	namespace := "default"
	result := BuildList(namespace)

	// Verify -o json is present
	if !slices.Contains(result, "-o") || !slices.Contains(result, "json") {
		t.Errorf("BuildList should contain '-o json', got: %v", result)
	}

	expected := []string{"helm", "list", "-n", "default", "-o", "json"}
	if !slices.Equal(result, expected) {
		t.Errorf("BuildList mismatch\nGot:      %v\nExpected: %v", result, expected)
	}
}

func TestBuildPluginList(t *testing.T) {
	result := BuildPluginList()

	expected := []string{"helm", "plugin", "list"}
	if !slices.Equal(result, expected) {
		t.Errorf("BuildPluginList mismatch\nGot:      %v\nExpected: %v", result, expected)
	}
}

func TestBuildPluginInstall(t *testing.T) {
	srcDir := "/path/to/plugin"
	result := BuildPluginInstall(srcDir)

	expected := []string{"helm", "plugin", "install", "/path/to/plugin"}
	if !slices.Equal(result, expected) {
		t.Errorf("BuildPluginInstall mismatch\nGot:      %v\nExpected: %v", result, expected)
	}
}
