package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/manifest"
)

// Select resolves the set of packages to plan.
//
// profile and packages are mutually exclusive: setting both is an error.
// Setting neither selects every package. The chosen roots are expanded to
// include their needs closure, then returned in topological order (needs
// before needers) with alphabetical tie-breaking. Unknown profile or package
// names are errors.
func Select(m *manifest.Manifest, profile string, packages []string) ([]*manifest.Package, error) {
	if profile != "" && len(packages) > 0 {
		return nil, fmt.Errorf("use --profile or --packages, not both")
	}

	var roots []string
	switch {
	case profile != "":
		pr, ok := m.Profiles[profile]
		if !ok {
			return nil, fmt.Errorf("unknown profile %q", profile)
		}
		roots = pr.Packages
	case len(packages) > 0:
		roots = packages
	default:
		for name := range m.Packages {
			roots = append(roots, name)
		}
	}

	for _, r := range roots {
		if _, ok := m.Packages[r]; !ok {
			return nil, fmt.Errorf("unknown package %q", r)
		}
	}

	// Expand needs closure.
	inSet := map[string]bool{}
	var stack []string
	stack = append(stack, roots...)
	for len(stack) > 0 {
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if inSet[name] {
			continue
		}
		inSet[name] = true
		for _, need := range m.Packages[name].Needs {
			if _, ok := m.Packages[need]; !ok {
				return nil, fmt.Errorf("unknown package %q", need)
			}
			if !inSet[need] {
				stack = append(stack, need)
			}
		}
	}

	return topoSort(m, inSet)
}

// topoSort orders the selected set so each package follows all of its needs,
// breaking ties alphabetically. Kahn's algorithm with a sorted ready set.
// Returns an error if the dependency graph contains a cycle.
func topoSort(m *manifest.Manifest, inSet map[string]bool) ([]*manifest.Package, error) {
	indeg := map[string]int{}
	for name := range inSet {
		for _, need := range m.Packages[name].Needs {
			if inSet[need] {
				indeg[name]++
			}
		}
	}

	placed := map[string]bool{}
	var order []*manifest.Package
	for len(order) < len(inSet) {
		// Collect ready nodes (indegree 0, not yet placed), sorted.
		var ready []string
		for name := range inSet {
			if !placed[name] && indeg[name] == 0 {
				ready = append(ready, name)
			}
		}
		sort.Strings(ready)
		if len(ready) == 0 {
			var remaining []string
			for name := range inSet {
				if !placed[name] {
					remaining = append(remaining, name)
				}
			}
			sort.Strings(remaining)
			return nil, fmt.Errorf("dependency cycle among: %s", strings.Join(remaining, ", "))
		}
		next := ready[0]
		placed[next] = true
		order = append(order, m.Packages[next])
		// Decrement indegree of packages that need `next`.
		for name := range inSet {
			if placed[name] {
				continue
			}
			for _, need := range m.Packages[name].Needs {
				if need == next {
					indeg[name]--
				}
			}
		}
	}
	return order, nil
}
