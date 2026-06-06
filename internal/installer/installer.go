// Package installer provides a tree-based parallel executor for app install
// jobs. It supports best-effort failure handling: when an app fails, all of
// its transitive dependents are skipped, but unrelated apps continue.
package installer

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"

	"github.com/guneet-xyz/kubolt/internal/depgraph"
	"github.com/guneet-xyz/kubolt/internal/output"
)

// AppJob describes a single app install job.
type AppJob struct {
	Name string
	// Run executes the actual install. stdout/stderr receive the app's output.
	Run func(ctx context.Context, stdout, stderr io.Writer) error
}

// Plan describes a tree-based install plan.
type Plan struct {
	// Nodes maps an app name to its direct dependencies (e.g. "app-a" → ["app-b", "app-c"])
	Nodes map[string][]string
	// Jobs maps app name → job.
	Jobs map[string]AppJob
	// Dependents maps an app name to all transitive apps that depend on it.
	Dependents map[string][]string
}

// Executor runs a Plan.
type Executor struct {
	// Parallelism caps in-flight jobs. Values <=1 run sequentially.
	Parallelism int
	// Sink receives progress events. If nil, events are discarded.
	Sink output.Sink
}

// Result summarises the outcome of a Run.
type Result struct {
	Succeeded []string
	Failed    []string
	Skipped   []string
}

// runError is returned by Run when at least one job failed.
type runError struct{ msg string }

func (e *runError) Error() string { return e.msg }

// Run executes the plan and returns a Result. The returned error is non-nil
// if at least one job failed or the context was cancelled.
func (e *Executor) Run(ctx context.Context, p Plan) (Result, error) {
	sink := e.Sink
	if sink == nil {
		sink = output.NopSink{}
	}

	sink.Emit(output.Event{Kind: output.TreeStart, Count: len(p.Nodes)})

	w, err := depgraph.NewWalker(p.Nodes)
	if err != nil {
		sink.Emit(output.Event{Kind: output.TreeDone})
		return Result{}, err
	}

	ch := w.Walk(ctx)

	semCap := max(e.Parallelism, 1)
	sem := make(chan struct{}, semCap)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded []string
		failed    []string
		skipped   []string
	)

	for r := range ch {
		parents := p.Nodes[r.Name]
		sink.Emit(output.Event{Kind: output.NodeReady, App: r.Name, Parents: parents})

		if len(r.DepFailures) > 0 {
			mu.Lock()
			skipped = append(skipped, r.Name)
			mu.Unlock()
			sink.Emit(output.Event{
				Kind:    output.NodeSkip,
				App:     r.Name,
				Parents: parents,
				Reason:  "dependency failed: " + strings.Join(r.DepFailures, ", "),
			})
			// Walker has already counted cascaded nodes; do NOT call w.Done().
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(name string, parents []string) {
			defer wg.Done()
			defer func() { <-sem }()

			job, ok := p.Jobs[name]
			if !ok {
				err := errMissing(name)
				w.Done(name, err)
				mu.Lock()
				failed = append(failed, name)
				mu.Unlock()
				sink.Emit(output.Event{Kind: output.NodeDone, App: name, Parents: parents, Err: err})
				return
			}

			sink.Emit(output.Event{Kind: output.NodeStart, App: name, Parents: parents})

			stdout := newNodeLineWriter(sink, name, parents, "stdout")
			stderr := newNodeLineWriter(sink, name, parents, "stderr")

			runErr := job.Run(ctx, stdout, stderr)
			stdout.Flush()
			stderr.Flush()

			w.Done(name, runErr)

			mu.Lock()
			if runErr != nil {
				failed = append(failed, name)
			} else {
				succeeded = append(succeeded, name)
			}
			mu.Unlock()

			sink.Emit(output.Event{Kind: output.NodeDone, App: name, Parents: parents, Err: runErr})
		}(r.Name, parents)
	}

	wg.Wait()

	sink.Emit(output.Event{Kind: output.TreeDone})

	res := Result{Succeeded: succeeded, Failed: failed, Skipped: skipped}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, ctxErr
	}
	if len(failed) > 0 {
		return res, &runError{msg: "one or more apps failed"}
	}
	return res, nil
}

// BuildDependents computes the transitive closure of dependents.
// reverseDeps maps an app to the immediate apps that depend on it
// (i.e. apps that list it in their DependsOn). The returned map maps each
// app to ALL transitive apps that depend on it.
//
// groups is only used to enumerate the set of all apps.
func BuildDependents(groups [][]string, reverseDeps map[string][]string) map[string][]string {
	all := []string{}
	for _, g := range groups {
		all = append(all, g...)
	}

	out := make(map[string][]string, len(all))
	for _, name := range all {
		seen := make(map[string]bool)
		var stack []string
		stack = append(stack, reverseDeps[name]...)
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[n] {
				continue
			}
			seen[n] = true
			stack = append(stack, reverseDeps[n]...)
		}
		deps := make([]string, 0, len(seen))
		for k := range seen {
			deps = append(deps, k)
		}
		out[name] = deps
	}
	return out
}

// missingJobError reports a missing job for a plan entry.
type missingJobError struct{ name string }

func (m *missingJobError) Error() string { return "installer: no job for app " + m.name }

func errMissing(name string) error { return &missingJobError{name: name} }

// nodeLineWriter buffers writes and emits NodeLine events (tree vocabulary).
type nodeLineWriter struct {
	sink    output.Sink
	app     string
	parents []string
	stream  string

	mu  sync.Mutex
	buf bytes.Buffer
}

func newNodeLineWriter(sink output.Sink, app string, parents []string, stream string) *nodeLineWriter {
	return &nodeLineWriter{sink: sink, app: app, parents: parents, stream: stream}
}

func (w *nodeLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := string(data[:idx])
		next := make([]byte, len(data)-idx-1)
		copy(next, data[idx+1:])
		w.buf.Reset()
		w.buf.Write(next)
		w.sink.Emit(output.Event{
			Kind:    output.NodeLine,
			App:     w.app,
			Parents: w.parents,
			Stream:  w.stream,
			Text:    line,
		})
	}
	return len(p), nil
}

// Flush emits any buffered partial line.
func (w *nodeLineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		return
	}
	w.sink.Emit(output.Event{
		Kind:    output.NodeLine,
		App:     w.app,
		Parents: w.parents,
		Stream:  w.stream,
		Text:    w.buf.String(),
	})
	w.buf.Reset()
}
