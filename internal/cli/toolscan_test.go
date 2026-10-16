package cli

import (
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
)

func TestScanTools(t *testing.T) {
	home := t.TempDir()
	fr := &run.FakeRunner{Responses: map[string]run.Response{
		home + "/.local/bin/proj version":    {Stdout: "proj 0.1.1 (a, d)\n"},    // latest tool, behind
		home + "/.local/bin/scratch version": {Stdout: "scratch 0.5.1 (a, d)\n"}, // pinned, matches
	}}
	rel := release.FakeReleases{Files: map[string][]byte{
		"https://tackle.tools/dl/proj/latest": []byte("0.1.2\n"),
	}}
	ctx := &machine.Context{Home: home, OS: "darwin", Arch: "arm64", Runner: fr, Releases: rel, Cache: map[string]string{}}

	pkg := &manifest.Package{Name: "terminal", Steps: []manifest.Step{
		manifest.DownloadStep{Site: "tackle.tools", Tool: "proj", Version: "latest", Bin: "proj"},
		manifest.DownloadStep{Site: "tackle.tools", Tool: "scratch", Version: "0.5.1", Bin: "scratch"},
	}}

	got := scanTools(ctx, []*manifest.Package{pkg})
	if len(got) != 2 {
		t.Fatalf("want 2 statuses, got %d", len(got))
	}
	if got[0].Tool != "proj" || got[0].Mode != "latest" || got[0].Installed != "0.1.1" || got[0].Target != "0.1.2" || !got[0].Behind {
		t.Fatalf("proj: %+v", got[0])
	}
	if got[1].Tool != "scratch" || got[1].Mode != "pinned" || got[1].Behind {
		t.Fatalf("scratch should be up-to-date pinned: %+v", got[1])
	}
}

// TestScanToolsPerToolNetworkFailureNonFatal verifies that one "latest"
// tool's unresolvable /dl/<tool>/latest pointer is reported on that tool's
// status (Err set, Behind false) rather than aborting the scan — a second,
// resolvable "latest" tool must still come back with its own standing.
func TestScanToolsPerToolNetworkFailureNonFatal(t *testing.T) {
	home := t.TempDir()
	fr := &run.FakeRunner{Responses: map[string]run.Response{
		home + "/.local/bin/unreachable version": {Stdout: "unreachable 0.1.0 (a, d)\n"},
		home + "/.local/bin/proj version":        {Stdout: "proj 0.1.1 (a, d)\n"},
	}}
	// Files intentionally omits the "unreachable" pointer so Download errors.
	rel := release.FakeReleases{Files: map[string][]byte{
		"https://tackle.tools/dl/proj/latest": []byte("0.1.2\n"),
	}}
	ctx := &machine.Context{Home: home, OS: "darwin", Arch: "arm64", Runner: fr, Releases: rel, Cache: map[string]string{}}

	pkg := &manifest.Package{Name: "terminal", Steps: []manifest.Step{
		manifest.DownloadStep{Site: "tackle.tools", Tool: "unreachable", Version: "latest", Bin: "unreachable"},
		manifest.DownloadStep{Site: "tackle.tools", Tool: "proj", Version: "latest", Bin: "proj"},
	}}

	got := scanTools(ctx, []*manifest.Package{pkg})
	if len(got) != 2 {
		t.Fatalf("want 2 statuses, got %d", len(got))
	}
	if got[0].Tool != "unreachable" || got[0].Err == nil || got[0].Target != "" || got[0].Behind {
		t.Fatalf("unreachable: want Err set, Target empty, Behind false; got %+v", got[0])
	}
	if got[1].Tool != "proj" || got[1].Err != nil || !got[1].Behind || got[1].Target != "0.1.2" {
		t.Fatalf("proj should still be reported behind: %+v", got[1])
	}
}

// TestScanToolsLatestNewerInstalledNotBehind covers M2: a "latest" tool whose
// installed version is NEWER than the resolved latest pointer (a rolled-back
// pointer, or a locally-built prerelease) must not be reported Behind — only
// a strictly newer Target counts.
func TestScanToolsLatestNewerInstalledNotBehind(t *testing.T) {
	home := t.TempDir()
	fr := &run.FakeRunner{Responses: map[string]run.Response{
		home + "/.local/bin/proj version": {Stdout: "proj 0.2.0 (a, d)\n"},
	}}
	rel := release.FakeReleases{Files: map[string][]byte{
		"https://tackle.tools/dl/proj/latest": []byte("0.1.9\n"),
	}}
	ctx := &machine.Context{Home: home, OS: "darwin", Arch: "arm64", Runner: fr, Releases: rel, Cache: map[string]string{}}

	pkg := &manifest.Package{Name: "terminal", Steps: []manifest.Step{
		manifest.DownloadStep{Site: "tackle.tools", Tool: "proj", Version: "latest", Bin: "proj"},
	}}

	got := scanTools(ctx, []*manifest.Package{pkg})
	if len(got) != 1 || got[0].Behind {
		t.Fatalf("installed newer than latest pointer must not be Behind: %+v", got)
	}
}
