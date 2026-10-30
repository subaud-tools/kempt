package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/run"
)

func serviceHandler(t *testing.T) engine.Handler {
	t.Helper()
	h, ok := engine.HandlerFor("service")
	if !ok {
		t.Fatal("service handler not registered")
	}
	return h
}

func svcCtx(t *testing.T, r run.Runner, uid int) *machine.Context {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	return &machine.Context{
		Home:    tmp,
		RepoDir: tmp,
		OS:      "darwin",
		Arch:    "arm64",
		UID:     uid,
		Runner:  r,
	}
}

var goldenStep = manifest.ServiceStep{
	Label:   "com.example.svc",
	Program: []string{"/usr/bin/foo", "--flag=a&b"},
}

// goldenPlist is the exact expected render for goldenStep. Built with explicit
// \t / \n so the tab layout is unambiguous.
const goldenPlist = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
	"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" +
	"<plist version=\"1.0\">\n" +
	"<dict>\n" +
	"\t<key>KeepAlive</key>\n" +
	"\t<true/>\n" +
	"\t<key>Label</key>\n" +
	"\t<string>com.example.svc</string>\n" +
	"\t<key>ProgramArguments</key>\n" +
	"\t<array>\n" +
	"\t\t<string>/usr/bin/foo</string>\n" +
	"\t\t<string>--flag=a&amp;b</string>\n" +
	"\t</array>\n" +
	"\t<key>RunAtLoad</key>\n" +
	"\t<true/>\n" +
	"</dict>\n" +
	"</plist>\n"

func TestServiceRenderGolden(t *testing.T) {
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	got := renderPlist(ctx, goldenStep)
	if got != goldenPlist {
		t.Fatalf("render mismatch\n--- got ---\n%q\n--- want ---\n%q", got, goldenPlist)
	}
}

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

// fullStep exercises every optional field: env (2 keys, one with `&`, one with
// `~`), stdout/stderr with `~`, ProcessType, ThrottleInterval, SessionType,
// and KeepAlive=false (RunAtLoad left nil → defaults true).
func fullStep() manifest.ServiceStep {
	return manifest.ServiceStep{
		Label:   "com.example.full",
		Program: []string{"/usr/bin/foo", "--flag=a&b"},
		Env: map[string]string{
			"URL":   "/x?a=1&b=2",
			"HOME2": "~/data",
		},
		Stdout:           "~/Library/Logs/foo.out",
		Stderr:           "~/Library/Logs/foo.err",
		ProcessType:      "Interactive",
		ThrottleInterval: intPtr(30),
		SessionType:      "Aqua",
		KeepAlive:        boolPtr(false),
	}
}

func fullPlist(ctx *machine.Context) string {
	return "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n" +
		"<plist version=\"1.0\">\n" +
		"<dict>\n" +
		"\t<key>EnvironmentVariables</key>\n" +
		"\t<dict>\n" +
		"\t\t<key>HOME2</key>\n" +
		"\t\t<string>" + filepath.Join(ctx.Home, "data") + "</string>\n" +
		"\t\t<key>URL</key>\n" +
		"\t\t<string>/x?a=1&amp;b=2</string>\n" +
		"\t</dict>\n" +
		"\t<key>KeepAlive</key>\n" +
		"\t<false/>\n" +
		"\t<key>Label</key>\n" +
		"\t<string>com.example.full</string>\n" +
		"\t<key>LimitLoadToSessionType</key>\n" +
		"\t<string>Aqua</string>\n" +
		"\t<key>ProcessType</key>\n" +
		"\t<string>Interactive</string>\n" +
		"\t<key>ProgramArguments</key>\n" +
		"\t<array>\n" +
		"\t\t<string>/usr/bin/foo</string>\n" +
		"\t\t<string>--flag=a&amp;b</string>\n" +
		"\t</array>\n" +
		"\t<key>RunAtLoad</key>\n" +
		"\t<true/>\n" +
		"\t<key>StandardErrorPath</key>\n" +
		"\t<string>" + filepath.Join(ctx.Home, "Library/Logs/foo.err") + "</string>\n" +
		"\t<key>StandardOutPath</key>\n" +
		"\t<string>" + filepath.Join(ctx.Home, "Library/Logs/foo.out") + "</string>\n" +
		"\t<key>ThrottleInterval</key>\n" +
		"\t<integer>30</integer>\n" +
		"</dict>\n" +
		"</plist>\n"
}

func TestServiceRenderFullGolden(t *testing.T) {
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	got := renderPlist(ctx, fullStep())
	want := fullPlist(ctx)
	if got != want {
		t.Fatalf("full render mismatch\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// A KeepAlive=false step must render <false/>; RunAtLoad nil stays <true/>.
func TestServiceRenderKeepAliveFalse(t *testing.T) {
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	st := manifest.ServiceStep{
		Label:     "com.example.ka",
		Program:   []string{"/usr/bin/foo"},
		KeepAlive: boolPtr(false),
	}
	got := renderPlist(ctx, st)
	if !strings.Contains(got, "<key>KeepAlive</key>\n\t<false/>\n") {
		t.Fatalf("KeepAlive not <false/>:\n%s", got)
	}
	if !strings.Contains(got, "<key>RunAtLoad</key>\n\t<true/>\n") {
		t.Fatalf("RunAtLoad not defaulted <true/>:\n%s", got)
	}
}

// Idempotency: after writing the rendered full plist, Inspect reports OpNoop.
func TestServiceInspectFullNoop(t *testing.T) {
	h := serviceHandler(t)
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	st := fullStep()
	p := plistPath(ctx, st.Label)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(renderPlist(ctx, st)), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := h.Inspect(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v (%q), want noop", d.Op, d.Detail)
	}
}

func plistPath(ctx *machine.Context, label string) string {
	return filepath.Join(ctx.Home, "Library", "LaunchAgents", label+".plist")
}

func TestServiceInspectNonDarwinBlocked(t *testing.T) {
	h := serviceHandler(t)
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	ctx.OS = "linux"
	d, err := h.Inspect(ctx, goldenStep)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpBlocked {
		t.Fatalf("op = %v (%q), want blocked", d.Op, d.Detail)
	}
}

func TestServiceInspectMissingChange(t *testing.T) {
	h := serviceHandler(t)
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	d, err := h.Inspect(ctx, goldenStep)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v (%q), want change", d.Op, d.Detail)
	}
}

func TestServiceInspectDiffersChange(t *testing.T) {
	h := serviceHandler(t)
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	p := plistPath(ctx, goldenStep.Label)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := h.Inspect(ctx, goldenStep)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpChange {
		t.Fatalf("op = %v (%q), want change", d.Op, d.Detail)
	}
}

func TestServiceInspectSameNoop(t *testing.T) {
	h := serviceHandler(t)
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	p := plistPath(ctx, goldenStep.Label)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(goldenPlist), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := h.Inspect(ctx, goldenStep)
	if err != nil {
		t.Fatal(err)
	}
	if d.Op != engine.OpNoop {
		t.Fatalf("op = %v (%q), want noop", d.Op, d.Detail)
	}
}

func TestServiceApplyWritesAndReloads(t *testing.T) {
	h := serviceHandler(t)
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := svcCtx(t, fr, 501)
	p := plistPath(ctx, goldenStep.Label)
	bootout := "launchctl bootout gui/501 " + p
	bootstrap := "launchctl bootstrap gui/501 " + p
	fr.Responses[bootstrap] = run.Response{}
	// bootout intentionally left unscripted: its error must be ignored.

	if err := h.Apply(ctx, goldenStep); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("plist not written: %v", err)
	}
	if string(b) != goldenPlist {
		t.Fatalf("plist content mismatch\n--- got ---\n%q", string(b))
	}
	want := []string{bootout, bootstrap}
	if len(fr.Calls) != 2 || fr.Calls[0] != want[0] || fr.Calls[1] != want[1] {
		t.Fatalf("calls = %v, want %v", fr.Calls, want)
	}
}

// TestServiceEnvExpansionVerbatim asserts that bare relative env values render
// verbatim (not repo-joined) and that ~-prefixed values still expand to home.
func TestServiceEnvExpansionVerbatim(t *testing.T) {
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	st := manifest.ServiceStep{
		Label:   "com.example.env",
		Program: []string{"/usr/bin/foo"},
		Env: map[string]string{
			"NODE_ENV": "production",
			"HOME_DIR": "~/stuff",
		},
	}
	got := renderPlist(ctx, st)

	// Bare relative value must appear verbatim — never repo-joined.
	wantEnvDict := "\t<key>EnvironmentVariables</key>\n" +
		"\t<dict>\n" +
		"\t\t<key>HOME_DIR</key>\n" +
		"\t\t<string>" + filepath.Join(ctx.Home, "stuff") + "</string>\n" +
		"\t\t<key>NODE_ENV</key>\n" +
		"\t\t<string>production</string>\n" +
		"\t</dict>\n"
	if !strings.Contains(got, wantEnvDict) {
		t.Fatalf("env dict mismatch\n--- got ---\n%q\n--- want (substring) ---\n%q", got, wantEnvDict)
	}
	// Also assert the full rendered output doesn't contain the RepoDir for NODE_ENV.
	repoJoined := filepath.Join(ctx.RepoDir, "production")
	if strings.Contains(got, repoJoined) {
		t.Fatalf("env value was repo-joined: found %q in plist", repoJoined)
	}
}

func TestServiceRenderStartInterval(t *testing.T) {
	ctx := svcCtx(t, &run.FakeRunner{}, 501)
	iv := 1800
	st := manifest.ServiceStep{
		Label:         "com.example.every",
		Program:       []string{"/usr/bin/foo"},
		KeepAlive:     boolPtr(false),
		StartInterval: &iv,
	}
	got := renderPlist(ctx, st)
	if !strings.Contains(got, "<key>StartInterval</key>\n\t<integer>1800</integer>\n") {
		t.Fatalf("StartInterval missing:\n%s", got)
	}
	// Keys stay alphabetical: StandardOutPath < StartInterval < ThrottleInterval.
	if strings.Index(got, "<key>RunAtLoad</key>") > strings.Index(got, "<key>StartInterval</key>") {
		t.Fatalf("key order:\n%s", got)
	}
}

func TestServiceApplyNoopIssuesNoCommands(t *testing.T) {
	h := serviceHandler(t)
	fr := &run.FakeRunner{Responses: map[string]run.Response{}}
	ctx := svcCtx(t, fr, 501)
	p := plistPath(ctx, goldenStep.Label)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(goldenPlist), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.Apply(ctx, goldenStep); err != nil {
		t.Fatal(err)
	}
	if len(fr.Calls) != 0 {
		t.Fatalf("calls = %v, want none", fr.Calls)
	}
}
