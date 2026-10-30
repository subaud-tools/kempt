package engine_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

type configCheckingRunner struct {
	config             string
	bootstrapSawConfig bool
}

func (r *configCheckingRunner) LookPath(s string) (string, error) {
	return "", fmt.Errorf("unexpected lookup %s", s)
}
func (r *configCheckingRunner) Run(s string, args ...string) (string, error) {
	if s != "launchctl" {
		return "", fmt.Errorf("unexpected command %s", s)
	}
	if args[0] == "bootstrap" {
		if _, err := os.Stat(r.config); err != nil {
			return "", fmt.Errorf("config missing at bootstrap: %w", err)
		}
		r.bootstrapSawConfig = true
	}
	return "", nil
}
func TestSetupMergeBeforeService(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	config := filepath.Join(home, "config.json")
	r := &configCheckingRunner{config: config}
	ctx := &machine.Context{Home: home, RepoDir: home, OS: "darwin", Arch: "arm64", Runner: r}
	pkgs := []*manifest.Package{
		{Name: "tool"},
		{Name: "tool/setup/tool", Layer: "tool/setup/tool", SetupParent: "tool", Needs: []string{"tool"}, Steps: []manifest.Step{
			manifest.JSONMergeStep{File: config, Merge: map[string]any{"ready": true}},
			manifest.ServiceStep{Label: "review", Program: []string{"/bin/unused"}},
		}},
	}
	p, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	engine.Render(p, &out, true)
	n := engine.Execute(ctx, p, &out)
	t.Log(out.String())
	if n != 0 || !r.bootstrapSawConfig {
		t.Fatalf("service did not receive preceding declared config: failures=%d configAtBootstrap=%v", n, r.bootstrapSawConfig)
	}
}
