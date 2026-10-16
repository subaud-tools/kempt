package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/manifest"
)

func TestNewCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if got := Dispatch([]string{"new", dir}, &out, &errw); got != 0 {
		t.Fatalf("exit = %d, stderr = %s", got, errw.String())
	}
	for _, rel := range []string{"kempt.toml", "README.md", ".github/workflows/kempt-lint.yml"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestNewGeneratedManifestValidatesClean(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if got := Dispatch([]string{"new", dir}, &out, &errw); got != 0 {
		t.Fatalf("exit = %d, stderr = %s", got, errw.String())
	}
	src, err := os.ReadFile(filepath.Join(dir, "kempt.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m, findings := manifest.Parse(src)
	if m != nil {
		findings = append(findings, manifest.Validate(m)...)
	}
	if len(findings) != 0 {
		t.Fatalf("expected zero findings, got %v", findings)
	}
}

func TestNewRefusesExisting(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if got := Dispatch([]string{"new", dir}, &out, &errw); got != 0 {
		t.Fatalf("first new: exit = %d, stderr = %s", got, errw.String())
	}
	out.Reset()
	errw.Reset()
	if got := Dispatch([]string{"new", dir}, &out, &errw); got != 2 {
		t.Fatalf("second new: exit = %d (want 2), stderr = %s", got, errw.String())
	}
}

func TestNewDefaultsToCwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir) // restored when the test ends
	var out, errw bytes.Buffer
	if got := Dispatch([]string{"new"}, &out, &errw); got != 0 {
		t.Fatalf("exit = %d, stderr = %s", got, errw.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "kempt.toml")); err != nil {
		t.Fatalf("expected kempt.toml in cwd: %v", err)
	}
}

// TestEmbeddedTemplateCoversAllKinds guards that the exemplar exercises every
// one of the 10 primitive kinds, so it stays complete as kinds evolve.
func TestEmbeddedTemplateCoversAllKinds(t *testing.T) {
	m, findings := manifest.Parse(kemptTemplate)
	if m == nil {
		t.Fatalf("embedded template failed to parse: %v", findings)
	}
	kinds := map[string]bool{}
	for _, pkg := range m.Packages {
		for _, step := range pkg.Steps {
			kinds[step.Kind()] = true
		}
	}
	want := []string{
		"install", "symlink", "github-release", "download", "git-clone",
		"service", "json-merge", "toml-merge", "line-in-file", "verify",
	}
	for _, k := range want {
		if !kinds[k] {
			t.Errorf("embedded template missing kind %q", k)
		}
	}
}
