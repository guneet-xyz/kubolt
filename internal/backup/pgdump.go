package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/guneet-xyz/kubolt/internal/manifest"
)

// pgDumpStrategy backs up a Postgres database by streaming pg_dump -Fc
// output from kubectl exec directly to a local file.
type pgDumpStrategy struct {
	b *Backuper
}

func init() {
	registerStrategy(manifest.TargetPgDump, func(b *Backuper) Strategy {
		return &pgDumpStrategy{b: b}
	})
}

const pgDumpEnvScript = `if [ -z "${PGUSER:-}" ]; then
	PGUSER="${POSTGRES_USER:-${POSTGRESQL_USER:-${POSTGRESQL_USERNAME:-}}}"
fi
if [ -n "${PGUSER:-}" ]; then
	export PGUSER
fi
if [ -z "${PGPASSWORD:-}" ]; then
	PGPASSWORD="${POSTGRES_PASSWORD:-${POSTGRESQL_PASSWORD:-}}"
fi
if [ -n "${PGPASSWORD:-}" ]; then
	export PGPASSWORD
fi
exec pg_dump -Fc "$1"`

// Backup runs pg_dump -Fc inside the pod and streams the output to
// localTsDir/<app>-<db>.dump. Writes to a .partial file first and
// renames on success; leaves .partial for forensics on failure.
func (s *pgDumpStrategy) Backup(ctx context.Context, app manifest.App, target manifest.Target, localTsDir string) error {
	b := s.b

	pod, err := resolvePod(b, app.Namespace, target.PodSelector)
	if err != nil {
		return fmt.Errorf("resolving pod for app %q: %w", app.Name, err)
	}
	db, err := resolveDatabase(b, app.Namespace, pod)
	if err != nil {
		return fmt.Errorf("resolving database for app %q: %w", app.Name, err)
	}

	final := filepath.Join(localTsDir, fmt.Sprintf("%s-%s.dump", app.Name, db))
	partial := final + ".partial"

	if b.DryRun {
		fmt.Fprintf(b.Stdout, "[dry-run] kubectl exec -n %s %s -- sh -c '<postgres env> exec pg_dump -Fc %s' > %s\n",
			app.Namespace, pod, db, final)
		return nil
	}

	f, err := os.Create(partial)
	if err != nil {
		return fmt.Errorf("creating partial dump file: %w", err)
	}

	// Build the pg_dump command, streaming stdout directly to the partial file.
	cmd := execCommand("kubectl", "exec", "-n", app.Namespace, pod, "--", "sh", "-c", pgDumpEnvScript, "--", db)
	cmd.Stdout = f
	cmd.Stderr = b.outStderr()

	if err := cmd.Start(); err != nil {
		_ = f.Close()
		return fmt.Errorf("starting pg_dump for app %q: %w", app.Name, err)
	}

	// Watch for context cancellation and kill the process if it fires.
	killDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		case <-killDone:
		}
	}()

	waitErr := cmd.Wait()
	close(killDone)

	_ = f.Close()

	if ctx.Err() != nil {
		return fmt.Errorf("pg_dump for app %q cancelled: %w", app.Name, ctx.Err())
	}
	if waitErr != nil {
		return fmt.Errorf("pg_dump for app %q failed: %w", app.Name, waitErr)
	}

	// Atomic rename: partial → final.
	if err := os.Rename(partial, final); err != nil {
		return fmt.Errorf("renaming dump file for app %q: %w", app.Name, err)
	}
	return nil
}
