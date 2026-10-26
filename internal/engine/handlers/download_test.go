package handlers

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/tools-common/auth"
)

// mustStatBinPresent makes os.Stat(binPath(ctx, bin)) succeed by creating the
// file under a real temp dir. Sets ctx.Home to a fresh t.TempDir() so tests
// exercising the real filesystem check don't touch the caller's ctx.Home.
func mustStatBinPresent(t *testing.T, ctx *machine.Context, bin string) {
	t.Helper()
	ctx.Home = t.TempDir()
	p := binPath(ctx, bin)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadInspectPinDrift(t *testing.T) {
	// proj present at 0.1.1, manifest pins 0.1.2 -> OpChange upgrade
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := ctxWith(fr, nil)
	mustStatBinPresent(t, ctx, "proj")
	fr.Responses[binPath(ctx, "proj")+" version"] = run.Response{Stdout: "proj 0.1.1 (abc, 2026-09-01)\n"}
	st := manifest.DownloadStep{Site: "tackle.tools", Tool: "proj", Version: "0.1.2", Bin: "proj"}

	d, err := downloadHandler{}.Inspect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange || d.Detail != "upgrade proj 0.1.1 -> 0.1.2" {
		t.Fatalf("got (%v,%q)", d.Op, d.Detail)
	}
}

func TestDownloadInspectPinMatchNoop(t *testing.T) {
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := ctxWith(fr, nil)
	mustStatBinPresent(t, ctx, "proj")
	fr.Responses[binPath(ctx, "proj")+" version"] = run.Response{Stdout: "proj 0.1.2 (abc, 2026-09-02)\n"}
	st := manifest.DownloadStep{Site: "tackle.tools", Tool: "proj", Version: "0.1.2", Bin: "proj"}
	d, _ := downloadHandler{}.Inspect(ctx, st)
	if d.Op != engine.OpNoop {
		t.Fatalf("pin match should noop, got %v", d.Op)
	}
}

func TestDownloadInspectLatestPresenceOnly(t *testing.T) {
	// latest present -> noop, and Inspect makes NO release calls (nil Releases proves it)
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := ctxWith(fr, nil) // nil Releases: a network call would panic
	mustStatBinPresent(t, ctx, "proj")
	fr.Responses[binPath(ctx, "proj")+" version"] = run.Response{Stdout: "proj 0.1.1 (abc, 2026)\n"}
	st := manifest.DownloadStep{Site: "tackle.tools", Tool: "proj", Version: "latest", Bin: "proj"}
	d, _ := downloadHandler{}.Inspect(ctx, st)
	if d.Op != engine.OpNoop {
		t.Fatalf("latest present should noop, got %v", d.Op)
	}
}

func TestDownloadInspect(t *testing.T) {
	// FakeReleases with no files: Inspect must make no Releases calls.
	ctx := ghCtx(t, release.FakeReleases{})
	h := downloadHandler{}
	st := manifest.DownloadStep{Site: "example.tools", Tool: "demo", Bin: "demo"}

	d, err := h.Inspect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v, want change", d.Op)
	}
	if d.Detail != "install demo from example.tools (latest)" {
		t.Fatalf("detail = %q", d.Detail)
	}

	// Pinned version shows in detail.
	dp, err := h.Inspect(ctx, manifest.DownloadStep{Site: "example.tools", Tool: "demo", Version: "1.4.0", Bin: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if dp.Detail != "install demo from example.tools (1.4.0)" {
		t.Fatalf("pinned detail = %q", dp.Detail)
	}

	// Present → OpNoop.
	bin := filepath.Join(ctx.Home, ".local", "bin", "demo")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err = h.Inspect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v, want noop", d.Op)
	}
	if d.Detail != "download demo" {
		t.Fatalf("detail = %q", d.Detail)
	}
}

func TestDownloadApplyLatest(t *testing.T) {
	content := []byte("#!/bin/sh\necho demo\n")
	tgz := makeTarGz(t, "demo", content)
	assetName := "demo_linux_amd64.tar.gz"
	assetURL := "https://example.tools/dl/demo/1.4.0/" + assetName
	rel := release.FakeReleases{
		Files: map[string][]byte{
			"https://example.tools/dl/demo/latest": []byte("1.4.0\n"),
			assetURL:                               tgz,
			assetURL + ".sha256":                   checksums(assetName, tgz),
		},
	}
	ctx := ghCtx(t, rel)
	h := downloadHandler{}
	st := manifest.DownloadStep{Site: "example.tools", Tool: "demo", Bin: "demo"}

	if err := h.Apply(ctx, st); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(ctx.Home, ".local", "bin", "demo")
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("binary content mismatch: %q", got)
	}
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
	}

	// Re-inspect → OpNoop.
	d, err := h.Inspect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("re-inspect op = %v, want noop", d.Op)
	}

	// No leftover staged files.
	entries, _ := os.ReadDir(filepath.Dir(bin))
	for _, e := range entries {
		if e.Name() != "demo" {
			t.Fatalf("leftover file: %s", e.Name())
		}
	}
}

func TestDownloadApplyPinnedSkipsPointer(t *testing.T) {
	content := []byte("pinned-binary")
	tgz := makeTarGz(t, "demo", content)
	assetName := "demo_linux_amd64.tar.gz"
	assetURL := "https://example.tools/dl/demo/2.0.0/" + assetName
	// No "latest" pointer served: a pointer fetch would error.
	rel := release.FakeReleases{
		Files: map[string][]byte{
			assetURL:             tgz,
			assetURL + ".sha256": checksums(assetName, tgz),
		},
	}
	ctx := ghCtx(t, rel)
	h := downloadHandler{}
	st := manifest.DownloadStep{Site: "example.tools", Tool: "demo", Version: "2.0.0", Bin: "demo"}

	if err := h.Apply(ctx, st); err != nil {
		t.Fatalf("pinned apply should skip pointer fetch: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ctx.Home, ".local", "bin", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("binary content mismatch: %q", got)
	}
}

func TestDownloadApplyChecksumMismatch(t *testing.T) {
	tgz := makeTarGz(t, "demo", []byte("real"))
	assetName := "demo_linux_amd64.tar.gz"
	assetURL := "https://example.tools/dl/demo/1.4.0/" + assetName
	rel := release.FakeReleases{
		Files: map[string][]byte{
			"https://example.tools/dl/demo/latest": []byte("1.4.0\n"),
			assetURL:                               tgz,
			// checksum over different bytes → mismatch.
			assetURL + ".sha256": checksums(assetName, []byte("other")),
		},
	}
	ctx := ghCtx(t, rel)
	h := downloadHandler{}
	st := manifest.DownloadStep{Site: "example.tools", Tool: "demo", Bin: "demo"}

	if err := h.Apply(ctx, st); err == nil {
		t.Fatal("expected checksum mismatch error")
	}

	binDir := filepath.Join(ctx.Home, ".local", "bin")
	entries, err := os.ReadDir(binDir)
	if err == nil && len(entries) != 0 {
		t.Fatalf("expected no files in %s, got %v", binDir, entries)
	}
}

// gatedRel serves hail 1.4.0 only through signed links: each of the three
// files is reachable at its link's URL, never at its /dl URL.
func gatedRel(t *testing.T, sums []byte) (release.FakeReleases, []byte) {
	t.Helper()
	content := []byte("gated-binary")
	tgz := makeTarGz(t, "hail", content)
	assetName := "hail_linux_amd64.tar.gz"
	if sums == nil {
		sums = checksums(assetName, tgz)
	}
	return release.FakeReleases{
		Links: map[string]string{
			"hail hail/latest":                         "https://signed.example/latest?sig=s1",
			"hail hail/1.4.0/" + assetName:             "https://signed.example/asset?sig=s2",
			"hail hail/1.4.0/" + assetName + ".sha256": "https://signed.example/sums?sig=s3",
		},
		Files: map[string][]byte{
			"https://signed.example/latest?sig=s1": []byte("1.4.0\n"),
			"https://signed.example/asset?sig=s2":  tgz,
			"https://signed.example/sums?sig=s3":   sums,
		},
	}, content
}

func TestDownloadApplyGatedFetchesThroughLinks(t *testing.T) {
	rel, content := gatedRel(t, nil)
	ctx := ghCtx(t, rel)
	st := manifest.DownloadStep{Site: "hail.tools", Tool: "hail", Bin: "hail"}

	if err := (downloadHandler{}).Apply(ctx, st); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(ctx.Home, ".local", "bin", "hail"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("binary content mismatch: %q", got)
	}
}

func TestDownloadApplyGatedChecksumMismatch(t *testing.T) {
	rel, _ := gatedRel(t, checksums("hail_linux_amd64.tar.gz", []byte("other")))
	ctx := ghCtx(t, rel)
	st := manifest.DownloadStep{Site: "hail.tools", Tool: "hail", Bin: "hail"}

	if err := (downloadHandler{}).Apply(ctx, st); err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if _, err := os.Stat(filepath.Join(ctx.Home, ".local", "bin", "hail")); !os.IsNotExist(err) {
		t.Fatalf("binary landed despite the mismatch: %v", err)
	}
}

func TestDownloadApplyGatedNotSignedInNamesKemptLogin(t *testing.T) {
	for _, linkErr := range []error{auth.ErrNotSignedIn, auth.ErrSignInRefused} {
		ctx := ghCtx(t, release.FakeReleases{LinkErr: linkErr})
		st := manifest.DownloadStep{Site: "diazo.tools", Tool: "diazo", Bin: "diazo"}

		err := (downloadHandler{}).Apply(ctx, st)
		if err == nil || !strings.Contains(err.Error(), "run `kempt login`") || strings.Contains(err.Error(), "<tool>") {
			t.Fatalf("err = %v, want the sign-in line naming kempt login", err)
		}
	}
}
