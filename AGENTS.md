# AGENTS.md

Notes for coding agents working on the kubolt repository. Keep this file current when the architecture, testing conventions, or release process change.

## What this repo is

Kubolt is a Go CLI that drives Helm-based cluster management from a single `kubolt.yaml` manifest. It resolves an app dependency graph, runs `helm upgrade --install` in topological order, performs interrupt-safe PVC backups over SSH, and self-updates from signed GitHub releases. It replaces a pile of ad-hoc shell scripts with one statically-typed, testable binary.

## Architecture

- `cmd/`: Cobra commands. One file per command: `install`, `uninstall`, `backup`, `validate`, `list`, `version`, `upgrade`. `root.go` wires the command tree and global flags.
- `internal/manifest/`: YAML parsing for `kubolt.yaml`, schema types, and dependency-graph queries (e.g. "what depends on app X").
- `internal/depgraph/`: Exposes `TopoSort() []string` for ordered installs and a `Walker` for parallel DAG traversal. The walker releases each node as soon as its dependencies complete, using a dispatcher-goroutine pattern with a channel-based ready-set; callers consume `Ready` and report completion via `Done(name, err)`. Pure logic, no I/O.
- `internal/installer/`: Owns `Plan{Nodes, Jobs, Dependents}` and an `Executor` that walks the DAG via the depgraph walker, gated by a semaphore-based parallelism cap. `Executor.Run(ctx, plan)` emits `NodeStart` / `NodeDone` / `NodeSkip` / `TreeStart` / `TreeDone` events to a sink instead of touching stdout directly.
- `internal/output/`: Defines the `Sink` interface plus three implementations: `BubbleTeaSink` (Bubble Tea v2, tree-rendered TUI for interactive terminals), `LineSink` (plain prefixed-line output, no ANSI, safe for CI logs), and `NopSink` (tests). The `resolveOutputMode(cmd, w)` helper in `cmd/output_mode.go` selects the right sink from `--plain` / `--tui` flags and TTY detection.
- `internal/helm/`: Argument builders for helm subcommands and the `exec.Cmd` wrapper. Exposes a `SetExecCommand` test seam so unit tests can stub helm invocations.
- `internal/backup/`: Interrupt-safe PVC backup orchestrator. Owns the scale-down / copy / scale-up state machine and the signal handler that runs restore on `SIGINT`/`SIGTERM`.
- `internal/preflight/`: Pre-flight checks for required binaries (`helm`, `kubectl`, `ssh`), kube context auth, and SSH reachability.
- `internal/version/`: Holds the version string injected at build time via `-ldflags "-X .../internal/version.Version=..."`.

## Adding a new command

1. Create `cmd/<name>.go`.
2. Define a `cobra.Command` and register it in `cmd/root.go` via `rootCmd.AddCommand(...)`.
3. If the command renders progress, call `resolveOutputMode(cmd, os.Stdout)` (from `cmd/output_mode.go`) to pick the right `output.Sink`: a `BubbleTeaSink` for TTYs and a `LineSink` for plain or non-interactive output. Pass the sink into your core function. For `BubbleTeaSink`, call `sink.Run(ctx)` in a goroutine before kicking off execution and `sink.Wait()` after the executor returns.
4. Extract the command's real work into a testable core function. The `RunE` should be a thin shell that parses flags, loads the manifest, then calls the core.
5. For any external process, route through `internal/helm` (or a similar package-level `execCommand` variable) so tests can stub it with `helm.SetExecCommand`.
6. Write `cmd/<name>_test.go`. Cover the happy path, at least one failure path, and any flag interactions. Use golden files when output is structured.

## Testing conventions

- TDD: write the test first, watch it fail, make it pass.
- Every package that shells out exposes a package-level `execCommand` (or equivalent `Set*` setter) as the seam for stubbing subprocesses. Never call `exec.Command` directly from business logic.
- Golden files live in `cmd/testdata/` next to the command they exercise. Regenerate them only when output changes intentionally.
- Run the full suite with the race detector before sending a PR:

  ```sh
  go test -race ./...
  ```

- Table-driven tests are the default style. Keep cases small and named.
- When testing Bubble Tea components, drive `Update()` directly with synthetic messages (e.g., `sinkEventMsg{event: ...}`). Never golden-file `View()` output, since ANSI codes and frame timing make those goldens fragile. Assert on model state (`m.tasks`, `m.total`, etc.) after each `Update()` call.

## Release process

1. Land changes on `main` using Conventional Commits (`feat:`, `fix:`, `chore:`, `refactor:`, etc.). The commit type drives the next version.
2. `go-semantic-release` runs on `main`, computes the next semver, and pushes the tag.
3. The tag triggers `.github/workflows/release.yml`, which cross-compiles binaries, generates `checksums.txt`, and signs the checksum file with cosign in keyless mode using the workflow's OIDC identity.
4. `install.sh` and `kubolt upgrade` consume the same release assets and verify checksums (and optionally signatures) before installing.

If a release needs to be skipped, use a commit type that does not bump the version (for example `chore:` or `docs:`) or follow the `go-semantic-release` skip convention configured in the repo.
