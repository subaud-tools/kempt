package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

func testCtx(t *testing.T) *machine.Context {
	t.Helper()
	tmp := t.TempDir()
	return &machine.Context{
		Home:    tmp,
		RepoDir: tmp,
		OS:      "darwin",
		Arch:    "arm64",
		Runner:  run.RealRunner{},
	}
}

func handler(t *testing.T) engine.Handler {
	t.Helper()
	h, ok := engine.HandlerFor("symlink")
	if !ok {
		t.Fatal("symlink handler not registered")
	}
	return h
}

func TestSymlinkInspectOutcomes(t *testing.T) {
	h := handler(t)

	t.Run("correct link is noop", func(t *testing.T) {
		ctx := testCtx(t)
		from := filepath.Join(ctx.RepoDir, "src")
		to := filepath.Join(ctx.RepoDir, "link")
		if err := os.Symlink(from, to); err != nil {
			t.Fatal(err)
		}
		d, err := h.Inspect(ctx, manifest.SymlinkStep{From: "src", To: to})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpNoop {
			t.Fatalf("op = %v, detail = %q, want noop", d.Op, d.Detail)
		}
	})

	t.Run("missing is change create", func(t *testing.T) {
		ctx := testCtx(t)
		to := filepath.Join(ctx.RepoDir, "link")
		d, err := h.Inspect(ctx, manifest.SymlinkStep{From: "src", To: to})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange || !strings.Contains(d.Detail, "(create)") {
			t.Fatalf("got %v %q, want change create", d.Op, d.Detail)
		}
	})

	t.Run("wrong target is change retarget", func(t *testing.T) {
		ctx := testCtx(t)
		to := filepath.Join(ctx.RepoDir, "link")
		if err := os.Symlink(filepath.Join(ctx.RepoDir, "other"), to); err != nil {
			t.Fatal(err)
		}
		d, err := h.Inspect(ctx, manifest.SymlinkStep{From: "src", To: to})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange || !strings.Contains(d.Detail, "retarget from") {
			t.Fatalf("got %v %q, want change retarget", d.Op, d.Detail)
		}
	})

	t.Run("real file with backup is change", func(t *testing.T) {
		ctx := testCtx(t)
		to := filepath.Join(ctx.RepoDir, "link")
		if err := os.WriteFile(to, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := h.Inspect(ctx, manifest.SymlinkStep{From: "src", To: to, Backup: true})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange || !strings.Contains(d.Detail, "backup existing") {
			t.Fatalf("got %v %q, want change backup", d.Op, d.Detail)
		}
	})

	t.Run("real file without backup is blocked", func(t *testing.T) {
		ctx := testCtx(t)
		to := filepath.Join(ctx.RepoDir, "link")
		if err := os.WriteFile(to, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		d, err := h.Inspect(ctx, manifest.SymlinkStep{From: "src", To: to})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpBlocked || !strings.Contains(d.Detail, "backup = false") {
			t.Fatalf("got %v %q, want blocked", d.Op, d.Detail)
		}
	})
}

func assertNoopAfterApply(t *testing.T, h engine.Handler, ctx *machine.Context, s manifest.Step) {
	t.Helper()
	if err := h.Apply(ctx, s); err != nil {
		t.Fatalf("apply: %v", err)
	}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatalf("re-inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("after apply op = %v (%q), want noop", d.Op, d.Detail)
	}
}

func TestSymlinkApplyCreate(t *testing.T) {
	h := handler(t)
	ctx := testCtx(t)
	to := filepath.Join(ctx.RepoDir, "nested", "link")
	assertNoopAfterApply(t, h, ctx, manifest.SymlinkStep{From: "src", To: to})
}

func TestSymlinkApplyRetarget(t *testing.T) {
	h := handler(t)
	ctx := testCtx(t)
	to := filepath.Join(ctx.RepoDir, "link")
	if err := os.Symlink(filepath.Join(ctx.RepoDir, "other"), to); err != nil {
		t.Fatal(err)
	}
	assertNoopAfterApply(t, h, ctx, manifest.SymlinkStep{From: "src", To: to})
}

func TestSymlinkApplyBackup(t *testing.T) {
	h := handler(t)
	ctx := testCtx(t)
	to := filepath.Join(ctx.RepoDir, "link")
	if err := os.WriteFile(to, []byte("orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertNoopAfterApply(t, h, ctx, manifest.SymlinkStep{From: "src", To: to, Backup: true})
	b, err := os.ReadFile(to + ".bak")
	if err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if string(b) != "orig" {
		t.Fatalf("backup content = %q, want orig", string(b))
	}
}

func TestSymlinkApplyBackupCollision(t *testing.T) {
	h := handler(t)
	ctx := testCtx(t)
	to := filepath.Join(ctx.RepoDir, "link")
	if err := os.WriteFile(to, []byte("orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to+".bak", []byte("prev"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(ctx, manifest.SymlinkStep{From: "src", To: to, Backup: true}); err == nil {
		t.Fatal("want error on backup collision")
	}
}
