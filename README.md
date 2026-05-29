# kubolt

A Go CLI that replaces shell scripts for Helm-based cluster management. Kubolt reads a `kubolt.yaml` manifest describing your apps, their dependencies, and PVC backup configuration, then orchestrates Helm operations with dependency-aware ordering and interrupt-safe backups.

## Installation

### One-liner

```sh
curl -sSL https://raw.githubusercontent.com/guneet-xyz/kubolt/main/install.sh | sh
```

The installer downloads the matching release binary, verifies its SHA-256 checksum, and drops it into `$KUBOLT_INSTALL_DIR`.

Installer environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `KUBOLT_VERSION` | Release tag to install | `latest` |
| `KUBOLT_INSTALL_DIR` | Destination directory | `$HOME/.local/bin` |
| `KUBOLT_INSECURE_SKIP_CHECKSUM` | Skip checksum verification when set to `1` | `0` |

### Manual download

Grab the archive for your OS/arch from the [releases page](https://github.com/guneet-xyz/kubolt/releases), extract it, and place `kubolt` somewhere on your `PATH`.

### From source

```sh
go install github.com/guneet-xyz/kubolt@latest
```

## Quick start

```sh
cd <cluster-dir>
kubolt validate
kubolt list
kubolt install caddy
kubolt backup --dir ./backups walls
```

## The `kubolt.yaml` manifest

The manifest lives in your cluster directory and declares every app kubolt manages. Chart paths are resolved relative to the manifest's directory.

### Example

```yaml
apiVersion: kubolt.io/v1
apps:
  - name: cert-manager
    namespace: cert-manager
    chartPath: charts/cert-manager

  - name: caddy
    namespace: ingress
    chartPath: charts/caddy
    dependsOn:
      - cert-manager

  - name: walls
    namespace: walls
    chartPath: charts/walls
    dependsOn:
      - caddy
    backup:
      targets:
        - type: filesystem
          pvc: walls-data
        - type: filesystem
          pvc: walls-cache
        - type: pg_dump
          podSelector: "app=walls-postgres"
      scaleDeployments: true
```

### Schema reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `apiVersion` | string | yes | Must be `kubolt.io/v1`. |
| `apps` | list | yes | One entry per Helm release kubolt manages. |
| `apps[].name` | string | yes | Logical app name. Used as the Helm release name and on the CLI. |
| `apps[].namespace` | string | yes | Kubernetes namespace for the release. |
| `apps[].chartPath` | string | yes | Path to the chart directory, relative to the manifest file. |
| `apps[].dependsOn` | list of strings | no | Names of other apps that must be installed first. |
| `apps[].backup` | object | no | Backup configuration. Omit for apps with no persistent state. |
| `apps[].backup.targets` | list of objects | yes when `backup` is set | One or more backup targets. Each target has a `type` and type-specific fields. |
| `apps[].backup.targets[].type` | string | yes | `filesystem` — SSH+tar of a PVC. `pg_dump` — `kubectl exec pg_dump -Fc` inside a pod. |
| `apps[].backup.targets[].pvc` | string | required when `type: filesystem` | Name of the PVC to archive. |
| `apps[].backup.targets[].podSelector` | string | required when `type: pg_dump` | Label selector (e.g. `app=postgres`) to find the single running pod to exec into. |
| `apps[].backup.scaleDeployments` | bool | no | Scale deployments in the namespace to 0 during backup. Defaults to `true`. |

> **Migrating from `pvcs:`** — The old `backup.pvcs:` field has been removed. Replace:
> ```yaml
> backup:
>   pvcs:
>     - my-pvc
> ```
> with:
> ```yaml
> backup:
>   targets:
>     - type: filesystem
>       pvc: my-pvc
> ```

## Commands

### `install [app]`

With an app name, installs the target app along with its full dependency chain in topological order. Without an arg, installs **every app in the manifest** in dependency order. Every app is run through `helm upgrade --install`, even if already installed, so the manifest is the source of truth.

- Flags: `--dry-run` (print the helm commands without executing).
- Environment:
  - `HELM_FORCE_CONFLICTS`: when set, appends `--force-conflicts` to helm calls.
  - `HELM_TAKE_OWNERSHIP`: when set, appends `--take-ownership` to helm calls.
- Exit codes: `0` on success, non-zero on manifest, dependency, or helm failure.

### `uninstall <app>`

Runs `helm uninstall` for the target app. PVCs are never deleted. If any app that depends on the target is currently installed, the command blocks and exits non-zero.

- Flags: `--dry-run`.
- Exit codes: `0` on success, non-zero when blocked by an installed dependent or when helm fails.

### `backup [app ...]`

Backs up targets declared under `apps[].backup.targets`. Two strategies are supported:

- **`filesystem`**: scales the app's deployments to `0` (when `scaleDeployments` is true), streams the PVC contents over SSH using `tar`, copies the archive locally with `scp`, then restores replica counts. The scale-up also runs on `SIGINT`/`SIGTERM`.
- **`pg_dump`**: runs `kubectl exec <pod> -- pg_dump -Fc <db>` and streams the output to `<dir>/<timestamp>/<app>-<dbname>.dump`. No SSH, no scale-down. The database name is auto-detected from the pod's environment (`PGDATABASE`, `POSTGRES_DB`, `POSTGRESQL_DATABASE`, first non-empty wins).

With no positional args, every app with a `backup` block is processed.

Preflight checks: `kubectl` is always required. `ssh`, `scp`, and `tar` are only checked when at least one `filesystem` target is selected.

- Flags:
  - `--dir <path>`: destination directory for archives (default `./backups`).
  - `--dry-run`: print the planned operations without scaling or copying.
- Environment:
  - `KUBOLT_SSH_HOST`: SSH target used to pull PVC contents.
- Exit codes: `0` on success, non-zero on any backup or restore failure.

### `validate`

Renders every app in the manifest with `helm template`. Failures are accumulated and reported together so you see all broken charts in one run.

- Flags: `--dry-run`.
- Exit codes: `0` when all charts render, non-zero if any fail.

### `list`

Lists every app in the manifest alongside its Helm release status (installed, missing, failed). Takes no flags.

- Exit codes: `0` on success, non-zero on manifest or Helm query failure.

### `version`

Prints `kubolt <version>`, where `<version>` is the value injected at build time via ldflags.

- Exit codes: `0`.

### `upgrade`

Self-updates the kubolt binary from GitHub Releases. The downloaded archive's checksum is verified against the release's `checksums.txt` before the binary is swapped in place.

- Flags:
  - `--require-signature`: also verify the cosign signature on the checksum file before installing.
  - `--dry-run`: show what would be downloaded without modifying the binary.
- Exit codes: `0` on success, non-zero on download, checksum, or signature failure.

## Environment variables

| Variable | Description | Default |
|----------|-------------|---------|
| `KUBOLT_SSH_HOST` | SSH target used by `backup` to read PVC contents. | unset |
| `OBSCURO_PASSWORD` | Password forwarded to charts that consume the `obscuro` secret during install. | unset |
| `HELM_FORCE_CONFLICTS` | When set (non-empty), appends `--force-conflicts` to helm install calls. | unset |
| `HELM_TAKE_OWNERSHIP` | When set (non-empty), appends `--take-ownership` to helm install calls. | unset |

## Behavior notes

- `install` always walks the full dependency chain and runs `helm upgrade --install` on every app in it. There is no "skip if already installed" shortcut, so the manifest stays authoritative.
- `uninstall` never deletes PersistentVolumeClaims and blocks when another installed app depends on the target. Remove dependents first.
- `backup` scales deployments to `0` before copying PVC contents and restores the original replica counts even when the process receives `SIGINT` or `SIGTERM`. An interrupted backup will not leave your workloads down.

## Development

```sh
git clone https://github.com/guneet-xyz/kubolt
cd kubolt
go test ./...
go build -o kubolt .
```

## Releases

Releases are tagged by `go-semantic-release` from Conventional Commits on `main`, built by `release.yml`, and signed with cosign in keyless mode.

Verify a release archive before installing manually:

```sh
cosign verify-blob \
  --certificate kubolt_<version>_<os>_<arch>.tar.gz.pem \
  --signature  kubolt_<version>_<os>_<arch>.tar.gz.sig \
  --certificate-identity-regexp 'https://github.com/guneet-xyz/kubolt/.github/workflows/release\.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  kubolt_<version>_<os>_<arch>.tar.gz
```
