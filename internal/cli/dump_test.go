package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/run"
)

func TestDumpHappyPath(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()

	// Create a real symlink <home>/.zshrc -> <repo>/zsh/.zshrc.
	if err := os.MkdirAll(filepath.Join(repo, "zsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "zsh", ".zshrc")
	if err := os.WriteFile(target, []byte("# zshrc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal(err)
	}

	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath brew":       {Stdout: "/opt/homebrew/bin/brew"},
		"brew leaves":         {Stdout: "ripgrep\njq\n"},
		"brew list --cask -1": {Stdout: "ghostty\n"},
	}}
	withContextHome(t, home, fake)

	var out, errw bytes.Buffer
	code := Dispatch([]string{"dump", "-repo", repo}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	s := out.String()
	for _, want := range []string{
		"spec = 1",
		`formulas = ["jq", "ripgrep"]`,
		`casks = ["ghostty"]`,
		`from = "zsh/.zshrc"`,
		`to = "~/.zshrc"`,
	} {
		if !contains(s, want) {
			t.Fatalf("stdout missing %q:\n%s", want, s)
		}
	}

	// Read-only: home should contain only the fixture symlink; repo only zsh/.
	assertOnlyFixture(t, home, []string{".zshrc"})
	assertOnlyFixture(t, repo, []string{"zsh"})
}

func TestDumpNoBrew(t *testing.T) {
	home := t.TempDir()
	// lookpath brew unscripted -> not found.
	fake := &run.FakeRunner{Responses: map[string]run.Response{}}
	withContextHome(t, home, fake)

	var out, errw bytes.Buffer
	code := Dispatch([]string{"dump"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	s := out.String()
	if !contains(s, "brew not found") {
		t.Fatalf("stdout missing 'brew not found' comment:\n%s", s)
	}
	if contains(s, "packages.brew.install") {
		t.Fatalf("stdout should not have an install step:\n%s", s)
	}
	assertOnlyFixture(t, home, nil)
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

func assertOnlyFixture(t *testing.T, dir string, allowed []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, a := range allowed {
		set[a] = true
	}
	for _, e := range entries {
		if !set[e.Name()] {
			t.Fatalf("unexpected entry %q in %s (dump must be read-only)", e.Name(), dir)
		}
	}
}
