package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/guneet-xyz/kubolt/internal/helm"
	"github.com/guneet-xyz/kubolt/internal/manifest"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all apps and their install status",
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

type helmRelease struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

type appStatus struct {
	name       string
	namespace  string
	status     string
	chart      string
	appVersion string
}

func runList(cmd *cobra.Command, args []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(cwd, "kubolt.yaml")
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return fmt.Errorf("loading manifest: %w", err)
	}

	runner := &helm.Runner{
		DryRun: dryRun,
		Stdout: Stdout,
		Stderr: Stderr,
	}

	statuses := make([]appStatus, len(m.Apps))
	for i, app := range m.Apps {
		statuses[i] = appStatus{name: app.Name, namespace: app.Namespace, status: "missing", chart: "-", appVersion: "-"}
	}

	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(8)

	for i, app := range m.Apps {
		i, app := i, app
		g.Go(func() error {
			out, err := runner.Capture(helm.BuildList(app.Namespace))
			if err != nil || len(out) == 0 {
				return nil
			}
			var releases []helmRelease
			if err := json.Unmarshal(out, &releases); err != nil {
				return nil
			}
			for _, rel := range releases {
				if rel.Name == app.Name {
					statuses[i].status = rel.Status
					statuses[i].chart = rel.Chart
					statuses[i].appVersion = rel.AppVersion
					break
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].name < statuses[j].name
	})

	w := tabwriter.NewWriter(Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "APP\tNAMESPACE\tSTATUS\tCHART\tAPP VERSION")
	for _, s := range statuses {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.name, s.namespace, s.status, s.chart, s.appVersion)
	}
	return w.Flush()
}
