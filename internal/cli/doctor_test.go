package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
)

func TestDoctorRegistered(t *testing.T) {
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"commands", "--json"}, &out, &errw); code != 0 {
		t.Fatalf("commands exit=%d", code)
	}
	if !strings.Contains(out.String(), "\"doctor\"") {
		t.Fatalf("doctor not in commands index: %s", out.String())
	}
}

// writeDoctorManifest writes a live JSON settings file plus a minimal manifest
// with a single json-merge step (arrays=replace) targeting it, and returns the
// manifest path. The live file's `packages` array is `liveArray`; the manifest
// declares `desiredArray`. When live holds entries the manifest omits, D1 emits
// an Error (arrays=replace), which drives doctor's exit code to 1.
func writeDoctorManifest(t *testing.T, desiredArray, liveArray string) string {
	t.Helper()
	dir := t.TempDir()
	live := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(live, []byte(fmt.Sprintf(`{"packages":[%s]}`, liveArray)), 0o644); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`
[kempt]
spec = 1

[packages.pi]
description = "pi"
  [[packages.pi.json-merge]]
  file = %q
  arrays = "replace"
  merge = { packages = [%s] }
`, live, desiredArray)
	manifestPath := filepath.Join(dir, "kempt.toml")
	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}

// hermeticState makes loadState report a fresh machine so resolveSelection
// defaults to "select all packages in the manifest" rather than picking up any
// real saved state on the host running the tests.
func hermeticState(t *testing.T) {
	t.Helper()
	orig := loadState
	loadState = func() (*state.State, bool, error) { return &state.State{}, false, nil }
	t.Cleanup(func() { loadState = orig })
}

// TestDoctorExitCode asserts the doctor command's process exit code via Dispatch:
// an ERROR-severity finding => 1, a clean manifest => 0, a bad flag => 2.
func TestDoctorExitCode(t *testing.T) {
	t.Run("error finding exits 1", func(t *testing.T) {
		hermeticState(t)
		// live has "npm:extra" that the manifest does not declare; arrays=replace
		// makes the undeclared entry an Error.
		manifestPath := writeDoctorManifest(t, `"npm:a"`, `"npm:a","npm:extra"`)
		withContextHome(t, t.TempDir(), &run.FakeRunner{})
		var out, errw bytes.Buffer
		if code := Dispatch([]string{"doctor", "-manifest", manifestPath}, &out, &errw); code != 1 {
			t.Fatalf("exit = %d, want 1; out=%s err=%s", code, out.String(), errw.String())
		}
	})

	t.Run("clean manifest exits 0", func(t *testing.T) {
		hermeticState(t)
		// live matches the manifest exactly: no findings.
		manifestPath := writeDoctorManifest(t, `"npm:a"`, `"npm:a"`)
		withContextHome(t, t.TempDir(), &run.FakeRunner{})
		var out, errw bytes.Buffer
		if code := Dispatch([]string{"doctor", "-manifest", manifestPath}, &out, &errw); code != 0 {
			t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
		}
	})

	t.Run("bad flag exits 2", func(t *testing.T) {
		hermeticState(t)
		var out, errw bytes.Buffer
		if code := Dispatch([]string{"doctor", "-nope"}, &out, &errw); code != 2 {
			t.Fatalf("exit = %d, want 2; out=%s err=%s", code, out.String(), errw.String())
		}
	})
}
