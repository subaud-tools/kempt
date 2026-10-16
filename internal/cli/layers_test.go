package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/layers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
	tools "github.com/subaud-tools/tools-common"
)

// The base owns settings.json packages with arrays = "replace".
const layeredBase = `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.json-merge]]
  file = "~/settings.json"
  arrays = "replace"
  merge = { packages = ["npm:a"] }
`

const workLayer = `
[kempt]
spec = 1
layer = "user"
[packages.mkt]
  [[packages.mkt.json-merge]]
  file = "~/settings.json"
  arrays = "replace"
  merge = { packages = ["git:mkt"] }
`

// layeredMachine saves a state with the base repo and one work layer, and
// points the CLI's state and context seams at temp dirs.
func layeredMachine(t *testing.T, layerSrc string, extra ...state.Layer) (home, repo, layerDir string, store *state.Store, fr *run.FakeRunner) {
	t.Helper()
	repo, home, layerDir = t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(layeredBase), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "kempt.toml"), []byte(layerSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	store = &state.Store{Dir: t.TempDir()}
	st := &state.State{RepoDir: repo, Packages: []string{"pi"}, Layers: append([]state.Layer{
		{Name: "work", Scope: "user", Source: state.LayerSource{Kind: "path", Dir: layerDir}, Packages: []string{"mkt"}},
	}, extra...)}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	fr = &run.FakeRunner{Responses: map[string]run.Response{
		"git -C " + repo + " pull --rebase --autostash": {},
	}}
	origLoad, origSave, origCtx, origSelf := loadState, saveState, newContext, selfUpdate
	loadState = func() (*state.State, bool, error) { return store.Load() }
	saveState = func(s *state.State) error { return store.Save(s) }
	newContext = func(dir string) (*machine.Context, error) {
		return &machine.Context{Home: home, RepoDir: dir, OS: "darwin", Arch: "arm64", Runner: fr,
			Releases: release.FakeReleases{}, Cache: map[string]string{}}, nil
	}
	selfUpdate = func(*tools.App, io.Writer, io.Writer) (bool, string, error) { return false, "dev", nil }
	t.Cleanup(func() { loadState, saveState, newContext, selfUpdate = origLoad, origSave, origCtx, origSelf })
	return
}

func settingsPackages(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct{ Packages []string }
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return strings.Join(v.Packages, ",")
}

// The regression layers fix: a layer's entry in a replace-owned array survives
// every converge, apply and update alike.
func TestLayerAdditionSurvivesApplyAndUpdate(t *testing.T) {
	home, _, _, _, _ := layeredMachine(t, workLayer)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("apply exit %d: %s %s", code, out.String(), errw.String())
	}
	if got := settingsPackages(t, home); got != "npm:a,git:mkt" {
		t.Fatalf("after apply: %s", got)
	}
	for i := 0; i < 2; i++ {
		out.Reset()
		if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
			t.Fatalf("update exit %d: %s %s", code, out.String(), errw.String())
		}
		if got := settingsPackages(t, home); got != "npm:a,git:mkt" {
			t.Fatalf("after update %d: %s\n%s", i+1, got, out.String())
		}
	}
}

// An explicit -manifest (or -packages) is a one-off run on that manifest only.
func TestExplicitSelectionIgnoresLayers(t *testing.T) {
	home, repo, _, _, _ := layeredMachine(t, workLayer)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes", "-manifest", filepath.Join(repo, "kempt.toml"), "-packages", "pi"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errw.String())
	}
	if got := settingsPackages(t, home); got != "npm:a" {
		t.Errorf("layers applied to an explicit selection: %s", got)
	}
}

func TestPlanNamesLayerPackagesAndReportsSkippedAndHeld(t *testing.T) {
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".kempt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".kempt", "kempt.toml"), []byte("[kempt]\nspec = 1\nlayer = \"project\"\n[packages.dev]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layeredMachine(t, workLayer,
		state.Layer{Name: "gone", Scope: "user", Source: state.LayerSource{Kind: "path", Dir: "/nonexistent/kempt-layer"}},
		state.Layer{Name: "galley", Scope: "project", Project: proj, Approved: "sha256:stale",
			Source: state.LayerSource{Kind: "path", Dir: proj, File: ".kempt/kempt.toml"}},
	)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"plan"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errw.String())
	}
	for _, want := range []string{
		"package pi\n",
		"(from base, work)",
		"skipped layer gone: /nonexistent/kempt-layer/kempt.toml not found\n",
		"held galley (layer changed since you applied it): run kempt layer apply galley\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan output missing %q:\n%s", want, out.String())
		}
	}
}

func TestLayerFindingsFailTheRun(t *testing.T) {
	layeredMachine(t, "[kempt]\nspec = 1\n[packages.mkt]\n")
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"plan"}, &out, &errw); code == 0 {
		t.Fatalf("a layer with findings did not fail the plan: %s", out.String())
	}
	if !strings.Contains(errw.String(), "layer work") || !strings.Contains(errw.String(), "kempt.layer") {
		t.Errorf("findings not reported: %s", errw.String())
	}
}

var _ = layers.DefaultFile

// machineWithoutLayers is layeredMachine with an empty layer list.
func machineWithoutLayers(t *testing.T) (home, repo string, store *state.Store, fr *run.FakeRunner) {
	t.Helper()
	home, repo, _, store, fr = layeredMachine(t, workLayer)
	st, _, _ := store.Load()
	st.Layers = nil
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	return
}

func layerDirWith(t *testing.T, rel, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func savedLayers(t *testing.T, store *state.Store) []state.Layer {
	t.Helper()
	st, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return st.Layers
}

func TestLayerAddPathAppliesAndSaves(t *testing.T) {
	home, _, store, _ := machineWithoutLayers(t)
	dir := layerDirWith(t, "kempt.toml", workLayer)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"layer", "add", "work", dir, "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errw.String())
	}
	ls := savedLayers(t, store)
	if len(ls) != 1 || ls[0].Name != "work" || ls[0].Scope != "user" || ls[0].Source.Kind != "path" || ls[0].Source.Dir != dir {
		t.Fatalf("saved layers = %+v", ls)
	}
	if got := settingsPackages(t, home); got != "npm:a,git:mkt" {
		t.Errorf("settings = %s", got)
	}
	if !strings.Contains(out.String(), "added layer work (user, path)") {
		t.Errorf("output: %s", out.String())
	}
}

// Declining the plan leaves the machine's layers as they were.
func TestLayerAddDeclinedKeepsState(t *testing.T) {
	home, _, store, _ := machineWithoutLayers(t)
	dir := layerDirWith(t, "kempt.toml", workLayer)
	orig := stdin
	stdin = strings.NewReader("n\n")
	t.Cleanup(func() { stdin = orig })
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"layer", "add", "work", dir}, &out, &errw); code == 0 {
		t.Fatalf("declined add exited 0: %s", out.String())
	}
	if ls := savedLayers(t, store); len(ls) != 0 {
		t.Errorf("declined add saved %+v", ls)
	}
	if _, err := os.Stat(filepath.Join(home, "settings.json")); err == nil {
		t.Error("declined add changed the machine")
	}
}

func TestLayerAddRejectsBadInput(t *testing.T) {
	_, _, _, _ = machineWithoutLayers(t)
	good := layerDirWith(t, "kempt.toml", workLayer)
	project := layerDirWith(t, "kempt.toml", "[kempt]\nspec = 1\nlayer = \"project\"\n[packages.dev]\n")
	base := layerDirWith(t, "kempt.toml", "[kempt]\nspec = 1\n[packages.x]\n")
	cases := map[string][]string{
		"slash in name":                 {"layer", "add", "a/b", good, "-yes"},
		"project file without -project": {"layer", "add", "p", project, "-yes"},
		"base manifest is not a layer":  {"layer", "add", "b", base, "-yes"},
		"missing source":                {"layer", "add", "m", filepath.Join(good, "nope"), "-yes"},
	}
	for name, args := range cases {
		var out, errw bytes.Buffer
		if code := Dispatch(args, &out, &errw); code == 0 {
			t.Errorf("%s: exited 0: %s", name, out.String())
		}
	}
	var out, errw bytes.Buffer
	Dispatch([]string{"layer", "add", "work", good, "-yes"}, &out, &errw)
	if code := Dispatch([]string{"layer", "add", "work", good, "-yes"}, &out, &errw); code == 0 {
		t.Error("duplicate layer name accepted")
	}
}

// A git source reuses an existing checkout whose origin matches, as init does.
func TestLayerAddGitReusesMatchingCheckout(t *testing.T) {
	_, _, store, fr := machineWithoutLayers(t)
	dir := layerDirWith(t, "layers/work/kempt.toml", workLayer)
	url := "https://github.com/me/private.git"
	fr.Responses["git -C "+dir+" remote get-url origin"] = run.Response{Stdout: url + "\n"}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"layer", "add", "work", url, "-dir", dir, "-file", "layers/work/kempt.toml", "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errw.String())
	}
	ls := savedLayers(t, store)
	if len(ls) != 1 || ls[0].Source.Kind != "git" || ls[0].Source.URL != url || ls[0].Source.File != "layers/work/kempt.toml" {
		t.Fatalf("saved = %+v", ls)
	}
	fr.Responses["git -C "+dir+" remote get-url origin"] = run.Response{Stdout: "https://github.com/other/repo.git\n"}
	if code := Dispatch([]string{"layer", "add", "w2", url, "-dir", dir, "-file", "layers/work/kempt.toml", "-yes"}, &out, &errw); code == 0 {
		t.Error("checkout with a different origin accepted")
	}
}

// A project layer: named after its directory, relative paths resolve inside the
// checkout (the apply re-check included), and its hash is recorded; a later
// change is held until `layer apply`.
func TestLayerAddProjectAndHoldUntilApply(t *testing.T) {
	_, _, store, _ := machineWithoutLayers(t)
	proj := layerDirWith(t, ".kempt/kempt.toml", `
[kempt]
spec = 1
layer = "project"
[packages.dev]
  [[packages.dev.symlink]]
  from = "config/tool.json"
  to = ".tool.json"
`)
	proj = filepath.Join(filepath.Dir(proj), filepath.Base(proj))
	if err := os.MkdirAll(filepath.Join(proj, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "config", "tool.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"layer", "add", "-project", proj, "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errw.String())
	}
	if target, err := os.Readlink(filepath.Join(proj, ".tool.json")); err != nil || target != filepath.Join(proj, "config", "tool.json") {
		t.Fatalf("project link = %q, %v", target, err)
	}
	ls := savedLayers(t, store)
	if len(ls) != 1 || ls[0].Name != filepath.Base(proj) || ls[0].Scope != "project" || ls[0].Project != proj || !strings.HasPrefix(ls[0].Approved, "sha256:") {
		t.Fatalf("saved = %+v", ls)
	}
	name := ls[0].Name
	if err := os.WriteFile(filepath.Join(proj, ".kempt", "kempt.toml"), []byte("[kempt]\nspec = 1\nlayer = \"project\"\n[packages.dev]\ndescription = \"changed\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	Dispatch([]string{"plan"}, &out, &errw)
	if !strings.Contains(out.String(), "held "+name+" (layer changed since you applied it)") {
		t.Errorf("changed project layer not held:\n%s", out.String())
	}
	out.Reset()
	if code := Dispatch([]string{"layer", "apply", name, "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("layer apply exit %d: %s %s", code, out.String(), errw.String())
	}
	out.Reset()
	Dispatch([]string{"plan"}, &out, &errw)
	if strings.Contains(out.String(), "held ") {
		t.Errorf("still held after layer apply:\n%s", out.String())
	}
}

func TestLayerListAndRemove(t *testing.T) {
	_, _, _, store, _ := layeredMachine(t, workLayer)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"layer", "list"}, &out, &errw); code != 0 {
		t.Fatal(errw.String())
	}
	if !strings.Contains(out.String(), "work") || !strings.Contains(out.String(), "user") || !strings.Contains(out.String(), "path") || !strings.Contains(out.String(), "mkt") {
		t.Errorf("list = %s", out.String())
	}
	out.Reset()
	if code := Dispatch([]string{"layer", "remove", "work"}, &out, &errw); code != 0 {
		t.Fatal(errw.String())
	}
	if ls := savedLayers(t, store); len(ls) != 0 {
		t.Errorf("not removed: %+v", ls)
	}
	if !strings.Contains(out.String(), "nothing was uninstalled") {
		t.Errorf("remove output: %s", out.String())
	}
	if code := Dispatch([]string{"layer", "remove", "work"}, &out, &errw); code == 0 {
		t.Error("removing an unknown layer exited 0")
	}
}

// update pulls git user layers, never a project checkout, and a failed layer
// pull is a warning.
func TestUpdatePullsGitUserLayers(t *testing.T) {
	proj := layerDirWith(t, ".kempt/kempt.toml", "[kempt]\nspec = 1\nlayer = \"project\"\n[packages.dev]\n")
	gitDir := layerDirWith(t, "kempt.toml", workLayer)
	brokenDir := layerDirWith(t, "kempt.toml", "[kempt]\nspec = 1\nlayer = \"user\"\n[packages.b]\n")
	_, _, _, _, fr := layeredMachine(t, workLayer,
		state.Layer{Name: "shared", Scope: "user", Source: state.LayerSource{Kind: "git", URL: "u", Dir: gitDir}},
		state.Layer{Name: "flaky", Scope: "user", Source: state.LayerSource{Kind: "git", URL: "v", Dir: brokenDir}},
		state.Layer{Name: "proj", Scope: "project", Project: proj, Source: state.LayerSource{Kind: "git", Dir: proj, File: ".kempt/kempt.toml"}},
	)
	fr.Responses["git -C "+gitDir+" pull --rebase --autostash"] = run.Response{}
	fr.Responses["git -C "+brokenDir+" pull --rebase --autostash"] = run.Response{Err: run.ErrForTest}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), errw.String())
	}
	pulled := strings.Join(fr.Calls, "\n")
	if !strings.Contains(pulled, "git -C "+gitDir+" pull") {
		t.Error("git user layer not pulled")
	}
	if strings.Contains(pulled, "git -C "+proj+" pull") {
		t.Error("project checkout was pulled")
	}
	if !strings.Contains(out.String(), "could not pull layer flaky") {
		t.Errorf("failed layer pull not reported:\n%s", out.String())
	}
}

func TestAdoptAndDropInALayer(t *testing.T) {
	_, _, layerDir, store, _ := layeredMachine(t, workLayer+"\n[packages.extra]\n")
	_ = layerDir
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"adopt", "-layer", "work", "extra"}, &out, &errw); code != 0 {
		t.Fatalf("adopt: %s %s", out.String(), errw.String())
	}
	if got := strings.Join(savedLayers(t, store)[0].Packages, ","); got != "extra,mkt" {
		t.Errorf("after adopt: %s", got)
	}
	if code := Dispatch([]string{"adopt", "-layer", "work", "nope"}, &out, &errw); code == 0 {
		t.Error("adopted an unknown package")
	}
	if code := Dispatch([]string{"drop", "-layer", "work", "mkt"}, &out, &errw); code != 0 {
		t.Fatalf("drop: %s %s", out.String(), errw.String())
	}
	if got := strings.Join(savedLayers(t, store)[0].Packages, ","); got != "extra" {
		t.Errorf("after drop: %s", got)
	}
}

// After a layered apply, doctor sees the union: the layer's entry in a
// replace-owned array is not extra-array drift, and a layer's relative symlink
// resolves against the layer's root.
func TestDoctorSeesLayers(t *testing.T) {
	src := workLayer + `
  [[packages.mkt.symlink]]
  from = "cfg/skills"
  to = "~/.skills"
`
	_, _, layerDir, _, _ := layeredMachine(t, src)
	if err := os.MkdirAll(filepath.Join(layerDir, "cfg", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("apply: %s %s", out.String(), errw.String())
	}
	out.Reset()
	Dispatch([]string{"doctor", "-json"}, &out, &errw)
	for _, code := range []string{"extra-array", "symlink"} {
		if strings.Contains(out.String(), `"check": "`+code+`"`) {
			t.Errorf("doctor reported %s on a converged layered machine:\n%s", code, out.String())
		}
	}
	out.Reset()
	if code := Dispatch([]string{"verify"}, &out, &errw); code != 0 {
		t.Errorf("verify on a layered machine: %s %s", out.String(), errw.String())
	}
}
