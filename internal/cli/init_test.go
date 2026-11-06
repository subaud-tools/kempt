package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/picker"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
)

const initManifest = `
[kempt]
spec = 1

[packages.tools]
description = "developer tools"
  [[packages.tools.symlink]]
  from = "src/rc"
  to = "~/.rc"

[profiles.developer]
description = "developer profile"
packages = ["tools"]
`

const initTestURL = "https://example.com/dotfiles.git"

// setupInit writes the manifest+source into a repo tempdir, points saveState at
// a tempdir store, installs a newContext returning a Context with the given
// FakeRunner and a tempdir Home, and scripts the git seams. It returns
// (stateStore, home, dir). By default RemoteURL errors (dir is "not a repo")
// and `git clone <url> <dir>` succeeds.
func setupInit(t *testing.T, r *run.FakeRunner) (store *state.Store, home, dir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kempt.toml"), []byte(initManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "rc"), []byte("rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home = t.TempDir()
	store = &state.Store{Dir: t.TempDir()}

	origSave, origCtx, origPicker := saveState, newContext, pickerRun
	saveState = func(s *state.State) error { return store.Save(s) }
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
	t.Cleanup(func() {
		saveState, newContext, pickerRun = origSave, origCtx, origPicker
	})
	return store, home, dir
}

func TestInitProfileNonInteractive(t *testing.T) {
	r := &run.FakeRunner{}
	store, home, dir := setupInit(t, r)
	r.Responses = map[string]run.Response{
		"git -C " + dir + " remote get-url origin": {Err: os.ErrNotExist},
		"git clone " + initTestURL + " " + dir:     {Stdout: ""},
	}

	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", initTestURL, "-dir", dir, "-profile", "developer", "-yes"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}

	// Clone was called.
	cloned := false
	for _, c := range r.Calls {
		if c == "git clone "+initTestURL+" "+dir {
			cloned = true
		}
	}
	if !cloned {
		t.Fatalf("git clone not called; calls=%v", r.Calls)
	}

	// State saved.
	st, existed, err := store.Load()
	if err != nil || !existed {
		t.Fatalf("state not saved: existed=%v err=%v", existed, err)
	}
	if st.RepoDir != dir || st.RepoURL != initTestURL || st.Profile != "developer" {
		t.Fatalf("state = %+v, want dir/url/profile set", st)
	}
	if len(st.Packages) != 1 || st.Packages[0] != "tools" {
		t.Fatalf("state packages = %v, want [tools]", st.Packages)
	}
	if st.AutoApplyFiles {
		t.Fatalf("AutoApplyFiles = true, want false")
	}

	// Applied: symlink created.
	link := filepath.Join(home, ".rc")
	if target, err := os.Readlink(link); err != nil {
		t.Fatalf("symlink not created: %v", err)
	} else if want := filepath.Join(dir, "src", "rc"); target != want {
		t.Fatalf("link target = %q, want %q", target, want)
	}
}

// TestInitURLAfterFlags: the repo-URL positional can come after the flags
// (SplitArgs must still extract the trailing positional), not just before them.
func TestInitURLAfterFlags(t *testing.T) {
	r := &run.FakeRunner{}
	store, home, dir := setupInit(t, r)
	r.Responses = map[string]run.Response{
		"git -C " + dir + " remote get-url origin": {Err: os.ErrNotExist},
		"git clone " + initTestURL + " " + dir:     {Stdout: ""},
	}

	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", "-dir", dir, "-profile", "developer", "-yes", initTestURL}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}

	// Clone was called.
	cloned := false
	for _, c := range r.Calls {
		if c == "git clone "+initTestURL+" "+dir {
			cloned = true
		}
	}
	if !cloned {
		t.Fatalf("git clone not called; calls=%v", r.Calls)
	}

	// State saved.
	st, existed, err := store.Load()
	if err != nil || !existed {
		t.Fatalf("state not saved: existed=%v err=%v", existed, err)
	}
	if st.RepoDir != dir || st.RepoURL != initTestURL || st.Profile != "developer" {
		t.Fatalf("state = %+v, want dir/url/profile set", st)
	}
	if len(st.Packages) != 1 || st.Packages[0] != "tools" {
		t.Fatalf("state packages = %v, want [tools]", st.Packages)
	}

	// Applied: symlink created.
	link := filepath.Join(home, ".rc")
	if target, err := os.Readlink(link); err != nil {
		t.Fatalf("symlink not created: %v", err)
	} else if want := filepath.Join(dir, "src", "rc"); target != want {
		t.Fatalf("link target = %q, want %q", target, want)
	}
}

func TestInitInteractivePicker(t *testing.T) {
	r := &run.FakeRunner{}
	store, home, dir := setupInit(t, r)
	r.Responses = map[string]run.Response{
		"git -C " + dir + " remote get-url origin": {Err: os.ErrNotExist},
		"git clone " + initTestURL + " " + dir:     {Stdout: ""},
	}
	pickerRun = func(profiles []picker.Profile, items []picker.Item) (picker.Result, error) {
		return picker.Result{Profile: "developer", Packages: []string{"tools"}, Confirmed: true}, nil
	}
	setStdin(t, "y\n")

	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", initTestURL, "-dir", dir}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	st, existed, err := store.Load()
	if err != nil || !existed {
		t.Fatalf("state not saved: existed=%v err=%v", existed, err)
	}
	if st.Profile != "developer" || len(st.Packages) != 1 || st.Packages[0] != "tools" {
		t.Fatalf("state = %+v, want picker selection", st)
	}
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
}

func TestInitCancelledPicker(t *testing.T) {
	r := &run.FakeRunner{}
	store, _, dir := setupInit(t, r)
	r.Responses = map[string]run.Response{
		"git -C " + dir + " remote get-url origin": {Err: os.ErrNotExist},
		"git clone " + initTestURL + " " + dir:     {Stdout: ""},
	}
	pickerRun = func(profiles []picker.Profile, items []picker.Item) (picker.Result, error) {
		return picker.Result{Confirmed: false}, nil
	}
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", initTestURL, "-dir", dir}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	if _, existed, _ := store.Load(); existed {
		t.Fatalf("state should not be saved on cancel")
	}
}

func TestInitUnknownProfile(t *testing.T) {
	r := &run.FakeRunner{}
	_, _, dir := setupInit(t, r)
	r.Responses = map[string]run.Response{
		"git -C " + dir + " remote get-url origin": {Err: os.ErrNotExist},
		"git clone " + initTestURL + " " + dir:     {Stdout: ""},
	}
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", initTestURL, "-dir", dir, "-profile", "nope", "-yes"}, &out, &errw)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; err=%s", code, errw.String())
	}
}

func TestInitYesWithoutProfile(t *testing.T) {
	r := &run.FakeRunner{}
	_, _, dir := setupInit(t, r)
	r.Responses = map[string]run.Response{
		"git -C " + dir + " remote get-url origin": {Err: os.ErrNotExist},
		"git clone " + initTestURL + " " + dir:     {Stdout: ""},
	}
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", initTestURL, "-dir", dir, "-yes"}, &out, &errw)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; err=%s", code, errw.String())
	}
}

func TestInitNoURLEmptyDir(t *testing.T) {
	r := &run.FakeRunner{}
	_, _, dir := setupInit(t, r)
	// dir is not a repo: RemoteURL errors, and no url was provided.
	r.Responses = map[string]run.Response{
		"git -C " + dir + " remote get-url origin": {Err: os.ErrNotExist},
	}
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", "-dir", dir, "-profile", "developer", "-yes"}, &out, &errw)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; err=%s", code, errw.String())
	}
}

// TestInitExtraPositional verifies that a second positional argument is
// rejected with exit 2.
func TestInitExtraPositional(t *testing.T) {
	r := &run.FakeRunner{}
	_, _, dir := setupInit(t, r)
	// RemoteURL and clone not reached; extra-positional check is pre-repo.
	r.Responses = map[string]run.Response{}
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", initTestURL, "extra-arg", "-dir", dir}, &out, &errw)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; err=%s", code, errw.String())
	}
}

// upToDateCheckout scripts dir as a clean checkout of initTestURL that is
// already at its upstream.
func upToDateCheckout(dir string) map[string]run.Response {
	return map[string]run.Response{
		"git -C " + dir + " remote get-url origin":                   {Stdout: initTestURL + "\n"},
		"git -C " + dir + " fetch --quiet":                           {},
		"git -C " + dir + " status --porcelain --untracked-files=no": {},
		"git -C " + dir + " rev-list --count HEAD..@{u}":             {Stdout: "0\n"},
	}
}

// TestInitReuseExistingRepo verifies that when -dir already contains a repo
// whose origin == the given URL, Clone is NOT called and init proceeds.
func TestInitReuseExistingRepo(t *testing.T) {
	r := &run.FakeRunner{}
	store, home, dir := setupInit(t, r)
	// RemoteURL returns the url → reuse path; no clone scripted.
	r.Responses = upToDateCheckout(dir)
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", initTestURL, "-dir", dir, "-profile", "developer", "-yes"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	// Assert git clone was NOT called.
	for _, c := range r.Calls {
		if strings.HasPrefix(c, "git clone") {
			t.Fatalf("git clone should not have been called; calls=%v", r.Calls)
		}
	}
	// State saved with correct URL.
	st, existed, err := store.Load()
	if err != nil || !existed {
		t.Fatalf("state not saved: existed=%v err=%v", existed, err)
	}
	if st.RepoURL != initTestURL {
		t.Fatalf("state.RepoURL = %q, want %q", st.RepoURL, initTestURL)
	}
	// Symlink applied.
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
}

// TestInitOriginMismatch verifies that when the repo dir's origin differs from
// the supplied URL, init exits 2 with a UsageError.
func TestInitOriginMismatch(t *testing.T) {
	r := &run.FakeRunner{}
	_, _, dir := setupInit(t, r)
	const otherURL = "https://other.example.com/dotfiles.git"
	r.Responses = map[string]run.Response{
		"git -C " + dir + " remote get-url origin": {Stdout: otherURL + "\n"},
	}
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", initTestURL, "-dir", dir, "-profile", "developer", "-yes"}, &out, &errw)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (mismatch); err=%s", code, errw.String())
	}
}

// TestInitAdoptOrigin verifies that when no URL is given but -dir is an
// existing repo, the origin URL is adopted and saved to state.
func TestInitAdoptOrigin(t *testing.T) {
	r := &run.FakeRunner{}
	store, home, dir := setupInit(t, r)
	// RemoteURL returns a URL; no url positional provided → adopt path.
	r.Responses = upToDateCheckout(dir)
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", "-dir", dir, "-profile", "developer", "-yes"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	st, existed, err := store.Load()
	if err != nil || !existed {
		t.Fatalf("state not saved: existed=%v err=%v", existed, err)
	}
	if st.RepoURL != initTestURL {
		t.Fatalf("state.RepoURL = %q, want adopted %q", st.RepoURL, initTestURL)
	}
	// Symlink applied.
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err != nil {
		t.Fatalf("symlink not created: %v", err)
	}
}

// hermeticGit clears the GIT_* environment (a hook or worktree around the test
// run sets some) and points git at no global or system config.
func hermeticGit(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

// commitManifest writes kempt.toml in the work tree at dir and commits it.
func commitManifest(t *testing.T, dir, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "kempt.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "manifest")
}

// existingClone makes a remote holding initManifest and a clone of it at dir,
// and runs init against the real git with a temp Home and state store.
func existingClone(t *testing.T) (remote, upstream, dir, home string, store *state.Store) {
	t.Helper()
	hermeticGit(t)
	root := t.TempDir()
	remote, upstream, dir = filepath.Join(root, "remote.git"), filepath.Join(root, "upstream"), filepath.Join(root, "repo")
	git(t, root, "init", "-q", "--bare", "-b", "main", remote)
	git(t, root, "clone", "-q", remote, upstream)
	if err := os.MkdirAll(filepath.Join(upstream, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upstream, "src", "rc"), []byte("rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitManifest(t, upstream, initManifest)
	git(t, upstream, "push", "-q", "origin", "HEAD:main")
	git(t, root, "clone", "-q", remote, dir)
	home, store = t.TempDir(), &state.Store{Dir: t.TempDir()}
	origSave, origCtx := saveState, newContext
	saveState = func(s *state.State) error { return store.Save(s) }
	newContext = func(repoDir string) (*machine.Context, error) {
		return &machine.Context{Home: home, RepoDir: repoDir, OS: "darwin", Arch: "arm64", Runner: run.RealRunner{}, Cache: map[string]string{}}, nil
	}
	t.Cleanup(func() { saveState, newContext = origSave, origCtx })
	return remote, upstream, dir, home, store
}

const remoteOnlyProfile = initManifest + `
[profiles.remote-only]
description = "a profile only the remote has"
packages = ["tools"]
`

func TestInitBringsExistingCloneUpToDate(t *testing.T) {
	remote, upstream, dir, home, _ := existingClone(t)
	commitManifest(t, upstream, remoteOnlyProfile)
	git(t, upstream, "push", "-q", "origin", "HEAD:main")

	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", remote, "-dir", dir, "-profile", "remote-only", "-yes"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}
	if _, err := os.Readlink(filepath.Join(home, ".rc")); err != nil {
		t.Fatalf("remote-only profile not applied: %v", err)
	}
}

func TestInitStopsOnCheckoutItCannotFastForward(t *testing.T) {
	for name, tc := range map[string]struct {
		local  func(t *testing.T, dir string)
		reason string
	}{
		"diverged": {func(t *testing.T, dir string) {
			commitManifest(t, dir, initManifest+"\n# a local commit\n")
		}, "diverged"},
		"uncommitted": {func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "kempt.toml"), []byte(initManifest+"\n# local edit\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "uncommitted"},
	} {
		t.Run(name, func(t *testing.T) {
			remote, upstream, dir, home, store := existingClone(t)
			commitManifest(t, upstream, remoteOnlyProfile)
			git(t, upstream, "push", "-q", "origin", "HEAD:main")
			tc.local(t, dir)
			head := git(t, dir, "rev-parse", "HEAD")

			var out, errw bytes.Buffer
			code := Dispatch([]string{"init", remote, "-dir", dir, "-profile", "remote-only", "-yes"}, &out, &errw)
			if code != 2 {
				t.Fatalf("exit = %d, want 2; out=%s err=%s", code, out.String(), errw.String())
			}
			if msg := errw.String(); !strings.Contains(msg, dir) || !strings.Contains(msg, tc.reason) {
				t.Fatalf("error does not name %s and %q: %q", dir, tc.reason, msg)
			}
			if got := git(t, dir, "rev-parse", "HEAD"); got != head {
				t.Fatalf("checkout moved from %s to %s", head, got)
			}
			if _, existed, _ := store.Load(); existed {
				t.Fatal("state saved though init stopped")
			}
			if _, err := os.Lstat(filepath.Join(home, ".rc")); err == nil {
				t.Fatal("init applied though it stopped")
			}
		})
	}
}
