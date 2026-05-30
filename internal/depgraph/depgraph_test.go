package depgraph

import (
	"errors"
	"testing"
)

func TestTopoSort_SingleNode(t *testing.T) {
	nodes := map[string][]string{
		"a": {},
	}
	result, err := TopoSort(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0] != "a" {
		t.Errorf("expected [a], got %v", result)
	}
}

func TestTopoSort_LinearChain(t *testing.T) {
	nodes := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {},
	}
	result, err := TopoSort(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(result))
	}
	// c must come before b, b must come before a
	if result[0] != "c" || result[1] != "b" || result[2] != "a" {
		t.Errorf("expected [c b a], got %v", result)
	}
}

func TestTopoSort_Diamond(t *testing.T) {
	nodes := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
		"d": {},
	}
	result, err := TopoSort(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(result))
	}
	// d must be first, a must be last
	if result[0] != "d" {
		t.Errorf("expected d first, got %v", result)
	}
	if result[3] != "a" {
		t.Errorf("expected a last, got %v", result)
	}
	// b and c must come before a
	bIdx, cIdx := -1, -1
	for i, n := range result {
		if n == "b" {
			bIdx = i
		}
		if n == "c" {
			cIdx = i
		}
	}
	if bIdx >= 3 || cIdx >= 3 {
		t.Errorf("b and c must come before a, got %v", result)
	}
}

func TestTopoSort_SelfLoop(t *testing.T) {
	nodes := map[string][]string{
		"a": {"a"},
	}
	result, err := TopoSort(nodes)
	if err == nil {
		t.Fatal("expected error for self-loop, got nil")
	}
	if !errors.Is(err, ErrCycle) {
		t.Errorf("expected ErrCycle, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on error, got %v", result)
	}
	errMsg := err.Error()
	if errMsg != "cycle detected: a → a" {
		t.Errorf("expected 'cycle detected: a → a', got %q", errMsg)
	}
}

func TestTopoSort_TwoNodeCycle(t *testing.T) {
	nodes := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	result, err := TopoSort(nodes)
	if err == nil {
		t.Fatal("expected error for cycle, got nil")
	}
	if !errors.Is(err, ErrCycle) {
		t.Errorf("expected ErrCycle, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on error, got %v", result)
	}
	errMsg := err.Error()
	// Should contain both a and b in the cycle message
	if !contains(errMsg, "a") || !contains(errMsg, "b") {
		t.Errorf("expected cycle message to contain both a and b, got %q", errMsg)
	}
}

func TestTopoSort_UnknownDep(t *testing.T) {
	nodes := map[string][]string{
		"a": {"b"},
	}
	result, err := TopoSort(nodes)
	if err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
	if !errors.Is(err, ErrUnknownDep) {
		t.Errorf("expected ErrUnknownDep, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on error, got %v", result)
	}
}

func TestTopoSort_EmptyInput(t *testing.T) {
	nodes := map[string][]string{}
	result, err := TopoSort(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %v", result)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestWaves_LinearChain(t *testing.T) {
	nodes := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {},
	}
	waves, err := Waves(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(waves))
	}
	// Wave 0: [c], Wave 1: [b], Wave 2: [a]
	if len(waves[0]) != 1 || waves[0][0] != "c" {
		t.Errorf("wave 0: expected [c], got %v", waves[0])
	}
	if len(waves[1]) != 1 || waves[1][0] != "b" {
		t.Errorf("wave 1: expected [b], got %v", waves[1])
	}
	if len(waves[2]) != 1 || waves[2][0] != "a" {
		t.Errorf("wave 2: expected [a], got %v", waves[2])
	}
}

func TestWaves_Diamond(t *testing.T) {
	nodes := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
		"d": {},
	}
	waves, err := Waves(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 3 {
		t.Fatalf("expected 3 waves, got %d", len(waves))
	}
	// Wave 0: [d], Wave 1: [b, c], Wave 2: [a]
	if len(waves[0]) != 1 || waves[0][0] != "d" {
		t.Errorf("wave 0: expected [d], got %v", waves[0])
	}
	if len(waves[1]) != 2 || waves[1][0] != "b" || waves[1][1] != "c" {
		t.Errorf("wave 1: expected [b c], got %v", waves[1])
	}
	if len(waves[2]) != 1 || waves[2][0] != "a" {
		t.Errorf("wave 2: expected [a], got %v", waves[2])
	}
}

func TestWaves_DisconnectedRoots(t *testing.T) {
	nodes := map[string][]string{
		"a": {},
		"b": {},
		"c": {"a"},
	}
	waves, err := Waves(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(waves))
	}
	// Wave 0: [a, b], Wave 1: [c]
	if len(waves[0]) != 2 {
		t.Fatalf("wave 0: expected 2 nodes, got %d", len(waves[0]))
	}
	if waves[0][0] != "a" || waves[0][1] != "b" {
		t.Errorf("wave 0: expected [a b], got %v", waves[0])
	}
	if len(waves[1]) != 1 || waves[1][0] != "c" {
		t.Errorf("wave 1: expected [c], got %v", waves[1])
	}
}

func TestWaves_SingleNode(t *testing.T) {
	nodes := map[string][]string{
		"x": {},
	}
	waves, err := Waves(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 1 {
		t.Fatalf("expected 1 wave, got %d", len(waves))
	}
	if len(waves[0]) != 1 || waves[0][0] != "x" {
		t.Errorf("expected [x], got %v", waves[0])
	}
}

func TestWaves_EmptyGraph(t *testing.T) {
	nodes := map[string][]string{}
	waves, err := Waves(nodes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(waves) != 0 {
		t.Errorf("expected 0 waves, got %d", len(waves))
	}
}

func TestWaves_CycleSelfLoop(t *testing.T) {
	nodes := map[string][]string{
		"a": {"a"},
	}
	waves, err := Waves(nodes)
	if err == nil {
		t.Fatal("expected error for self-loop, got nil")
	}
	if !errors.Is(err, ErrCycle) {
		t.Errorf("expected ErrCycle, got %v", err)
	}
	if waves != nil {
		t.Errorf("expected nil waves on error, got %v", waves)
	}
}

func TestWaves_CycleTwoNodes(t *testing.T) {
	nodes := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	waves, err := Waves(nodes)
	if err == nil {
		t.Fatal("expected error for cycle, got nil")
	}
	if !errors.Is(err, ErrCycle) {
		t.Errorf("expected ErrCycle, got %v", err)
	}
	if waves != nil {
		t.Errorf("expected nil waves on error, got %v", waves)
	}
}

func TestWaves_UnknownDep(t *testing.T) {
	nodes := map[string][]string{
		"a": {"b"},
	}
	waves, err := Waves(nodes)
	if err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
	if !errors.Is(err, ErrUnknownDep) {
		t.Errorf("expected ErrUnknownDep, got %v", err)
	}
	if waves != nil {
		t.Errorf("expected nil waves on error, got %v", waves)
	}
}
