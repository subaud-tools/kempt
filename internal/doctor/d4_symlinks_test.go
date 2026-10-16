package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/manifest"
)

func TestD4ForeignFile(t *testing.T) {
	ctx := ctxT(t)
	to := filepath.Join(t.TempDir(), "link")
	writeFile(t, to, "real file where a symlink is expected")
	pkgs := []*manifest.Package{{Name: "p", Steps: []manifest.Step{
		manifest.SymlinkStep{From: "src", To: to},
	}}}
	got := CheckSymlinks(ctx, pkgs)
	if len(got) != 1 || got[0].Severity != Warn {
		t.Fatalf("want 1 warn (foreign), got %+v", got)
	}
}

func TestD4BrokenLink(t *testing.T) {
	ctx := ctxT(t)
	to := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(t.TempDir(), "does-not-exist"), to); err != nil {
		t.Fatal(err)
	}
	got := CheckSymlinks(ctx, []*manifest.Package{{Name: "p", Steps: []manifest.Step{
		manifest.SymlinkStep{From: "src", To: to},
	}}})
	if len(got) != 1 || got[0].Severity != Error {
		t.Fatalf("want 1 error (broken), got %+v", got)
	}
}

func TestD4AbsentNoFinding(t *testing.T) {
	ctx := ctxT(t)
	to := filepath.Join(t.TempDir(), "nope")
	if got := CheckSymlinks(ctx, []*manifest.Package{{Name: "p", Steps: []manifest.Step{
		manifest.SymlinkStep{From: "src", To: to},
	}}}); len(got) != 0 {
		t.Fatalf("absent target is plan's job, want none, got %+v", got)
	}
}

func TestD4TildeFromNoFalsePositive(t *testing.T) {
	ctx := ctxT(t)
	// The handler links `to` at ctx.Expand("~/thing") = Home/thing. D4 must use
	// the same basis; filepath.Join(RepoDir, "~/thing") would not match and
	// would false-positive as mispointed.
	target := ctx.Expand("~/thing")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, to); err != nil {
		t.Fatal(err)
	}
	pkgs := []*manifest.Package{{Name: "p", Steps: []manifest.Step{
		manifest.SymlinkStep{From: "~/thing", To: to},
	}}}
	if got := CheckSymlinks(ctx, pkgs); len(got) != 0 {
		t.Fatalf("~/ From must match the handler's ctx.Expand basis; want no finding, got %+v", got)
	}
}

func TestD4MispointedWarn(t *testing.T) {
	ctx := ctxT(t)
	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(other, to); err != nil { // exists but != ctx.Expand("src")
		t.Fatal(err)
	}
	pkgs := []*manifest.Package{{Name: "p", Steps: []manifest.Step{
		manifest.SymlinkStep{From: "src", To: to},
	}}}
	got := CheckSymlinks(ctx, pkgs)
	if len(got) != 1 || got[0].Severity != Warn {
		t.Fatalf("want 1 warn (mispointed), got %+v", got)
	}
}
