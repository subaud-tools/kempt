package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func lineHandler(t *testing.T) engine.Handler {
	t.Helper()
	h, ok := engine.HandlerFor("line-in-file")
	if !ok {
		t.Fatal("line-in-file handler not registered")
	}
	return h
}

func TestLineInFileInspectOutcomes(t *testing.T) {
	h := lineHandler(t)

	t.Run("present is noop", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "f.txt")
		if err := os.WriteFile(f, []byte("alpha\nexport X=1\nbeta\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := h.Inspect(ctx, manifest.LineInFileStep{File: f, Line: "export X=1"})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpNoop {
			t.Fatalf("op = %v (%q), want noop", d.Op, d.Detail)
		}
	})

	t.Run("absent is change append", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "f.txt")
		if err := os.WriteFile(f, []byte("alpha\nbeta\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := h.Inspect(ctx, manifest.LineInFileStep{File: f, Line: "export X=1"})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange || !strings.Contains(d.Detail, "(append line)") {
			t.Fatalf("got %v %q, want change append line", d.Op, d.Detail)
		}
	})

	t.Run("missing file is change create", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "nope.txt")
		d, err := h.Inspect(ctx, manifest.LineInFileStep{File: f, Line: "export X=1"})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange || !strings.Contains(d.Detail, "(create file)") {
			t.Fatalf("got %v %q, want change create file", d.Op, d.Detail)
		}
	})

	t.Run("substring match is not a full line", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "f.txt")
		if err := os.WriteFile(f, []byte("export X=100\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := h.Inspect(ctx, manifest.LineInFileStep{File: f, Line: "export X=1"})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange {
			t.Fatalf("op = %v (%q), want change (substring not a full line)", d.Op, d.Detail)
		}
	})
}

func TestLineInFileApplyAppendPreserves(t *testing.T) {
	h := lineHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "f.txt")
	if err := os.WriteFile(f, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := manifest.LineInFileStep{File: f, Line: "export X=1"}
	assertNoopAfterApply(t, h, ctx, s)
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "alpha\nbeta\nexport X=1\n" {
		t.Fatalf("content = %q", string(b))
	}
}

func TestLineInFileApplyNewlineHygiene(t *testing.T) {
	h := lineHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "f.txt")
	// No trailing newline.
	if err := os.WriteFile(f, []byte("alpha\nbeta"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := manifest.LineInFileStep{File: f, Line: "export X=1"}
	assertNoopAfterApply(t, h, ctx, s)
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "alpha\nbeta\nexport X=1\n" {
		t.Fatalf("content = %q", string(b))
	}
}

func TestLineInFileApplyCreatesFileAndParents(t *testing.T) {
	h := lineHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "nested", "dir", "f.txt")
	s := manifest.LineInFileStep{File: f, Line: "export X=1"}
	assertNoopAfterApply(t, h, ctx, s)
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "export X=1\n" {
		t.Fatalf("content = %q", string(b))
	}
	fi, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", fi.Mode().Perm())
	}
}
