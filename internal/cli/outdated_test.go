package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
)

// writeTempManifest writes content to kempt.toml in a fresh tempdir and
// returns the dir.
func writeTempManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "kempt.toml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// stubContext overrides newContext to return a Context whose Home is a fresh
// tempdir (with the binaries named in versionRuns created so os.Stat
// passes), scripted with a FakeRunner from versionRuns and FakeReleases from
// releaseFiles. rawRuns adds un-prefixed run keys (for example "pi list",
// "npm view <pkg> version"). It returns a restore func.
func stubContextRuns(t *testing.T, repoDir string, versionRuns, rawRuns, releaseFiles map[string]string) (func(), *run.FakeRunner) {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	for key, stdout := range versionRuns {
		bin := strings.TrimSuffix(key, " version")
		fullKey := filepath.Join(binDir, bin) + " version"
		fr.Responses[fullKey] = run.Response{Stdout: stdout}
		if err := os.WriteFile(filepath.Join(binDir, bin), []byte(""), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for key, stdout := range rawRuns {
		fr.Responses[key] = run.Response{Stdout: stdout}
	}
	rel := release.FakeReleases{Files: map[string][]byte{}}
	for url, content := range releaseFiles {
		rel.Files[url] = []byte(content)
	}
	orig := newContext
	newContext = func(dir string) (*machine.Context, error) {
		return &machine.Context{
			Home: home, RepoDir: dir, OS: "darwin", Arch: "arm64",
			Runner: fr, Releases: rel, Cache: map[string]string{},
		}, nil
	}
	return func() { newContext = orig }, fr
}

// stubContext is a convenience wrapper around stubContextRuns for callers that
// need no raw runs.
func stubContext(t *testing.T, repoDir string, runResponses, releaseFiles map[string]string) func() {
	restore, _ := stubContextRuns(t, repoDir, runResponses, nil, releaseFiles)
	return restore
}

func TestOutdatedListsBehind(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.terminal]
  [[packages.terminal.download]]
  site = "tackle.tools"
  tool = "proj"
  version = "latest"
  bin = "proj"
`)
	restore := stubContext(t, dir, map[string]string{
		"proj version": "proj 0.1.1 (a, d)\n",
	}, map[string]string{
		"https://tackle.tools/dl/proj/latest": "0.1.2\n",
	})
	defer restore()

	var out, errw bytes.Buffer
	if err := runOutdated([]string{"-manifest", dir + "/kempt.toml", "-packages", "terminal"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "proj") || !strings.Contains(out.String(), "0.1.1") || !strings.Contains(out.String(), "0.1.2") {
		t.Fatalf("want proj 0.1.1 -> 0.1.2, got: %q", out.String())
	}
}

func TestOutdatedUpToDate(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.terminal]
  [[packages.terminal.download]]
  site = "tackle.tools"
  tool = "proj"
  version = "0.1.2"
  bin = "proj"
`)
	restore := stubContext(t, dir, map[string]string{
		"proj version": "proj 0.1.2 (a, d)\n",
	}, map[string]string{})
	defer restore()

	var out, errw bytes.Buffer
	if err := runOutdated([]string{"-manifest", dir + "/kempt.toml", "-packages", "terminal"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "everything up to date") {
		t.Fatalf("want up to date message, got: %q", out.String())
	}
}

// TestOutdatedReportsPerToolErrorNonFatal covers I1: one tool whose latest
// pointer can't be resolved must print a per-tool error line, while a second,
// resolvable tool that is behind still prints its own standing.
func TestOutdatedReportsPerToolErrorNonFatal(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.terminal]
  [[packages.terminal.download]]
  site = "tackle.tools"
  tool = "unreachable"
  version = "latest"
  bin = "unreachable"
  [[packages.terminal.download]]
  site = "tackle.tools"
  tool = "proj"
  version = "latest"
  bin = "proj"
`)
	restore := stubContext(t, dir, map[string]string{
		"unreachable version": "unreachable 0.1.0 (a, d)\n",
		"proj version":        "proj 0.1.1 (a, d)\n",
	}, map[string]string{
		// "unreachable"'s pointer is intentionally not scripted.
		"https://tackle.tools/dl/proj/latest": "0.1.2\n",
	})
	defer restore()

	var out, errw bytes.Buffer
	if err := runOutdated([]string{"-manifest", dir + "/kempt.toml", "-packages", "terminal"}, &out, &errw); err != nil {
		t.Fatalf("outdated must not fail on a per-tool network error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "unreachable") || !strings.Contains(got, "could not resolve latest") {
		t.Fatalf("want a per-tool error line for unreachable, got: %q", got)
	}
	if !strings.Contains(got, "proj") || !strings.Contains(got, "0.1.1") || !strings.Contains(got, "0.1.2") {
		t.Fatalf("want proj's standing to still print, got: %q", got)
	}
	if strings.Contains(got, "everything up to date") {
		t.Fatalf("an errored tool must not count as up to date, got: %q", got)
	}
}

// TestOutdatedJSON covers M3: -json emits a JSON array with the expected
// fields for a behind tool.
func TestOutdatedJSON(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.terminal]
  [[packages.terminal.download]]
  site = "tackle.tools"
  tool = "proj"
  version = "latest"
  bin = "proj"
`)
	restore := stubContext(t, dir, map[string]string{
		"proj version": "proj 0.1.1 (a, d)\n",
	}, map[string]string{
		"https://tackle.tools/dl/proj/latest": "0.1.2\n",
	})
	defer restore()

	var out, errw bytes.Buffer
	if err := runOutdated([]string{"-manifest", dir + "/kempt.toml", "-packages", "terminal", "-json"}, &out, &errw); err != nil {
		t.Fatal(err)
	}

	var rows []outdatedJSON
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("-json output is not valid JSON: %v\noutput: %s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Tool != "proj" || r.Installed != "0.1.1" || r.Target != "0.1.2" || r.Mode != "latest" || !r.Behind || r.Error != "" {
		t.Fatalf("unexpected row: %+v", r)
	}
}
