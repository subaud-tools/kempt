package engine_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// A layer's relative paths resolve against the layer's own root, not the base
// repo: two packages with different roots each link their own file.
func TestBuildPlanResolvesEachPackageAgainstItsRoot(t *testing.T) {
	ctx := buildCtx(t)
	layerRoot := t.TempDir()
	for _, d := range []string{ctx.RepoDir, layerRoot} {
		if err := os.MkdirAll(filepath.Join(d, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "src", "f"), []byte(d), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkgs := []*manifest.Package{
		{Name: "base", Steps: []manifest.Step{manifest.SymlinkStep{From: "src/f", To: "~/base-f"}}},
		{Name: "work/p", Root: layerRoot, Steps: []manifest.Step{manifest.SymlinkStep{From: "src/f", To: "~/work-f"}}},
	}
	plan, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if failed := engine.Execute(ctx, plan, &out); failed != 0 {
		t.Fatalf("failed=%d: %s", failed, out.String())
	}
	for link, root := range map[string]string{"base-f": ctx.RepoDir, "work-f": layerRoot} {
		got, err := os.Readlink(filepath.Join(ctx.Home, link))
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(root, "src", "f"); got != want {
			t.Errorf("%s -> %s; want %s", link, got, want)
		}
	}
	if got := engine.FilterByClass(plan, manifest.ClassFiles).Packages[1].Root; got != layerRoot {
		t.Errorf("FilterByClass dropped the root: %q", got)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func applyPkgs(t *testing.T, pkgs []*manifest.Package) (*engine.Plan, string) {
	t.Helper()
	ctx := buildCtx(t)
	plan, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if failed := engine.Execute(ctx, plan, &out); failed != 0 {
		t.Fatalf("failed=%d: %s", failed, out.String())
	}
	return plan, ctx.Home
}

// The regression layers exist to fix: the base sets settings.json packages with
// arrays = "replace", and a layer adds one. Both must be in the result, and an
// entry nobody declares must still be removed.
func TestFoldReplaceArrayIsUnionOfLayers(t *testing.T) {
	ctx := buildCtx(t)
	file := filepath.Join(ctx.Home, "settings.json")
	if err := os.WriteFile(file, []byte(`{"packages":["npm:stale"],"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgs := []*manifest.Package{
		{Name: "pi", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/settings.json", Arrays: "replace",
			Merge: map[string]any{"packages": []any{"npm:a", "npm:b"}}}}},
		{Name: "work/mkt", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/settings.json", Arrays: "replace",
			Merge: map[string]any{"packages": []any{"git:mkt", "npm:a"}}}}},
	}
	plan, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if failed := engine.Execute(ctx, plan, &out); failed != 0 {
		t.Fatal(out.String())
	}
	got := readJSON(t, file)
	want := []any{"npm:a", "npm:b", "git:mkt"}
	if fmt.Sprint(got["packages"]) != fmt.Sprint(want) {
		t.Errorf("packages = %v; want %v", got["packages"], want)
	}
	if got["theme"] != "dark" {
		t.Errorf("unrelated key lost: %v", got)
	}
	// One folded step, attributed to both layers; the layer's own step is gone.
	if n := len(plan.Packages[0].Steps); n != 1 {
		t.Fatalf("base steps = %d", n)
	}
	if n := len(plan.Packages[1].Steps); n != 0 {
		t.Errorf("layer kept its own merge step (%d); it should be folded into the base's", n)
	}
	if d := plan.Packages[0].Steps[0].Delta.Detail; !strings.Contains(d, "(from base, work)") {
		t.Errorf("detail %q does not name the contributors", d)
	}
	// Idempotent: a second plan is a no-op.
	plan2, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	if plan2.Packages[0].Steps[0].Delta.Op != engine.OpNoop {
		t.Errorf("second plan not a no-op: %+v", plan2.Packages[0].Steps[0].Delta)
	}
}

func TestFoldMergesMapsAndNotesScalarOverride(t *testing.T) {
	plan, home := applyPkgs(t, []*manifest.Package{
		{Name: "pi", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json",
			Merge: map[string]any{"model": "opus", "permission": map[string]any{"read": "allow"}}}}},
		{Name: "work/p", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json",
			Merge: map[string]any{"model": "sonnet", "permission": map[string]any{"schedule": "allow"}}}}},
	})
	got := readJSON(t, filepath.Join(home, "c.json"))
	if got["model"] != "sonnet" {
		t.Errorf("later layer did not win the scalar: %v", got["model"])
	}
	perm, _ := got["permission"].(map[string]any)
	if perm["read"] != "allow" || perm["schedule"] != "allow" {
		t.Errorf("maps not deep-merged: %v", perm)
	}
	if d := plan.Packages[0].Steps[0].Delta.Detail; !strings.Contains(d, "model: work overrides base") {
		t.Errorf("detail %q does not name the override", d)
	}
}

// A layer appending to an array the base owns with replace: the entry joins the
// replace union instead of being removed and re-added on every converge.
func TestFoldAppendJoinsReplaceOwnedArray(t *testing.T) {
	ctx := buildCtx(t)
	pkgs := []*manifest.Package{
		{Name: "pi", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/m.json", Arrays: "replace",
			Merge: map[string]any{"list": []any{"r1"}}}}},
		{Name: "work/p", Layer: "work", Steps: []manifest.Step{
			manifest.JSONMergeStep{File: "~/m.json", Merge: map[string]any{"list": []any{"a1"}, "other": []any{"o1"}}},
		}},
	}
	for run := 1; run <= 2; run++ {
		plan, err := engine.BuildPlan(ctx, pkgs)
		if err != nil {
			t.Fatal(err)
		}
		changes := 0
		for _, pp := range plan.Packages {
			for _, sr := range pp.Steps {
				if sr.Delta.Op == engine.OpChange {
					changes++
				}
			}
		}
		if run == 2 && changes != 0 {
			t.Fatalf("second converge has %d changes: the layers flip-flop", changes)
		}
		var out bytes.Buffer
		if failed := engine.Execute(ctx, plan, &out); failed != 0 {
			t.Fatal(out.String())
		}
	}
	got := readJSON(t, filepath.Join(ctx.Home, "m.json"))
	if fmt.Sprint(got["list"]) != "[r1 a1]" || fmt.Sprint(got["other"]) != "[o1]" {
		t.Errorf("got %v; want list [r1 a1] and the layer's own other [o1]", got)
	}
}

// Both modes from two layers into one key: one replace union, applied after the
// append fold.
func TestFoldMixedModesUnion(t *testing.T) {
	_, home := applyPkgs(t, []*manifest.Package{
		{Name: "pi", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/m.json", Arrays: "replace",
			Merge: map[string]any{"list": []any{"r1"}}}}},
		{Name: "work/p", Layer: "work", Steps: []manifest.Step{
			manifest.JSONMergeStep{File: "~/m.json", Merge: map[string]any{"list": []any{"a1"}}},
			manifest.JSONMergeStep{File: "~/m.json", Arrays: "replace", Merge: map[string]any{"list": []any{"r2"}}},
		}},
	})
	if got := readJSON(t, filepath.Join(home, "m.json"))["list"]; fmt.Sprint(got) != "[r1 r2 a1]" {
		t.Errorf("list = %v; want [r1 r2 a1]", got)
	}
}

// Merges within one layer are not folded: a base-only machine plans exactly as
// before layers existed.
func TestNoFoldWithinOneLayer(t *testing.T) {
	plan, _ := applyPkgs(t, []*manifest.Package{
		{Name: "claude", Steps: []manifest.Step{
			manifest.JSONMergeStep{File: "~/s.json", Merge: map[string]any{"a": 1}},
			manifest.JSONMergeStep{File: "~/s.json", Merge: map[string]any{"b": 2}},
		}},
	})
	if n := len(plan.Packages[0].Steps); n != 2 {
		t.Errorf("steps = %d; want the 2 unfolded", n)
	}
}

func TestExclusiveTargetConflictAcrossLayersIsAnError(t *testing.T) {
	ctx := buildCtx(t)
	_, err := engine.BuildPlan(ctx, []*manifest.Package{
		{Name: "core", Steps: []manifest.Step{manifest.SymlinkStep{From: "a", To: "~/.zshrc"}}},
		{Name: "work/z", Layer: "work", Steps: []manifest.Step{manifest.SymlinkStep{From: "b", To: "~/.zshrc"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "~/.zshrc") || !strings.Contains(err.Error(), "base (core)") || !strings.Contains(err.Error(), "work (work/z)") {
		t.Fatalf("err = %v; want a conflict naming the target and both layers", err)
	}
	_, err = engine.BuildPlan(ctx, []*manifest.Package{
		{Name: "core", Steps: []manifest.Step{manifest.ServiceStep{Label: "tools.x", Program: []string{"x"}}}},
		{Name: "work/s", Layer: "work", Steps: []manifest.Step{manifest.ServiceStep{Label: "tools.x", Program: []string{"y"}}}},
	})
	if err == nil || !strings.Contains(err.Error(), "tools.x") {
		t.Fatalf("service label conflict not reported: %v", err)
	}
}

func TestIdenticalExclusiveTargetKeepsOneCopy(t *testing.T) {
	ctx := buildCtx(t)
	step := manifest.GitCloneStep{Repo: "https://github.com/x/y", To: "~/.y"}
	plan, err := engine.BuildPlan(ctx, []*manifest.Package{
		{Name: "core", Steps: []manifest.Step{step}},
		{Name: "work/y", Layer: "work", Steps: []manifest.Step{step}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(plan.Packages[1].Steps); n != 0 {
		t.Errorf("duplicate clone kept (%d steps)", n)
	}
}

// Two layers pinning one pi entry differently: the later wins, and the plan
// says so.
func TestLaterLayerPinOverridesEarlier(t *testing.T) {
	ctx := buildCtx(t)
	plan, err := engine.BuildPlan(ctx, []*manifest.Package{
		{Name: "pi", Steps: []manifest.Step{manifest.InstallStep{Pi: []string{"npm:pi-hail", "npm:pi-bang"}}}},
		{Name: "work/pin", Layer: "work", Steps: []manifest.Step{manifest.InstallStep{Pi: []string{"npm:pi-hail@0.5.0"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := plan.Packages[0].Steps[0].Step.(manifest.InstallStep)
	if fmt.Sprint(base.Pi) != "[npm:pi-bang]" {
		t.Errorf("base pi = %v; the overridden entry should be dropped", base.Pi)
	}
	var notes bytes.Buffer
	engine.Render(plan, &notes, false)
	if !strings.Contains(notes.String(), "npm:pi-hail@0.5.0 (work overrides base: npm:pi-hail)") {
		t.Errorf("override not reported:\n%s", notes.String())
	}
}

// Two layers appending to a list the base owns with replace: both entries join
// the union, and the folded step stays in the base's package (it is never
// moved to where a folded-away step used to be).
func TestFoldTwoAppendingLayersStayInBase(t *testing.T) {
	plan, home := applyPkgs(t, []*manifest.Package{
		{Name: "pi", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/m.json", Arrays: "replace",
			Merge: map[string]any{"list": []any{"r1"}}}}},
		{Name: "work/p", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/m.json",
			Merge: map[string]any{"list": []any{"w1"}}}}},
		{Name: "home/p", Layer: "home", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/m.json",
			Merge: map[string]any{"list": []any{"h1"}}}}},
	})
	if got := readJSON(t, filepath.Join(home, "m.json"))["list"]; fmt.Sprint(got) != "[r1 w1 h1]" {
		t.Errorf("list = %v", got)
	}
	if n := len(plan.Packages[0].Steps); n != 1 {
		t.Errorf("base package has %d steps; the folded replace step belongs there", n)
	}
	for _, pp := range plan.Packages[1:] {
		if len(pp.Steps) != 0 {
			t.Errorf("%s holds %d steps; its merge was folded into the base's", pp.Name, len(pp.Steps))
		}
	}
}

// A layer's remove paths survive the fold of several layers' merges into one
// file: a later layer removes what an earlier one set (in either arrays mode),
// a later layer re-sets what an earlier one removed, a remove-only step keeps
// the other layers' keys, and an append step absorbed into a replace fold keeps
// its removals. Each case converges: the second plan has no changes.
func TestFoldKeepsRemovePaths(t *testing.T) {
	stdio := `{"s":{"d":{"command":"x","args":[]}}}`
	cases := []struct {
		name, start, want string
		pkgs              []*manifest.Package
	}{
		{"later layer removes what the base sets", "", `map[s:map[d:map[url:u]]]`, []*manifest.Package{
			{Name: "base", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json",
				Merge: map[string]any{"s": map[string]any{"d": map[string]any{"command": "x", "args": []any{}}}}}}},
			{Name: "work/p", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json",
				Remove: []string{"s.d.command", "s.d.args"}, Merge: map[string]any{"s": map[string]any{"d": map[string]any{"url": "u"}}}}}},
		}},
		{"remove-only layer keeps the base's keys", stdio, `map[model:opus s:map[d:map[args:[]]]]`, []*manifest.Package{
			{Name: "base", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json", Merge: map[string]any{"model": "opus"}}}},
			{Name: "work/p", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json", Remove: []string{"s.d.command"}}}},
		}},
		{"absorbed append step keeps its removals", stdio, `map[list:[r1 a1] s:map[d:map[args:[]]]]`, []*manifest.Package{
			{Name: "base", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json", Arrays: "replace",
				Merge: map[string]any{"list": []any{"r1"}}}}},
			{Name: "work/p", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json",
				Remove: []string{"s.d.command"}, Merge: map[string]any{"list": []any{"a1"}}}}},
		}},
		{"later append layer removes what a replace base sets", "", `map[list:[r1] s:map[d:map[url:u]]]`, []*manifest.Package{
			{Name: "base", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json", Arrays: "replace",
				Merge: map[string]any{"list": []any{"r1"}, "s": map[string]any{"d": map[string]any{"command": "x", "args": []any{}}}}}}},
			{Name: "work/p", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json",
				Remove: []string{"s.d.command", "s.d.args"}, Merge: map[string]any{"s": map[string]any{"d": map[string]any{"url": "u"}}}}}},
		}},
		{"later append layer sets what a replace base removes", stdio, `map[list:[r1] s:map[d:map[command:y]]]`, []*manifest.Package{
			{Name: "base", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json", Arrays: "replace",
				Remove: []string{"s.d"}, Merge: map[string]any{"list": []any{"r1"}}}}},
			{Name: "work/p", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json",
				Merge: map[string]any{"s": map[string]any{"d": map[string]any{"command": "y"}}}}}},
		}},
		{"replace base rebuilds an entry a later append layer adds to", stdio, `map[list:[r1] s:map[d:map[type:http url:u]]]`, []*manifest.Package{
			{Name: "base", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json", Arrays: "replace",
				Remove: []string{"s.d.url", "s.d"}, Merge: map[string]any{"list": []any{"r1"}, "s": map[string]any{"d": map[string]any{"type": "http"}}}}}},
			{Name: "work/p", Layer: "work", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/c.json",
				Merge: map[string]any{"s": map[string]any{"d": map[string]any{"url": "u"}}}}}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := buildCtx(t)
			f := filepath.Join(ctx.Home, "c.json")
			if tc.start != "" {
				if err := os.WriteFile(f, []byte(tc.start), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for run := 1; run <= 2; run++ {
				plan, err := engine.BuildPlan(ctx, tc.pkgs)
				if err != nil {
					t.Fatal(err)
				}
				for _, pp := range plan.Packages {
					for _, sr := range pp.Steps {
						if run == 2 && sr.Delta.Op == engine.OpChange {
							t.Fatalf("second converge still changes: %s", sr.Delta.Detail)
						}
					}
				}
				var out bytes.Buffer
				if failed := engine.Execute(ctx, plan, &out); failed != 0 {
					t.Fatalf("failed=%d: %s", failed, out.String())
				}
			}
			if got := fmt.Sprint(readJSON(t, f)); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
