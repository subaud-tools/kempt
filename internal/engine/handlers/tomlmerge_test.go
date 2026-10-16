package handlers

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func tomlHandler(t *testing.T) engine.Handler {
	t.Helper()
	h, ok := engine.HandlerFor("toml-merge")
	if !ok {
		t.Fatal("toml-merge handler not registered")
	}
	return h
}

func writeTOML(t *testing.T, path string, v map[string]any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := toml.NewEncoder(f).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func readTOML(t *testing.T, path string) map[string]any {
	t.Helper()
	var v map[string]any
	if _, err := toml.DecodeFile(path, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTOMLMergeSubsetNoop(t *testing.T) {
	h := tomlHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.toml")
	writeTOML(t, f, map[string]any{"a": int64(1), "b": map[string]any{"c": int64(2)}})
	s := manifest.TomlMergeStep{File: f, Merge: map[string]any{"b": map[string]any{"c": int64(2)}}}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v (%q), want noop", d.Op, d.Detail)
	}
}

func TestTOMLMergeNestedMapPreservesSiblings(t *testing.T) {
	h := tomlHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.toml")
	writeTOML(t, f, map[string]any{"b": map[string]any{"keep": int64(1)}})
	s := manifest.TomlMergeStep{File: f, Merge: map[string]any{"b": map[string]any{"add": int64(2)}}}

	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange || !strings.Contains(d.Detail, "merge keys: b") {
		t.Fatalf("got %v %q, want change merge keys: b", d.Op, d.Detail)
	}
	assertNoopAfterApply(t, h, ctx, s)

	got := readTOML(t, f)
	want := map[string]any{"b": map[string]any{"keep": int64(1), "add": int64(2)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTOMLMergeArrayAppend(t *testing.T) {
	h := tomlHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.toml")
	writeTOML(t, f, map[string]any{"list": []any{"a"}})
	s := manifest.TomlMergeStep{File: f, Merge: map[string]any{"list": []any{"b"}}}
	assertNoopAfterApply(t, h, ctx, s)
	got := readTOML(t, f)
	want := map[string]any{"list": []any{"a", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTOMLMergeScalarOverwrite(t *testing.T) {
	h := tomlHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.toml")
	writeTOML(t, f, map[string]any{"a": int64(1)})
	s := manifest.TomlMergeStep{File: f, Merge: map[string]any{"a": int64(2)}}
	assertNoopAfterApply(t, h, ctx, s)
	got := readTOML(t, f)
	want := map[string]any{"a": int64(2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTOMLMergeInvalidTOMLBlocked(t *testing.T) {
	h := tomlHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.toml")
	if err := os.WriteFile(f, []byte("not = valid = toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := manifest.TomlMergeStep{File: f, Merge: map[string]any{"a": int64(1)}}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpBlocked || !strings.Contains(d.Detail, "not valid TOML") {
		t.Fatalf("got %v %q, want blocked invalid TOML", d.Op, d.Detail)
	}
}

func TestTOMLMergeCreateMissingFile(t *testing.T) {
	h := tomlHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "nested", "c.toml")
	s := manifest.TomlMergeStep{File: f, Merge: map[string]any{"a": int64(1)}}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange || !strings.Contains(d.Detail, "(create file)") {
		t.Fatalf("got %v %q, want change create file", d.Op, d.Detail)
	}
	assertNoopAfterApply(t, h, ctx, s)
	got := readTOML(t, f)
	want := map[string]any{"a": int64(1)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	fi, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", fi.Mode().Perm())
	}
}

func TestTOMLMergeDetailBase(t *testing.T) {
	h := tomlHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.toml")
	writeTOML(t, f, map[string]any{"a": int64(1)})
	s := manifest.TomlMergeStep{File: f, Merge: map[string]any{"a": int64(1)}}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Detail != "toml-merge "+f {
		t.Fatalf("detail = %q, want %q", d.Detail, "toml-merge "+f)
	}
}

func TestTOMLMergeExpandsHomeToken(t *testing.T) {
	h := tomlHandler(t)
	ctx := testCtx(t)
	ctx.Home = "/Users/tester"
	f := filepath.Join(ctx.RepoDir, "config.toml")
	writeTOML(t, f, map[string]any{})
	s := manifest.TomlMergeStep{File: f, Merge: map[string]any{
		"mcp_servers": map[string]any{"muster": map[string]any{"command": "${HOME}/.local/bin/muster"}},
	}}
	if err := h.Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	got := readTOML(t, f)
	srv := got["mcp_servers"].(map[string]any)["muster"].(map[string]any)
	if want := "/Users/tester/.local/bin/muster"; srv["command"] != want {
		t.Fatalf("command = %q, want %q", srv["command"], want)
	}
}
