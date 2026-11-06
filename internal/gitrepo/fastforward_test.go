package gitrepo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/gitrepo"
	"github.com/subaud-tools/kempt/internal/run"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
}

// A fast-forward that would bring in a file the local clone holds as ignored
// must fail and leave the local file as it was.
func TestFastForwardKeepsIgnoredLocalFile(t *testing.T) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			t.Setenv(k, "")
			_ = os.Unsetenv(k)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	root := t.TempDir()
	remote, upstream, local := filepath.Join(root, "remote.git"), filepath.Join(root, "upstream"), filepath.Join(root, "local")
	git(t, root, "init", "-q", "--bare", "-b", "main", remote)
	git(t, root, "clone", "-q", remote, upstream)
	if err := os.WriteFile(filepath.Join(upstream, ".gitignore"), []byte("secret.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, upstream, "add", ".gitignore")
	git(t, upstream, "commit", "-q", "-m", "ignore")
	git(t, upstream, "push", "-q", "origin", "HEAD:main")
	git(t, root, "clone", "-q", remote, local)

	ignored := filepath.Join(local, "secret.env")
	if err := os.WriteFile(ignored, []byte("local only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upstream, "secret.env"), []byte("from upstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, upstream, "add", "-f", "secret.env")
	git(t, upstream, "commit", "-q", "-m", "track secret.env")
	git(t, upstream, "push", "-q", "origin", "HEAD:main")
	git(t, local, "fetch", "-q")

	if err := gitrepo.FastForward(run.RealRunner{}, local); err == nil {
		t.Fatal("fast-forward over an ignored local file succeeded")
	}
	if b, _ := os.ReadFile(ignored); string(b) != "local only\n" {
		t.Fatalf("ignored local file overwritten: %q", b)
	}
}
