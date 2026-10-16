package doctor

import (
	"testing"

	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

func TestD5PendingChangeIsInfo(t *testing.T) {
	// A symlink step whose target is absent → plan reports OpChange.
	c, _ := machine.New(t.TempDir(), &run.FakeRunner{})
	c.Home = t.TempDir()
	pkgs := []*manifest.Package{{Name: "p", Steps: []manifest.Step{
		manifest.SymlinkStep{From: "src", To: c.Home + "/absent-link"},
	}}}
	got := CheckRollup(c, pkgs)
	var infos int
	for _, f := range got {
		if f.Severity == Info {
			infos++
		}
	}
	if infos == 0 {
		t.Fatalf("want an info rollup for pending changes, got %+v", got)
	}
}
