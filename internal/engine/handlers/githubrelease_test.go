package handlers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
)

func ghCtx(t *testing.T, rel release.Releases) *machine.Context {
	t.Helper()
	return &machine.Context{
		Home:     t.TempDir(),
		OS:       "linux",
		Arch:     "amd64",
		Runner:   &run.FakeRunner{},
		Releases: rel,
		Cache:    map[string]string{},
	}
}

func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "prefix/" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checksums(asset string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset))
}

func checksumsUpper(asset string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(fmt.Sprintf("%s  %s\n", strings.ToUpper(hex.EncodeToString(sum[:])), asset))
}

func TestGithubReleaseInspect(t *testing.T) {
	ctx := ghCtx(t, release.FakeReleases{})
	h := githubReleaseHandler{}
	st := manifest.GithubReleaseStep{Repo: "example/tool", Asset: "tool.tar.gz", Bin: "tool"}

	// Missing → OpChange.
	d, err := h.Inspect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v, want change", d.Op)
	}
	if d.Detail != "install tool from example/tool (latest)" {
		t.Fatalf("detail = %q", d.Detail)
	}

	// Present → OpNoop.
	bin := filepath.Join(ctx.Home, ".local", "bin", "tool")
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
	if d.Detail != "github-release tool" {
		t.Fatalf("detail = %q", d.Detail)
	}
}

func TestGithubReleaseApplyTarGz(t *testing.T) {
	asset := "tool-linux-amd64.tar.gz"
	content := []byte("#!/bin/sh\necho hi\n")
	tgz := makeTarGz(t, "tool", content)
	baseURL := "https://github.com/example/tool/releases/download/v1.0.0/"
	rel := release.FakeReleases{
		Tags: map[string]string{"example/tool": "v1.0.0"},
		Files: map[string][]byte{
			baseURL + asset:           tgz,
			baseURL + "checksums.txt": checksums(asset, tgz),
		},
	}
	ctx := ghCtx(t, rel)
	h := githubReleaseHandler{}
	st := manifest.GithubReleaseStep{Repo: "example/tool", Asset: "tool-{os}-{arch}.tar.gz", Bin: "tool"}

	if err := h.Apply(ctx, st); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(ctx.Home, ".local", "bin", "tool")
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
		if e.Name() != "tool" {
			t.Fatalf("leftover file: %s", e.Name())
		}
	}
}

func TestGithubReleaseApplyChecksumMismatch(t *testing.T) {
	asset := "tool-linux-amd64.tar.gz"
	tgz := makeTarGz(t, "tool", []byte("real"))
	baseURL := "https://github.com/example/tool/releases/download/v1.0.0/"
	// checksum computed over different bytes → mismatch.
	badSum := checksums(asset, []byte("other"))
	rel := release.FakeReleases{
		Tags: map[string]string{"example/tool": "v1.0.0"},
		Files: map[string][]byte{
			baseURL + asset:           tgz,
			baseURL + "checksums.txt": badSum,
		},
	}
	ctx := ghCtx(t, rel)
	h := githubReleaseHandler{}
	st := manifest.GithubReleaseStep{Repo: "example/tool", Asset: "tool-{os}-{arch}.tar.gz", Bin: "tool"}

	if err := h.Apply(ctx, st); err == nil {
		t.Fatal("expected checksum mismatch error")
	}

	// Nothing written anywhere under .local/bin.
	binDir := filepath.Join(ctx.Home, ".local", "bin")
	entries, err := os.ReadDir(binDir)
	if err == nil && len(entries) != 0 {
		t.Fatalf("expected no files in %s, got %v", binDir, entries)
	}
}

func TestGithubReleaseApplyMissingChecksumLine(t *testing.T) {
	asset := "tool-linux-amd64.tar.gz"
	tgz := makeTarGz(t, "tool", []byte("real"))
	baseURL := "https://github.com/example/tool/releases/download/v1.0.0/"
	rel := release.FakeReleases{
		Tags: map[string]string{"example/tool": "v1.0.0"},
		Files: map[string][]byte{
			baseURL + asset:           tgz,
			baseURL + "checksums.txt": []byte("deadbeef  someother.tar.gz\n"),
		},
	}
	ctx := ghCtx(t, rel)
	h := githubReleaseHandler{}
	st := manifest.GithubReleaseStep{Repo: "example/tool", Asset: "tool-{os}-{arch}.tar.gz", Bin: "tool"}

	if err := h.Apply(ctx, st); err == nil {
		t.Fatal("expected missing-checksum-line error")
	}
}

func TestGithubReleaseApplyUppercaseChecksum(t *testing.T) {
	// checksums.txt with uppercase hex digits should still pass.
	asset := "tool-linux-amd64.tar.gz"
	content := []byte("#!/bin/sh\necho upper\n")
	tgz := makeTarGz(t, "tool", content)
	baseURL := "https://github.com/example/tool/releases/download/v3.0.0/"
	rel := release.FakeReleases{
		Tags: map[string]string{"example/tool": "v3.0.0"},
		Files: map[string][]byte{
			baseURL + asset:           tgz,
			baseURL + "checksums.txt": checksumsUpper(asset, tgz),
		},
	}
	ctx := ghCtx(t, rel)
	h := githubReleaseHandler{}
	st := manifest.GithubReleaseStep{Repo: "example/tool", Asset: "tool-{os}-{arch}.tar.gz", Bin: "tool"}

	if err := h.Apply(ctx, st); err != nil {
		t.Fatalf("uppercase checksum should be accepted: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(ctx.Home, ".local", "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("binary content mismatch: %q", got)
	}
}

func TestGithubReleaseApplyRawBinary(t *testing.T) {
	asset := "tool-linux-amd64"
	content := []byte("raw-binary-bytes")
	baseURL := "https://github.com/example/tool/releases/download/v2.0.0/"
	rel := release.FakeReleases{
		Tags: map[string]string{"example/tool": "v2.0.0"},
		Files: map[string][]byte{
			baseURL + asset:           content,
			baseURL + "checksums.txt": checksums(asset, content),
		},
	}
	ctx := ghCtx(t, rel)
	h := githubReleaseHandler{}
	st := manifest.GithubReleaseStep{Repo: "example/tool", Asset: "tool-{os}-{arch}", Bin: "tool"}

	if err := h.Apply(ctx, st); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(ctx.Home, ".local", "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("raw binary mismatch: %q", got)
	}
}
