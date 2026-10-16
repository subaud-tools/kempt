package state_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/subaud-tools/kempt/internal/state"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	s := &state.Store{Dir: t.TempDir()}
	in := &state.State{RepoDir: "/r", Profile: "developer", Packages: []string{"core", "terminal"}, AutoApplyFiles: true}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, existed, err := s.Load()
	if err != nil || !existed {
		t.Fatalf("existed=%v err=%v", existed, err)
	}
	if got.Profile != "developer" || len(got.Packages) != 2 || !got.AutoApplyFiles {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestLoadMissingIsNotError(t *testing.T) {
	s := &state.Store{Dir: t.TempDir()}
	st, existed, err := s.Load()
	if err != nil || existed || st == nil {
		t.Fatalf("want zero state, existed=false, no err; got %+v %v %v", st, existed, err)
	}
}

func TestLoadCorruptIsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&state.Store{Dir: dir}).Load(); err == nil {
		t.Fatal("want error on corrupt state")
	}
}

func TestStatusRoundTrip(t *testing.T) {
	s := &state.Store{Dir: t.TempDir()}
	in := &state.Status{Behind: 3, FileChanges: 2, SoftwareChanges: 1, Blocked: 0, CheckedAt: time.Now()}
	if err := s.SaveStatus(in); err != nil {
		t.Fatal(err)
	}
	got, existed, err := s.LoadStatus()
	if err != nil || !existed || got.Behind != 3 || got.FileChanges != 2 || got.SoftwareChanges != 1 {
		t.Fatalf("status round trip: %+v existed=%v err=%v", got, existed, err)
	}
}

func TestSaveCreatesDir(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "a", "b", "kempt")
	if err := (&state.Store{Dir: nested}).Save(&state.State{RepoDir: "/r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(nested, "state.json")); err != nil {
		t.Fatalf("state.json not created: %v", err)
	}
}
