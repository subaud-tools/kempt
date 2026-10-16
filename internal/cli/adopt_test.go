package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/state"
)

const adoptManifest = `
[kempt]
spec = 1

[packages.base]
description = "base"

[packages.a]
description = "a"
needs = ["base"]

[packages.b]
description = "b"
`

// withAdoptEnv writes a manifest into a tempdir repo, points loadState/saveState
// at a tempdir store seeded with the given saved packages, and returns the store.
func withAdoptEnv(t *testing.T, saved []string) (*state.Store, string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(adoptManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &state.Store{Dir: t.TempDir()}
	st := &state.State{RepoDir: repo, Packages: saved}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	origLoad, origSave := loadState, saveState
	loadState = func() (*state.State, bool, error) { return store.Load() }
	saveState = func(s *state.State) error { return store.Save(s) }
	t.Cleanup(func() { loadState, saveState = origLoad, origSave })
	return store, repo
}

func TestAdoptPullsNeedsClosure(t *testing.T) {
	store, _ := withAdoptEnv(t, []string{"b"})
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"adopt", "a"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errw.String())
	}
	st, _, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(st.Packages, ",")
	if got != "a,b,base" {
		t.Fatalf("packages = %q, want a,b,base", got)
	}
	if !strings.Contains(out.String(), "adopted a") || !strings.Contains(out.String(), "base") {
		t.Fatalf("stdout = %q, want adopted a (+ deps: base)", out.String())
	}
}

func TestAdoptNoDeps(t *testing.T) {
	store, _ := withAdoptEnv(t, nil)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"adopt", "b"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errw.String())
	}
	st, _, _ := store.Load()
	if strings.Join(st.Packages, ",") != "b" {
		t.Fatalf("packages = %v, want [b]", st.Packages)
	}
	if strings.Contains(out.String(), "deps") {
		t.Fatalf("stdout should not mention deps: %q", out.String())
	}
}

func TestAdoptUnknownPackage(t *testing.T) {
	withAdoptEnv(t, nil)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"adopt", "nope"}, &out, &errw); code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
}

func TestAdoptNoState(t *testing.T) {
	origLoad := loadState
	loadState = func() (*state.State, bool, error) { return &state.State{}, false, nil }
	t.Cleanup(func() { loadState = origLoad })
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"adopt", "a"}, &out, &errw); code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(errw.String(), "kempt init") {
		t.Fatalf("stderr = %q, want init hint", errw.String())
	}
}

func TestAdoptWrongArgCount(t *testing.T) {
	withAdoptEnv(t, nil)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"adopt"}, &out, &errw); code != 2 {
		t.Fatalf("adopt no arg: exit = %d, want 2", code)
	}
	if code := Dispatch([]string{"adopt", "a", "b"}, &out, &errw); code != 2 {
		t.Fatalf("adopt two args: exit = %d, want 2", code)
	}
}

func TestDropRemoves(t *testing.T) {
	store, _ := withAdoptEnv(t, []string{"b", "base"})
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"drop", "b"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errw.String())
	}
	st, _, _ := store.Load()
	if strings.Join(st.Packages, ",") != "base" {
		t.Fatalf("packages = %v, want [base]", st.Packages)
	}
	if !strings.Contains(out.String(), "dropped b") {
		t.Fatalf("stdout = %q, want dropped b", out.String())
	}
}

func TestDropNotInSelection(t *testing.T) {
	withAdoptEnv(t, []string{"base"}) // "b" is not in the saved selection
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"drop", "b"}, &out, &errw); code != 2 {
		t.Fatalf("exit = %d, want 2 (usage); err=%s", code, errw.String())
	}
	if !strings.Contains(errw.String(), "not in the current selection") {
		t.Fatalf("stderr = %q, want 'not in the current selection'", errw.String())
	}
}

func TestDropRefusedWhenNeeded(t *testing.T) {
	withAdoptEnv(t, []string{"a", "base"})
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"drop", "base"}, &out, &errw); code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(errw.String(), "a") {
		t.Fatalf("stderr = %q, want mention of a", errw.String())
	}
}
