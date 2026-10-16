package layers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/state"
)

const baseSrc = `
[kempt]
spec = 1
[packages.core]
[packages.pi]
needs = ["core"]
[packages.zsh]
`

func writeLayer(t *testing.T, src string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kempt.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func parse(t *testing.T, src string) *manifest.Manifest {
	t.Helper()
	m, f := manifest.Parse([]byte(src))
	if len(f) > 0 {
		t.Fatal(f)
	}
	return m
}

func TestComposeNamespacesAndResolvesNeeds(t *testing.T) {
	dir := writeLayer(t, `
[kempt]
spec = 1
layer = "user"
[packages.mkt]
needs = ["pi", "helper"]
[packages.helper]
`)
	res := Load([]state.Layer{{Name: "work", Scope: "user", Source: state.LayerSource{Kind: "path", Dir: dir}, Packages: []string{"mkt"}}}, "")
	if len(res.Loaded) != 1 || len(res.Skipped)+len(res.Held) != 0 || len(res.Findings) != 0 {
		t.Fatalf("load: %+v", res)
	}
	m, sel, err := Compose(parse(t, baseSrc), []string{"zsh", "pi"}, res.Loaded)
	if err != nil {
		t.Fatal(err)
	}
	mkt := m.Packages["work/mkt"]
	if mkt == nil || mkt.Layer != "work" || mkt.Root != dir {
		t.Fatalf("work/mkt = %+v", mkt)
	}
	if got := strings.Join(mkt.Needs, ","); got != "pi,work/helper" {
		t.Errorf("needs = %s; want the base's pi and the layer's own helper", got)
	}
	if strings.Join(sel, ",") != "zsh,pi,work/mkt" {
		t.Errorf("selection = %v", sel)
	}
	pkgs, err := engine.Select(m, "", sel)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range Order(pkgs, res.Loaded) {
		names = append(names, p.Name)
	}
	// Base first (topologically), then the layer: never alphabetical across layers.
	if got := strings.Join(names, ","); got != "core,pi,zsh,work/helper,work/mkt" {
		t.Errorf("order = %s", got)
	}
}

func TestComposeRejectsUnknownNeedAndUnknownPackage(t *testing.T) {
	dir := writeLayer(t, "[kempt]\nspec = 1\nlayer = \"user\"\n[packages.a]\nneeds = [\"nope\"]\n")
	res := Load([]state.Layer{{Name: "w", Scope: "user", Source: state.LayerSource{Kind: "path", Dir: dir}, Packages: []string{"a"}}}, "")
	if _, _, err := Compose(parse(t, baseSrc), []string{"core"}, res.Loaded); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("unknown need: err = %v", err)
	}
	clean := writeLayer(t, "[kempt]\nspec = 1\nlayer = \"user\"\n[packages.a]\n")
	res = Load([]state.Layer{{Name: "w", Scope: "user", Source: state.LayerSource{Kind: "path", Dir: clean}, Packages: []string{"missing"}}}, "")
	if _, _, err := Compose(parse(t, baseSrc), []string{"core"}, res.Loaded); err == nil || !strings.Contains(err.Error(), `w/missing`) {
		t.Errorf("unknown package: err = %v", err)
	}
}

// A missing source is skipped with a reason; the rest still loads.
func TestLoadSkipsMissingSource(t *testing.T) {
	ok := writeLayer(t, "[kempt]\nspec = 1\nlayer = \"user\"\n[packages.a]\n")
	res := Load([]state.Layer{
		{Name: "gone", Scope: "user", Source: state.LayerSource{Kind: "path", Dir: filepath.Join(ok, "nope")}},
		{Name: "here", Scope: "user", Source: state.LayerSource{Kind: "path", Dir: ok}, Packages: []string{"a"}},
	}, "")
	if len(res.Loaded) != 1 || res.Loaded[0].Layer.Name != "here" {
		t.Fatalf("loaded = %+v", res.Loaded)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Name != "gone" || !strings.Contains(res.Skipped[0].Reason, "not found") {
		t.Errorf("skipped = %+v", res.Skipped)
	}
}

// A project-added layer whose file changed since it was applied is held.
func TestLoadHoldsChangedProjectLayer(t *testing.T) {
	dir := writeLayer(t, "[kempt]\nspec = 1\nlayer = \"project\"\n[packages.dev]\n")
	file := filepath.Join(dir, "kempt.toml")
	approved, err := FileHash(file)
	if err != nil {
		t.Fatal(err)
	}
	l := state.Layer{Name: "galley", Scope: "project", Project: dir, Approved: approved,
		Source: state.LayerSource{Kind: "path", Dir: dir}, Packages: []string{"dev"}}
	if res := Load([]state.Layer{l}, ""); len(res.Loaded) != 1 || len(res.Held) != 0 {
		t.Fatalf("unchanged layer not loaded: %+v", res)
	}
	if err := os.WriteFile(file, []byte("[kempt]\nspec = 1\nlayer = \"project\"\n[packages.dev]\ndescription = \"changed\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Load([]state.Layer{l}, "")
	if len(res.Loaded) != 0 || len(res.Held) != 1 || res.Held[0].Name != "galley" {
		t.Errorf("changed project layer not held: %+v", res)
	}
}

// The file's own scope must match how the layer was added, and a base manifest
// (no layer scope) is not a layer.
func TestLoadRejectsScopeMismatch(t *testing.T) {
	for _, src := range []string{"[kempt]\nspec = 1\n", "[kempt]\nspec = 1\nlayer = \"project\"\n"} {
		dir := writeLayer(t, src)
		res := Load([]state.Layer{{Name: "w", Scope: "user", Source: state.LayerSource{Kind: "path", Dir: dir}}}, "")
		if len(res.Findings["w"]) == 0 {
			t.Errorf("%q: no scope finding", src)
		}
	}
}

// A project layer resolves against its checkout; a user layer against the
// layer file's directory.
func TestRoots(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".kempt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".kempt", "kempt.toml"), []byte("[kempt]\nspec = 1\nlayer = \"project\"\n[packages.dev]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Load([]state.Layer{{Name: "p", Scope: "project", Project: proj, Approved: mustHash(t, filepath.Join(proj, ".kempt", "kempt.toml")),
		Source: state.LayerSource{Kind: "path", Dir: proj, File: ".kempt/kempt.toml"}, Packages: []string{"dev"}}}, "")
	if len(res.Loaded) != 1 || res.Loaded[0].Root != proj {
		t.Errorf("project root = %+v", res.Loaded)
	}
}

func mustHash(t *testing.T, p string) string {
	t.Helper()
	h, err := FileHash(p)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// A project-added layer with no recorded approval was never accepted: held.
func TestLoadHoldsUnapprovedProjectLayer(t *testing.T) {
	dir := writeLayer(t, "[kempt]\nspec = 1\nlayer = \"project\"\n[packages.dev]\n")
	res := Load([]state.Layer{{Name: "p", Scope: "project", Project: dir, Source: state.LayerSource{Kind: "path", Dir: dir}}}, "")
	if len(res.Held) != 1 || len(res.Loaded) != 0 {
		t.Errorf("unapproved project layer loaded: %+v", res)
	}
}
