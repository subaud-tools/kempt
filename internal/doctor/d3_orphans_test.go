package doctor

import (
	"testing"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

func ctxRunner(t *testing.T, fake *run.FakeRunner) *machine.Context {
	c, _ := machine.New(t.TempDir(), fake)
	c.Home = t.TempDir()
	return c
}

func TestD3FlagsPiOrphan(t *testing.T) {
	ctx := ctxRunner(t, &run.FakeRunner{Responses: map[string]run.Response{
		"pi list": {Stdout: "npm:declared@1.0.0\nnpm:orphan@2.0.0\n"},
	}})
	pkgs := []*manifest.Package{{Name: "pi", Steps: []manifest.Step{
		manifest.InstallStep{Pi: []string{"npm:declared"}},
	}}}
	got := CheckOrphans(ctx, pkgs, manifest.DoctorConfig{})
	if len(got) != 1 || got[0].Severity != Warn || got[0].Check != "orphan" {
		t.Fatalf("want 1 warn orphan, got %+v", got)
	}
}

func TestD3IgnoreSuppresses(t *testing.T) {
	ctx := ctxRunner(t, &run.FakeRunner{Responses: map[string]run.Response{
		"pi list": {Stdout: "npm:orphan@2.0.0\n"},
	}})
	pkgs := []*manifest.Package{{Name: "pi", Steps: []manifest.Step{manifest.InstallStep{Pi: []string{}}}}}
	got := CheckOrphans(ctx, pkgs, manifest.DoctorConfig{Ignore: []string{"npm:orphan"}})
	if len(got) != 0 {
		t.Fatalf("ignore should suppress, got %+v", got)
	}
}

func TestD3FlagsNpmOrphanWhenEnabled(t *testing.T) {
	ctx := ctxRunner(t, &run.FakeRunner{Responses: map[string]run.Response{
		"pi list":                    {Stdout: ""},
		"npm ls -g --depth=0 --json": {Stdout: `{"dependencies":{"leftover":{"version":"1.2.3"}}}`},
	}})
	pkgs := []*manifest.Package{{Name: "pi", Steps: []manifest.Step{manifest.InstallStep{Npm: []string{}}}}}
	got := CheckOrphans(ctx, pkgs, manifest.DoctorConfig{CheckNpmOrphans: true})
	if len(got) != 1 || got[0].Severity != Info || got[0].Package != "npm" {
		t.Fatalf("want 1 info npm orphan, got %+v", got)
	}
}

func TestD3GlobIgnoreSuppresses(t *testing.T) {
	ctx := ctxRunner(t, &run.FakeRunner{Responses: map[string]run.Response{
		"pi list": {Stdout: "npm:pi-quiet@0.2.0\n"},
	}})
	pkgs := []*manifest.Package{{Name: "pi", Steps: []manifest.Step{manifest.InstallStep{Pi: []string{}}}}}
	got := CheckOrphans(ctx, pkgs, manifest.DoctorConfig{Ignore: []string{"glob:pi-*"}})
	if len(got) != 0 {
		t.Fatalf("glob ignore should suppress, got %+v", got)
	}
}
