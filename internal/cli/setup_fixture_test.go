package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
	"github.com/subaud-tools/tools-common/setupbundle"
)

// Unrelated git pulls and doctor's pi inventory are substituted. The version executable
// really runs; any implicit setup/installer command fails this fixture.
type fixtureRunner struct {
	run.RealRunner
	t *testing.T
}

func (r fixtureRunner) Run(name string, args ...string) (string, error) {
	if name == "git" || (name == "pi" && len(args) == 1 && args[0] == "list") {
		return "", nil
	}
	if len(args) != 1 || args[0] != "version" {
		r.t.Fatalf("unexpected installer invocation: %s %v", name, args)
		return "", fmt.Errorf("unexpected installer invocation")
	}
	return r.RealRunner.Run(name, args...)
}

type approvalHook func()

func (f approvalHook) Read(p []byte) (int, error) { f(); return copy(p, "y\n"), nil }

func TestReleaseFixtureConvergesAndReviewsMatchingUpdate(t *testing.T) {
	files := map[string][]byte{}
	fixture := "../manifest/testdata/release-setup"
	fragment, err := os.ReadFile(filepath.Join(fixture, "kempt.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0.0", "1.1.0"} {
		assets := fstest.MapFS{}
		err := fs.WalkDir(os.DirFS(filepath.Join(fixture, "files")), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, err := os.ReadFile(filepath.Join(fixture, "files", path))
			if err != nil {
				return err
			}
			assets[path] = &fstest.MapFile{Data: append(data, []byte("# release "+version+"\n")...)}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := setupbundle.Build("proj", version, fragment, assets)
		if err != nil {
			t.Fatal(err)
		}
		base := "/dl/proj/" + version + "/"
		binary, sums := fakeTarball(t, "proj", "#!/bin/sh\n[ \"$1\" = version ] || exit 97\nprintf 'proj "+version+" (fixture, fixture)\\n'\n")
		files[base+"proj_darwin_arm64.tar.gz"] = []byte(binary)
		files[base+"proj_darwin_arm64.tar.gz.sha256"] = []byte(sums)
		files[base+"proj_setup.tar.gz"] = bundle
		files[base+"proj_setup.tar.gz.sha256"] = []byte(fmt.Sprintf("%x  proj_setup.tar.gz\n", sha256.Sum256(bundle)))
	}
	var mu sync.Mutex
	latest, calls := "1.0.0", 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if r.URL.Path == "/dl/proj/latest" {
			_, _ = fmt.Fprintln(w, latest)
			return
		}
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	rel := release.RealReleases{Client: server.Client()}
	home, repo, _ := setupMachine(t, rel)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	src := strings.Replace(setupManifest, "tackle.tools", strings.TrimPrefix(server.URL, "https://"), 1)
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	newContext = func(dir string) (*machine.Context, error) {
		return &machine.Context{Home: home, RepoDir: dir, OS: "darwin", Arch: "arm64", Runner: fixtureRunner{t: t}, Releases: rel, Cache: map[string]string{}}, nil
	}
	command := func(args ...string) string {
		t.Helper()
		var out, errw bytes.Buffer
		if code := Dispatch(args, &out, &errw); code != 0 {
			t.Fatalf("%v exit %d: %s%s", args, code, &out, &errw)
		}
		return out.String()
	}
	read := func(path string) []byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// Introduce a foreign file after review: earlier steps may succeed, but
	// the run must fail visibly and leave the remaining conflict in the next plan.
	foreign := filepath.Join(home, ".claude/skills/proj/reference.md")
	stdin = approvalHook(func() {
		mu.Lock()
		latest = "1.1.0"
		mu.Unlock()
		if err := os.MkdirAll(filepath.Dir(foreign), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(foreign, []byte("foreign\n"), 0644); err != nil {
			t.Fatal(err)
		}
	})
	t.Cleanup(func() { stdin = os.Stdin })
	var partial, partialErr bytes.Buffer
	if code := Dispatch([]string{"apply"}, &partial, &partialErr); code == 0 || !strings.Contains(partial.String(), "failed") {
		t.Fatalf("partial apply not visible: %d %s%s", code, &partial, &partialErr)
	}
	if string(read(foreign)) != "foreign\n" {
		t.Fatal("foreign content overwritten")
	}
	if !strings.Contains(string(read(filepath.Join(home, ".config/zsh/proj.zsh"))), "1.0.0") || !strings.Contains(string(read(filepath.Join(home, ".local/bin/proj"))), "1.0.0") {
		t.Fatal("prepared binary/setup changed with latest pointer")
	}
	remaining := command("plan", "-v")
	if !strings.Contains(remaining, "blocked") || !strings.Contains(remaining, foreign) {
		t.Fatalf("partial work absent from next plan: %s", remaining)
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	latest = "1.0.0"
	mu.Unlock()
	command("apply", "-yes")
	store, err := state.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(home, ".local/bin/proj"), filepath.Join(home, ".config/zsh/proj.zsh"), filepath.Join(home, ".claude/skills/proj/SKILL.md"), filepath.Join(home, ".claude/skills/proj/reference.md"), filepath.Join(store.Dir, "managed-files.json")}
	for path, want := range map[string]string{
		paths[1]: "proj() { command proj \"$@\"; }\n# release 1.0.0\n",
		paths[2]: "---\nname: proj\ndescription: fixture skill\n---\nSee reference.md.\n# release 1.0.0\n",
		paths[3]: "Fixture reference.\n# release 1.0.0\n",
	} {
		if got := string(read(path)); got != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
	before := map[string][]byte{}
	for _, p := range paths {
		before[p] = read(p)
	}
	version, err := (run.RealRunner{}).Run(paths[0], "version")
	if err != nil || !strings.Contains(version, "1.0.0") {
		t.Fatalf("version %q: %v", version, err)
	}
	firstPlan := command("plan", "-v")
	command("apply", "-yes")
	for _, p := range paths {
		if !bytes.Equal(before[p], read(p)) {
			t.Fatalf("repeat changed %s", p)
		}
	}
	mu.Lock()
	requests := calls
	mu.Unlock()
	if secondPlan := command("plan", "-v"); secondPlan != firstPlan {
		t.Fatalf("repeat plan differs:\n%s\n%s", firstPlan, secondPlan)
	}
	mu.Lock()
	after := calls
	latest = "1.1.0"
	mu.Unlock()
	if after != requests {
		t.Fatalf("offline plan made %d release requests", after-requests)
	}
	stdin = strings.NewReader("n\n")
	t.Cleanup(func() { stdin = os.Stdin })
	var previewOut, previewErr bytes.Buffer
	if code := Dispatch([]string{"apply"}, &previewOut, &previewErr); code != 1 {
		t.Fatalf("declined apply exit %d", code)
	}
	preview := previewOut.String()
	if !strings.Contains(preview, "1.0.0 -> 1.1.0") || !strings.Contains(preview, "update content") || !strings.Contains(preview, "aborted") {
		t.Fatalf("missing candidate preview: %s", preview)
	}
	for _, p := range paths {
		if !bytes.Equal(before[p], read(p)) {
			t.Fatalf("preview changed %s", p)
		}
	}
	updated := command("update")
	if !strings.Contains(updated, "managed-file ") || strings.Index(updated, "managed-file ") > strings.Index(updated, "applied:") {
		t.Fatalf("setup not previewed before update: %s", updated)
	}
	for _, p := range paths[:4] {
		if !strings.Contains(string(read(p)), "1.1.0") {
			t.Fatalf("unmatched release content in %s", p)
		}
	}
	command("doctor")
	// Missing managed content is unhealthy and attributed, then repaired normally.
	if err := os.Remove(paths[3]); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := Dispatch([]string{"doctor"}, &out, &errw); code == 0 || !strings.Contains(out.String(), "proj/setup/proj") {
		t.Fatalf("missing file not attributed: %d %s%s", code, &out, &errw)
	}
	command("apply", "-yes")
	command("doctor")
	// Losing the cached bundle is unresolved, not a healthy offline plan.
	cached, err := filepath.Glob(filepath.Join(store.Dir, "setup", "*.json"))
	if err != nil || len(cached) != 1 {
		t.Fatalf("cache records %v: %v", cached, err)
	}
	if err := os.Remove(cached[0]); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	requests = calls
	mu.Unlock()
	unresolved := command("plan")
	if !strings.Contains(unresolved, "not resolved") || !strings.Contains(unresolved, "kempt apply") {
		t.Fatalf("missing cache passed: %s", unresolved)
	}
	mu.Lock()
	after = calls
	mu.Unlock()
	if after != requests {
		t.Fatalf("unresolved offline plan made %d requests", after-requests)
	}
}
