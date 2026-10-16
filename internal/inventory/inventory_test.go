package inventory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/run"
)

func ctxWith(fake *run.FakeRunner) *machine.Context {
	c, _ := machine.New("/r", fake)
	c.Home = "/h"
	return c
}

func TestPiInventoryTwoLineFormat(t *testing.T) {
	// Real `pi list` prints two lines per package: a spec line at the entry
	// indent (2 spaces) and a deeper-indented (4 spaces) resolved path. Only
	// the spec lines are entries; the resolved-path lines must be skipped.
	stdout := "User packages:\n" +
		"  npm:pi-quiet\n" +
		"    /Users/you/.pi/agent/npm/node_modules/pi-quiet\n" +
		"  git:github.com/obra/superpowers\n" +
		"    /Users/you/.pi/agent/git/github.com/obra/superpowers\n"
	c := ctxWith(&run.FakeRunner{Responses: map[string]run.Response{
		"pi list": {Stdout: stdout},
	}})
	inv, err := Pi(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv) != 2 {
		t.Fatalf("want exactly 2 entries, got %d: %v", len(inv), inv)
	}
	if _, ok := inv["npm:pi-quiet"]; !ok {
		t.Fatalf("missing npm:pi-quiet entry: %v", inv)
	}
	if _, ok := inv["git:github.com/obra/superpowers"]; !ok {
		t.Fatalf("missing git:github.com/obra/superpowers entry: %v", inv)
	}
	if _, ok := inv["/Users/you/.pi/agent/npm/node_modules/pi-quiet"]; ok {
		t.Fatal("resolved-path continuation line must not be an entry")
	}
	if _, ok := inv["/Users/you/.pi/agent/git/github.com/obra/superpowers"]; ok {
		t.Fatal("resolved-path continuation line must not be an entry")
	}
}

func TestPiInventoryResolvesRollingVersionFromPackageJSON(t *testing.T) {
	// A rolling (unversioned `npm:`) entry has no @version in its `pi list`
	// spec line; the installed version lives in the resolved package's
	// package.json, reachable via the deeper-indented resolved-path line.
	// inventory.Pi must resolve it, or outdated/upgrade can never tell a
	// rolling extension is behind.
	pkgDir := filepath.Join(t.TempDir(), "node_modules", "pi-mcp-adapter")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"),
		[]byte(`{"name":"pi-mcp-adapter","version":"2.35.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout := "User packages:\n" +
		"  npm:pi-mcp-adapter\n" +
		"    " + pkgDir + "\n"
	c := ctxWith(&run.FakeRunner{Responses: map[string]run.Response{
		"pi list": {Stdout: stdout},
	}})
	inv, err := Pi(c)
	if err != nil {
		t.Fatal(err)
	}
	if inv["npm:pi-mcp-adapter"] != "2.35.0" {
		t.Fatalf("rolling entry version = %q, want 2.35.0 (resolved from package.json)", inv["npm:pi-mcp-adapter"])
	}
}

func TestPiInventorySkipsHeaders(t *testing.T) {
	c := ctxWith(&run.FakeRunner{Responses: map[string]run.Response{
		"pi list": {Stdout: "User packages:\n  npm:typescript@5.0.0\n  /Users/me/local-tool\n"},
	}})
	inv, err := Pi(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inv["User packages:"]; ok {
		t.Fatal("header must not be an entry")
	}
	if inv["npm:typescript"] != "5.0.0" {
		t.Fatalf("typescript version = %q", inv["npm:typescript"])
	}
}

func TestPiPathsMapsSpecToResolvedPath(t *testing.T) {
	stdout := "User packages:\n" +
		"  npm:pi-quiet@0.2.1\n" +
		"    /Users/you/.pi/agent/npm/node_modules/pi-quiet\n" +
		"  git:github.com/obra/superpowers\n" +
		"    /Users/you/.pi/agent/git/github.com/obra/superpowers\n"
	c := ctxWith(&run.FakeRunner{Responses: map[string]run.Response{
		"pi list": {Stdout: stdout},
	}})
	paths, err := PiPaths(c)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths["git:github.com/obra/superpowers"]; got != "/Users/you/.pi/agent/git/github.com/obra/superpowers" {
		t.Errorf("superpowers path = %q", got)
	}
	if got := paths["npm:pi-quiet"]; got != "/Users/you/.pi/agent/npm/node_modules/pi-quiet" {
		t.Errorf("pi-quiet path = %q", got)
	}
}
