package backup

import (
	"fmt"
	"strings"
)

// resolvePod returns the name of a single Running pod matching selector in namespace.
// Fails if zero or multiple pods match.
func resolvePod(b *Backuper, namespace, selector string) (string, error) {
	out, err := b.captureCmd("kubectl", "get", "pod",
		"-n", namespace,
		"-l", selector,
		"-o", `jsonpath={range .items[?(@.status.phase=='Running')]}{.metadata.name} {end}`,
	)
	if err != nil {
		return "", fmt.Errorf("listing pods for selector %q in namespace %q: %w", selector, namespace, err)
	}
	names := strings.Fields(strings.TrimSpace(string(out)))
	switch len(names) {
	case 0:
		return "", fmt.Errorf("no Running pod matches selector %q in namespace %q", selector, namespace)
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("selector %q in namespace %q matches multiple pods: %s — narrow the selector",
			selector, namespace, strings.Join(names, ", "))
	}
}

// resolveDatabase discovers the database name from the pod's environment.
// Probes PGDATABASE, POSTGRES_DB, POSTGRESQL_DATABASE in that order.
// Returns an error if none are set.
func resolveDatabase(b *Backuper, namespace, pod string) (string, error) {
	envVars := []string{"PGDATABASE", "POSTGRES_DB", "POSTGRESQL_DATABASE"}
	for _, envVar := range envVars {
		out, err := b.captureCmdQuiet("kubectl", "exec", "-n", namespace, pod, "--", "printenv", envVar)
		if err != nil {
			// printenv exits non-zero when the var is not set; try next
			continue
		}
		if v := strings.TrimSpace(string(out)); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("pod %q in namespace %q has none of PGDATABASE, POSTGRES_DB, POSTGRESQL_DATABASE set — cannot determine database for pg_dump",
		pod, namespace)
}
