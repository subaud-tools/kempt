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

func gitCloneHandler(t *testing.T) engine.Handler {
	t.Helper()
	h, ok := engine.HandlerFor("git-clone")
	if !ok {
		t.Fatal("git-clone handler not registered")
	}
	return h
}

// gitCtx builds a Context whose Home/RepoDir are a fresh tempdir and whose
// Runner is the given FakeRunner.
func gitCtx(t *testing.T, r run.Runner) *machine.Context {
	t.Helper()
	tmp := t.TempDir()
	return &machine.Context{
		Home:    tmp,
		RepoDir: tmp,
		OS:      "darwin",
		Arch:    "arm64",
		Runner:  r,
	}
}

const gitRepo = "https://github.com/example/repo"

func TestGitCloneInspectNoop(t *testing.T) {
	h := gitCloneHandler(t)
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := gitCtx(t, fr)
	to := filepath.Join(ctx.RepoDir, "clone")
	if err := os.MkdirAll(filepath.Join(to, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	key := "git -C " + to + " remote get-url origin"
	fr.Responses[key] = run.Response{Stdout: gitRepo + "\n"}

	d, err := h.Inspect(ctx, manifest.GitCloneStep{Repo: gitRepo, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v, detail = %q, want noop", d.Op, d.Detail)
	}
}

func TestGitCloneInspectOriginMismatch(t *testing.T) {
	h := gitCloneHandler(t)
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := gitCtx(t, fr)
	to := filepath.Join(ctx.RepoDir, "clone")
	if err := os.MkdirAll(filepath.Join(to, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	key := "git -C " + to + " remote get-url origin"
	fr.Responses[key] = run.Response{Stdout: "https://github.com/other/repo\n"}

	d, err := h.Inspect(ctx, manifest.GitCloneStep{Repo: gitRepo, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpBlocked || !strings.Contains(d.Detail, "origin is https://github.com/other/repo") {
		t.Fatalf("got %v %q, want blocked origin mismatch", d.Op, d.Detail)
	}
}

func TestGitCloneInspectSSHOriginHTTPSRepo(t *testing.T) {
	for _, c := range []struct {
		origin string
		want   engine.Op
	}{
		{"git@github.com:example/repo.git", engine.OpNoop},
		{"git@github.com:example/other.git", engine.OpBlocked},
	} {
		h := gitCloneHandler(t)
		fr := &run.FakeRunner{Responses: map[string]run.Response{}}
		ctx := gitCtx(t, fr)
		to := filepath.Join(ctx.RepoDir, "clone")
		if err := os.MkdirAll(filepath.Join(to, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		fr.Responses["git -C "+to+" remote get-url origin"] = run.Response{Stdout: c.origin + "\n"}

		d, err := h.Inspect(ctx, manifest.GitCloneStep{Repo: gitRepo + ".git", To: to})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != c.want {
			t.Errorf("origin %s: op = %v, detail = %q, want %v", c.origin, d.Op, d.Detail, c.want)
		}
	}
}

func TestGitCloneInspectMissingChange(t *testing.T) {
	h := gitCloneHandler(t)
	ctx := gitCtx(t, &run.FakeRunner{})
	to := filepath.Join(ctx.RepoDir, "clone")

	t.Run("default ref", func(t *testing.T) {
		d, err := h.Inspect(ctx, manifest.GitCloneStep{Repo: gitRepo, To: to})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange || !strings.Contains(d.Detail, "default") {
			t.Fatalf("got %v %q, want change default", d.Op, d.Detail)
		}
	})

	t.Run("pinned ref", func(t *testing.T) {
		d, err := h.Inspect(ctx, manifest.GitCloneStep{Repo: gitRepo, To: to, Ref: "v1.2.3"})
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange || !strings.Contains(d.Detail, "ref v1.2.3") {
			t.Fatalf("got %v %q, want change ref", d.Op, d.Detail)
		}
	})
}

func TestGitCloneInspectExistsNotGit(t *testing.T) {
	h := gitCloneHandler(t)
	ctx := gitCtx(t, &run.FakeRunner{})
	to := filepath.Join(ctx.RepoDir, "clone")
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	// Non-empty dir without .git.
	if err := os.WriteFile(filepath.Join(to, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := h.Inspect(ctx, manifest.GitCloneStep{Repo: gitRepo, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpBlocked || !strings.Contains(d.Detail, "exists, not a git repo") {
		t.Fatalf("got %v %q, want blocked not-git", d.Op, d.Detail)
	}
}

func TestGitCloneApplyClone(t *testing.T) {
	h := gitCloneHandler(t)
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := gitCtx(t, fr)
	to := filepath.Join(ctx.RepoDir, "clone")
	cloneKey := "git clone " + gitRepo + " " + to
	fr.Responses[cloneKey] = run.Response{}

	if err := h.Apply(ctx, manifest.GitCloneStep{Repo: gitRepo, To: to}); err != nil {
		t.Fatal(err)
	}
	if len(fr.Calls) != 1 || fr.Calls[0] != cloneKey {
		t.Fatalf("calls = %v, want [%q]", fr.Calls, cloneKey)
	}
}

func TestGitCloneApplyCloneWithRef(t *testing.T) {
	h := gitCloneHandler(t)
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := gitCtx(t, fr)
	to := filepath.Join(ctx.RepoDir, "clone")
	cloneKey := "git clone " + gitRepo + " " + to
	checkoutKey := "git -C " + to + " checkout v1.2.3"
	fr.Responses[cloneKey] = run.Response{}
	fr.Responses[checkoutKey] = run.Response{}

	if err := h.Apply(ctx, manifest.GitCloneStep{Repo: gitRepo, To: to, Ref: "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	want := []string{cloneKey, checkoutKey}
	if len(fr.Calls) != 2 || fr.Calls[0] != want[0] || fr.Calls[1] != want[1] {
		t.Fatalf("calls = %v, want %v", fr.Calls, want)
	}
}
