// Package installer provides a wave-based parallel executor for app install
// jobs. It supports best-effort failure handling: when an app fails, all of
// its transitive dependents are skipped, but unrelated apps continue.
package installer

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/guneet-xyz/kubolt/internal/output"
)

// AppJob describes a single app install job.
type AppJob struct {
	Name string
	// Run executes the actual install. stdout/stderr receive the app's output.
	Run func(ctx context.Context, stdout, stderr io.Writer) error
}

// Plan describes a full install plan.
type Plan struct {
	// Waves is the wave-ordered list of app names (from depgraph.Waves()).
	Waves [][]string
	// Jobs maps app name → job.
	Jobs map[string]AppJob
	// Dependents maps an app name to all transitive apps that depend on it.
	// If app A is in this map and A fails, every name in Dependents[A] is
	// skipped.
	Dependents map[string][]string
}

// Executor runs a Plan.
type Executor struct {
	// Parallelism caps in-flight jobs per wave. Values <=1 run sequentially.
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

	var (
		mu        sync.Mutex
		skipSet   = make(map[string]bool)
		started   = make(map[string]bool)
		succeeded []string
		failed    []string
		skipped   []string
	)

	addSkipsLocked := func(names []string) {
		for _, n := range names {
			skipSet[n] = true
		}
	}

waves:
	for waveIdx, wave := range p.Waves {
		if ctxErr := ctx.Err(); ctxErr != nil {
			break waves
		}

		sink.Emit(output.Event{Kind: output.WaveStart, Wave: waveIdx})

		// Snapshot skip status & build list of runnable jobs.
		mu.Lock()
		runnable := make([]string, 0, len(wave))
		for _, name := range wave {
			if skipSet[name] {
				skipped = append(skipped, name)
				continue
			}
			runnable = append(runnable, name)
			started[name] = true
		}
		mu.Unlock()

		// Emit skips for apps in this wave that were already in skipSet.
		for _, name := range wave {
			mu.Lock()
			isSkip := skipSet[name] && !started[name]
			mu.Unlock()
			if isSkip {
				sink.Emit(output.Event{Kind: output.AppSkip, Wave: waveIdx, App: name, Reason: "dependency failed"})
			}
		}

		runOne := func(name string) {
			job, ok := p.Jobs[name]
			if !ok {
				mu.Lock()
				failed = append(failed, name)
				addSkipsLocked(p.Dependents[name])
				mu.Unlock()
				sink.Emit(output.Event{Kind: output.AppDone, Wave: waveIdx, App: name, Err: errMissing(name)})
				return
			}

			sink.Emit(output.Event{Kind: output.AppStart, Wave: waveIdx, App: name})

			stdout := newLineWriter(sink, waveIdx, name, "stdout")
			stderr := newLineWriter(sink, waveIdx, name, "stderr")

			err := job.Run(ctx, stdout, stderr)
			stdout.Flush()
			stderr.Flush()

			sink.Emit(output.Event{Kind: output.AppDone, Wave: waveIdx, App: name, Err: err})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, name)
				addSkipsLocked(p.Dependents[name])
			} else {
				succeeded = append(succeeded, name)
			}
		}

		if e.Parallelism <= 1 {
			for _, name := range runnable {
				runOne(name)
			}
		} else {
			sem := make(chan struct{}, e.Parallelism)
			var wg sync.WaitGroup
			for _, name := range runnable {
				name := name
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					runOne(name)
				}()
			}
			wg.Wait()
		}

		sink.Emit(output.Event{Kind: output.WaveEnd, Wave: waveIdx})
	}

	// Any apps never started but in skipSet get reported as skipped.
	mu.Lock()
	seenSkipped := make(map[string]bool, len(skipped))
	for _, n := range skipped {
		seenSkipped[n] = true
	}
	for _, wave := range p.Waves {
		for _, name := range wave {
			if !started[name] && skipSet[name] && !seenSkipped[name] {
				skipped = append(skipped, name)
				seenSkipped[name] = true
			}
		}
	}
	mu.Unlock()

	res := Result{Succeeded: succeeded, Failed: failed, Skipped: skipped}

	sink.Emit(output.Event{Kind: output.AllDone})

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
// waves is only used to enumerate the set of all apps.
func BuildDependents(waves [][]string, reverseDeps map[string][]string) map[string][]string {
	all := []string{}
	for _, w := range waves {
		all = append(all, w...)
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

// errMissing reports a missing job for a wave entry.
type missingJobError struct{ name string }

func (m *missingJobError) Error() string { return "installer: no job for app " + m.name }

func errMissing(name string) error { return &missingJobError{name: name} }

// lineWriter buffers writes and emits one AppLine event per line.
type lineWriter struct {
	sink   output.Sink
	wave   int
	app    string
	stream string

	mu  sync.Mutex
	buf bytes.Buffer
}

func newLineWriter(sink output.Sink, wave int, app, stream string) *lineWriter {
	return &lineWriter{sink: sink, wave: wave, app: app, stream: stream}
}

func (w *lineWriter) Write(p []byte) (int, error) {
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
		// advance past the newline
		next := make([]byte, len(data)-idx-1)
		copy(next, data[idx+1:])
		w.buf.Reset()
		w.buf.Write(next)
		w.sink.Emit(output.Event{
			Kind:   output.AppLine,
			Wave:   w.wave,
			App:    w.app,
			Stream: w.stream,
			Text:   line,
		})
	}
	return len(p), nil
}

// Flush emits any buffered partial line.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		return
	}
	w.sink.Emit(output.Event{
		Kind:   output.AppLine,
		Wave:   w.wave,
		App:    w.app,
		Stream: w.stream,
		Text:   w.buf.String(),
	})
	w.buf.Reset()
}
