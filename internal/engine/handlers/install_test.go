package handlers

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/inventory"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

func getInstallHandler(t *testing.T) engine.Handler {
	t.Helper()
	h, ok := engine.HandlerFor("install")
	if !ok {
		t.Fatal("install handler not registered")
	}
	return h
}

// installCtx builds a Context wired to the given FakeRunner. OS defaults to
// darwin; callers override via the returned Context.
func installCtx(t *testing.T, os string, fake *run.FakeRunner) *machine.Context {
	t.Helper()
	return &machine.Context{
		Home:    t.TempDir(),
		RepoDir: t.TempDir(),
		OS:      os,
		Arch:    "arm64",
		Runner:  fake,
		Cache:   map[string]string{},
	}
}

// brewFound scripts the LookPath hit plus inventory outputs.
func brewFound(formulas, casks, taps string) map[string]run.Response {
	return map[string]run.Response{
		"lookpath brew":          {Stdout: "/opt/homebrew/bin/brew"},
		"brew list --formula -1": {Stdout: formulas},
		"brew list --cask -1":    {Stdout: casks},
		"brew tap":               {Stdout: taps},
	}
}

func TestInstallInspectAllPresent(t *testing.T) {
	fake := &run.FakeRunner{Responses: brewFound("jq\nripgrep\n", "ghostty\n", "homebrew/cask\n")}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{
		Formulas: []string{"jq", "ripgrep"},
		Casks:    []string{"ghostty"},
		Taps:     []string{"homebrew/cask"},
	}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop", d.Op)
	}
	if d.Detail != "brew: 4 present" {
		t.Fatalf("detail = %q; want %q", d.Detail, "brew: 4 present")
	}
	// Only inventory commands (plus lookpath) may have run.
	wantCalls := []string{
		"lookpath brew",
		"brew list --formula -1",
		"brew list --cask -1",
		"brew tap",
	}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Fatalf("calls = %v; want %v", fake.Calls, wantCalls)
	}
}

func TestInstallInspectMissing(t *testing.T) {
	fake := &run.FakeRunner{Responses: brewFound("ripgrep\n", "", "")}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{
		Formulas: []string{"jq", "ripgrep", "fd"},
		Casks:    []string{"ghostty"},
		Taps:     []string{"x/y"},
	}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
	want := "brew install: fd jq; casks: ghostty; taps: x/y"
	if d.Detail != want {
		t.Fatalf("detail = %q; want %q", d.Detail, want)
	}
}

func TestInstallInspectMissingFormulasOnly(t *testing.T) {
	fake := &run.FakeRunner{Responses: brewFound("", "ghostty\n", "x/y\n")}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{
		Formulas: []string{"jq"},
		Casks:    []string{"ghostty"},
		Taps:     []string{"x/y"},
	}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Detail != "brew install: jq" {
		t.Fatalf("detail = %q; want %q", d.Detail, "brew install: jq")
	}
}

func TestInstallInspectMissingCasksOnly(t *testing.T) {
	// No formulas missing; only casks absent — Detail must not have a stray semicolon.
	fake := &run.FakeRunner{Responses: brewFound("jq\n", "", "")}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{
		Formulas: []string{"jq"},
		Casks:    []string{"ghostty"},
	}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
	want := "brew install: casks: ghostty"
	if d.Detail != want {
		t.Fatalf("detail = %q; want %q", d.Detail, want)
	}
}

func TestInstallInspectMissingTapsOnly(t *testing.T) {
	// No formulas or casks missing; only taps absent.
	fake := &run.FakeRunner{Responses: brewFound("jq\n", "ghostty\n", "")}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{
		Formulas: []string{"jq"},
		Casks:    []string{"ghostty"},
		Taps:     []string{"x/y"},
	}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
	want := "brew install: taps: x/y"
	if d.Detail != want {
		t.Fatalf("detail = %q; want %q", d.Detail, want)
	}
}

func TestInstallWingetOnWindowsBlocked(t *testing.T) {
	// Windows + winget content → OpBlocked (winget not implemented this phase).
	fake := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := installCtx(t, "windows", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Winget: []string{"Microsoft.WindowsTerminal"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpBlocked {
		t.Fatalf("op = %v; want OpBlocked", d.Op)
	}
	want := "install (backend winget not implemented in this phase)"
	if d.Detail != want {
		t.Fatalf("detail = %q; want %q", d.Detail, want)
	}
}

func TestInstallInspectMemoizesCache(t *testing.T) {
	fake := &run.FakeRunner{Responses: brewFound("jq\n", "", "")}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{Formulas: []string{"jq"}}}

	if _, err := h.Inspect(ctx, step); err != nil {
		t.Fatalf("Inspect 1: %v", err)
	}
	if _, err := h.Inspect(ctx, step); err != nil {
		t.Fatalf("Inspect 2: %v", err)
	}
	// Each inventory command should have run at most once.
	counts := map[string]int{}
	for _, c := range fake.Calls {
		counts[c]++
	}
	if counts["brew list --formula -1"] != 1 {
		t.Fatalf("formula inventory ran %d times; want 1", counts["brew list --formula -1"])
	}
}

func TestInstallInventoryError(t *testing.T) {
	resp := brewFound("", "", "")
	resp["brew list --formula -1"] = run.Response{Err: errors.New("brew broke")}
	fake := &run.FakeRunner{Responses: resp}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{Formulas: []string{"jq"}}}

	if _, err := h.Inspect(ctx, step); err == nil {
		t.Fatal("expected error from failing inventory command")
	}
}

func TestInstallBrewNotFound(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{Formulas: []string{"jq"}}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpBlocked {
		t.Fatalf("op = %v; want OpBlocked", d.Op)
	}
	if d.Detail != "install (brew not found)" {
		t.Fatalf("detail = %q; want %q", d.Detail, "install (brew not found)")
	}
}

func TestInstallAptNotImplemented(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Apt: []string{"jq"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpBlocked {
		t.Fatalf("op = %v; want OpBlocked", d.Op)
	}
	if d.Detail != "install (backend apt not implemented in this phase)" {
		t.Fatalf("detail = %q", d.Detail)
	}
}

func TestInstallWingetOnDarwinSkips(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Winget: []string{"jq"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpSkip {
		t.Fatalf("op = %v; want OpSkip", d.Op)
	}
	if d.Detail != "install (no backend for darwin)" {
		t.Fatalf("detail = %q", d.Detail)
	}
}

func TestInstallLinuxBrewWins(t *testing.T) {
	fake := &run.FakeRunner{Responses: brewFound("jq\n", "", "")}
	ctx := installCtx(t, "linux", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{
		Brew: &manifest.BrewSpec{Formulas: []string{"jq"}},
		Apt:  []string{"ripgrep"},
	}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop (brew wins)", d.Op)
	}
}

func TestInstallApplyOrderAndCacheClear(t *testing.T) {
	resp := brewFound("ripgrep\n", "", "")
	resp["brew tap x/y"] = run.Response{}
	resp["brew install fd jq"] = run.Response{}
	resp["brew install --cask ghostty"] = run.Response{}
	fake := &run.FakeRunner{Responses: resp}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{
		Formulas: []string{"jq", "ripgrep", "fd"},
		Casks:    []string{"ghostty"},
		Taps:     []string{"x/y"},
	}}

	// Inspect first to populate cache (mirrors BuildPlan → Execute).
	if _, err := h.Inspect(ctx, step); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	// Prime cache keys so we can prove Apply deletes them.
	ctx.Cache["brew list --formula -1"] = "ripgrep\n"

	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{
		"brew tap x/y",
		"brew install fd jq",
		"brew install --cask ghostty",
	}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, want)
	}
	for _, k := range []string{"brew list --formula -1", "brew list --cask -1", "brew tap"} {
		if _, ok := ctx.Cache[k]; ok {
			t.Fatalf("cache key %q not cleared after Apply", k)
		}
	}
}

// --- npm backend ---

const npmInvCmd = "npm ls -g --depth=0 --json"

func TestInstallNpmAllPresent(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		npmInvCmd:      {Stdout: `{"dependencies":{"typescript":{"version":"5.0.0"},"prettier":{"version":"3.0.0"}}}`},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"typescript", "prettier"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop", d.Op)
	}
	if d.Detail != "npm: 2 present" {
		t.Fatalf("detail = %q; want %q", d.Detail, "npm: 2 present")
	}
	wantCalls := []string{"lookpath npm", npmInvCmd}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Fatalf("calls = %v; want %v", fake.Calls, wantCalls)
	}
}

func TestInstallNpmMissing(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm":            {Stdout: "/usr/bin/npm"},
		npmInvCmd:                 {Stdout: `{"dependencies":{"typescript":{"version":"5.0.0"}}}`},
		"npm install -g prettier": {},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"typescript", "prettier"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
	if d.Detail != "npm install: prettier" {
		t.Fatalf("detail = %q; want %q", d.Detail, "npm install: prettier")
	}

	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// LookPath guard in npmApply now precedes the install command.
	want := []string{"lookpath npm", "npm install -g prettier"}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, want)
	}
	if _, ok := ctx.Cache[npmInvCmd]; ok {
		t.Fatalf("npm cache key not cleared after Apply")
	}
}

func TestInstallNpmNotFound(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"typescript"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpBlocked {
		t.Fatalf("op = %v; want OpBlocked", d.Op)
	}
	if d.Detail != "install (npm not found)" {
		t.Fatalf("detail = %q; want %q", d.Detail, "install (npm not found)")
	}
}

// --- pi backend ---

func TestInstallPiAllPresent(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath pi": {Stdout: "/usr/bin/pi"},
		"pi list":     {Stdout: "npm:typescript@5.0.0\n/Users/me/local-tool\n"},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"npm:typescript", "/Users/me/local-tool"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop", d.Op)
	}
	if d.Detail != "pi: 2 present" {
		t.Fatalf("detail = %q; want %q", d.Detail, "pi: 2 present")
	}
	wantCalls := []string{"lookpath pi", "pi list"}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Fatalf("calls = %v; want %v", fake.Calls, wantCalls)
	}
}

func TestInstallPiMissing(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath pi":           {Stdout: "/usr/bin/pi"},
		"pi list":               {Stdout: "npm:typescript@5.0.0\n"},
		"pi install npm:eslint": {},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"npm:typescript", "npm:eslint"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
	if d.Detail != "pi install: npm:eslint" {
		t.Fatalf("detail = %q; want %q", d.Detail, "pi install: npm:eslint")
	}

	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// LookPath guard in piApply now precedes the install command.
	want := []string{"lookpath pi", "pi install npm:eslint"}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, want)
	}
	if _, ok := ctx.Cache["pi list"]; ok {
		t.Fatalf("pi cache key not cleared after Apply")
	}
}

// A bad entry (here a local path absent on this machine) must not stop the
// entries after it: the pinned fix behind it still installs, and the error
// names the entry that failed.
func TestInstallPiBadEntryDoesNotBlockRest(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath pi":                    {Stdout: "/usr/bin/pi"},
		"pi list":                        {Stdout: ""},
		"pi install /missing/ext":        {Err: errors.New("Path does not exist")},
		"pi install npm:artifacts@1.4.1": {},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"/missing/ext", "npm:artifacts@1.4.1"}}

	err := h.Apply(ctx, step)
	if err == nil || !strings.Contains(err.Error(), "/missing/ext") {
		t.Fatalf("Apply err = %v; want an error naming /missing/ext", err)
	}
	want := []string{"lookpath pi", "pi list", "pi install /missing/ext", "pi install npm:artifacts@1.4.1"}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, want)
	}
}

func TestInstallPiNotFound(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"npm:typescript"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpBlocked {
		t.Fatalf("op = %v; want OpBlocked", d.Op)
	}
	if d.Detail != "install (pi not found)" {
		t.Fatalf("detail = %q; want %q", d.Detail, "install (pi not found)")
	}
}

// --- combined brew + npm (additive) ---

func TestInstallCombinedBrewNpm(t *testing.T) {
	resp := brewFound("jq\n", "", "")
	resp["brew install fd"] = run.Response{}
	resp["lookpath npm"] = run.Response{Stdout: "/usr/bin/npm"}
	resp[npmInvCmd] = run.Response{Stdout: `{"dependencies":{"typescript":{"version":"5.0.0"}}}`}
	resp["npm install -g prettier"] = run.Response{}
	fake := &run.FakeRunner{Responses: resp}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{
		Brew: &manifest.BrewSpec{Formulas: []string{"jq", "fd"}},
		Npm:  []string{"typescript", "prettier"},
	}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
	want := "brew install: fd; npm install: prettier"
	if d.Detail != want {
		t.Fatalf("detail = %q; want %q", d.Detail, want)
	}

	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// LookPath guard in npmApply now precedes the install command.
	wantCalls := []string{"brew install fd", "lookpath npm", "npm install -g prettier"}
	if !reflect.DeepEqual(fake.Calls, wantCalls) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, wantCalls)
	}
}

func TestInstallApplySkipsEmptyGroups(t *testing.T) {
	resp := brewFound("", "", "")
	resp["brew install jq"] = run.Response{}
	fake := &run.FakeRunner{Responses: resp}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{Formulas: []string{"jq"}}}

	if _, err := h.Inspect(ctx, step); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"brew install jq"}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, want)
	}
}

// TestInstallCombinedBrewChangeNpmAbsent verifies finding #1: when brew has
// changes and npm is absent, Inspect returns OpChange (brew segment) with npm
// blocked in the Detail; Apply issues the brew install and does NOT error on
// npm, and no npm commands are recorded.
func TestInstallCombinedBrewChangeNpmAbsent(t *testing.T) {
	resp := brewFound("jq\n", "", "") // fd missing → brew OpChange
	resp["brew install fd"] = run.Response{}
	// npm absent: no "lookpath npm" response → LookPath returns error
	fake := &run.FakeRunner{Responses: resp}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{
		Brew: &manifest.BrewSpec{Formulas: []string{"jq", "fd"}},
		Npm:  []string{"prettier"},
	}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	// Combined: brew=OpChange, npm=OpBlocked → overall OpChange.
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}

	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	// LookPath guard fires but returns nil; no npm ls or npm install must run.
	for _, c := range fake.Calls {
		if c == npmInvCmd || strings.HasPrefix(c, "npm install") {
			t.Fatalf("unexpected npm inventory/install call after brew-only apply: %q", c)
		}
	}
	if len(fake.Calls) == 0 {
		t.Fatal("expected brew install call but got none")
	}
}

// TestInstallPiHeaderTolerated verifies finding #2: section-header lines in
// "pi list" output (e.g. "User packages:") are silently skipped so they are
// not mistaken for package identifiers.
func TestInstallPiHeaderTolerated(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath pi": {Stdout: "/usr/bin/pi"},
		// Fixture with a real-world section header.
		"pi list": {Stdout: "User packages:\n  npm:typescript@5.0.0\n  /Users/me/local-tool\n"},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"npm:typescript", "/Users/me/local-tool"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop (header must be ignored)", d.Op)
	}
	if d.Detail != "pi: 2 present" {
		t.Fatalf("detail = %q; want \"pi: 2 present\"", d.Detail)
	}
}

// TestInstallNpmScopedPackage verifies finding #3: the npm inventory parser
// preserves @scope/name for scoped packages (the entire segment after
// node_modules/, NOT just the basename), so a scoped desired entry matches.
func TestInstallNpmScopedPackage(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		// Scoped package: the JSON dependency key is the @scope/pkg name.
		npmInvCmd: {Stdout: `{"dependencies":{"@earendil-works/pi-coding-agent":{"version":"1.0.0"}}}`},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"@earendil-works/pi-coding-agent"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop (scoped package must be recognized)", d.Op)
	}
	if d.Detail != "npm: 1 present" {
		t.Fatalf("detail = %q; want \"npm: 1 present\"", d.Detail)
	}
}

// --- version-aware npm/pi backends ---

func npmJSON(deps map[string]string) string {
	type depEntry struct {
		Version string `json:"version"`
	}
	type payload struct {
		Dependencies map[string]depEntry `json:"dependencies"`
	}
	p := payload{Dependencies: make(map[string]depEntry, len(deps))}
	for k, v := range deps {
		p.Dependencies[k] = depEntry{Version: v}
	}
	b, err := json.Marshal(p)
	if err != nil {
		panic("npmJSON: marshal failed: " + err.Error())
	}
	return string(b)
}

func TestSplitNameVersion(t *testing.T) {
	cases := []struct {
		in, name, ver string
	}{
		{"npm:pi-tmux-bridge@0.1.1", "npm:pi-tmux-bridge", "0.1.1"},
		{"npm:pi-tmux-bridge", "npm:pi-tmux-bridge", ""},
		{"@earendil-works/pi-coding-agent", "@earendil-works/pi-coding-agent", ""},
		{"@earendil-works/pi-coding-agent@1.2.3", "@earendil-works/pi-coding-agent", "1.2.3"},
		{"/local/path", "/local/path", ""},
		{"npm:@gotgenes/pi-permission-system@27.0.1", "npm:@gotgenes/pi-permission-system", "27.0.1"},
	}
	for _, c := range cases {
		name, ver := splitNameVersion(c.in)
		if name != c.name || ver != c.ver {
			t.Errorf("splitNameVersion(%q) = (%q, %q); want (%q, %q)", c.in, name, ver, c.name, c.ver)
		}
	}
}

func TestNpmInventoryNonJSON(t *testing.T) {
	// Non-JSON stdout (e.g. npm warning printed to stdout) must return an error,
	// not an empty inventory. If it returned empty, needing() would mark every
	// desired package absent → spurious reinstall of everything.
	nonstd := "npm warn registry Unexpected warning from registry\nnot json at all"
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		npmInvCmd: {Stdout: nonstd},
	}}
	ctx := installCtx(t, "darwin", fake)

	// inventory.Npm itself must return an error.
	_, err := inventory.Npm(ctx)
	if err == nil {
		t.Fatal("inventory.Npm: expected error on non-JSON stdout, got nil")
	}

	// Inspect (via npmInspect) must propagate the error rather than returning
	// a spurious OpChange.
	fake2 := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		npmInvCmd:      {Stdout: nonstd},
	}}
	ctx2 := installCtx(t, "darwin", fake2)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"typescript"}}
	_, inspectErr := h.Inspect(ctx2, step)
	if inspectErr == nil {
		t.Fatal("Inspect: expected error on non-JSON npm output, got nil")
	}
}

func TestNpmInventoryParse(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		npmInvCmd: {Stdout: `{"dependencies":{"pkg":{"version":"1.2.3"},"@scope/n":{"version":"0.4.0"}}}`},
	}}
	ctx := installCtx(t, "darwin", fake)
	inv, err := inventory.Npm(ctx)
	if err != nil {
		t.Fatalf("inventory.Npm: %v", err)
	}
	want := map[string]string{"pkg": "1.2.3", "@scope/n": "0.4.0"}
	if !reflect.DeepEqual(inv, want) {
		t.Fatalf("inv = %v; want %v", inv, want)
	}

	// Empty dependencies → empty map.
	fake2 := &run.FakeRunner{Responses: map[string]run.Response{npmInvCmd: {Stdout: `{}`}}}
	ctx2 := installCtx(t, "darwin", fake2)
	inv2, err := inventory.Npm(ctx2)
	if err != nil {
		t.Fatalf("inventory.Npm empty: %v", err)
	}
	if len(inv2) != 0 {
		t.Fatalf("empty inv = %v; want empty", inv2)
	}
}

func TestInstallNpmPinnedMatch(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		npmInvCmd:      {Stdout: npmJSON(map[string]string{"pkg": "1.2.3"})},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"pkg@1.2.3"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop", d.Op)
	}
}

func TestInstallNpmPinnedMismatch(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm":             {Stdout: "/usr/bin/npm"},
		npmInvCmd:                  {Stdout: npmJSON(map[string]string{"pkg": "1.2.0"})},
		"npm install -g pkg@1.2.3": {},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"pkg@1.2.3"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
	if d.Detail != "npm install: pkg@1.2.3" {
		t.Fatalf("detail = %q; want %q", d.Detail, "npm install: pkg@1.2.3")
	}

	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"lookpath npm", "npm install -g pkg@1.2.3"}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, want)
	}
}

func TestInstallNpmPinnedAbsent(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		npmInvCmd:      {Stdout: `{"dependencies":{}}`},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"pkg@1.2.3"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
}

func TestInstallNpmUnversionedAnyVersion(t *testing.T) {
	// Backward compat: unversioned desired satisfied by any installed version.
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		npmInvCmd:      {Stdout: npmJSON(map[string]string{"pkg": "9.9.9"})},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Npm: []string{"pkg"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop", d.Op)
	}
}

func TestInstallNpmScopedPinned(t *testing.T) {
	const name = "@earendil-works/pi-coding-agent"
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		npmInvCmd:      {Stdout: npmJSON(map[string]string{name: "1.0.0"})},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)

	// Matching pinned → OpNoop.
	d, err := h.Inspect(ctx, manifest.InstallStep{Npm: []string{name + "@1.0.0"}})
	if err != nil {
		t.Fatalf("Inspect match: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("match op = %v; want OpNoop", d.Op)
	}

	// Mismatched pinned → OpChange.
	fake2 := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		npmInvCmd:      {Stdout: npmJSON(map[string]string{name: "1.0.0"})},
	}}
	ctx2 := installCtx(t, "darwin", fake2)
	d2, err := h.Inspect(ctx2, manifest.InstallStep{Npm: []string{name + "@1.0.1"}})
	if err != nil {
		t.Fatalf("Inspect mismatch: %v", err)
	}
	if d2.Op != engine.OpChange {
		t.Fatalf("mismatch op = %v; want OpChange", d2.Op)
	}

	// Unversioned scoped → OpNoop at any version.
	fake3 := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath npm": {Stdout: "/usr/bin/npm"},
		npmInvCmd:      {Stdout: npmJSON(map[string]string{name: "7.7.7"})},
	}}
	ctx3 := installCtx(t, "darwin", fake3)
	d3, err := h.Inspect(ctx3, manifest.InstallStep{Npm: []string{name}})
	if err != nil {
		t.Fatalf("Inspect unversioned: %v", err)
	}
	if d3.Op != engine.OpNoop {
		t.Fatalf("unversioned op = %v; want OpNoop", d3.Op)
	}
}

func TestInstallPiPinnedMatch(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath pi": {Stdout: "/usr/bin/pi"},
		"pi list":     {Stdout: "npm:pi-tmux-bridge@0.1.1\n"},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"npm:pi-tmux-bridge@0.1.1"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop", d.Op)
	}
}

func TestInstallPiPinnedMismatch(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath pi":                         {Stdout: "/usr/bin/pi"},
		"pi list":                             {Stdout: "npm:pi-tmux-bridge@0.1.1\n"},
		"pi install npm:pi-tmux-bridge@0.1.2": {},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"npm:pi-tmux-bridge@0.1.2"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v; want OpChange", d.Op)
	}
	if d.Detail != "pi install: npm:pi-tmux-bridge@0.1.2" {
		t.Fatalf("detail = %q", d.Detail)
	}

	fake.Calls = nil
	if err := h.Apply(ctx, step); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"lookpath pi", "pi install npm:pi-tmux-bridge@0.1.2"}
	if !reflect.DeepEqual(fake.Calls, want) {
		t.Fatalf("apply calls = %v; want %v", fake.Calls, want)
	}
}

func TestInstallPiUnversionedAnyVersion(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath pi": {Stdout: "/usr/bin/pi"},
		"pi list":     {Stdout: "npm:pi-tmux-bridge@0.1.1\n"},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"npm:pi-tmux-bridge"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop", d.Op)
	}
}

func TestInstallPiLocalPathUnversioned(t *testing.T) {
	fake := &run.FakeRunner{Responses: map[string]run.Response{
		"lookpath pi": {Stdout: "/usr/bin/pi"},
		"pi list":     {Stdout: "/Users/me/local-tool\n"},
	}}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Pi: []string{"/Users/me/local-tool"}}

	d, err := h.Inspect(ctx, step)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v; want OpNoop", d.Op)
	}
}

// A failing tap must not stop the taps after it.
func TestInstallBrewBadTapDoesNotBlockRest(t *testing.T) {
	resp := brewFound("", "", "")
	resp["brew tap bad/tap"] = run.Response{Err: errors.New("no such repo")}
	resp["brew tap good/tap"] = run.Response{}
	fake := &run.FakeRunner{Responses: resp}
	ctx := installCtx(t, "darwin", fake)
	h := getInstallHandler(t)
	step := manifest.InstallStep{Brew: &manifest.BrewSpec{Taps: []string{"bad/tap", "good/tap"}}}

	err := h.Apply(ctx, step)
	if err == nil || !strings.Contains(err.Error(), "bad/tap") {
		t.Fatalf("Apply err = %v; want an error naming bad/tap", err)
	}
	ran := strings.Join(fake.Calls, "\n")
	if !strings.Contains(ran, "brew tap good/tap") {
		t.Fatalf("good/tap not attempted; calls = %v", fake.Calls)
	}
}
