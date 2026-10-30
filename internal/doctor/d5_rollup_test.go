package doctor

import (
	"strings"
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

func TestD5MissingManagedFileNamesOwningPackage(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	c, _ := machine.New(t.TempDir(), &run.FakeRunner{})
	c.Home = t.TempDir()
	pkgs := []*manifest.Package{{Name: "proj/setup/proj", SetupParent: "proj", Steps: []manifest.Step{
		manifest.ManagedFileStep{From: "proj.zsh", To: "~/.config/zsh/proj.zsh", Mode: "0644", Source: &manifest.SetupSource{Tool: "proj", Version: "1.0.0", Data: []byte("wrapper\n")}},
	}}}
	report := Run(c, pkgs, manifest.DoctorConfig{})
	for _, f := range report.Findings {
		if f.Package == "proj/setup/proj" && f.Severity == Error && strings.Contains(f.Detail, "managed-file") && strings.Contains(f.Remediation, "kempt apply") {
			return
		}
	}
	t.Fatalf("missing managed file must be attributed and unhealthy: %+v", report)
}
