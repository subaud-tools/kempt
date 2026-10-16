package cli

import (
	"bytes"
	"testing"

	"github.com/subaud-tools/kempt/internal/state"
)

// withConfigStore points loadState/saveState at a fresh tempdir store.
func withConfigStore(t *testing.T) *state.Store {
	t.Helper()
	store := &state.Store{Dir: t.TempDir()}
	origLoad, origSave := loadState, saveState
	loadState = func() (*state.State, bool, error) { return store.Load() }
	saveState = func(s *state.State) error { return store.Save(s) }
	t.Cleanup(func() { loadState, saveState = origLoad, origSave })
	return store
}

func TestConfigGetDefaultFalse(t *testing.T) {
	withConfigStore(t)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"config", "get", "auto-apply-files"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d, want 0; err=%s", code, errw.String())
	}
	if out.String() != "false\n" {
		t.Fatalf("got %q, want false", out.String())
	}
}

func TestConfigSetGetRoundTrip(t *testing.T) {
	store := withConfigStore(t)
	// Seed some state so we can confirm the field toggles without clobbering.
	if err := store.Save(&state.State{RepoDir: "/repo", Packages: []string{"a"}}); err != nil {
		t.Fatal(err)
	}

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"config", "set", "auto-apply-files", "true"}, &out, &errw); code != 0 {
		t.Fatalf("set exit = %d, want 0; err=%s", code, errw.String())
	}
	if out.String() != "auto-apply-files = true\n" {
		t.Fatalf("set out = %q", out.String())
	}

	// Round-trip: get now returns true.
	out.Reset()
	if code := Dispatch([]string{"config", "get", "auto-apply-files"}, &out, &errw); code != 0 {
		t.Fatalf("get exit = %d, want 0", code)
	}
	if out.String() != "true\n" {
		t.Fatalf("get out = %q, want true", out.String())
	}

	// Other fields preserved.
	st, _, _ := store.Load()
	if st.RepoDir != "/repo" || len(st.Packages) != 1 {
		t.Fatalf("state clobbered: %+v", st)
	}
}

func TestConfigSetBadBoolExits2(t *testing.T) {
	withConfigStore(t)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"config", "set", "auto-apply-files", "maybe"}, &out, &errw); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestConfigUnknownKeyExits2(t *testing.T) {
	withConfigStore(t)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"config", "get", "nope"}, &out, &errw); code != 2 {
		t.Fatalf("get unknown key exit = %d, want 2", code)
	}
	if code := Dispatch([]string{"config", "set", "nope", "true"}, &out, &errw); code != 2 {
		t.Fatalf("set unknown key exit = %d, want 2", code)
	}
}

func TestConfigUnknownSubcommandExits2(t *testing.T) {
	withConfigStore(t)
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"config", "frob"}, &out, &errw); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
