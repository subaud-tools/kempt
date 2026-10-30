package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// managedCtx is a test context whose kempt state (managed-file ownership)
// lives in its own temporary directory.
func managedCtx(t *testing.T) *machine.Context {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	return testCtx(t)
}

func managedHandler(t *testing.T) engine.Handler {
	t.Helper()
	h, ok := engine.HandlerFor("managed-file")
	if !ok {
		t.Fatal("managed-file handler not registered")
	}
	return h
}

// bound is a managed-file step as BindSetup leaves it: from the verified
// bundle of proj 1.2.0.
func bound(from, to, mode, data string) manifest.ManagedFileStep {
	return manifest.ManagedFileStep{From: from, To: to, Mode: mode,
		Source: &manifest.SetupSource{Tool: "proj", Version: "1.2.0", Data: []byte(data)}}
}

func inspect(t *testing.T, ctx *machine.Context, s manifest.Step) engine.Delta {
	t.Helper()
	d, err := managedHandler(t).Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// converge applies a step the plan says changes, then checks re-inspection
// finds nothing left to do.
func converge(t *testing.T, ctx *machine.Context, s manifest.Step) {
	t.Helper()
	if d := inspect(t, ctx, s); d.Op != engine.OpChange {
		t.Fatalf("inspect = %v %q, want change", d.Op, d.Detail)
	}
	if err := managedHandler(t).Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	if d := inspect(t, ctx, s); d.Op != engine.OpNoop {
		t.Fatalf("re-inspect = %v %q, want noop", d.Op, d.Detail)
	}
}

func readString(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestManagedFileInstallsWrapper(t *testing.T) {
	ctx := managedCtx(t)
	s := bound("bin/p", "~/.local/bin/p", "0755", "#!/bin/sh\nexec proj \"$@\"\n")
	d := inspect(t, ctx, s)
	dest := filepath.Join(ctx.Home, ".local/bin/p")
	for _, want := range []string{"managed-file " + dest, "(create, mode 0755)", "proj 1.2.0 files/bin/p"} {
		if !strings.Contains(d.Detail, want) {
			t.Errorf("detail %q missing %q", d.Detail, want)
		}
	}
	converge(t, ctx, s)
	if got := readString(t, dest); got != "#!/bin/sh\nexec proj \"$@\"\n" {
		t.Fatalf("content = %q", got)
	}
	if fi, _ := os.Stat(dest); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

// Content already identical to the bundle's is adopted without writing it.
func TestManagedFileAdoptsIdenticalWithoutWriting(t *testing.T) {
	ctx := managedCtx(t)
	dest := filepath.Join(ctx.Home, "f")
	if err := os.WriteFile(dest, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(dest)
	s := bound("f", "~/f", "0644", "same\n")
	if d := inspect(t, ctx, s); !strings.Contains(d.Detail, "(adopt") {
		t.Fatalf("detail = %q, want adopt", d.Detail)
	}
	converge(t, ctx, s)
	after, _ := os.Stat(dest)
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("adopting identical content rewrote the file")
	}
}

func TestManagedFileUpdatesOwnedUnchangedTarget(t *testing.T) {
	ctx := managedCtx(t)
	converge(t, ctx, bound("f", "~/f", "0644", "v1\n"))
	next := bound("f", "~/f", "0644", "v2\n")
	if d := inspect(t, ctx, next); !strings.Contains(d.Detail, "(update content, mode 0644)") {
		t.Fatalf("detail = %q, want update", d.Detail)
	}
	converge(t, ctx, next)
	if got := readString(t, filepath.Join(ctx.Home, "f")); got != "v2\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestManagedFileBlocksModifiedTarget(t *testing.T) {
	ctx := managedCtx(t)
	converge(t, ctx, bound("f", "~/f", "0644", "v1\n"))
	dest := filepath.Join(ctx.Home, "f")
	if err := os.WriteFile(dest, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	next := bound("f", "~/f", "0644", "v2\n")
	if d := inspect(t, ctx, next); d.Op != engine.OpBlocked || !strings.Contains(d.Detail, "changed since kempt wrote it") {
		t.Fatalf("inspect = %v %q, want blocked", d.Op, d.Detail)
	}
	if err := managedHandler(t).Apply(ctx, next); err == nil {
		t.Fatal("apply overwrote a modified file")
	}
	if got := readString(t, dest); got != "edited\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestManagedFileBlocksForeignTarget(t *testing.T) {
	ctx := managedCtx(t)
	dest := filepath.Join(ctx.Home, "f")
	if err := os.WriteFile(dest, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := bound("f", "~/f", "0644", "theirs\n")
	if d := inspect(t, ctx, s); d.Op != engine.OpBlocked || !strings.Contains(d.Detail, "not kempt's") {
		t.Fatalf("inspect = %v %q, want blocked", d.Op, d.Detail)
	}
	if err := managedHandler(t).Apply(ctx, s); err == nil {
		t.Fatal("apply overwrote a foreign file")
	}
	if got := readString(t, dest); got != "mine\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestManagedFileBlocksSymlinkTarget(t *testing.T) {
	ctx := managedCtx(t)
	other := filepath.Join(ctx.Home, "other")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(ctx.Home, "f")); err != nil {
		t.Fatal(err)
	}
	if d := inspect(t, ctx, bound("f", "~/f", "0644", "x")); d.Op != engine.OpBlocked {
		t.Fatalf("inspect = %v %q, want blocked", d.Op, d.Detail)
	}
}

// A managed-file step with no verified bundle behind it reads nothing.
func TestManagedFileWithoutBundleIsBlocked(t *testing.T) {
	ctx := managedCtx(t)
	s := manifest.ManagedFileStep{From: "f", To: "~/f", Mode: "0644"}
	if d := inspect(t, ctx, s); d.Op != engine.OpBlocked || !strings.Contains(d.Detail, "no verified setup bundle") {
		t.Fatalf("inspect = %v %q, want blocked", d.Op, d.Detail)
	}
}

// Each file of a multi-file skill is its own step; a sibling the user added
// in the skill's directory is left alone.
func TestManagedFileMultiFileSkillPreservesSiblings(t *testing.T) {
	ctx := managedCtx(t)
	dir := filepath.Join(ctx.Home, ".claude/skills/proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("user notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := []manifest.ManagedFileStep{
		bound("skills/proj/SKILL.md", "~/.claude/skills/proj/SKILL.md", "0644", "---\nname: proj\n---\n"),
		bound("skills/proj/references/attach.md", "~/.claude/skills/proj/references/attach.md", "0644", "attach\n"),
	}
	for _, s := range steps {
		converge(t, ctx, s)
	}
	if got := readString(t, filepath.Join(dir, "references/attach.md")); got != "attach\n" {
		t.Fatalf("reference = %q", got)
	}
	if got := readString(t, filepath.Join(dir, "notes.md")); got != "user notes\n" {
		t.Fatalf("sibling = %q", got)
	}
}
