package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/state"
)

// TestMain stubs loadState to an empty absent state so that plan/apply/verify
// tests are hermetic: they never read ~/.local/share/kempt/state.json from the
// developer's machine. Tests that need specific saved state (adopt/drop tests)
// override loadState/saveState locally via withAdoptEnv.
func TestMain(m *testing.M) {
	loadState = func() (*state.State, bool, error) { return &state.State{}, false, nil }
	os.Exit(m.Run())
}

func TestDispatchNoArgsIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	if got := Dispatch(nil, &out, &errw); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
	if !strings.Contains(errw.String(), "usage: kempt") {
		t.Fatalf("stderr missing usage block: %q", errw.String())
	}
}

func TestDispatchVersion(t *testing.T) {
	var out, errw bytes.Buffer
	if got := Dispatch([]string{"version"}, &out, &errw); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
	if !strings.Contains(out.String(), "kempt ") {
		t.Fatalf("stdout = %q, want version line", out.String())
	}
}

func TestDispatchFlagVersion(t *testing.T) {
	var out, errw bytes.Buffer
	if got := Dispatch([]string{"--version"}, &out, &errw); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
	if !strings.Contains(out.String(), "kempt ") {
		t.Fatalf("stdout = %q, want version line", out.String())
	}
}

func TestDispatchUnknownCommand(t *testing.T) {
	var out, errw bytes.Buffer
	if got := Dispatch([]string{"frobnicate"}, &out, &errw); got != 2 {
		t.Fatalf("exit = %d, want 2", got)
	}
	if !strings.Contains(errw.String(), "kempt: unknown command") {
		t.Fatalf("stderr = %q, want unknown command", errw.String())
	}
}

// TestDispatchHelpListsCommandsOnce: help lists the built-ins plus kempt's own
// commands, and update (kempt's override) appears exactly once.
func TestDispatchHelpListsCommandsOnce(t *testing.T) {
	var out, errw bytes.Buffer
	if got := Dispatch([]string{"help"}, &out, &errw); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
	text := out.String()
	for _, name := range []string{"version", "help", "update", "plan", "apply"} {
		if !strings.Contains(text, name) {
			t.Fatalf("help missing %q:\n%s", name, text)
		}
	}
	if n := strings.Count(text, "\n  update "); n != 1 {
		t.Fatalf("update listed %d times, want 1:\n%s", n, text)
	}
}
