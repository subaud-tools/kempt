package doctor

import (
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

func TestRunAggregates(t *testing.T) {
	c := ctxRunner(t, &run.FakeRunner{Responses: map[string]run.Response{"pi list": {Stdout: ""}}})
	f := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, f, `{"packages":["npm:a","npm:a@1.0.0"]}`)
	pkgs := pkgWithMerge(f, map[string]any{"packages": []any{"npm:a"}}, "")
	rep := Run(c, pkgs, manifest.DoctorConfig{})
	// D1 (extra) + D2 (dup) both fire on the same file.
	if len(rep.Findings) < 2 {
		t.Fatalf("want >=2 findings from D1+D2, got %+v", rep.Findings)
	}
}
