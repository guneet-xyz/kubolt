package depgraph

import (
	"context"
	"sort"
	"sync"
)

// Ready signals that a node's dependencies have all completed and it is
// available for processing. DepFailures lists names of upstream deps that
// failed (direct deps in the failed set), so the consumer can skip or surface
// the error. DepFailures is nil for successful nodes.
type Ready struct {
	Name        string
	DepFailures []string
}

// Walker streams Ready nodes from a dependency graph as deps complete.
// Consumers receive Ready values from the channel returned by Walk and
// report progress (success or failure) back via Done.
//
// Concurrency model:
//   - One internal dispatcher goroutine owns the exposed readyCh and is the
//     sole writer/closer.
//   - Done may be called from any goroutine; state mutations are serialized
//     by mu, while channel sends happen outside the lock against an internal
//     buffered channel (pendingCh) sized to the node count so they never
//     block.
//   - Cancelling the ctx passed to Walk causes the dispatcher to exit and
//     close readyCh.
type Walker struct {
	mu         sync.Mutex
	nodes      map[string][]string
	inDegree   map[string]int
	dependents map[string][]string
	failed     map[string]bool
	done       map[string]bool
	completed  int
	total      int

	pendingCh chan Ready
	readyCh   chan Ready

	walkOnce sync.Once
}

// NewWalker validates the graph (unknown deps, cycles) and returns a Walker.
// Errors wrap ErrUnknownDep or ErrCycle and are errors.Is-compatible.
//
// Initial zero-in-degree nodes are seeded into the internal pending queue
// immediately so they are emitted as soon as Walk is called.
func NewWalker(nodes map[string][]string) (*Walker, error) {
	if _, err := TopoSort(nodes); err != nil {
		return nil, err
	}

	total := len(nodes)
	// Buffer sized to total so pendingCh sends never block: every node is
	// emitted at most once, so the buffer can hold every emission. +1 keeps
	// total=0 from creating an unbuffered channel (harmless either way).
	bufSize := total + 1

	w := &Walker{
		nodes:      nodes,
		inDegree:   make(map[string]int, total),
		dependents: make(map[string][]string, total),
		failed:     make(map[string]bool),
		done:       make(map[string]bool),
		total:      total,
		pendingCh:  make(chan Ready, bufSize),
		readyCh:    make(chan Ready, bufSize),
	}

	for node, deps := range nodes {
		w.inDegree[node] = len(deps)
		for _, dep := range deps {
			w.dependents[dep] = append(w.dependents[dep], node)
		}
	}
	// Sort dependent lists for deterministic emit order.
	for k := range w.dependents {
		sort.Strings(w.dependents[k])
	}

	// Seed initial zero-in-degree nodes (sorted for determinism).
	var initial []string
	for node, deg := range w.inDegree {
		if deg == 0 {
			initial = append(initial, node)
		}
	}
	sort.Strings(initial)
	for _, n := range initial {
		w.pendingCh <- Ready{Name: n}
	}

	return w, nil
}

// Walk returns a channel that emits Ready values as nodes unblock. The
// channel is closed when every node has been emitted (success or cascaded
// failure) or when ctx is cancelled.
//
// Walk should be called once per Walker. A second call returns the same
// channel; the ctx from the first call is the one that controls shutdown.
func (w *Walker) Walk(ctx context.Context) <-chan Ready {
	w.walkOnce.Do(func() {
		go w.dispatch(ctx)
	})
	return w.readyCh
}

// dispatch is the sole owner/closer of readyCh. It forwards items from
// pendingCh to readyCh until either every node has been forwarded or ctx
// is cancelled.
func (w *Walker) dispatch(ctx context.Context) {
	defer close(w.readyCh)
	for forwarded := 0; forwarded < w.total; {
		select {
		case <-ctx.Done():
			return
		case r := <-w.pendingCh:
			select {
			case <-ctx.Done():
				return
			case w.readyCh <- r:
				forwarded++
			}
		}
	}
}

// Done reports completion of node `name`. A non-nil err propagates as a
// DepFailure to every transitive dependent of `name`, each emitted on the
// Ready channel with DepFailures listing its direct deps that failed.
//
// Done is idempotent (subsequent calls for the same name are no-ops) and
// safe to call from multiple goroutines concurrently. Calls for unknown
// names are silently ignored.
func (w *Walker) Done(name string, err error) {
	w.mu.Lock()

	if _, ok := w.nodes[name]; !ok {
		w.mu.Unlock()
		return
	}
	if w.done[name] {
		w.mu.Unlock()
		return
	}

	w.done[name] = true
	w.completed++

	var toEmit []Ready

	if err != nil {
		w.failed[name] = true
		// BFS through dependents, marking each as failed and emitting Ready
		// with DepFailures listing the direct deps in the failed set.
		queue := append([]string(nil), w.dependents[name]...)
		for len(queue) > 0 {
			d := queue[0]
			queue = queue[1:]
			if w.done[d] {
				continue
			}
			w.done[d] = true
			w.failed[d] = true
			w.completed++

			var depFails []string
			for _, dep := range w.nodes[d] {
				if w.failed[dep] {
					depFails = append(depFails, dep)
				}
			}
			sort.Strings(depFails)

			toEmit = append(toEmit, Ready{Name: d, DepFailures: depFails})
			queue = append(queue, w.dependents[d]...)
		}
	} else {
		// Success: decrement each direct dependent's remaining-dep counter
		// and emit any that now have zero remaining (and aren't failed).
		for _, d := range w.dependents[name] {
			if w.done[d] {
				continue
			}
			w.inDegree[d]--
			if w.inDegree[d] == 0 && !w.failed[d] {
				toEmit = append(toEmit, Ready{Name: d})
			}
		}
	}

	w.mu.Unlock()

	// Send outside the lock. pendingCh is buffered to total so sends are
	// non-blocking; the dispatcher (single owner of readyCh) handles
	// forwarding and closing.
	for _, r := range toEmit {
		w.pendingCh <- r
	}
}
