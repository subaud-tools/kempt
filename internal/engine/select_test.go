package engine

import (
	"testing"

	"github.com/subaud-tools/kempt/internal/manifest"
)

func parse(t *testing.T, src string) *manifest.Manifest {
	t.Helper()
	m, findings := manifest.Parse([]byte(src))
	if len(findings) > 0 {
		t.Fatalf("parse findings: %v", findings)
	}
	return m
}

func names(pkgs []*manifest.Package) []string {
	out := make([]string, len(pkgs))
	for i, p := range pkgs {
		out[i] = p.Name
	}
	return out
}

func eq(a, b []string) bool {
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

const twoPkg = `
[kempt]
spec = 1
[packages.a]
description = "a"
[packages.b]
description = "b"
needs = ["a"]
`

func TestSelectTopoOrder(t *testing.T) {
	m := parse(t, twoPkg)
	got, err := Select(m, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(got), []string{"a", "b"}) {
		t.Fatalf("order = %v, want [a b]", names(got))
	}
}

func TestSelectClosurePullsNeed(t *testing.T) {
	m := parse(t, twoPkg)
	got, err := Select(m, "", []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(got), []string{"a", "b"}) {
		t.Fatalf("closure = %v, want [a b]", names(got))
	}
}

func TestSelectBothFlagsError(t *testing.T) {
	m := parse(t, twoPkg)
	if _, err := Select(m, "dev", []string{"b"}); err == nil {
		t.Fatal("want error for both profile and packages")
	}
}

func TestSelectUnknownPackageError(t *testing.T) {
	m := parse(t, twoPkg)
	if _, err := Select(m, "", []string{"nope"}); err == nil {
		t.Fatal("want error for unknown package")
	}
}

func TestSelectUnknownProfileError(t *testing.T) {
	m := parse(t, twoPkg)
	if _, err := Select(m, "ghost", nil); err == nil {
		t.Fatal("want error for unknown profile")
	}
}

func TestSelectCyclicDependencyError(t *testing.T) {
	// Parse WITHOUT Validate so the cycle is not caught before Select.
	m, _ := manifest.Parse([]byte(`
[kempt]
spec = 1
[packages.a]
description = "a"
needs = ["b"]
[packages.b]
description = "b"
needs = ["a"]
`))
	_, err := Select(m, "", nil)
	if err == nil {
		t.Fatal("want error for cyclic dependency, got nil")
	}
}

func TestSelectAlphabeticalTieBreak(t *testing.T) {
	m := parse(t, `
[kempt]
spec = 1
[packages.z]
description = "z"
[packages.a]
description = "a"
[packages.m]
description = "m"
`)
	got, err := Select(m, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !eq(names(got), []string{"a", "m", "z"}) {
		t.Fatalf("order = %v, want [a m z]", names(got))
	}
}
