package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
)

type installedVersionRunner struct {
	*run.FakeRunner
	bin string
}

func (r installedVersionRunner) Run(name string, args ...string) (string, error) {
	if name == r.bin {
		b, e := os.ReadFile(r.bin)
		return string(b), e
	}
	return r.FakeRunner.Run(name, args...)
}
func TestUpdateRechecksLegacyVerification(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	tar, sum := fakeTarball(t, "proj", "proj 1.1.0")
	rel := release.FakeReleases{Tags: map[string]string{"example/proj": "v1.1.0"}, Files: map[string][]byte{
		"https://tackle.tools/dl/proj/latest":                                []byte("1.1.0"),
		"https://tackle.tools/dl/proj/1.1.0/proj_darwin_arm64.tar.gz":        []byte(tar),
		"https://tackle.tools/dl/proj/1.1.0/proj_darwin_arm64.tar.gz.sha256": []byte(sum),
	}}
	r := &run.FakeRunner{Responses: map[string]run.Response{}}
	home, repo := setupUpdate(t, r, rel)
	bin := filepath.Join(home, ".local", "bin", "proj")
	if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("proj 1.0.0"), 0755); err != nil {
		t.Fatal(err)
	}
	src := `[kempt]
spec = 1
[packages.a]
[[packages.a.download]]
site = "tackle.tools"
tool = "proj"
version = "latest"
bin = "proj"
[[packages.a.verify]]
version-current = { repo = "example/proj", command = "` + bin + ` version" }
`
	if err := os.WriteFile(filepath.Join(repo, "kempt.toml"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	r.Responses["git -C "+repo+" pull --rebase --autostash"] = run.Response{}
	old := newContext
	newContext = func(dir string) (*machine.Context, error) {
		ctx, e := old(dir)
		if e == nil {
			ctx.Runner = installedVersionRunner{r, bin}
		}
		return ctx, e
	}
	var out, errw bytes.Buffer
	code := Dispatch([]string{"update"}, &out, &errw)
	t.Logf("exit=%d\n%s%s", code, &out, &errw)
	if code != 0 || strings.Contains(out.String(), "blocked (unresolved)") {
		t.Fatal("successful legacy roll left stale verification unresolved")
	}

	if b, err := os.ReadFile(bin); err != nil || string(b) != "proj 1.1.0" {
		t.Fatalf("binary not rolled: %q %v", b, err)
	}
	preview, roll := strings.Index(out.String(), "verify:"), strings.Index(out.String(), "rolled proj")
	if preview < 0 || roll < preview {
		t.Fatal("verification preview must precede rolling")
	}
}
