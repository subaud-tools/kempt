package cli

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/subaud-tools/kempt/internal/state"
)

func withStatusStore(t *testing.T, dir string) func() {
	t.Helper()
	orig := statusStore
	statusStore = func() (*state.Store, error) {
		return &state.Store{Dir: dir}, nil
	}
	return func() { statusStore = orig }
}

func saveStatus(t *testing.T, dir string, st *state.Status) {
	t.Helper()
	store := &state.Store{Dir: dir}
	if err := store.SaveStatus(st); err != nil {
		t.Fatalf("SaveStatus: %v", err)
	}
}

func dispatchStatus(t *testing.T, dir string) (stdout string, exit int) {
	t.Helper()
	defer withStatusStore(t, dir)()
	var out, errw bytes.Buffer
	exit = Dispatch([]string{"status"}, &out, &errw)
	return out.String(), exit
}

func TestStatusNoCache(t *testing.T) {
	dir := t.TempDir()
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: no status yet — run kempt refresh\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusAllZero(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt:       time.Now(),
		Behind:          0,
		FileChanges:     0,
		SoftwareChanges: 0,
		Blocked:         0,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: up to date\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusBehindOnly(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt: time.Now(),
		Behind:    2,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: 2 behind · kempt update\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusChangesOnly(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt:       time.Now(),
		FileChanges:     3,
		SoftwareChanges: 1,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: 3 file, 1 software changes pending · kempt update\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusFileChangesOnlyShowsBothCounts(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt:       time.Now(),
		FileChanges:     2,
		SoftwareChanges: 0,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: 2 file, 0 software changes pending · kempt update\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusSoftwareChangesOnlyShowsBothCounts(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt:       time.Now(),
		FileChanges:     0,
		SoftwareChanges: 4,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: 0 file, 4 software changes pending · kempt update\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusBehindAndChanges(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt:       time.Now(),
		Behind:          5,
		FileChanges:     3,
		SoftwareChanges: 2,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: 5 behind · 3 file, 2 software changes pending · kempt update\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusBehindAndBlocked(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt: time.Now(),
		Behind:    1,
		Blocked:   3,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: 1 behind; 3 blocked · kempt update\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusAllSegments(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt:       time.Now(),
		Behind:          2,
		FileChanges:     1,
		SoftwareChanges: 3,
		Blocked:         4,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: 2 behind · 1 file, 3 software changes pending; 4 blocked · kempt update\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusBlockedOnly(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt: time.Now(),
		Blocked:   2,
	})
	got, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: 2 blocked · kempt update\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStatusAlwaysExitZero(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt: time.Now(),
		Behind:    99,
		Blocked:   10,
	})
	_, code := dispatchStatus(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (status always exits 0)", code)
	}
}

// TestStatusOutputHasNoTrailingSpace checks the output doesn't have trailing spaces.
func TestStatusOutputHasNoTrailingSpace(t *testing.T) {
	dir := t.TempDir()
	saveStatus(t, dir, &state.Status{
		CheckedAt:       time.Now(),
		Behind:          1,
		FileChanges:     2,
		SoftwareChanges: 3,
		Blocked:         0,
	})
	got, _ := dispatchStatus(t, dir)
	line := strings.TrimRight(got, "\n")
	if strings.HasSuffix(line, " ") {
		t.Fatalf("trailing space in %q", line)
	}
}

// TestStatusStoreError injects a failing statusStore and asserts exit 0 + fallback line.
func TestStatusStoreError(t *testing.T) {
	orig := statusStore
	statusStore = func() (*state.Store, error) {
		return nil, fmt.Errorf("injected store failure")
	}
	t.Cleanup(func() { statusStore = orig })

	var out, errw bytes.Buffer
	code := Dispatch([]string{"status"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: status unavailable\n"
	if got := out.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestStatusCorruptJSON writes bad JSON into status.json and asserts exit 0 + fallback line.
func TestStatusCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	// Write corrupt JSON directly into the store file.
	statusFile := dir + "/status.json"
	if err := os.WriteFile(statusFile, []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	orig := statusStore
	statusStore = func() (*state.Store, error) {
		return &state.Store{Dir: dir}, nil
	}
	t.Cleanup(func() { statusStore = orig })

	var out, errw bytes.Buffer
	code := Dispatch([]string{"status"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "kempt: no status yet \u2014 run kempt refresh\n"
	if got := out.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func init() {
	// Ensure the os package is available (used in TestMain from cli_test.go).
	_ = os.DevNull
}
