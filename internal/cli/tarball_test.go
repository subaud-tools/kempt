package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/kempt/internal/state"
)

// buildTarGz builds a gzipped tar with every file under a single wrapper dir
// (as GitHub codeload archives do). Extra members can be appended raw.
func buildTarGz(t *testing.T, prefix string, files map[string]string, extra ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tw.WriteHeader(&tar.Header{Name: prefix + "/", Typeflag: tar.TypeDir, Mode: 0o755}))
	for name, content := range files {
		must(tw.WriteHeader(&tar.Header{Name: prefix + "/" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))}))
		_, err := tw.Write([]byte(content))
		must(err)
	}
	for i := range extra {
		must(tw.WriteHeader(&extra[i]))
	}
	must(tw.Close())
	must(gz.Close())
	return buf.Bytes()
}

func TestExtractTarballStripsWrapperDir(t *testing.T) {
	dir := t.TempDir()
	arc := buildTarGz(t, "dotfiles-main", map[string]string{
		"kempt.toml": "[kempt]\nspec = 1\n",
		"src/rc":     "rc\n",
	})
	if err := extractTarballStripped(bytes.NewReader(arc), dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "kempt.toml")); err != nil || !bytes.Contains(b, []byte("spec = 1")) {
		t.Fatalf("kempt.toml not extracted at top level: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "rc")); err != nil {
		t.Fatalf("nested src/rc not extracted: %v", err)
	}
}

func TestExtractTarballRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	arc := buildTarGz(t, "x", nil, tar.Header{Name: "x/../escape", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0})
	err := extractTarballStripped(bytes.NewReader(arc), dir)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("escapes")) {
		t.Fatalf("err = %v, want an escapes-destination error", err)
	}
}

func TestExtractTarballRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	arc := buildTarGz(t, "x", nil, tar.Header{Name: "x/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777})
	err := extractTarballStripped(bytes.NewReader(arc), dir)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("link")) {
		t.Fatalf("err = %v, want a refusing-link error", err)
	}
}

func TestIsTarballURL(t *testing.T) {
	cases := map[string]bool{
		"https://github.com/o/r/archive/refs/heads/main.tar.gz": true,
		"https://example.com/x.tgz":                             true,
		"https://example.com/kempt.toml":                        false,
		"git@github.com:o/r.git":                                false,
		"https://github.com/o/r.git":                            false,
	}
	for in, want := range cases {
		if got := isTarballURL(in); got != want {
			t.Errorf("isTarballURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestInitFromTarball(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	store := &state.Store{Dir: t.TempDir()}

	arc := buildTarGz(t, "dotfiles-main", map[string]string{
		"kempt.toml": initManifest,
		"src/rc":     "rc\n",
	})

	origSave, origCtx, origPicker, origOpen := saveState, newContext, pickerRun, openTarballStream
	saveState = func(s *state.State) error { return store.Save(s) }
	newContext = func(repoDir string) (*machine.Context, error) {
		return &machine.Context{Home: home, RepoDir: repoDir, OS: "darwin", Arch: "arm64", Runner: &run.FakeRunner{}, Cache: map[string]string{}}, nil
	}
	openTarballStream = func(url string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(arc)), nil
	}
	t.Cleanup(func() {
		saveState, newContext, pickerRun, openTarballStream = origSave, origCtx, origPicker, origOpen
	})

	url := "https://github.com/schuettc/dotfiles/archive/refs/heads/main.tar.gz"
	var out, errw bytes.Buffer
	code := Dispatch([]string{"init", url, "-dir", dir, "-profile", "developer", "-yes"}, &out, &errw)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%s err=%s", code, out.String(), errw.String())
	}

	if _, err := os.Stat(filepath.Join(dir, "kempt.toml")); err != nil {
		t.Fatalf("manifest not extracted: %v", err)
	}
	st, existed, err := store.Load()
	if err != nil || !existed {
		t.Fatalf("state not saved: %v existed=%v", err, existed)
	}
	if st.RepoKind != "tarball" {
		t.Fatalf("RepoKind = %q, want tarball", st.RepoKind)
	}
	if st.RepoURL != url {
		t.Fatalf("RepoURL = %q, want %q", st.RepoURL, url)
	}
	if _, err := os.Lstat(filepath.Join(home, ".rc")); err != nil {
		t.Fatalf("symlink ~/.rc not created from extracted tree: %v", err)
	}
}
