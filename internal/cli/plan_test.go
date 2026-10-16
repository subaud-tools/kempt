package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
)

// writeLinuxOnlyManifest creates a manifest with a package restricted to linux only,
// and the src file it references. Returns the manifest path.
func writeLinuxOnlyManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := `
[kempt]
spec = 1

[packages.linuxpkg]
description = "linux only package"
[packages.linuxpkg.only]
os = "linux"
  [[packages.linuxpkg.symlink]]
  from = "src/rc"
  to = "~/.rc"
`
	manifestPath := filepath.Join(dir, "kempt.toml")
	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "rc"), []byte("rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}

// TestPlanOSFlagLinuxShowsPackage verifies that -os linux causes a linux-only
// package to appear as planned (not skipped) even when the base OS is darwin.
func TestPlanOSFlagLinuxShowsPackage(t *testing.T) {
	manifestPath := writeLinuxOnlyManifest(t)
	home := t.TempDir()
	// Base context is darwin/arm64.
	withContextHome(t, home, &run.FakeRunner{})

	var out, errw bytes.Buffer
	code := Dispatch([]string{"plan", "-manifest", manifestPath, "-os", "linux", "-arch", "amd64"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	// "(skipped:" only appears when a package is actually skipped; the summary line
	// always has "N skipped" which is fine.
	if strings.Contains(out.String(), "(skipped:") {
		t.Fatalf("-os linux: output should NOT contain '(skipped:', got:\n%s", out.String())
	}
}

// TestPlanBaseDarwinSkipsLinuxPackage verifies that without -os override a
// linux-only package is skipped when newContext reports OS=darwin.
func TestPlanBaseDarwinSkipsLinuxPackage(t *testing.T) {
	manifestPath := writeLinuxOnlyManifest(t)
	home := t.TempDir()
	// withContextHome already sets OS=darwin, so no -os flag needed.
	withContextHome(t, home, &run.FakeRunner{})

	var out, errw bytes.Buffer
	code := Dispatch([]string{"plan", "-manifest", manifestPath}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	// "(skipped:" appears when a package is actually skipped by an only-clause.
	if !strings.Contains(out.String(), "(skipped:") {
		t.Fatalf("darwin base OS: output should contain '(skipped:', got:\n%s", out.String())
	}
}

// TestPlanSavedSelectionExitZero verifies the post-init front-door flow:
// after `kempt init --profile developer` the state has both Profile and Packages
// set. A subsequent plain `kempt plan -manifest ...` must exit 0, not 2.
// The bug was that resolveSelection returned (st.Profile, st.Packages) causing
// engine.Select to reject "both profile and packages".
func TestPlanSavedSelectionExitZero(t *testing.T) {
	// Build a minimal manifest with package "mypkg" in a tempdir.
	dir := t.TempDir()
	manifestContent := `
[kempt]
spec = 1

[packages.mypkg]
description = "my package"
  [[packages.mypkg.symlink]]
  from = "src/rc"
  to = "~/.rc"
`
	manifestPath := filepath.Join(dir, "kempt.toml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create the symlink source so plan can inspect it.
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "rc"), []byte("rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Override loadState to return a state that has BOTH Profile and Packages
	// set (as init would produce), plus the repo dir pointing at our tempdir.
	origLoad := loadState
	loadState = func() (*state.State, bool, error) {
		return &state.State{
			RepoDir:  dir,
			Profile:  "developer",
			Packages: []string{"mypkg"},
		}, true, nil
	}
	t.Cleanup(func() { loadState = origLoad })

	// Override newContext to use a hermetic home so symlink inspection is safe.
	home := t.TempDir()
	withContextHome(t, home, &run.FakeRunner{})

	var out, errw bytes.Buffer
	code := Dispatch([]string{"plan", "-manifest", manifestPath}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (saved-selection should not trigger both-set error)\nstdout: %s\nstderr: %s",
			code, out.String(), errw.String())
	}
}

func TestNewContextAbsolutizesRepoDir(t *testing.T) {
	ctx, err := newContext(".")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(ctx.RepoDir) {
		t.Fatalf("RepoDir = %q, want absolute", ctx.RepoDir)
	}
}
