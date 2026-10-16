package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func jsonHandler(t *testing.T) engine.Handler {
	t.Helper()
	h, ok := engine.HandlerFor("json-merge")
	if !ok {
		t.Fatal("json-merge handler not registered")
	}
	return h
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestJSONMergeSubsetNoop(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.json")
	writeJSON(t, f, map[string]any{"a": 1.0, "b": map[string]any{"c": 2.0}})
	s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"b": map[string]any{"c": 2.0}}}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v (%q), want noop", d.Op, d.Detail)
	}
}

func TestJSONMergeNestedMapPreservesSiblings(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.json")
	writeJSON(t, f, map[string]any{"b": map[string]any{"keep": 1.0}})
	s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"b": map[string]any{"add": 2.0}}}

	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange || !strings.Contains(d.Detail, "merge keys: b") {
		t.Fatalf("got %v %q, want change merge keys: b", d.Op, d.Detail)
	}
	assertNoopAfterApply(t, h, ctx, s)

	got := readJSON(t, f)
	want := map[string]any{"b": map[string]any{"keep": 1.0, "add": 2.0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestJSONMergeArrayEnsureElement(t *testing.T) {
	h := jsonHandler(t)

	t.Run("already present is noop", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "c.json")
		writeJSON(t, f, map[string]any{"list": []any{"a", "b"}})
		s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"list": []any{"b"}}}
		d, err := h.Inspect(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpNoop {
			t.Fatalf("op = %v (%q), want noop", d.Op, d.Detail)
		}
	})

	t.Run("missing element appended once", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "c.json")
		writeJSON(t, f, map[string]any{"list": []any{"a"}})
		s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"list": []any{"b"}}}
		assertNoopAfterApply(t, h, ctx, s)
		got := readJSON(t, f)
		want := map[string]any{"list": []any{"a", "b"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

func TestJSONMergeArraysReplace(t *testing.T) {
	h := jsonHandler(t)

	t.Run("replace shrinks array", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "c.json")
		writeJSON(t, f, map[string]any{"packages": []any{"a", "b", "c"}})
		s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"packages": []any{"a", "b"}}, Arrays: "replace"}

		d, err := h.Inspect(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange {
			t.Fatalf("op = %v (%q), want change", d.Op, d.Detail)
		}
		if err := h.Apply(ctx, s); err != nil {
			t.Fatal(err)
		}
		got := readJSON(t, f)
		want := map[string]any{"packages": []any{"a", "b"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		d2, err := h.Inspect(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		if d2.Op != engine.OpNoop {
			t.Fatalf("re-inspect op = %v (%q), want noop", d2.Op, d2.Detail)
		}
	})

	t.Run("append default is noop on same inputs", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "c.json")
		writeJSON(t, f, map[string]any{"packages": []any{"a", "b", "c"}})
		s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"packages": []any{"a", "b"}}}
		d, err := h.Inspect(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpNoop {
			t.Fatalf("op = %v (%q), want noop", d.Op, d.Detail)
		}
	})

	t.Run("replace nested map array child", func(t *testing.T) {
		ctx := testCtx(t)
		f := filepath.Join(ctx.RepoDir, "c.json")
		writeJSON(t, f, map[string]any{"outer": map[string]any{"keep": 1.0, "list": []any{"x", "y", "z"}}})
		s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"outer": map[string]any{"list": []any{"x"}}}, Arrays: "replace"}

		d, err := h.Inspect(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		if d.Op != engine.OpChange {
			t.Fatalf("op = %v (%q), want change", d.Op, d.Detail)
		}
		if err := h.Apply(ctx, s); err != nil {
			t.Fatal(err)
		}
		got := readJSON(t, f)
		want := map[string]any{"outer": map[string]any{"keep": 1.0, "list": []any{"x"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

func TestJSONMergeScalarOverwrite(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.json")
	writeJSON(t, f, map[string]any{"a": 1.0})
	s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"a": 2.0}}
	assertNoopAfterApply(t, h, ctx, s)
	got := readJSON(t, f)
	want := map[string]any{"a": 2.0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestJSONMergeInvalidJSONBlocked(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.json")
	if err := os.WriteFile(f, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"a": 1.0}}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpBlocked || !strings.Contains(d.Detail, "not valid JSON") {
		t.Fatalf("got %v %q, want blocked invalid JSON", d.Op, d.Detail)
	}
}

func TestJSONMergeCreateMissingFile(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "nested", "c.json")
	s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"a": 1.0}}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange || !strings.Contains(d.Detail, "(create file)") {
		t.Fatalf("got %v %q, want change create file", d.Op, d.Detail)
	}
	assertNoopAfterApply(t, h, ctx, s)
	got := readJSON(t, f)
	want := map[string]any{"a": 1.0}
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
	// trailing newline
	b, _ := os.ReadFile(f)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("want trailing newline, got %q", string(b))
	}
}

// TestJSONMergeIntegerKeyNoSpuriousChange verifies that a TOML-decoded int64
// value (e.g. count=42) is not spuriously listed as a changed key when the
// current JSON file already contains the same integer as float64 (JSON numbers
// always decode to float64). The fix normalises desired via toAny before
// passing to mergeKeys so both sides share the same float64 type.
func TestJSONMergeIntegerKeyNoSpuriousChange(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.json")
	writeJSON(t, f, map[string]any{"count": 42.0, "name": "old"})
	// int64(42) mirrors what TOML decoding produces for an integer value.
	s := manifest.JSONMergeStep{File: f, Merge: map[string]any{"count": int64(42), "name": "new"}}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v, want change", d.Op)
	}
	if !strings.Contains(d.Detail, "name") {
		t.Fatalf("detail %q should mention name", d.Detail)
	}
	if strings.Contains(d.Detail, "count") {
		t.Fatalf("detail %q should NOT mention count (spurious type-mismatch)", d.Detail)
	}
}

func TestIsSubset(t *testing.T) {
	tests := []struct {
		name         string
		desired, cur any
		want         bool
	}{
		{"scalar equal", 1.0, 1.0, true},
		{"scalar differ", 1.0, 2.0, false},
		{"map subset", map[string]any{"a": 1.0}, map[string]any{"a": 1.0, "b": 2.0}, true},
		{"map missing key", map[string]any{"c": 1.0}, map[string]any{"a": 1.0}, false},
		{"nested map subset", map[string]any{"a": map[string]any{"b": 1.0}}, map[string]any{"a": map[string]any{"b": 1.0, "c": 2.0}}, true},
		{"array elements present", []any{"b"}, []any{"a", "b"}, true},
		{"array element missing", []any{"z"}, []any{"a", "b"}, false},
		// type-conflict hotspots
		{"desired map current scalar", map[string]any{"a": 1.0}, "scalar", false},
		{"desired array current nil", []any{"a"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSubset(tt.desired, tt.cur, false); got != tt.want {
				t.Fatalf("isSubset = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name               string
		desired, cur, want any
	}{
		{"scalar overwrite", 2.0, 1.0, 2.0},
		{"map recurse", map[string]any{"a": 2.0}, map[string]any{"a": 1.0, "b": 3.0}, map[string]any{"a": 2.0, "b": 3.0}},
		{"array append missing", []any{"b", "c"}, []any{"a", "b"}, []any{"a", "b", "c"}},
		{"nested map", map[string]any{"x": map[string]any{"y": 2.0}}, map[string]any{"x": map[string]any{"z": 1.0}}, map[string]any{"x": map[string]any{"y": 2.0, "z": 1.0}}},
		// type-conflict hotspot: desired map, current scalar → desired map wins, no panic
		{"desired map current scalar", map[string]any{"a": 1.0}, "scalar", map[string]any{"a": 1.0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := merge(tt.desired, tt.cur, false); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("merge = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJSONMergeExpandsHomeToken(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	ctx.Home = "/Users/tester"
	f := filepath.Join(ctx.RepoDir, "hooks.json")
	writeJSON(t, f, map[string]any{})
	s := manifest.JSONMergeStep{File: f, Merge: map[string]any{
		"hooks": map[string]any{"cmd": "${HOME}/.local/bin/muster hook"},
		"keep":  "~/.config/x",
	}}
	if err := h.Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, f).(map[string]any)
	hooks := got["hooks"].(map[string]any)
	if want := "/Users/tester/.local/bin/muster hook"; hooks["cmd"] != want {
		t.Fatalf("cmd = %q, want %q", hooks["cmd"], want)
	}
	if got["keep"] != "~/.config/x" {
		t.Fatalf("bare ~ was rewritten: %q", got["keep"])
	}
	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("second inspect op = %v (%q), want noop", d.Op, d.Detail)
	}
}

// The Daybook switch: a stdio server entry becomes an http one. remove drops
// the keys the old entry had before the merge, so command and args do not
// linger next to url; a second run is a no-op.
func TestJSONMergeRemoveKeys(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, ".mcp.json")
	writeJSON(t, f, map[string]any{"mcpServers": map[string]any{
		"daybook": map[string]any{"command": "scripts/daybook-mcp", "args": []any{}},
		"galley":  map[string]any{"command": "galley"},
	}})
	s := manifest.JSONMergeStep{
		File:   f,
		Remove: []string{"mcpServers.daybook.command", "mcpServers.daybook.args", "mcpServers.gone.command"},
		Merge: map[string]any{"mcpServers": map[string]any{
			"daybook": map[string]any{"type": "http", "url": "https://daybook.example/lm/mcp"},
		}},
	}

	d, err := h.Inspect(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange || !strings.Contains(d.Detail, "remove: mcpServers.daybook.args, mcpServers.daybook.command") {
		t.Fatalf("op = %v (%q), want change naming the removed keys", d.Op, d.Detail)
	}
	if err := h.Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"mcpServers": map[string]any{
		"daybook": map[string]any{"type": "http", "url": "https://daybook.example/lm/mcp"},
		"galley":  map[string]any{"command": "galley"},
	}}
	if got := readJSON(t, f); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if d, _ := h.Inspect(ctx, s); d.Op != engine.OpNoop {
		t.Fatalf("second inspect op = %v (%q), want noop", d.Op, d.Detail)
	}

	// A removed key reappearing (drift) is a change again.
	writeJSON(t, f, map[string]any{"mcpServers": map[string]any{
		"daybook": map[string]any{"type": "http", "url": "https://daybook.example/lm/mcp", "command": "x"},
		"galley":  map[string]any{"command": "galley"},
	}})
	if d, _ := h.Inspect(ctx, s); d.Op != engine.OpChange {
		t.Fatalf("drift inspect op = %v (%q), want change", d.Op, d.Detail)
	}
}

// remove applies before merge, so removing a whole entry and merging it anew
// replaces it, and stays a no-op once applied.
func TestJSONMergeRemoveThenMergeReplacesSubtree(t *testing.T) {
	h := jsonHandler(t)
	ctx := testCtx(t)
	f := filepath.Join(ctx.RepoDir, "c.json")
	writeJSON(t, f, map[string]any{"s": map[string]any{"d": map[string]any{"command": "x", "args": []any{"y"}}}})
	s := manifest.JSONMergeStep{File: f, Remove: []string{"s.d"}, Merge: map[string]any{"s": map[string]any{"d": map[string]any{"url": "u"}}}}
	if err := h.Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"s": map[string]any{"d": map[string]any{"url": "u"}}}
	if got := readJSON(t, f); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if d, _ := h.Inspect(ctx, s); d.Op != engine.OpNoop {
		t.Fatalf("second inspect op = %v (%q), want noop", d.Op, d.Detail)
	}
}
