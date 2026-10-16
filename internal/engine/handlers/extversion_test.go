package handlers

import (
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/inventory"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

func extCtx(resp map[string]run.Response) *machine.Context {
	return &machine.Context{
		Runner: &run.FakeRunner{Responses: resp},
		Cache:  map[string]string{},
	}
}

func TestRollingExtensionsSelectsUnversioned(t *testing.T) {
	step := manifest.InstallStep{
		Pi:  []string{"npm:pi-creel", "npm:pi-quiet@0.2.0"},
		Npm: []string{"typescript", "@scope/x@1.0.0"},
	}
	got := RollingExtensions(step)
	if len(got) != 2 {
		t.Fatalf("want 2 rolling entries, got %d: %v", len(got), got)
	}
	if got[0] != (ExtEntry{Backend: "pi", Entry: "npm:pi-creel", Pkg: "pi-creel"}) {
		t.Errorf("pi entry = %+v", got[0])
	}
	if got[1] != (ExtEntry{Backend: "npm", Entry: "typescript", Pkg: "typescript"}) {
		t.Errorf("npm entry = %+v", got[1])
	}
}

func TestRollingExtensionsExcludesNonRegistry(t *testing.T) {
	step := manifest.InstallStep{
		Pi:  []string{"npm:pi-creel", "/Users/me/dev/pi-thing", "pi-bare"},
		Npm: []string{"typescript", "@scope/x", "./local/pkg", "/abs/pkg"},
	}
	got := RollingExtensions(step)
	want := []ExtEntry{
		{Backend: "pi", Entry: "npm:pi-creel", Pkg: "pi-creel"},
		{Backend: "npm", Entry: "typescript", Pkg: "typescript"},
		{Backend: "npm", Entry: "@scope/x", Pkg: "@scope/x"},
	}
	if len(got) != len(want) {
		t.Fatalf("want %d entries, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
}

func TestExtLatestReadsNpmView(t *testing.T) {
	ctx := extCtx(map[string]run.Response{
		"npm view pi-creel version": {Stdout: "0.1.1\n"},
	})
	v, err := ExtLatest(ctx, ExtEntry{Backend: "pi", Entry: "npm:pi-creel", Pkg: "pi-creel"})
	if err != nil || v != "0.1.1" {
		t.Fatalf("ExtLatest = %q, %v; want 0.1.1, nil", v, err)
	}
}

func TestExtInstalledVersionFromPiList(t *testing.T) {
	ctx := extCtx(map[string]run.Response{
		"pi list": {Stdout: "User packages:\n  npm:pi-creel@0.1.0\n    /home/u/.pi/agent/npm/node_modules/pi-creel\n"},
	})
	v, known := ExtInstalledVersion(ctx, ExtEntry{Backend: "pi", Entry: "npm:pi-creel", Pkg: "pi-creel"})
	if !known || v != "0.1.0" {
		t.Fatalf("ExtInstalledVersion = %q, %v; want 0.1.0, true", v, known)
	}
}

// TestRollExtensionRunsPiUpdateAndInvalidatesCache: an npm-sourced pi entry
// rolls with `pi update`, which installs <pkg>@latest. `pi install` would keep
// the caret range pi recorded at first install (npm:pi-hail stayed on 0.1.1
// behind 0.7.0), and a versioned `pi install` would pin the settings entry.
func TestRollExtensionRunsPiUpdateAndInvalidatesCache(t *testing.T) {
	fr := &run.FakeRunner{Responses: map[string]run.Response{
		"pi update npm:pi-creel": {Stdout: ""},
	}}
	ctx := &machine.Context{Runner: fr, Cache: map[string]string{inventory.PiCmd: "stale"}}
	if err := RollExtension(ctx, ExtEntry{Backend: "pi", Entry: "npm:pi-creel", Pkg: "pi-creel"}); err != nil {
		t.Fatalf("RollExtension: %v", err)
	}
	if _, cached := ctx.Cache[inventory.PiCmd]; cached {
		t.Error("pi inventory cache not invalidated")
	}
	if len(fr.Calls) != 1 || fr.Calls[0] != "pi update npm:pi-creel" {
		t.Errorf("calls = %v", fr.Calls)
	}
}

// TestRollExtensionToFailsWhenVersionUnchanged: a roll whose command succeeds
// but leaves the entry behind is an error, not a success. This is the shape of
// the pi-hail bug: `pi install` exited 0 and `update` printed "rolled".
func TestRollExtensionToFailsWhenVersionUnchanged(t *testing.T) {
	ctx := extCtx(map[string]run.Response{
		"pi update npm:pi-hail": {Stdout: ""},
		"pi list":               {Stdout: "  npm:pi-hail@0.1.1\n"},
	})
	err := RollExtensionTo(ctx, ExtEntry{Backend: "pi", Entry: "npm:pi-hail", Pkg: "pi-hail"}, "0.7.0")
	if err == nil {
		t.Fatal("RollExtensionTo = nil; want an error for a roll that left 0.1.1 behind 0.7.0")
	}
	if !strings.Contains(err.Error(), "0.1.1") || !strings.Contains(err.Error(), "0.7.0") {
		t.Errorf("error %q should name the installed and target versions", err)
	}
}

func TestRollExtensionToSucceedsWhenVersionReachesTarget(t *testing.T) {
	ctx := extCtx(map[string]run.Response{
		"pi update npm:pi-hail": {Stdout: ""},
		"pi list":               {Stdout: "  npm:pi-hail@0.7.0\n"},
	})
	if err := RollExtensionTo(ctx, ExtEntry{Backend: "pi", Entry: "npm:pi-hail", Pkg: "pi-hail"}, "0.7.0"); err != nil {
		t.Fatalf("RollExtensionTo: %v", err)
	}
}

func TestRollExtensionToFailsWhenVersionUnreadable(t *testing.T) {
	ctx := extCtx(map[string]run.Response{
		"pi update npm:pi-hail": {Stdout: ""},
		"pi list":               {Stdout: ""},
	})
	if err := RollExtensionTo(ctx, ExtEntry{Backend: "pi", Entry: "npm:pi-hail", Pkg: "pi-hail"}, "0.7.0"); err == nil {
		t.Fatal("RollExtensionTo = nil; want an error when the installed version cannot be read after the roll")
	}
}

func TestRollExtensionNpmInstallsLatestAndInvalidatesCache(t *testing.T) {
	fr := &run.FakeRunner{Responses: map[string]run.Response{
		"npm install -g typescript@latest": {Stdout: ""},
	}}
	ctx := &machine.Context{Runner: fr, Cache: map[string]string{inventory.NpmCmd: "stale"}}
	if err := RollExtension(ctx, ExtEntry{Backend: "npm", Entry: "typescript", Pkg: "typescript"}); err != nil {
		t.Fatalf("RollExtension: %v", err)
	}
	if _, cached := ctx.Cache[inventory.NpmCmd]; cached {
		t.Error("npm inventory cache not invalidated")
	}
	if len(fr.Calls) != 1 || fr.Calls[0] != "npm install -g typescript@latest" {
		t.Errorf("calls = %v", fr.Calls)
	}
}

func TestExtInstalledVersionFromNpmInventory(t *testing.T) {
	ctx := extCtx(map[string]run.Response{
		inventory.NpmCmd: {Stdout: `{"dependencies":{"typescript":{"version":"5.4.0"}}}`},
	})
	v, known := ExtInstalledVersion(ctx, ExtEntry{Backend: "npm", Entry: "typescript", Pkg: "typescript"})
	if !known || v != "5.4.0" {
		t.Fatalf("ExtInstalledVersion = %q, %v; want 5.4.0, true", v, known)
	}
}

const (
	spOld = "5bf4e78aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	spNew = "9c01d2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var spEntry = ExtEntry{Backend: "git", Entry: "git:github.com/obra/superpowers", Pkg: "github.com/obra/superpowers"}

func TestRollingExtensionsIncludesUnpinnedGit(t *testing.T) {
	step := manifest.InstallStep{
		Pi: []string{"git:github.com/obra/superpowers", "git:github.com/x/pinned@v1.0.0"},
	}
	got := RollingExtensions(step)
	if len(got) != 1 || got[0] != spEntry {
		t.Fatalf("got %+v; want only %+v", got, spEntry)
	}
}

func TestExtInstalledVersionGitReadsCloneHead(t *testing.T) {
	ctx := extCtx(map[string]run.Response{
		"pi list": {Stdout: "User packages:\n  git:github.com/obra/superpowers\n    /h/.pi/agent/git/github.com/obra/superpowers\n"},
		"git -C /h/.pi/agent/git/github.com/obra/superpowers rev-parse HEAD": {Stdout: spOld + "\n"},
	})
	v, known := ExtInstalledVersion(ctx, spEntry)
	if !known || v != spOld[:12] {
		t.Fatalf("ExtInstalledVersion = %q, %v; want %q, true", v, known, spOld[:12])
	}
}

func TestExtLatestGitReadsRemoteHead(t *testing.T) {
	ctx := extCtx(map[string]run.Response{
		"git ls-remote https://github.com/obra/superpowers HEAD": {Stdout: spNew + "\tHEAD\n"},
	})
	v, err := ExtLatest(ctx, spEntry)
	if err != nil || v != spNew[:12] {
		t.Fatalf("ExtLatest = %q, %v; want %q, nil", v, err, spNew[:12])
	}
}

func TestExtBehind(t *testing.T) {
	npm := ExtEntry{Backend: "pi", Entry: "npm:pi-creel", Pkg: "pi-creel"}
	cases := []struct {
		e                 ExtEntry
		target, installed string
		want              bool
	}{
		{npm, "0.1.1", "0.1.0", true},
		{npm, "0.1.0", "0.1.1", false},
		{spEntry, spNew[:12], spOld[:12], true},
		{spEntry, spOld[:12], spOld[:12], false},
		{spEntry, spNew[:12], "", false},
	}
	for _, c := range cases {
		if got := ExtBehind(c.e, c.target, c.installed); got != c.want {
			t.Errorf("ExtBehind(%s, %q, %q) = %v; want %v", c.e.Entry, c.target, c.installed, got, c.want)
		}
	}
}

func TestRollExtensionGitRunsPiInstall(t *testing.T) {
	fr := &run.FakeRunner{Responses: map[string]run.Response{
		"pi install git:github.com/obra/superpowers": {Stdout: ""},
	}}
	ctx := &machine.Context{Runner: fr, Cache: map[string]string{inventory.PiCmd: "stale"}}
	if err := RollExtension(ctx, spEntry); err != nil {
		t.Fatalf("RollExtension: %v", err)
	}
	if _, cached := ctx.Cache[inventory.PiCmd]; cached {
		t.Error("pi inventory cache not invalidated")
	}
	if len(fr.Calls) != 1 || fr.Calls[0] != "pi install git:github.com/obra/superpowers" {
		t.Errorf("calls = %v", fr.Calls)
	}
}
