package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
)

// refreshManifest has BOTH a software step (install brew) and a files step
// (symlink) in one package — the split-policy invariant is exercised here.
const refreshManifest = `
[kempt]
spec = 1

[packages.core]
description = "core"
  [[packages.core.install]]
  brew = { formulas = ["foo"] }
  [[packages.core.symlink]]
  from = "src/rc"
  to = "~/.rc"
`

// brewResponses scripts a brew that exists but has nothing installed, so "foo"
// is missing → the install step inspects to OpChange (software pending).
func brewResponses(repo string) map[string]run.Response {
	return map[string]run.Response{
		"lookpath brew":                     {Stdout: "/opt/homebrew/bin/brew"},
		"brew list --formula -1":            {Stdout: ""},
		"brew list --cask -1":               {Stdout: ""},
		"brew tap":                          {Stdout: ""},
		"git -C " + repo + " fetch --quiet": {Stdout: ""},
		"git -C " + repo + " rev-list --count HEAD..@{u}": {Stdout: "0"},
	}
}

// setupRefresh writes the manifest+source into a repo tempdir, points
// loadState/saveState + statusStore at tempdir stores, pins now, and installs a
// newContext returning a Context with the given FakeRunner and a tempdir Home.
// It returns (statusStore dir, home, repo).
func setupRefresh(t *testing.T, autoApply bool, r *run.FakeRunner) (statusDir, home, repo string) {
	t.Helper()
	repo = t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(refreshManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "rc"), []byte("rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home = t.TempDir()
	statusDir = t.TempDir()

	stateStore := &state.Store{Dir: t.TempDir()}
	if err := stateStore.Save(&state.State{RepoDir: repo, Packages: []string{"core"}, AutoApplyFiles: autoApply}); err != nil {
		t.Fatal(err)
	}

	origLoad, origSave, origStatus, origCtx, origNow := loadState, saveState, statusStore, newContext, now
	loadState = func() (*state.State, bool, error) { return stateStore.Load() }
	saveState = func(s *state.State) error { return stateStore.Save(s) }
	statusStore = func() (*state.Store, error) { return &state.Store{Dir: statusDir}, nil }
	newContext = func(repoDir string) (*machine.Context, error) {
		return &machine.Context{
			Home:    home,
			RepoDir: repoDir,
			OS:      "darwin",
			Arch:    "arm64",
			Runner:  r,
			Cache:   map[string]string{},
		}, nil
	}
	now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() {
		loadState, saveState, statusStore, newContext, now = origLoad, origSave, origStatus, origCtx, origNow
	})
	return statusDir, home, repo
}

// TestRefreshSplitPolicyAppliesFilesNeverSoftware is the phase's key safety
// test: with AutoApplyFiles=true over a manifest containing both a files and a
// software change, the files change is applied (symlink created) while the
// software change is never executed (no "brew install" ever runs) and remains
// pending in the cache.
func TestRefreshSplitPolicyAppliesFilesNeverSoftware(t *testing.T) {
	r := &run.FakeRunner{}
	statusDir, home, repo := setupRefresh(t, true, r)
	r.Responses = brewResponses(repo)

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"refresh"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}

	// (a) files-class step applied: the symlink now exists.
	link := filepath.Join(home, ".rc")
	if target, err := os.Readlink(link); err != nil {
		t.Fatalf("symlink not created: %v", err)
	} else if want := filepath.Join(repo, "src", "rc"); target != want {
		t.Fatalf("link target = %q, want %q", target, want)
	}

	// (b) software never applied: no "brew install" command was ever recorded.
	for _, c := range r.Calls {
		if strings.HasPrefix(c, "brew install") {
			t.Fatalf("brew install was executed (calls=%v)", r.Calls)
		}
	}

	// (c) cache records software still pending, files now zero.
	store := &state.Store{Dir: statusDir}
	st, existed, err := store.LoadStatus()
	if err != nil || !existed {
		t.Fatalf("status not written: existed=%v err=%v", existed, err)
	}
	if st.SoftwareChanges < 1 {
		t.Fatalf("SoftwareChanges = %d, want >= 1 (still pending)", st.SoftwareChanges)
	}
	if st.FileChanges != 0 {
		t.Fatalf("FileChanges = %d, want 0 after auto-apply", st.FileChanges)
	}
	if !st.CheckedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("CheckedAt = %v, want pinned now", st.CheckedAt)
	}
}

// TestRefreshNoAutoApplyCountsBoth: without auto-apply, both file and software
// changes stay pending and nothing is applied.
func TestRefreshNoAutoApplyCountsBoth(t *testing.T) {
	r := &run.FakeRunner{}
	statusDir, home, repo := setupRefresh(t, false, r)
	r.Responses = brewResponses(repo)

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"refresh"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errw.String())
	}
	// No symlink created.
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err == nil {
		t.Fatalf("symlink should not be created without auto-apply")
	}
	store := &state.Store{Dir: statusDir}
	st, _, _ := store.LoadStatus()
	if st.FileChanges != 1 || st.SoftwareChanges != 1 {
		t.Fatalf("counts = file %d, software %d; want 1,1", st.FileChanges, st.SoftwareChanges)
	}
}

// TestRefreshFetchErrorStillWritesCache: a fetch failure must not blank the
// cache; refresh plans against the local checkout and still writes status.
func TestRefreshFetchErrorStillWritesCache(t *testing.T) {
	r := &run.FakeRunner{}
	statusDir, _, repo := setupRefresh(t, false, r)
	resp := brewResponses(repo)
	resp["git -C "+repo+" fetch --quiet"] = run.Response{Err: fmt.Errorf("network down")}
	r.Responses = resp

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"refresh"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errw.String())
	}
	store := &state.Store{Dir: statusDir}
	_, existed, err := store.LoadStatus()
	if err != nil || !existed {
		t.Fatalf("cache not written after fetch error: existed=%v err=%v", existed, err)
	}
	if !strings.Contains(out.String(), "fetch failed") {
		t.Fatalf("summary missing degradation note: %q", out.String())
	}
}

func TestRefreshNoStateIsUsageError(t *testing.T) {
	origLoad := loadState
	loadState = func() (*state.State, bool, error) { return &state.State{}, false, nil }
	t.Cleanup(func() { loadState = origLoad })
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"refresh"}, &out, &errw); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errw.String(), "kempt init") {
		t.Fatalf("stderr = %q, want init hint", errw.String())
	}
}
