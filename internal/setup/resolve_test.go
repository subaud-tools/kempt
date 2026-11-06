package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/tools-common/setupbundle"
)

func fixture(t *testing.T, fragment string) (*machine.Context, []*manifest.Package) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ctx := &machine.Context{Home: t.TempDir(), RepoDir: t.TempDir(), OS: "linux", Arch: "amd64", Runner: &run.FakeRunner{}, Releases: release.FakeReleases{Files: published(t, fragment, "1.0.0")}}
	pkgs := []*manifest.Package{{Name: "proj", Steps: []manifest.Step{manifest.DownloadStep{Site: "tackle.tools", Tool: "proj", Bin: "proj", Version: "1.0.0", Setup: true}}}}
	return ctx, pkgs
}

// published returns the release files of proj at version, with /latest
// pointing at it.
func published(t *testing.T, fragment, version string) map[string][]byte {
	t.Helper()
	b, err := setupbundle.Build("proj", version, []byte(fragment), fstest.MapFS{"wrapper": {Data: []byte("owned")}})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte(version + "\n")}
	base := "https://tackle.tools/dl/proj/" + version + "/"
	for name, data := range map[string][]byte{"proj_setup.tar.gz": b, "proj_linux_amd64.tar.gz": []byte("binary")} {
		files[base+name] = data
		sum := sha256.Sum256(data)
		files[base+name+".sha256"] = []byte(hex.EncodeToString(sum[:]))
	}
	return files
}

// offlineDownloadDetail caches latest=cached online with nothing installed,
// then installs a binary reporting installed and plans offline, as plan,
// refresh, doctor and verify do. It returns the download step's delta.
func offlineDownloadDetail(t *testing.T, cached, installed string) engine.Delta {
	t.Helper()
	ctx, _ := fixture(t, "")
	ctx.Releases = release.FakeReleases{Files: published(t, `[kempt]
spec=2
[packages.integration]
[[packages.integration.managed-file]]
from="wrapper"
to="~/.config/wrapper"
mode="0644"
`, cached)}
	pkgs := []*manifest.Package{{Name: "proj", Steps: []manifest.Step{manifest.DownloadStep{Site: "tackle.tools", Tool: "proj", Bin: "proj", Setup: true}}}}
	if _, err := Prepare(ctx, pkgs, true); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(ctx.Home, ".local", "bin", "proj")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("installed outside kempt"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx.Runner = &run.FakeRunner{Responses: map[string]run.Response{bin + " version": {Stdout: "proj " + installed + " (abc1234, 2026-11-05)"}}}
	prepared, err := Prepare(ctx, pkgs, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := engine.BuildPlan(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plan.Packages[0].Steps {
		if s.Step.Kind() == "download" {
			return s.Delta
		}
	}
	t.Fatal("no download step planned")
	return engine.Delta{}
}

func TestOfflinePlanNeverDowngradesBelowInstalled(t *testing.T) {
	d := offlineDownloadDetail(t, "0.6.0", "0.6.1")
	if d.Op != engine.OpNoop || strings.Contains(d.Detail, "-> 0.6.0") {
		t.Fatalf("cached 0.6.0 under installed 0.6.1 planned %v %q, want ok", d.Op, d.Detail)
	}
}

func TestOfflinePlanStillUpgradesToNewerCache(t *testing.T) {
	d := offlineDownloadDetail(t, "0.6.1", "0.6.0")
	if d.Op != engine.OpChange || d.Detail != "upgrade proj 0.6.0 -> 0.6.1" {
		t.Fatalf("cached 0.6.1 over installed 0.6.0 planned %v %q, want the upgrade", d.Op, d.Detail)
	}
}

func TestPrepareRejectsBundleWithoutIntegration(t *testing.T) {
	ctx, pkgs := fixture(t, "[kempt]\nspec=2\n")
	if _, err := Prepare(ctx, pkgs, true); err == nil {
		t.Fatal("empty bundle accepted as complete setup")
	}
}

func TestPreparedFilesParticipateInCompositionConflicts(t *testing.T) {
	ctx, pkgs := fixture(t, `[kempt]
 spec=2
 [packages.integration]
 [[packages.integration.managed-file]]
 from="wrapper"
 to="~/.config/wrapper"
 mode="0644"
 `)
	pkgs = append(pkgs, &manifest.Package{Name: "other", Steps: []manifest.Step{manifest.ManagedFileStep{From: "other", To: "~/.config/wrapper", Mode: "0644", Source: &manifest.SetupSource{Tool: "other", Version: "1.0.0", Data: []byte("other")}}}})
	prepared, err := Prepare(ctx, pkgs, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.BuildPlan(ctx, prepared); err == nil {
		t.Fatal("two different release-owned definitions were accepted for the same file")
	}
}

type countedReleases struct {
	release.FakeReleases
	calls *int
}

func (r countedReleases) Download(url string) ([]byte, error) {
	*r.calls++
	return r.FakeReleases.Download(url)
}
func (r countedReleases) LatestTag(repo string) (string, error) {
	*r.calls++
	return r.FakeReleases.LatestTag(repo)
}
func TestOfflinePreparedChecksNeverFetch(t *testing.T) {
	ctx, pkgs := fixture(t, `[kempt]
 spec=2
 [packages.integration]
 [[packages.integration.verify]]
 http-ok="https://example.invalid/health"
 `)
	if _, err := Prepare(ctx, pkgs, true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ctx.Releases = countedReleases{calls: &calls}
	prepared, err := Prepare(ctx, pkgs, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.BuildPlan(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("cached setup made %d offline network requests", calls)
	}
}

func TestSetupKeepsParentLayerPrecedence(t *testing.T) {
	ctx, pkgs := fixture(t, `[kempt]
 spec=2
 [packages.integration]
 [[packages.integration.json-merge]]
 file="~/.config/settings.json"
 merge={preference="release"}
 `)
	pkgs = append(pkgs, &manifest.Package{Name: "personal/preferences", Layer: "personal", Steps: []manifest.Step{manifest.JSONMergeStep{File: "~/.config/settings.json", Merge: map[string]any{"preference": "personal"}}}})
	prepared, err := Prepare(ctx, pkgs, true)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := engine.BuildPlan(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range plan.Packages {
		for _, s := range p.Steps {
			if m, ok := s.Step.(manifest.JSONMergeStep); ok {
				found = true
				if m.Merge["preference"] != "personal" {
					t.Fatalf("release displaced the later personal preference: %#v", m.Merge)
				}
			}
		}
	}
	if !found {
		t.Fatal("missing merged setup")
	}
}
