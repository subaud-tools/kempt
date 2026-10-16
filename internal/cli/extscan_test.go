package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/run"
)

func TestScanExtensionsReportsBehind(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-creel", "npm:pi-quiet@0.2.0"]
`)
	restore, _ := stubContextRuns(t, dir, nil, map[string]string{
		"pi list":                   "  npm:pi-creel@0.1.0\n  npm:pi-quiet@0.2.0\n",
		"npm view pi-creel version": "0.1.1\n",
	}, nil)
	defer restore()

	selected, ctx, err := loadSelectedContext(dir+"/kempt.toml", "", "", io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := scanExtensions(ctx, selected)
	if len(got) != 1 {
		t.Fatalf("want 1 rolling status (pinned excluded), got %d: %+v", len(got), got)
	}
	s := got[0]
	if s.Kind != "pi" || s.Tool != "pi-creel" || s.Installed != "0.1.0" || s.Target != "0.1.1" || !s.Behind {
		t.Errorf("status = %+v", s)
	}
}

func TestOutdatedIncludesExtensions(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-creel"]
`)
	restore, _ := stubContextRuns(t, dir, nil, map[string]string{
		"pi list":                   "  npm:pi-creel@0.1.0\n",
		"npm view pi-creel version": "0.1.1\n",
	}, nil)
	defer restore()

	var out bytes.Buffer
	if err := runOutdated([]string{"-manifest", dir + "/kempt.toml"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pi-creel  0.1.0 -> 0.1.1") {
		t.Errorf("outdated output missing extension line:\n%s", out.String())
	}
}

func TestUpgradeRollsExtension(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-creel"]
`)
	restore, fr := stubContextRuns(t, dir, nil, map[string]string{
		"npm view pi-creel version": "0.1.1\n",
		"pi update npm:pi-creel":    "",
	}, nil)
	defer restore()
	fr.Sequences = map[string][]run.Response{"pi list": {{Stdout: "  npm:pi-creel@0.1.0\n"}, {Stdout: "  npm:pi-creel@0.1.1\n"}}}

	var out bytes.Buffer
	if err := runUpgrade([]string{"-yes", "-manifest", dir + "/kempt.toml"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range fr.Calls {
		if c == "pi update npm:pi-creel" {
			found = true
		}
	}
	if !found {
		t.Errorf("upgrade did not run `pi update npm:pi-creel`; calls=%v", fr.Calls)
	}
	if !strings.Contains(out.String(), "upgraded pi-creel to 0.1.1") {
		t.Errorf("upgrade output missing confirmation:\n%s", out.String())
	}
}

// TestUpgradeFailsOnNoOpRoll: upgrade does not claim "upgraded" when the roll
// left the entry behind; it fails naming the versions.
func TestUpgradeFailsOnNoOpRoll(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-hail"]
`)
	restore, _ := stubContextRuns(t, dir, nil, map[string]string{
		"pi list":                  "  npm:pi-hail@0.1.1\n",
		"npm view pi-hail version": "0.7.0\n",
		"pi update npm:pi-hail":    "",
	}, nil)
	defer restore()

	var out bytes.Buffer
	err := runUpgrade([]string{"-yes", "-manifest", dir + "/kempt.toml"}, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "still at 0.1.1 after the roll (latest 0.7.0)") {
		t.Fatalf("runUpgrade error = %v; want the no-op roll reported", err)
	}
	if strings.Contains(out.String(), "upgraded pi-hail") {
		t.Errorf("output claims an upgrade that did not happen:\n%s", out.String())
	}
}

// TestUpgradeMarksMajorBump: upgrade lists a major bump as one before it asks,
// so the confirmation says what it is agreeing to; -yes is that consent and
// applies it.
func TestUpgradeMarksMajorBump(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-mcp-adapter"]
`)
	restore, fr := stubContextRuns(t, dir, nil, map[string]string{
		"npm view pi-mcp-adapter version": "3.2.0\n",
		"pi update npm:pi-mcp-adapter":    "",
	}, nil)
	defer restore()
	fr.Sequences = map[string][]run.Response{"pi list": {
		{Stdout: "  npm:pi-mcp-adapter@2.38.0\n"},
		{Stdout: "  npm:pi-mcp-adapter@3.2.0\n"},
	}}

	var out bytes.Buffer
	if err := runUpgrade([]string{"-yes", "-manifest", dir + "/kempt.toml"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pi-mcp-adapter  2.38.0 -> 3.2.0  (major release)\n") {
		t.Errorf("upgrade list does not mark the major bump:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "upgraded pi-mcp-adapter to 3.2.0") {
		t.Errorf("-yes did not apply the major bump:\n%s", out.String())
	}
}

// TestOutdatedMarksMajorBump: outdated names a major bump as one, so what
// update will hold is visible before it runs.
func TestOutdatedMarksMajorBump(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-mcp-adapter", "npm:pi-creel"]
`)
	restore, _ := stubContextRuns(t, dir, nil, map[string]string{
		"pi list":                         "  npm:pi-mcp-adapter@2.38.0\n  npm:pi-creel@0.1.0\n",
		"npm view pi-mcp-adapter version": "3.2.0\n",
		"npm view pi-creel version":       "0.1.1\n",
	}, nil)
	defer restore()

	var out bytes.Buffer
	if err := runOutdated([]string{"-manifest", dir + "/kempt.toml"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pi-mcp-adapter  2.38.0 -> 3.2.0  (latest, major release)\n") {
		t.Errorf("outdated does not mark the major bump:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "pi-creel  0.1.0 -> 0.1.1  (latest)\n") {
		t.Errorf("outdated changed a minor bump's line:\n%s", out.String())
	}
}

func TestScanExtensionsReportsGitBehind(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["git:github.com/obra/superpowers"]
`)
	restore, _ := stubContextRuns(t, dir, nil, map[string]string{
		"pi list":                     "  git:github.com/obra/superpowers\n    /h/sp\n",
		"git -C /h/sp rev-parse HEAD": "5bf4e78aaaaaaaaaaaaa\n",
		"git ls-remote https://github.com/obra/superpowers HEAD": "9c01d2bbbbbbbbbbbbbb\tHEAD\n",
	}, nil)
	defer restore()

	selected, ctx, err := loadSelectedContext(dir+"/kempt.toml", "", "", io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := scanExtensions(ctx, selected)
	if len(got) != 1 {
		t.Fatalf("want 1 rolling status, got %d: %+v", len(got), got)
	}
	s := got[0]
	if s.Kind != "git" || s.Tool != "github.com/obra/superpowers" || s.Installed != "5bf4e78aaaaa" || s.Target != "9c01d2bbbbbb" || !s.Behind {
		t.Errorf("status = %+v", s)
	}
}
