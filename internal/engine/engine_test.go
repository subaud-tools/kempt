package engine_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

const buildSrc = `
[kempt]
spec = 1

[packages.a]
description = "a"
  [[packages.a.symlink]]
  from = "src/a"
  to = "a"

[packages.b]
description = "b"
needs = ["a"]
  [[packages.b.install]]
  brew = { formulas = ["foo"] }
  [[packages.b.symlink]]
  from = "src/b"
  to = "b"
  only = { os = "windows" }

[packages.win]
description = "win only"
only = { os = "windows" }
  [[packages.win.symlink]]
  from = "src/w"
  to = "w"
`

func buildCtx(t *testing.T) *machine.Context {
	t.Helper()
	tmp := t.TempDir()
	return &machine.Context{
		Home:    tmp,
		RepoDir: tmp,
		OS:      "darwin",
		Arch:    "arm64",
		// Empty FakeRunner: LookPath("brew") misses, so the install step
		// resolves to OpBlocked "(brew not found)" deterministically.
		Runner: &run.FakeRunner{},
		Cache:  map[string]string{},
	}
}

func buildPlan(t *testing.T, ctx *machine.Context) *engine.Plan {
	t.Helper()
	m, findings := manifest.Parse([]byte(buildSrc))
	if len(findings) > 0 {
		t.Fatalf("parse findings: %v", findings)
	}
	findings = manifest.Validate(m)
	if len(findings) > 0 {
		t.Fatalf("validate findings: %v", findings)
	}
	pkgs, err := engine.Select(m, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestBuildPlanOrderAndOps(t *testing.T) {
	ctx := buildCtx(t)
	plan := buildPlan(t, ctx)

	if len(plan.Packages) != 3 {
		t.Fatalf("packages = %d, want 3", len(plan.Packages))
	}
	names := []string{plan.Packages[0].Name, plan.Packages[1].Name, plan.Packages[2].Name}
	wantNames := []string{"a", "b", "win"}
	for i, w := range wantNames {
		if names[i] != w {
			t.Fatalf("order = %v; want %v", names, wantNames)
		}
	}

	// a: single symlink change
	if got := plan.Packages[0].Steps[0].Delta.Op; got != engine.OpChange {
		t.Fatalf("a step op = %v, want change", got)
	}

	// b: install -> blocked (brew not found via empty FakeRunner), symlink -> skipped by only
	b := plan.Packages[1]
	if b.Steps[0].Delta.Op != engine.OpBlocked {
		t.Fatalf("b install op = %v, want blocked", b.Steps[0].Delta.Op)
	}
	if b.Steps[0].Delta.Detail != "install (brew not found)" {
		t.Fatalf("b install detail = %q", b.Steps[0].Delta.Detail)
	}
	if b.Steps[1].Delta.Op != engine.OpSkip {
		t.Fatalf("b symlink op = %v, want skip", b.Steps[1].Delta.Op)
	}

	// win: whole package skipped
	if !plan.Packages[2].Skipped {
		t.Fatal("win package should be skipped")
	}
}

// bogusStep is a fake Step whose kind has no registered handler. Every real
// kind now has a handler (phase 1b complete), so the engine's "not implemented"
// fallback can only be exercised via an injected step type. We build a
// manifest.Package by hand to bypass Parse, which would reject an unknown kind.
type bogusStep struct{}

func (bogusStep) Kind() string          { return "bogus" }
func (bogusStep) Class() manifest.Class { return manifest.ClassReadOnly }

func TestBuildPlanUnimplementedHandlerFallback(t *testing.T) {
	ctx := buildCtx(t)
	pkg := &manifest.Package{Name: "z", Steps: []manifest.Step{bogusStep{}}}
	plan, err := engine.BuildPlan(ctx, []*manifest.Package{pkg})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Packages) != 1 || len(plan.Packages[0].Steps) != 1 {
		t.Fatalf("unexpected plan shape: %+v", plan)
	}
	step := plan.Packages[0].Steps[0]
	if step.Delta.Op != engine.OpBlocked {
		t.Fatalf("bogus step op = %v, want blocked", step.Delta.Op)
	}
	if step.Delta.Detail != "handler not implemented yet (phase 1b)" {
		t.Fatalf("bogus step detail = %q", step.Delta.Detail)
	}
}

func TestRenderNotesFooter(t *testing.T) {
	plan := &engine.Plan{
		Packages: []engine.PackagePlan{
			{Name: "core", Notes: []string{"run codex login", "grant access"}},
		},
	}
	var buf bytes.Buffer
	engine.Render(plan, &buf, true)
	out := buf.String()
	if !strings.Contains(out, "manual follow-ups:") {
		t.Fatalf("render output missing 'manual follow-ups:'\n%s", out)
	}
	if !strings.Contains(out, "  - run codex login") {
		t.Fatalf("render output missing '  - run codex login'\n%s", out)
	}
	if !strings.Contains(out, "  - grant access") {
		t.Fatalf("render output missing '  - grant access'\n%s", out)
	}
}

func TestRenderNotesAbsent(t *testing.T) {
	plan := &engine.Plan{
		Packages: []engine.PackagePlan{
			{Name: "core"},
		},
	}
	var buf bytes.Buffer
	engine.Render(plan, &buf, true)
	out := buf.String()
	if strings.Contains(out, "manual follow-ups:") {
		t.Fatalf("render output should NOT contain 'manual follow-ups:' when no notes\n%s", out)
	}
}

func TestRenderNotesSkippedPackage(t *testing.T) {
	// Notes must appear even when the package is only-skipped.
	plan := &engine.Plan{
		Packages: []engine.PackagePlan{
			{Name: "win", Skipped: true, Detail: "os != windows", Notes: []string{"install manually on windows"}},
		},
	}
	var buf bytes.Buffer
	engine.Render(plan, &buf, true)
	out := buf.String()
	if !strings.Contains(out, "manual follow-ups:") {
		t.Fatalf("render output missing 'manual follow-ups:' for skipped package with notes\n%s", out)
	}
	if !strings.Contains(out, "  - install manually on windows") {
		t.Fatalf("render output missing note line for skipped package\n%s", out)
	}
}

func TestRenderGolden(t *testing.T) {
	ctx := buildCtx(t)
	plan := buildPlan(t, ctx)

	var buf bytes.Buffer
	engine.Render(plan, &buf, true)

	want := fmt.Sprintf(`package a
  + symlink %s -> %s (create)
package b
  ! install (brew not found)
  - symlink (skipped: os != windows)
package win (skipped: os != windows)
1 changes, 0 ok, 1 skipped, 1 blocked
software changes: 0, file changes: 1
`, ctx.Expand("a"), ctx.Expand("src/a"))

	if buf.String() != want {
		t.Fatalf("render mismatch\n--- got ---\n%s\n--- want ---\n%s", buf.String(), want)
	}
}

// TestRenderQuietOmitsOkStepsAndNotes: the default (quiet) render prints only
// changed/skipped/blocked steps, drops fully-converged packages entirely, and
// omits the repeat-every-run manual follow-ups — while the footer counts stay
// identical to verbose.
func TestRenderQuietOmitsOkStepsAndNotes(t *testing.T) {
	plan := &engine.Plan{
		Packages: []engine.PackagePlan{
			{
				Name:  "converged",
				Notes: []string{"run codex login"},
				Steps: []engine.StepResult{
					{Step: manifest.SymlinkStep{From: "src/x", To: "x"}, Delta: engine.Delta{Op: engine.OpNoop, Detail: "symlink x"}},
				},
			},
			{
				Name: "changed",
				Steps: []engine.StepResult{
					{Step: manifest.SymlinkStep{From: "src/y", To: "y"}, Delta: engine.Delta{Op: engine.OpChange, Detail: "symlink y (create)"}},
				},
			},
		},
	}
	var quiet, verbose bytes.Buffer
	engine.Render(plan, &quiet, false)
	engine.Render(plan, &verbose, true)
	q := quiet.String()

	if strings.Contains(q, "converged") {
		t.Errorf("quiet render must omit a fully-converged package's header:\n%s", q)
	}
	if strings.Contains(q, "manual follow-ups:") {
		t.Errorf("quiet render must omit manual follow-ups:\n%s", q)
	}
	if !strings.Contains(q, "package changed") || !strings.Contains(q, "+ symlink y (create)") {
		t.Errorf("quiet render must still show the changed package + its change line:\n%s", q)
	}
	if !strings.Contains(q, "1 changes, 1 ok, 0 skipped, 0 blocked") {
		t.Errorf("quiet render must keep the full footer counts (ok included):\n%s", q)
	}
	// Verbose is the superset: the converged package, its ✓ step, and the notes.
	v := verbose.String()
	if !strings.Contains(v, "package converged") || !strings.Contains(v, "✓ symlink x") || !strings.Contains(v, "manual follow-ups:") {
		t.Errorf("verbose render must keep converged package, its ✓ step, and follow-ups:\n%s", v)
	}
}
