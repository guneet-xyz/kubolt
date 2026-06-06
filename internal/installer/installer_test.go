package installer

import (
	"sort"
	"testing"
)

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func equalSorted(a, b []string) bool {
	a = sortedCopy(a)
	b = sortedCopy(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuildDependents_Diamond(t *testing.T) {
	t.Parallel()
	groups := [][]string{{"a"}, {"b", "c"}, {"d"}}
	reverse := map[string][]string{
		"a": {"b", "c"},
		"b": {"d"},
		"c": {"d"},
	}
	got := BuildDependents(groups, reverse)
	cases := map[string][]string{
		"a": {"b", "c", "d"},
		"b": {"d"},
		"c": {"d"},
		"d": {},
	}
	for name, want := range cases {
		if !equalSorted(got[name], want) {
			t.Errorf("dependents[%q]=%v want %v", name, got[name], want)
		}
	}
}
