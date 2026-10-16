package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

// withContextHome overrides newContext with a Context whose Home is fixed so a
// test can inspect filesystem state after apply.
func withContextHome(t *testing.T, home string, r run.Runner) {
	t.Helper()
	orig := newContext
	newContext = func(repoDir string) (*machine.Context, error) {
		return &machine.Context{
			Home:    home,
			RepoDir: repoDir,
			OS:      "darwin",
			Arch:    "arm64",
			Runner:  r,
			Cache:   map[string]string{},
		}, nil
	}
	t.Cleanup(func() { newContext = orig })
}

// setStdin overrides the package stdin seam for the duration of a test.
func setStdin(t *testing.T, s string) {
	t.Helper()
	orig := stdin
	stdin = strings.NewReader(s)
	t.Cleanup(func() { stdin = orig })
}

const symlinkApplySrc = `
[kempt]
spec = 1

[packages.a]
description = "a"
  [[packages.a.symlink]]
  from = "src/rc"
  to = "~/.rc"
`

func writeSymlinkManifest(t *testing.T) (manifestPath, home string) {
	t.Helper()
	p := writeTemp(t, symlinkApplySrc)
	repo := filepath.Dir(p)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "rc"), []byte("rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p, t.TempDir()
}

func TestApplyYesFlagApplies(t *testing.T) {
	p, home := writeSymlinkManifest(t)
	withContextHome(t, home, &run.FakeRunner{})
	var out, errw bytes.Buffer
	code := Dispatch([]string{"apply", "-yes", "-manifest", p}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "applied:") {
		t.Fatalf("stdout missing applied line: %q", out.String())
	}
	link := filepath.Join(home, ".rc")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if want := filepath.Join(filepath.Dir(p), "src", "rc"); target != want {
		t.Fatalf("link target = %q, want %q", target, want)
	}
}

func TestApplyShortYesFlagApplies(t *testing.T) {
	p, home := writeSymlinkManifest(t)
	withContextHome(t, home, &run.FakeRunner{})
	var out, errw bytes.Buffer
	code := Dispatch([]string{"apply", "-y", "-manifest", p}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	if strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("-y should skip the prompt, but it was shown: %q", out.String())
	}
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err != nil {
		t.Fatalf("link not created: %v", err)
	}
}

// TestApplyHelpListsFlags guards the discoverability fix: -h/--help must print
// the command's flags (including -yes and its -y shorthand) and exit cleanly,
// rather than the bare "flag: help requested" that hid -yes.
func TestApplyHelpListsFlags(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		var out, errw bytes.Buffer
		code := Dispatch([]string{"apply", arg}, &out, &errw)
		if code != 0 {
			t.Fatalf("%s: exit = %d, want 0; err=%s", arg, code, errw.String())
		}
		got := out.String()
		for _, want := range []string{"-yes", "-y", "Usage: kempt apply"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: help missing %q; got:\n%s", arg, want, got)
			}
		}
		if strings.Contains(errw.String(), "help requested") {
			t.Fatalf("%s: leaked raw flag error: %q", arg, errw.String())
		}
	}
}

func TestApplyConfirmYesApplies(t *testing.T) {
	p, home := writeSymlinkManifest(t)
	withContextHome(t, home, &run.FakeRunner{})
	setStdin(t, "Y\n")
	var out, errw bytes.Buffer
	code := Dispatch([]string{"apply", "-manifest", p}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "apply 1 changes? [y/N]") {
		t.Fatalf("stdout missing prompt: %q", out.String())
	}
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err != nil {
		t.Fatalf("link not created: %v", err)
	}
}

func TestApplyDeclineAborts(t *testing.T) {
	p, home := writeSymlinkManifest(t)
	withContextHome(t, home, &run.FakeRunner{})
	setStdin(t, "n\n")
	var out, errw bytes.Buffer
	code := Dispatch([]string{"apply", "-manifest", p}, &out, &errw)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out=%s err=%s", code, out.String(), errw.String())
	}
	if !strings.Contains(out.String()+errw.String(), "aborted") {
		t.Fatalf("output missing aborted: out=%q err=%q", out.String(), errw.String())
	}
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err == nil {
		t.Fatalf("link should not have been created after decline")
	}
}

func TestApplyNothingToDo(t *testing.T) {
	home := t.TempDir()
	// A line-in-file whose line is already present -> OpNoop.
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("export EDITOR=vim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `
[kempt]
spec = 1
[packages.a]
description = "a"
  [[packages.a.line-in-file]]
  file = "~/.profile"
  line = "export EDITOR=vim"
`
	p := writeTemp(t, src)
	withContextHome(t, home, &run.FakeRunner{})
	var out, errw bytes.Buffer
	code := Dispatch([]string{"apply", "-manifest", p}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Fatalf("stdout missing 'nothing to do': %q", out.String())
	}
}

// nonConvergingHandler overrides an existing kind with an Apply that never
// converges: Inspect always reports OpChange and Apply is a no-op, so the
// post-apply honesty re-inspect must report the step as failed.
type nonConvergingHandler struct{}

func (nonConvergingHandler) Kind() string { return "line-in-file" }
func (nonConvergingHandler) Inspect(*machine.Context, manifest.Step) (engine.Delta, error) {
	return engine.Delta{Op: engine.OpChange, Detail: "line-in-file (never converges)"}, nil
}
func (nonConvergingHandler) Apply(*machine.Context, manifest.Step) error { return nil }

// installThenPresentRunner simulates a package manager: the tool it installs
// is absent from LookPath until its brew install command runs, then present.
// It drives the first-install verify regression from muster thread #358.
type installThenPresentRunner struct {
	tool      string
	installed bool
}

func (r *installThenPresentRunner) Run(name string, args ...string) (string, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch key {
	case "brew list --formula -1":
		if r.installed {
			return r.tool + "\n", nil
		}
		return "", nil
	case "brew list --cask -1", "brew tap":
		return "", nil
	case "brew install " + r.tool:
		r.installed = true
		return "", nil
	}
	return "", fmt.Errorf("unscripted command: %s", key)
}

func (r *installThenPresentRunner) LookPath(name string) (string, error) {
	if name == "brew" {
		return "/usr/local/bin/brew", nil
	}
	if name == r.tool && r.installed {
		return "/usr/local/bin/" + r.tool, nil
	}
	return "", exec.ErrNotFound
}

// TestApplyInstallThenVerifySameRun guards against a spurious "blocked" summary
// when one package installs a tool AND verifies its presence in the same run.
// The verify step is inspected pre-apply (tool absent -> OpBlocked); after the
// install applies, apply must re-inspect it against post-apply state so the run
// ends "1 applied, 0 failed" with no blocked line.
func TestApplyInstallThenVerifySameRun(t *testing.T) {
	home := t.TempDir()
	src := `
[kempt]
spec = 1
[packages.creel]
description = "creel"
  [[packages.creel.install]]
  brew = { formulas = ["creel"] }
  [[packages.creel.verify]]
  command-exists = "creel"
`
	p := writeTemp(t, src)
	withContextHome(t, home, &installThenPresentRunner{tool: "creel"})
	var out, errw bytes.Buffer
	code := Dispatch([]string{"apply", "-yes", "-manifest", p}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	s := out.String()
	if !strings.Contains(s, "1 applied, 0 failed") {
		t.Fatalf("stdout missing '1 applied, 0 failed': %q", s)
	}
	if strings.Contains(s, "blocked (unresolved)") {
		t.Fatalf("stdout should not report blocked after successful install+verify: %q", s)
	}
}

func TestApplyConvergenceHonestyFails(t *testing.T) {
	orig, _ := engine.HandlerFor("line-in-file")
	engine.RegisterHandler(nonConvergingHandler{})
	t.Cleanup(func() { engine.RegisterHandler(orig) })

	home := t.TempDir()
	src := `
[kempt]
spec = 1
[packages.a]
description = "a"
  [[packages.a.line-in-file]]
  file = "~/.profile"
  line = "export EDITOR=vim"
`
	p := writeTemp(t, src)
	withContextHome(t, home, &run.FakeRunner{})
	var out, errw bytes.Buffer
	code := Dispatch([]string{"apply", "-yes", "-manifest", p}, &out, &errw)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out=%s err=%s", code, out.String(), errw.String())
	}
}
