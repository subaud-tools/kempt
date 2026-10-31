package doctor

import (
	"bytes"
	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"path/filepath"
	"strings"
	"testing"
)

func TestD2FlagsDuplicateIdentity(t *testing.T) {
	ctx := ctxT(t)
	f := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, f, `{"packages":["npm:pi-quiet","npm:pi-quiet@0.2.0","npm:solo"]}`)
	pkgs := pkgWithMerge(f, map[string]any{"packages": []any{"npm:pi-quiet", "npm:solo"}}, "")
	got := CheckDupIdentity(ctx, pkgs)
	if len(got) != 1 || got[0].Severity != Warn {
		t.Fatalf("want 1 warn for pi-quiet, got %+v", got)
	}
}

func TestD2NoDuplicates(t *testing.T) {
	ctx := ctxT(t)
	f := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, f, `{"packages":["npm:a","npm:b"]}`)
	if got := CheckDupIdentity(ctx, pkgWithMerge(f, map[string]any{"packages": []any{"npm:a", "npm:b"}}, "")); len(got) != 0 {
		t.Fatalf("want none, got %+v", got)
	}
}

func TestDeclaredHookAndRetiredRegistrationHealth(t *testing.T) {
	ctx := ctxT(t)
	f := filepath.Join(ctx.Home, "settings.json")
	m, findings := manifest.Parse([]byte(`[kempt]
spec=2
[[packages.muster.json-merge]]
file="~/settings.json"
remove=["enabledPlugins.old@family"]
remove-hooks=[{event="Stop",matcher="",commands=["muster hook Stop claude","~/.local/bin/muster hook Stop claude"]}]
merge={hooks={Stop=[{hooks=[{type="command",command="~/.local/bin/muster hook Stop claude"}]}]}}
`))
	if len(findings) > 0 {
		t.Fatal(findings)
	}
	pkgs := []*manifest.Package{m.Packages["muster"]}
	writeFile(t, f, `{"enabledPlugins":{"old@family":true},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"muster hook Stop claude"},{"type":"command","command":"unrelated"}]},{"hooks":[{"type":"command","command":"~/.local/bin/muster hook Stop claude"}]}]}}`)
	got := CheckDupIdentity(ctx, pkgs)
	if len(got) != 2 {
		t.Fatalf("want duplicate and retired registration, got %+v", got)
	}
	for _, g := range got {
		if g.Package != "muster" || !strings.Contains(g.Remediation, "kempt apply") {
			t.Fatal(g)
		}
	}
	plan, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if engine.Execute(ctx, plan, &out) != 0 {
		t.Fatal(out.String())
	}
	if got = CheckDupIdentity(ctx, pkgs); len(got) != 0 {
		t.Fatalf("healthy after apply: %+v", got)
	}
	if got = CheckRollup(ctx, pkgs); len(got) != 0 {
		t.Fatalf("final canonical has drift: %+v", got)
	}
}
