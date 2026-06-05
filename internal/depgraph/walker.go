package depgraph

import "context"

// Ready signals that a node's dependencies have all completed and it is
// available for processing. DepFailures lists names of upstream deps that
// failed, so the consumer can skip or surface the error.
type Ready struct {
	Name        string
	DepFailures []string
}

// Walker streams Ready nodes from a dependency graph as deps complete.
// Consumers report progress via Done. Streaming logic lands in Task 6.
type Walker struct {
	nodes map[string][]string
}

// NewWalker validates the graph (unknown deps, cycles) and returns a Walker.
// Errors wrap ErrUnknownDep or ErrCycle and are errors.Is-compatible.
func NewWalker(nodes map[string][]string) (*Walker, error) {
	if _, err := TopoSort(nodes); err != nil {
		return nil, err
	}
	return &Walker{nodes: nodes}, nil
}

// Walk returns a channel that emits Ready values as nodes unblock.
// NOT YET IMPLEMENTED — see Task 6. Currently returns a closed channel.
func (w *Walker) Walk(ctx context.Context) <-chan Ready {
	_ = ctx
	ch := make(chan Ready)
	close(ch)
	return ch
}

// Done reports completion of node `name`. A non-nil err propagates as a
// DepFailure to dependents.
// NOT YET IMPLEMENTED — see Task 6. Currently a no-op.
func (w *Walker) Done(name string, err error) {
	_ = name
	_ = err
}
