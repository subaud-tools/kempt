package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ctxT(t *testing.T) *machine.Context {
	c, _ := machine.New(t.TempDir(), &run.FakeRunner{})
	c.Home = t.TempDir()
	return c
}

func pkgWithMerge(file string, merge map[string]any, arrays string) []*manifest.Package {
	return []*manifest.Package{{Name: "pi", Steps: []manifest.Step{
		manifest.JSONMergeStep{File: file, Merge: merge, Arrays: arrays},
	}}}
}

func TestD1FlagsAppendSuperset(t *testing.T) {
	ctx := ctxT(t)
	f := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, f, `{"packages":["npm:a","npm:a@1.0.0","npm:b"]}`)
	pkgs := pkgWithMerge(f, map[string]any{"packages": []any{"npm:a", "npm:b"}}, "")
	got := CheckExtraArray(ctx, pkgs)
	if len(got) != 1 || got[0].Severity != Warn {
		t.Fatalf("want 1 warn, got %+v", got)
	}
}

func TestD1ReplaceExtrasAreError(t *testing.T) {
	ctx := ctxT(t)
	f := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, f, `{"packages":["npm:a","npm:x"]}`)
	pkgs := pkgWithMerge(f, map[string]any{"packages": []any{"npm:a"}}, "replace")
	got := CheckExtraArray(ctx, pkgs)
	if len(got) != 1 || got[0].Severity != Error {
		t.Fatalf("want 1 error, got %+v", got)
	}
}

func TestD1ExpandsHomeToken(t *testing.T) {
	ctx := ctxT(t)
	f := filepath.Join(t.TempDir(), "settings.json")
	// Live file holds the expanded absolute path built from the same home
	// the ctx uses; the manifest declares it via the ${HOME} token.
	writeFile(t, f, `{"skills":["`+ctx.Home+`/x"]}`)
	pkgs := pkgWithMerge(f, map[string]any{"skills": []any{"${HOME}/x"}}, "replace")
	if got := CheckExtraArray(ctx, pkgs); len(got) != 0 {
		t.Fatalf("want none (${HOME} should expand to match live), got %+v", got)
	}
}

func TestD1CleanNoFindings(t *testing.T) {
	ctx := ctxT(t)
	f := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, f, `{"packages":["npm:a"]}`)
	if got := CheckExtraArray(ctx, pkgWithMerge(f, map[string]any{"packages": []any{"npm:a"}}, "replace")); len(got) != 0 {
		t.Fatalf("want none, got %+v", got)
	}
}
