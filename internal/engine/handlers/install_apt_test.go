package handlers

import (
	"errors"
	"reflect"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

// dpkgInvCmd is the FakeRunner key of apt's inventory read.
const dpkgInvCmd = `dpkg-query -W -f ${Package} ${Status}\n`

// aptFound scripts apt-get on PATH, passwordless sudo, and a dpkg inventory.
func aptFound(inventory string) map[string]run.Response {
	return map[string]run.Response{
		"lookpath apt-get": {Stdout: "/usr/bin/apt-get"},
		"sudo -n true":     {},
		dpkgInvCmd:         {Stdout: inventory},
	}
}

const dpkgSample = "jq install ok installed\n" +
	"ripgrep install ok installed\n" +
	"fd-find deinstall ok config-files\n" +
	"curl hold ok installed\n"

func TestInstallLinuxAptWinsOverBrew(t *testing.T) {
	fake := &run.FakeRunner{Responses: aptFound(dpkgSample)}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{
		Brew: &manifest.BrewSpec{Formulas: []string{"jq"}},
		Apt:  []string{"jq", "ripgrep"},
	}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop || d.Detail != "apt: 2 present" {
		t.Fatalf("delta = %v %q; want OpNoop %q", d.Op, d.Detail, "apt: 2 present")
	}
	for _, c := range fake.Calls {
		if c == "lookpath brew" || c == "brew list --formula -1" {
			t.Fatalf("brew probed on linux with apt content: %v", fake.Calls)
		}
	}
}

func TestInstallDarwinBrewWinsOverApt(t *testing.T) {
	fake := &run.FakeRunner{Responses: brewFound("jq\n", "", "")}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{
		Brew: &manifest.BrewSpec{Formulas: []string{"jq"}},
		Apt:  []string{"ripgrep"},
	}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop || d.Detail != "brew: 1 present" {
		t.Fatalf("delta = %v %q; want OpNoop %q", d.Op, d.Detail, "brew: 1 present")
	}
}

func TestInstallLinuxBrewOnlyStillUsesBrew(t *testing.T) {
	fake := &run.FakeRunner{Responses: brewFound("jq\n", "", "")}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{Formulas: []string{"jq"}}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop || d.Detail != "brew: 1 present" {
		t.Fatalf("delta = %v %q; want OpNoop %q", d.Op, d.Detail, "brew: 1 present")
	}
}

func TestInstallAptAllPresentIsNoop(t *testing.T) {
	fake := &run.FakeRunner{Responses: aptFound(dpkgSample)}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Apt: []string{"jq", "ripgrep", "curl"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop || d.Detail != "apt: 3 present" {
		t.Fatalf("delta = %v %q; want OpNoop %q", d.Op, d.Detail, "apt: 3 present")
	}
	// Nothing to install: only apt-get's lookup and the inventory read ran.
	want := []string{"lookpath apt-get", dpkgInvCmd}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("calls = %v; want %v", fake.Calls, want)
	}
}

func TestInstallAptMissingIsChange(t *testing.T) {
	fake := &run.FakeRunner{Responses: aptFound(dpkgSample)}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	// fd-find has only config files left, so it counts as missing.
	step := manifest.InstallStep{Apt: []string{"jq", "tmux", "fd-find"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange || d.Detail != "apt install: fd-find tmux" {
		t.Fatalf("delta = %v %q; want OpChange %q", d.Op, d.Detail, "apt install: fd-find tmux")
	}
}

func TestInstallAptInventoryMemoized(t *testing.T) {
	fake := &run.FakeRunner{Responses: aptFound(dpkgSample)}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Apt: []string{"jq"}}

	for i := 0; i < 2; i++ {
		if _, err := h.Inspect(ctx, step); err != nil {
			t.Fatalf("Inspect %d: %v", i, err)
		}
	}
	n := 0
	for _, c := range fake.Calls {
		if c == dpkgInvCmd {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("dpkg inventory ran %d times; want 1", n)
	}
}

func TestInstallAptGetNotFoundBlocked(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Apt: []string{"jq"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpBlocked || d.Detail != "install (apt-get not found)" {
		t.Fatalf("delta = %v %q; want OpBlocked %q", d.Op, d.Detail, "install (apt-get not found)")
	}
}

func TestInstallAptSudoRefusedBlocked(t *testing.T) {
	resp := aptFound(dpkgSample)
	resp["sudo -n true"] = run.Response{Err: errors.New("sudo: a password is required")}
	fake := &run.FakeRunner{Responses: resp}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Apt: []string{"tmux"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	want := "install (apt needs passwordless sudo: sudo -n refused)"
	if d.Op != engine.OpBlocked || d.Detail != want {
		t.Fatalf("delta = %v %q; want OpBlocked %q", d.Op, d.Detail, want)
	}

	// Apply installs nothing for a blocked apt list.
	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(fake.Calls) != 0 {
		t.Fatalf("apply calls = %v; want none", fake.Calls)
	}
}

func TestInstallAptApplyCommands(t *testing.T) {
	resp := aptFound(dpkgSample)
	resp["sudo -n apt-get update"] = run.Response{}
	resp["sudo -n apt-get install -y --no-install-recommends fd-find tmux"] = run.Response{}
	resp["sudo -n apt-get install -y --no-install-recommends zsh"] = run.Response{}
	fake := &run.FakeRunner{Responses: resp}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Apt: []string{"jq", "tmux", "fd-find"}}

	// Inspect first (mirrors BuildPlan → Execute).
	if _, err := h.Inspect(ctx, step); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{
		"sudo -n apt-get update",
		"sudo -n apt-get install -y --no-install-recommends fd-find tmux",
	}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, want)
	}
	if _, ok := ctx.Cache[dpkgInvCmd]; ok {
		t.Fatal("dpkg inventory cache not cleared after Apply")
	}

	// A second apt step in the same run updates the package lists only once.
	fake.Calls = nil
	other := manifest.InstallStep{Apt: []string{"zsh"}}
	if err := h.Apply(ctx, other); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	for _, c := range fake.Calls {
		if c == "sudo -n apt-get update" {
			t.Fatalf("apt-get update ran twice in one run: %v", fake.Calls)
		}
	}
	if fake.Calls[len(fake.Calls)-1] != "sudo -n apt-get install -y --no-install-recommends zsh" {
		t.Fatalf("apply 2 calls = %v", fake.Calls)
	}
}
