package depgraph

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// ErrUnknownDep is returned when a dependency references a node not in the input map.
var ErrUnknownDep = errors.New("unknown dependency")

// ErrCycle is returned when a dependency cycle is detected.
var ErrCycle = errors.New("cycle detected")

// TopoSort returns nodes in topological order (dependencies first).
// Input: adjacency map where key=node name, value=list of its dependencies.
// Returns error if a cycle exists or a dependency references an unknown node.
func TopoSort(nodes map[string][]string) ([]string, error) {
	// Handle empty input
	if len(nodes) == 0 {
		return []string{}, nil
	}

	// Validate all deps reference known nodes
	for node, deps := range nodes {
		_ = node
		for _, dep := range deps {
			if _, ok := nodes[dep]; !ok {
				return nil, fmt.Errorf("%w: %q", ErrUnknownDep, dep)
			}
		}
	}

	// Kahn's algorithm
	// In our model: nodes[node] = deps of node (things that must come BEFORE node)
	// So edges go: dep -> node (dep must be installed before node)
	// In-degree of node = number of its dependencies

	inDegree := make(map[string]int)
	queue := []string{}

	for node, deps := range nodes {
		inDegree[node] = len(deps)
		if len(deps) == 0 {
			queue = append(queue, node)
		}
	}
	sort.Strings(queue) // deterministic order

	result := make([]string, 0, len(nodes))
	for len(queue) > 0 {
		// pop first (maintain sorted order for determinism)
		sort.Strings(queue)
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		// find all nodes that depend on this node
		for other, deps := range nodes {
			if slices.Contains(deps, node) {
				inDegree[other]--
				if inDegree[other] == 0 {
					queue = append(queue, other)
				}
			}
		}
	}

	if len(result) != len(nodes) {
		// Cycle exists — find it
		cycle := findCycle(nodes)
		return nil, fmt.Errorf("%w: %s", ErrCycle, cycle)
	}

	return result, nil
}

// findCycle finds and formats a cycle path like "a → b → a"
func findCycle(nodes map[string][]string) string {
	visited := make(map[string]bool)
	path := make(map[string]bool)
	var pathSlice []string

	var dfs func(node string) []string
	dfs = func(node string) []string {
		visited[node] = true
		path[node] = true
		pathSlice = append(pathSlice, node)

		for _, dep := range nodes[node] {
			if !visited[dep] {
				if cycle := dfs(dep); cycle != nil {
					return cycle
				}
			} else if path[dep] {
				// Found cycle — extract it
				start := -1
				for i, n := range pathSlice {
					if n == dep {
						start = i
						break
					}
				}
				cycle := append(pathSlice[start:], dep)
				parts := make([]string, len(cycle))
				copy(parts, cycle)
				return parts
			}
		}

		path[node] = false
		pathSlice = pathSlice[:len(pathSlice)-1]
		return nil
	}

	// Sort for determinism
	nodeNames := make([]string, 0, len(nodes))
	for n := range nodes {
		nodeNames = append(nodeNames, n)
	}
	sort.Strings(nodeNames)

	for _, node := range nodeNames {
		if !visited[node] {
			if cycle := dfs(node); cycle != nil {
				return strings.Join(cycle, " → ")
			}
		}
	}
	return "unknown cycle"
}

// Waves returns nodes grouped by dependency level (wave).
// Each wave contains nodes that can be processed in parallel at that stage.
// Input: adjacency map where key=node name, value=list of its dependencies.
// Returns error if a cycle exists or a dependency references an unknown node.
func Waves(nodes map[string][]string) ([][]string, error) {
	// Handle empty input
	if len(nodes) == 0 {
		return [][]string{}, nil
	}

	// Validate all deps reference known nodes
	for node, deps := range nodes {
		_ = node
		for _, dep := range deps {
			if _, ok := nodes[dep]; !ok {
				return nil, fmt.Errorf("%w: %q", ErrUnknownDep, dep)
			}
		}
	}

	// Kahn's algorithm, collecting nodes by wave
	// In-degree of node = number of its dependencies
	inDegree := make(map[string]int)
	queue := []string{}

	for node, deps := range nodes {
		inDegree[node] = len(deps)
		if len(deps) == 0 {
			queue = append(queue, node)
		}
	}
	sort.Strings(queue) // deterministic order

	waves := [][]string{}
	totalProcessed := 0

	for len(queue) > 0 {
		// Current wave is the entire queue (all nodes with in-degree 0 at this stage)
		sort.Strings(queue)
		currentWave := make([]string, len(queue))
		copy(currentWave, queue)
		waves = append(waves, currentWave)
		totalProcessed += len(queue)

		// Process all nodes in current wave: update in-degrees of dependents
		nextQueue := []string{}
		for _, node := range currentWave {
			// Find all nodes that depend on this node
			for other, deps := range nodes {
				if slices.Contains(deps, node) {
					inDegree[other]--
					if inDegree[other] == 0 {
						nextQueue = append(nextQueue, other)
					}
				}
			}
		}

		queue = nextQueue
	}

	if totalProcessed != len(nodes) {
		// Cycle exists
		cycle := findCycle(nodes)
		return nil, fmt.Errorf("%w: %s", ErrCycle, cycle)
	}

	return waves, nil
}
