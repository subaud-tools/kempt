package engine_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
)

// integrationSrc exercises the files class end-to-end (symlink + line-in-file +
// json-merge) with the software class driven through FakeRunner (brew install).
const integrationSrc = `
[kempt]
spec = 1

[packages.base]
description = "base dotfiles"
  [[packages.base.symlink]]
  from = "src/rc"
  to = "~/.rc"
  [[packages.base.line-in-file]]
  file = "~/.profile"
  line = "export EDITOR=vim"

[packages.tools]
description = "cli tools"
needs = ["base"]
  [[packages.tools.install]]
  brew = { formulas = ["jq", "ripgrep"] }
  [[packages.tools.json-merge]]
  file = "~/.config/app.json"
  merge = { editor = "vim" }
`

// TestPhaseAcceptance is the phase 1b acceptance test: BuildPlan reports real
// deltas, Execute mutates the filesystem and invokes brew, and a second
// BuildPlan reports full convergence (all OpNoop).
func TestPhaseAcceptance(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "rc"), []byte("rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fr := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath brew":          {Stdout: "/opt/homebrew/bin/brew"},
		"brew list --formula -1": {Stdout: "jq\n"}, // ripgrep missing
		"brew list --cask -1":    {Stdout: ""},
		"brew tap":               {Stdout: ""},
		"brew install ripgrep":   {Stdout: ""},
	}}
	// FakeReleases is unused by this manifest but present to exercise the wiring.
	rel := release.FakeReleases{Tags: map[string]string{}, Files: map[string][]byte{}}

	ctx := &machine.Context{
		Home:     home,
		RepoDir:  repo,
		OS:       "darwin",
		Arch:     "arm64",
		Runner:   fr,
		Releases: rel,
		Cache:    map[string]string{},
	}

	m, findings := manifest.Parse([]byte(integrationSrc))
	if len(findings) > 0 {
		t.Fatalf("parse findings: %v", findings)
	}
	if findings = manifest.Validate(m); len(findings) > 0 {
		t.Fatalf("validate findings: %v", findings)
	}
	pkgs, err := engine.Select(m, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// --- First plan: everything should be a change. ---
	plan, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	assertAllChange(t, plan)

	// --- Execute: apply all changes. ---
	var out bytes.Buffer
	if failed := engine.Execute(ctx, plan, &out); failed != 0 {
		t.Fatalf("Execute failed = %d; out=%s", failed, out.String())
	}

	// Symlink created and pointing at the repo source.
	target, err := os.Readlink(filepath.Join(home, ".rc"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if want := filepath.Join(repo, "src", "rc"); target != want {
		t.Fatalf("symlink target = %q, want %q", target, want)
	}

	// Line appended.
	prof, err := os.ReadFile(filepath.Join(home, ".profile"))
	if err != nil {
		t.Fatalf("read .profile: %v", err)
	}
	if !bytes.Contains(prof, []byte("export EDITOR=vim")) {
		t.Fatalf(".profile missing line: %q", prof)
	}

	// JSON merged.
	jb, err := os.ReadFile(filepath.Join(home, ".config", "app.json"))
	if err != nil {
		t.Fatalf("read app.json: %v", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(jb, &merged); err != nil {
		t.Fatalf("app.json invalid: %v", err)
	}
	if merged["editor"] != "vim" {
		t.Fatalf("app.json editor = %v, want vim", merged["editor"])
	}

	// brew install call recorded.
	if !containsCall(fr.Calls, "brew install ripgrep") {
		t.Fatalf("brew install not called; calls=%v", fr.Calls)
	}

	// --- Convergence: reflect the install in the fake inventory, then a fresh
	// plan should be all OpNoop. ---
	fr.Responses["brew list --formula -1"] = run.Response{Stdout: "jq\nripgrep\n"}
	ctx.Cache = map[string]string{}
	plan2, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	assertAllNoop(t, plan2)
}

func assertAllChange(t *testing.T, p *engine.Plan) {
	t.Helper()
	var steps int
	for _, pp := range p.Packages {
		if pp.Skipped {
			t.Fatalf("package %s unexpectedly skipped", pp.Name)
		}
		for _, sr := range pp.Steps {
			steps++
			if sr.Delta.Op != engine.OpChange {
				t.Fatalf("package %s step %q op = %v, want OpChange", pp.Name, sr.Step.Kind(), sr.Delta.Op)
			}
		}
	}
	if steps != 4 {
		t.Fatalf("expected 4 steps, got %d", steps)
	}
}

func assertAllNoop(t *testing.T, p *engine.Plan) {
	t.Helper()
	for _, pp := range p.Packages {
		if pp.Skipped {
			t.Fatalf("package %s unexpectedly skipped", pp.Name)
		}
		for _, sr := range pp.Steps {
			if sr.Delta.Op != engine.OpNoop {
				t.Fatalf("package %s step %q op = %v (%s), want OpNoop", pp.Name, sr.Step.Kind(), sr.Delta.Op, sr.Delta.Detail)
			}
		}
	}
}

func containsCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}
