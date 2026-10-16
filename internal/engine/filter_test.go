package engine

import (
	"testing"

	"github.com/subaud-tools/kempt/internal/manifest"
)

// filterStep is a minimal Step whose Class() is configurable for tests.
type filterStep struct {
	kind  string
	class manifest.Class
}

func (s filterStep) Kind() string          { return s.kind }
func (s filterStep) Class() manifest.Class { return s.class }

func TestFilterByClassKeepsOnlyMatchingChanges(t *testing.T) {
	orig := &Plan{Packages: []PackagePlan{
		{Name: "a", Steps: []StepResult{
			{Step: filterStep{"symlink", manifest.ClassFiles}, Delta: Delta{Op: OpChange, Detail: "s1"}},
			{Step: filterStep{"install", manifest.ClassSoftware}, Delta: Delta{Op: OpChange, Detail: "sw1"}},
			{Step: filterStep{"line-in-file", manifest.ClassFiles}, Delta: Delta{Op: OpNoop, Detail: "noop"}},
		}},
		{Name: "b", Steps: []StepResult{
			{Step: filterStep{"install", manifest.ClassSoftware}, Delta: Delta{Op: OpChange, Detail: "sw2"}},
		}},
		{Name: "c", Skipped: true, Detail: "os != windows"},
	}}

	got := FilterByClass(orig, manifest.ClassFiles)

	// Only package "a" survives with a single files-class OpChange step.
	if len(got.Packages) != 1 {
		t.Fatalf("packages = %d, want 1 (%+v)", len(got.Packages), got.Packages)
	}
	if got.Packages[0].Name != "a" {
		t.Fatalf("package = %q, want a", got.Packages[0].Name)
	}
	if len(got.Packages[0].Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(got.Packages[0].Steps))
	}
	if d := got.Packages[0].Steps[0].Delta.Detail; d != "s1" {
		t.Fatalf("kept step = %q, want s1", d)
	}

	// Original plan unmutated.
	if len(orig.Packages) != 3 {
		t.Fatalf("orig packages mutated: %d", len(orig.Packages))
	}
	if len(orig.Packages[0].Steps) != 3 {
		t.Fatalf("orig steps mutated: %d", len(orig.Packages[0].Steps))
	}
}

func TestFilterByClassSoftware(t *testing.T) {
	orig := &Plan{Packages: []PackagePlan{
		{Name: "a", Steps: []StepResult{
			{Step: filterStep{"symlink", manifest.ClassFiles}, Delta: Delta{Op: OpChange, Detail: "s1"}},
			{Step: filterStep{"install", manifest.ClassSoftware}, Delta: Delta{Op: OpChange, Detail: "sw1"}},
		}},
	}}
	got := FilterByClass(orig, manifest.ClassSoftware)
	if len(got.Packages) != 1 || len(got.Packages[0].Steps) != 1 {
		t.Fatalf("unexpected shape: %+v", got)
	}
	if got.Packages[0].Steps[0].Delta.Detail != "sw1" {
		t.Fatalf("kept = %q, want sw1", got.Packages[0].Steps[0].Delta.Detail)
	}
}
