package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
	"github.com/subaud-tools/tools-common/setupbundle"
)

func TestReleaseServiceRecoversBootstrapFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	files := map[string][]byte{"https://tackle.tools/dl/proj/latest": []byte("1.0.0")}
	setupRelease(t, files, "1.0.0", "unused")
	fragment := `[kempt]
spec = 2
[packages.integration]
[[packages.integration.service]]
label = "review-recovery"
program = ["/unused/proj"]
`
	b, err := setupbundle.Build("proj", "1.0.0", []byte(fragment), fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	base := "https://tackle.tools/dl/proj/1.0.0/"
	files[base+"proj_setup.tar.gz"] = b
	files[base+"proj_setup.tar.gz.sha256"] = []byte(hex.EncodeToString(sum[:]) + "  proj_setup.tar.gz")
	home, _, r := setupMachine(t, release.FakeReleases{Files: files})
	plist := home + "/Library/LaunchAgents/review-recovery.plist"
	r.Responses["launchctl bootout gui/0 "+plist] = run.Response{}
	r.Sequences["launchctl bootstrap gui/0 "+plist] = []run.Response{{Err: fmt.Errorf("temporary bootstrap failure")}, {}}
	var out, errw bytes.Buffer
	first := Dispatch([]string{"apply", "-yes"}, &out, &errw)
	t.Logf("first exit=%d\n%s%s", first, &out, &errw)
	if first == 0 {
		t.Fatal("expected first bootstrap failure")
	}
	out.Reset()
	errw.Reset()
	second := Dispatch([]string{"apply", "-yes"}, &out, &errw)
	t.Logf("second exit=%d\n%s%s", second, &out, &errw)
	count := 0
	for _, call := range r.Calls {
		if call == "launchctl bootstrap gui/0 "+plist {
			count++
		}
	}
	if count != 2 || second != 0 {
		t.Fatalf("ordinary apply did not recover failed service: bootstrap calls=%d second exit=%d", count, second)
	}

	out.Reset()
	errw.Reset()
	if code := Dispatch([]string{"apply", "-yes"}, &out, &errw); code != 0 {
		t.Fatalf("unchanged apply failed: %d %s%s", code, &out, &errw)
	}
	after := 0
	for _, call := range r.Calls {
		if call == "launchctl bootstrap gui/0 "+plist {
			after++
		}
	}
	if after != count {
		t.Fatal("successful activation was retried on unchanged apply")
	}
}
