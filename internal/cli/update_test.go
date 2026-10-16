package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
	tools "github.com/subaud-tools/tools-common"
)

const updateManifest = `
[kempt]
spec = 1

[packages.a]
description = "a"
  [[packages.a.symlink]]
  from = "src/rc"
  to = "~/.rc"
`

// setupUpdate wires loadState, newContext (FakeRunner+FakeReleases, tempdir
// Home), and stubs the selfUpdate seam to a no-op so the binary-replace step
// never reaches the network. It returns (home, repo).
func setupUpdate(t *testing.T, r *run.FakeRunner, rel release.Releases) (home, repo string) {
	t.Helper()
	repo = t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(updateManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "rc"), []byte("rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home = t.TempDir()

	stateStore := &state.Store{Dir: t.TempDir()}
	if err := stateStore.Save(&state.State{RepoDir: repo, Packages: []string{"a"}}); err != nil {
		t.Fatal(err)
	}

	origLoad, origCtx, origSelf, origReexec := loadState, newContext, selfUpdate, reexecUpdate
	// Default: a re-exec "succeeds" (a real exec never returns) and does nothing.
	reexecUpdate = func() error { return nil }
	loadState = func() (*state.State, bool, error) { return stateStore.Load() }
	newContext = func(repoDir string) (*machine.Context, error) {
		return &machine.Context{
			Home:     home,
			RepoDir:  repoDir,
			OS:       "darwin",
			Arch:     "arm64",
			Runner:   r,
			Releases: rel,
			Cache:    map[string]string{},
		}, nil
	}
	// Default: self-update is a no-op (already latest).
	selfUpdate = func(app *tools.App, out, errw io.Writer) (bool, string, error) {
		return false, "dev", nil
	}
	t.Cleanup(func() {
		loadState, newContext, selfUpdate, reexecUpdate = origLoad, origCtx, origSelf, origReexec
	})
	return home, repo
}

// TestUpdateCurrentBinaryConverges: pull succeeds, self-update is a no-op, and
// a files-only manifest converges. Exit 0.
func TestUpdateCurrentBinaryConverges(t *testing.T) {
	r := &run.FakeRunner{}
	rel := release.FakeReleases{}
	home, repo := setupUpdate(t, r, rel)
	r.Responses = map[string]run.Response{
		"git -C " + repo + " pull --rebase --autostash": {Stdout: ""},
	}

	var out, errw bytes.Buffer
	code := Dispatch([]string{"update"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	// Files converged: symlink created.
	link := filepath.Join(home, ".rc")
	if target, err := os.Readlink(link); err != nil {
		t.Fatalf("symlink not created: %v", err)
	} else if want := filepath.Join(repo, "src", "rc"); target != want {
		t.Fatalf("link target = %q, want %q", target, want)
	}
}

// TestUpdateReexecsAfterSelfUpdate: once update has replaced its own binary,
// it hands off to the new one instead of rolling and converging with the old
// code. kempt 0.5.8 -> 0.5.9 printed 0.5.8's "rolled" lines because the roll
// ran in the process that had just been replaced.
func TestUpdateReexecsAfterSelfUpdate(t *testing.T) {
	r := &run.FakeRunner{}
	home, repo := setupUpdate(t, r, release.FakeReleases{})
	r.Responses = map[string]run.Response{
		"git -C " + repo + " pull --rebase --autostash": {Stdout: ""},
	}
	selfUpdate = func(app *tools.App, out, errw io.Writer) (bool, string, error) {
		return true, "0.5.10", nil
	}
	reexecs := 0
	reexecUpdate = func() error { reexecs++; return nil }

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d; out=%s err=%s", code, out.String(), errw.String())
	}
	if reexecs != 1 {
		t.Fatalf("reexecs = %d, want 1", reexecs)
	}
	if !strings.Contains(out.String(), "kempt updated dev -> 0.5.10\n") {
		t.Errorf("missing binary standing:\n%s", out.String())
	}
	if strings.Contains(out.String(), "rolling:") || strings.Contains(out.String(), "applied") {
		t.Errorf("the replaced process rolled or converged:\n%s", out.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".rc")); err == nil {
		t.Error("the replaced process converged files")
	}
}

// TestUpdateAfterReexecSkipsPullAndSelfUpdate: the re-exec'd process only rolls
// and converges; the pull and the self-update already happened.
func TestUpdateAfterReexecSkipsPullAndSelfUpdate(t *testing.T) {
	r := &run.FakeRunner{}
	home, _ := setupUpdate(t, r, release.FakeReleases{})
	t.Setenv(updateReexecEnv, "1")
	selfUpdate = func(app *tools.App, out, errw io.Writer) (bool, string, error) {
		t.Error("self-update ran again after the re-exec")
		return false, "", nil
	}
	reexecUpdate = func() error { t.Error("re-exec'd again"); return nil }

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d; out=%s err=%s", code, out.String(), errw.String())
	}
	for _, c := range r.Calls {
		if strings.Contains(c, "pull") {
			t.Errorf("pulled again after the re-exec: %q", c)
		}
	}
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err != nil {
		t.Errorf("did not converge after the re-exec: %v", err)
	}
}

// TestUpdateContinuesWhenReexecFails: a failed hand-off warns and carries on in
// the current process (the pre-0.5.10 behaviour) rather than aborting.
func TestUpdateContinuesWhenReexecFails(t *testing.T) {
	r := &run.FakeRunner{}
	home, repo := setupUpdate(t, r, release.FakeReleases{})
	r.Responses = map[string]run.Response{
		"git -C " + repo + " pull --rebase --autostash": {Stdout: ""},
	}
	selfUpdate = func(app *tools.App, out, errw io.Writer) (bool, string, error) {
		return true, "0.5.10", nil
	}
	reexecUpdate = func() error { return errors.New("exec format error") }

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d; out=%s err=%s", code, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "could not restart into kempt 0.5.10 (exec format error); continuing with this binary") {
		t.Errorf("missing re-exec warning:\n%s", out.String())
	}
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err != nil {
		t.Errorf("did not converge after a failed re-exec: %v", err)
	}
}

// TestUpdatePullErrorAborts: a pull failure must not be swallowed. Exit 1.
func TestUpdatePullErrorAborts(t *testing.T) {
	r := &run.FakeRunner{}
	rel := release.FakeReleases{}
	_, repo := setupUpdate(t, r, rel)
	r.Responses = map[string]run.Response{
		"git -C " + repo + " pull --rebase --autostash": {Err: fmt.Errorf("merge conflict")},
	}

	var out, errw bytes.Buffer
	code := Dispatch([]string{"update"}, &out, &errw)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out=%s err=%s", code, out.String(), errw.String())
	}
}

func TestUpdateNoStateIsUsageError(t *testing.T) {
	origLoad := loadState
	loadState = func() (*state.State, bool, error) { return &state.State{}, false, nil }
	t.Cleanup(func() { loadState = origLoad })
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"update"}, &out, &errw); code != 2 {
		t.Fatalf("exit = %d, want 2; err=%s", code, errw.String())
	}
}

func TestRollRollingRollsBehindExtension(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-creel", "npm:pi-quiet@0.2.0"]
`)
	restore, fr := stubContextRuns(t, dir, nil, map[string]string{
		"npm view pi-creel version": "0.1.1\n",
		"pi update npm:pi-creel":    "",
	}, nil)
	defer restore()
	fr.Sequences = map[string][]run.Response{"pi list": {
		{Stdout: "  npm:pi-creel@0.1.0\n  npm:pi-quiet@0.2.0\n"},
		{Stdout: "  npm:pi-creel@0.1.1\n  npm:pi-quiet@0.2.0\n"},
	}}

	selected, ctx, err := loadSelectedContext(dir+"/kempt.toml", "", "", io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := rollRolling(ctx, selected, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "rolled pi-creel to 0.1.1") {
		t.Errorf("missing roll line:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "rolling: 1 checked, 1 rolled, 0 skipped, 0 held") {
		t.Errorf("missing rolling summary:\n%s", out.String())
	}
	rolled := false
	for _, c := range fr.Calls {
		if c == "pi update npm:pi-creel" {
			rolled = true
		}
		if strings.Contains(c, "npm:pi-quiet") {
			t.Errorf("pinned entry must not be rolled; calls=%v", fr.Calls)
		}
	}
	if !rolled {
		t.Errorf("rolling entry not rolled; calls=%v", fr.Calls)
	}
}

// TestRollRollingReportsNoOpRollAsSkipped: a roll command that succeeds but
// leaves the entry behind is reported as skipped with the versions, never as
// "rolled". `update` once printed "rolled pi-hail to 0.7.0" while 0.1.1 stayed.
func TestRollRollingReportsNoOpRollAsSkipped(t *testing.T) {
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

	selected, ctx, err := loadSelectedContext(dir+"/kempt.toml", "", "", io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := rollRolling(ctx, selected, &out); err != nil {
		t.Fatal(err)
	}
	want := "skipping pi-hail: still at 0.1.1 after the roll (latest 0.7.0)\nrolling: 1 checked, 0 rolled, 1 skipped, 0 held\n"
	if got := out.String(); got != want {
		t.Errorf("output = %q; want %q", got, want)
	}
}

// TestRollRollingHoldsMajorBump: update never crosses a major version on its
// own. The entry is held, named with its versions and the command that takes
// it, and not rolled; a minor bump beside it still rolls. pi-mcp-adapter 3.0
// dropped the config file it had been reading, so a silent major roll can
// take a working setup down.
func TestRollRollingHoldsMajorBump(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-mcp-adapter", "npm:pi-creel"]
`)
	restore, fr := stubContextRuns(t, dir, nil, map[string]string{
		"npm view pi-mcp-adapter version": "3.2.0\n",
		"npm view pi-creel version":       "0.2.0\n",
		"pi update npm:pi-creel":          "",
	}, nil)
	defer restore()
	fr.Sequences = map[string][]run.Response{"pi list": {
		{Stdout: "  npm:pi-mcp-adapter@2.38.0\n  npm:pi-creel@0.1.0\n"},
		{Stdout: "  npm:pi-mcp-adapter@2.38.0\n  npm:pi-creel@0.2.0\n"},
	}}

	selected, ctx, err := loadSelectedContext(dir+"/kempt.toml", "", "", io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := rollRolling(ctx, selected, &out); err != nil {
		t.Fatal(err)
	}
	want := "held pi-mcp-adapter 2.38.0 -> 3.2.0 (major release): run kempt upgrade pi-mcp-adapter\n" +
		"rolled pi-creel to 0.2.0\n" +
		"rolling: 2 checked, 1 rolled, 0 skipped, 1 held\n"
	if got := out.String(); got != want {
		t.Errorf("output = %q; want %q", got, want)
	}
	for _, c := range fr.Calls {
		if strings.Contains(c, "pi-mcp-adapter") && !strings.HasPrefix(c, "npm view") {
			t.Errorf("held entry was acted on: %q", c)
		}
	}
}

// TestRollRollingSummaryWhenCurrent: nothing behind still prints a summary, so
// a no-op roll is distinguishable from a skipped one.
func TestRollRollingSummaryWhenCurrent(t *testing.T) {
	dir := writeTempManifest(t, `
[kempt]
spec = 1
[packages.pi]
  [[packages.pi.install]]
  pi = ["npm:pi-creel", "npm:pi-bang", "npm:pi-quiet@0.2.0"]
`)
	restore, _ := stubContextRuns(t, dir, nil, map[string]string{
		"pi list":                   "  npm:pi-creel@0.1.1\n  npm:pi-bang@1.0.0\n  npm:pi-quiet@0.2.0\n",
		"npm view pi-creel version": "0.1.1\n",
		"npm view pi-bang version":  "1.0.0\n",
	}, nil)
	defer restore()

	selected, ctx, err := loadSelectedContext(dir+"/kempt.toml", "", "", io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := rollRolling(ctx, selected, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "rolling: 2 checked, 0 rolled, 0 skipped, 0 held\n" {
		t.Errorf("output = %q", got)
	}
}

// TestUpdateReportsBinaryStanding: update always says where the binary stands,
// whether or not self-update replaced it.
func TestUpdateReportsBinaryStanding(t *testing.T) {
	cases := []struct {
		updated bool
		ver     string
		want    string
	}{
		{false, "0.5.4", "kempt 0.5.4 (current)\n"},
		{true, "0.5.5", "kempt updated dev -> 0.5.5\n"},
	}
	for _, c := range cases {
		r := &run.FakeRunner{}
		_, repo := setupUpdate(t, r, release.FakeReleases{})
		r.Responses = map[string]run.Response{
			"git -C " + repo + " pull --rebase --autostash": {Stdout: ""},
		}
		selfUpdate = func(app *tools.App, out, errw io.Writer) (bool, string, error) {
			return c.updated, c.ver, nil
		}
		var out, errw bytes.Buffer
		if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
			t.Fatalf("exit = %d; out=%s err=%s", code, out.String(), errw.String())
		}
		if !strings.Contains(out.String(), c.want) {
			t.Errorf("updated=%v: missing %q in:\n%s", c.updated, c.want, out.String())
		}
	}
}
