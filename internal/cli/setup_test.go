package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
	"github.com/subaud-tools/tools-common/setupbundle"
)

const setupManifest = `
[kempt]
spec = 2

[packages.proj]
description = "proj"
  [[packages.proj.download]]
  site = "tackle.tools"
  tool = "proj"
  version = "latest"
  bin = "proj"
  setup = true
`

const setupFragment = `
[kempt]
spec = 2

[packages.integration]
description = "proj integration"
  [[packages.integration.managed-file]]
  from = "proj.zsh"
  to = "~/.config/zsh/proj.zsh"
  mode = "0644"
`

// setupRelease adds proj version's binary and setup bundle (each with its
// sidecar) to files, the wrapper reading wrapper.
func setupRelease(t *testing.T, files map[string][]byte, version, wrapper string) {
	t.Helper()
	base := "https://tackle.tools/dl/proj/" + version + "/"
	asset, sum := fakeTarball(t, "proj", "proj "+version)
	files[base+"proj_darwin_arm64.tar.gz"] = []byte(asset)
	files[base+"proj_darwin_arm64.tar.gz.sha256"] = []byte(sum)
	b, err := setupbundle.Build("proj", version, []byte(setupFragment), fstest.MapFS{"proj.zsh": {Data: []byte(wrapper)}})
	if err != nil {
		t.Fatal(err)
	}
	d := sha256.Sum256(b)
	files[base+"proj_setup.tar.gz"] = b
	files[base+"proj_setup.tar.gz.sha256"] = []byte(fmt.Sprintf("%s  proj_setup.tar.gz\n", hex.EncodeToString(d[:])))
}

// setupMachine is an isolated machine with proj's saved selection: kempt's
// state under a temp XDG_DATA_HOME, proj installed at installed (scripted in
// order by versions), and rel as its releases.
func setupMachine(t *testing.T, rel release.Releases, versions ...string) (home, repo string, r *run.FakeRunner) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	repo = t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(setupManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	home = t.TempDir()
	bin := filepath.Join(home, ".local", "bin", "proj")
	r = &run.FakeRunner{Responses: map[string]run.Response{
		"git -C " + repo + " pull --rebase --autostash": {},
	}, Sequences: map[string][]run.Response{}}
	if len(versions) > 0 {
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, v := range versions {
			r.Sequences[bin+" version"] = append(r.Sequences[bin+" version"], run.Response{Stdout: "proj " + v + " (a, d)\n"})
		}
	}
	// setupUpdate installs (and restores) the seams; its own repo is unused.
	setupUpdate(t, r, rel)
	store := &state.Store{Dir: t.TempDir()}
	if err := store.Save(&state.State{RepoDir: repo, Packages: []string{"proj"}}); err != nil {
		t.Fatal(err)
	}
	loadState = func() (*state.State, bool, error) { return store.Load() }
	newContext = func(repoDir string) (*machine.Context, error) {
		return &machine.Context{Home: home, RepoDir: repoDir, OS: "darwin", Arch: "arm64", Runner: r, Releases: rel, Cache: map[string]string{}}, nil
	}
	return home, repo, r
}

// TestUpdateShowsBinaryAndSetupBeforeChanging: update used to roll an opted-in
// binary before building its plan, so the release's setup could only follow
// as an unreviewed afterthought. Both now appear in the plan before anything
// is written.
func TestUpdateShowsBinaryAndSetupBeforeChanging(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.1.0\n")}
	setupRelease(t, files, "1.1.0", "proj() { :; }\n")
	home, _, _ := setupMachine(t, release.FakeReleases{Files: files}, "1.0.0", "1.0.0", "1.1.0")

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d; out=%s err=%s", code, out.String(), errw.String())
	}
	got := out.String()
	upgrade := strings.Index(got, "upgrade proj 1.0.0 -> 1.1.0")
	wrapper := strings.Index(got, "managed-file "+filepath.Join(home, ".config", "zsh", "proj.zsh"))
	applied := strings.Index(got, "applied:")
	if upgrade < 0 || wrapper < 0 || applied < 0 || upgrade > applied || wrapper > applied {
		t.Fatalf("binary and setup must both be planned before any change:\n%s", got)
	}
	if strings.Contains(got, "rolled proj") {
		t.Fatalf("update rolled proj outside the plan:\n%s", got)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".config", "zsh", "proj.zsh")); err != nil || string(b) != "proj() { :; }\n" {
		t.Fatalf("wrapper = %q, %v", b, err)
	}
}

// movingReleases answers proj's latest pointer with each of latest in turn
// (the last repeating), standing in for a release published mid-review.
type movingReleases struct {
	release.FakeReleases
	latest *[]string
	gets   *int
}

func (m movingReleases) Download(url string) ([]byte, error) {
	*m.gets++
	if url == "https://tackle.tools/dl/proj/latest" {
		v := (*m.latest)[0]
		if len(*m.latest) > 1 {
			*m.latest = (*m.latest)[1:]
		}
		return []byte(v + "\n"), nil
	}
	return m.FakeReleases.Download(url)
}

// TestApplyInstallsTheReleaseItShowed: latest moving to 1.2.0 after apply
// showed its plan must not change the binary or the setup it applies.
func TestApplyInstallsTheReleaseItShowed(t *testing.T) {
	files := map[string][]byte{}
	setupRelease(t, files, "1.1.0", "v1.1\n")
	setupRelease(t, files, "1.2.0", "v1.2\n")
	latest, gets := []string{"1.1.0", "1.2.0"}, 0
	home, _, _ := setupMachine(t, movingReleases{release.FakeReleases{Files: files}, &latest, &gets})
	stdin = strings.NewReader("y\n")
	t.Cleanup(func() { stdin = os.Stdin })

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply"}, &out, &errw); code != 0 {
		t.Fatalf("exit = %d; out=%s err=%s", code, out.String(), errw.String())
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".local", "bin", "proj")); string(b) != "proj 1.1.0" {
		t.Fatalf("binary = %q, want the 1.1.0 the plan showed", b)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "zsh", "proj.zsh")); string(b) != "v1.1\n" {
		t.Fatalf("wrapper = %q, want 1.1.0's", b)
	}
}

// TestPlanReportsUnresolvedSetupOffline: with nothing resolved, plan makes no
// request and says what resolves the setup rather than showing a complete plan.
func TestPlanReportsUnresolvedSetupOffline(t *testing.T) {
	latest, gets := []string{"1.1.0"}, 0
	setupMachine(t, movingReleases{release.FakeReleases{}, &latest, &gets})

	var out, errw bytes.Buffer
	_ = Dispatch([]string{"plan"}, &out, &errw)
	if gets != 0 {
		t.Fatalf("plan made %d requests", gets)
	}
	if !strings.Contains(out.String(), "setup for proj is not resolved") || !strings.Contains(out.String(), "kempt apply") {
		t.Fatalf("want an unresolved setup naming kempt apply:\n%s%s", out.String(), errw.String())
	}
}

// TestApplyFailsWhenSetupCannotBeResolved: a selected setup the release does
// not publish is a failed apply, not a success that installed only the binary.
func TestApplyFailsWhenSetupCannotBeResolved(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.1.0\n")}
	setupRelease(t, files, "1.1.0", "w\n")
	delete(files, "https://tackle.tools/dl/proj/1.1.0/proj_setup.tar.gz")
	home, _, _ := setupMachine(t, release.FakeReleases{Files: files})

	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code == 0 {
		t.Fatalf("apply succeeded without proj's setup:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "proj")); err == nil {
		t.Fatal("apply installed the binary without its setup")
	}
}

// TestRefreshHoldsCandidateSetup: refresh applies files only, so it must not
// write a newer release's setup while that release's binary waits for apply.
func TestRefreshHoldsCandidateSetup(t *testing.T) {
	files := map[string][]byte{}
	setupRelease(t, files, "1.1.0", "v1.1\n")
	home, repo, r := setupMachine(t, release.FakeReleases{Files: files}, "1.0.0")
	pinned := strings.Replace(setupManifest, `version = "latest"`, `version = "1.1.0"`, 1)
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Responses["git -C "+repo+" fetch --quiet"] = run.Response{}
	r.Responses["git -C "+repo+" rev-list --count HEAD..@{u}"] = run.Response{Stdout: "0\n"}
	stdin = strings.NewReader("n\n")
	t.Cleanup(func() { stdin = os.Stdin })
	var out, errw bytes.Buffer
	_ = Dispatch([]string{"apply"}, &out, &errw) // resolves 1.1.0, then declined
	if !strings.Contains(out.String(), "managed-file ") || !strings.Contains(out.String(), "aborted") {
		t.Fatalf("candidate was not prepared and declined: %s%s", &out, &errw)
	}
	st, _, _ := loadState()
	st.AutoApplyFiles = true
	store := &state.Store{Dir: t.TempDir()}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	loadState = func() (*state.State, bool, error) { return store.Load() }

	if code := Dispatch([]string{"refresh"}, &out, &errw); code != 0 {
		t.Fatalf("refresh exit = %d; %s%s", code, out.String(), errw.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "zsh", "proj.zsh")); err == nil {
		t.Fatalf("refresh wrote 1.1.0's setup while proj 1.0.0 is installed:\n%s", out.String())
	}
}

func TestFailedBinaryBlocksDependentSetup(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.1.0\n")}
	setupRelease(t, files, "1.1.0", "w\n")
	// Checksummed, but not a binary archive: resolution succeeds, installation fails.
	base := "https://tackle.tools/dl/proj/1.1.0/proj_darwin_arm64.tar.gz"
	files[base] = []byte("not a tarball")
	sum := sha256.Sum256(files[base])
	files[base+".sha256"] = []byte(hex.EncodeToString(sum[:]))
	home, _, _ := setupMachine(t, release.FakeReleases{Files: files})
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code == 0 {
		t.Fatalf("failed binary succeeded: %s%s", &out, &errw)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "zsh", "proj.zsh")); !os.IsNotExist(err) {
		t.Fatalf("dependent setup ran after parent failure: %v\n%s", err, &out)
	}
	if !strings.Contains(out.String(), "blocked") {
		t.Fatalf("missing visible blocker: %s", &out)
	}
}

// Composition must not move a release's contribution ahead of its binary.
func TestFailedBinaryBlocksFoldedSetupMerge(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.1.0\n")}
	setupRelease(t, files, "1.1.0", "w\n")
	fragment := setupFragment + `
 [[packages.integration.json-merge]]
 file = "~/.config/shared.json"
 merge = { release = true }
 `
	b, err := setupbundle.Build("proj", "1.1.0", []byte(fragment), fstest.MapFS{"proj.zsh": {Data: []byte("w\n")}})
	if err != nil {
		t.Fatal(err)
	}
	base := "https://tackle.tools/dl/proj/1.1.0/"
	files[base+"proj_setup.tar.gz"] = b
	sum := sha256.Sum256(b)
	files[base+"proj_setup.tar.gz.sha256"] = []byte(hex.EncodeToString(sum[:]))
	files[base+"proj_darwin_arm64.tar.gz"] = []byte("bad")
	sum = sha256.Sum256([]byte("bad"))
	files[base+"proj_darwin_arm64.tar.gz.sha256"] = []byte(hex.EncodeToString(sum[:]))
	home, repo, _ := setupMachine(t, release.FakeReleases{Files: files})
	// A merge before the download used to absorb the release contribution.
	src := strings.Replace(setupManifest, `  [[packages.proj.download]]`, `  [[packages.proj.json-merge]]
 file = "~/.config/shared.json"
 merge = { preference = true }
 [[packages.proj.download]]`, 1)
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code == 0 {
		t.Fatal("expected failed binary")
	}
	data, _ := os.ReadFile(filepath.Join(home, ".config", "shared.json"))
	if strings.Contains(string(data), "release") {
		t.Fatalf("release merge ran before failed binary: %s\n%s", data, &out)
	}
}

func TestUpgradeReviewsMatchingSetup(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("2.0.0\n")}
	setupRelease(t, files, "2.0.0", "v2\n")
	home, _, _ := setupMachine(t, release.FakeReleases{Files: files}, "1.0.0", "1.0.0", "2.0.0")
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"upgrade", "-yes", "proj"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s%s", code, &out, &errw)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "zsh", "proj.zsh")); string(b) != "v2\n" {
		t.Fatalf("missing matching setup: %q\n%s", b, &out)
	}
	if strings.Index(out.String(), "managed-file ") > strings.Index(out.String(), "applied:") {
		t.Fatalf("setup not reviewed first: %s", &out)
	}
	out.Reset()
	errw.Reset()
	if code := Dispatch([]string{"plan", "-v"}, &out, &errw); code != 0 || strings.Contains(out.String(), "not resolved") || !strings.Contains(out.String(), "from proj 2.0.0") {
		t.Fatalf("upgrade did not cache the manifest's rolling selection: %d %s%s", code, &out, &errw)
	}
}

func TestRefreshHoldsSetupWhenInstalledIdentityUnknown(t *testing.T) {
	files := map[string][]byte{}
	setupRelease(t, files, "1.1.0", "v1.1\n")
	home, repo, r := setupMachine(t, release.FakeReleases{Files: files}, "dev")
	src := strings.Replace(setupManifest, `version = "latest"`, `version = "1.1.0"`, 1)
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	stdin = strings.NewReader("n\n")
	t.Cleanup(func() { stdin = os.Stdin })
	var out, errw bytes.Buffer
	_ = Dispatch([]string{"apply"}, &out, &errw)
	st, _, _ := loadState()
	st.AutoApplyFiles = true
	store := &state.Store{Dir: t.TempDir()}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	loadState = func() (*state.State, bool, error) { return store.Load() }
	r.Responses["git -C "+repo+" fetch --quiet"] = run.Response{}
	r.Responses["git -C "+repo+" rev-list --count HEAD..@{u}"] = run.Response{Stdout: "0\n"}
	if code := Dispatch([]string{"refresh"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s%s", code, &out, &errw)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "zsh", "proj.zsh")); !os.IsNotExist(err) {
		t.Fatalf("setup applied against unknown binary: %v\n%s", err, &out)
	}
}

func TestUpdateReportsHeldSetupMajor(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("2.0.0\n")}
	setupRelease(t, files, "1.0.0", "v1\n")
	home, _, _ := setupMachine(t, release.FakeReleases{Files: files}, "1.0.0")
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s%s", code, &out, &errw)
	}
	if !strings.Contains(out.String(), "held proj 1.0.0 -> 2.0.0") || !strings.Contains(out.String(), "kempt upgrade proj") {
		t.Fatalf("major hold not reported: %s", &out)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".local", "bin", "proj")); len(b) != 0 {
		t.Fatalf("held binary replaced: %q", b)
	}
}

func TestApplyReportsBlockedReleaseSetupAsIncomplete(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.1.0\n")}
	setupRelease(t, files, "1.1.0", "owned\n")
	home, _, _ := setupMachine(t, release.FakeReleases{Files: files}, "1.1.0")
	path := filepath.Join(home, ".config", "zsh", "proj.zsh")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code == 0 {
		t.Fatalf("blocked release setup reported success: %s%s", &out, &errw)
	}
	if b, _ := os.ReadFile(path); string(b) != "foreign\n" {
		t.Fatalf("foreign file changed: %q", b)
	}
}

type installedPathRunner struct {
	*run.FakeRunner
	home string
}

func (r installedPathRunner) LookPath(name string) (string, error) {
	path := filepath.Join(r.home, ".local", "bin", name)
	_, err := os.Stat(path)
	return path, err
}
func TestFreshSetupFollowsParentWithPendingVerify(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.1.0\n")}
	setupRelease(t, files, "1.1.0", "owned\n")
	home, repo, r := setupMachine(t, release.FakeReleases{Files: files})
	src := setupManifest + "\n[[packages.proj.verify]]\ncommand-exists = \"proj\"\n"
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	old := newContext
	newContext = func(dir string) (*machine.Context, error) {
		ctx, err := old(dir)
		if err == nil {
			ctx.Runner = installedPathRunner{r, home}
		}
		return ctx, err
	}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s%s", code, &out, &errw)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "zsh", "proj.zsh")); string(b) != "owned\n" {
		t.Fatalf("pending verify blocked setup: %q\n%s", b, &out)
	}
}

func TestFreshSetupVerifiesAfterParentInstall(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.1.0\n")}
	setupRelease(t, files, "1.1.0", "owned\n")
	b, err := setupbundle.Build("proj", "1.1.0", []byte(setupFragment+"\n[[packages.integration.verify]]\ncommand-exists=\"proj\"\n"), fstest.MapFS{"proj.zsh": {Data: []byte("owned\n")}})
	if err != nil {
		t.Fatal(err)
	}
	base := "https://tackle.tools/dl/proj/1.1.0/proj_setup.tar.gz"
	files[base] = b
	sum := sha256.Sum256(b)
	files[base+".sha256"] = []byte(hex.EncodeToString(sum[:]))
	home, _, r := setupMachine(t, release.FakeReleases{Files: files})
	old := newContext
	newContext = func(dir string) (*machine.Context, error) {
		ctx, err := old(dir)
		if err == nil {
			ctx.Runner = installedPathRunner{r, home}
		}
		return ctx, err
	}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s%s", code, &out, &errw)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "zsh", "proj.zsh")); string(b) != "owned\n" {
		t.Fatalf("setup not installed: %q", b)
	}
}

func TestUpdateReviewsSetupBeforeLegacyRoll(t *testing.T) {
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.1.0\n"), "https://tackle.tools/dl/aux/latest": []byte("1.1.0\n")}
	setupRelease(t, files, "1.1.0", "owned\n")
	asset, sum := fakeTarball(t, "aux", "aux 1.1.0")
	files["https://tackle.tools/dl/aux/1.1.0/aux_darwin_arm64.tar.gz"] = []byte(asset)
	files["https://tackle.tools/dl/aux/1.1.0/aux_darwin_arm64.tar.gz.sha256"] = []byte(sum)
	home, repo, r := setupMachine(t, release.FakeReleases{Files: files}, "1.0.0", "1.0.0", "1.1.0")
	aux := filepath.Join(home, ".local", "bin", "aux")
	if err := os.WriteFile(aux, nil, 0755); err != nil {
		t.Fatal(err)
	}
	r.Responses[aux+" version"] = run.Response{Stdout: "aux 1.0.0"}
	src := setupManifest + "\n[[packages.proj.download]]\nsite=\"tackle.tools\"\ntool=\"aux\"\nbin=\"aux\"\nversion=\"latest\"\n"
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"update"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s%s", code, &out, &errw)
	}
	plan, roll := strings.Index(out.String(), "managed-file "), strings.Index(out.String(), "rolled aux")
	if plan < 0 || roll < 0 || plan > roll {
		t.Fatalf("legacy roll happened before combined plan: %s", &out)
	}
}
