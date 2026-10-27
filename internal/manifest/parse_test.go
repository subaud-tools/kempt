// internal/manifest/parse_test.go
package manifest

import (
	"fmt"
	"os"
	"testing"

	"github.com/BurntSushi/toml"
)

func mustParse(t *testing.T, path string) *Manifest {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m, findings := Parse(src)
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %v", findings)
	}
	return m
}

func TestParseReference(t *testing.T) {
	m := mustParse(t, "testdata/reference.toml")
	if m.Spec != 1 {
		t.Fatalf("spec = %d, want 1", m.Spec)
	}
	core, ok := m.Packages["core"]
	if !ok {
		t.Fatal("missing package core")
	}
	if len(core.Steps) != 2 {
		t.Fatalf("core steps = %d, want 2", len(core.Steps))
	}
	if core.Steps[0].Kind() != "install" || core.Steps[1].Kind() != "symlink" {
		t.Fatalf("step order = %s,%s", core.Steps[0].Kind(), core.Steps[1].Kind())
	}
	if core.Steps[0].Class() != ClassSoftware || core.Steps[1].Class() != ClassFiles {
		t.Fatal("safety classes wrong")
	}
	dev := m.Profiles["developer"]
	if dev == nil || len(dev.Packages) == 0 {
		t.Fatal("developer profile missing")
	}
}

func TestStepsKeepWrittenOrderAcrossKinds(t *testing.T) {
	src := []byte(`
[kempt]
spec = 1
[packages.x]
description = "order test"
[[packages.x.symlink]]
from = "a"
to = "~/a"
[[packages.x.install]]
brew = { formulas = ["jq"] }
[[packages.x.symlink]]
from = "b"
to = "~/b"
`)
	m, findings := Parse(src)
	if len(findings) != 0 {
		t.Fatalf("findings: %v", findings)
	}
	got := []string{}
	for _, s := range m.Packages["x"].Steps {
		got = append(got, s.Kind())
	}
	want := []string{"symlink", "install", "symlink"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestIsFreeform(t *testing.T) {
	cases := []struct {
		name string
		key  toml.Key
		want bool
	}{
		{
			name: "suppressed merge sub-key",
			key:  toml.Key{"packages", "muster", "json-merge", "merge", "mcpServers"},
			want: true,
		},
		{
			name: "top-level json-merge.merge without packages prefix",
			key:  toml.Key{"json-merge", "merge"},
			want: false,
		},
		{
			name: "suppressed toml-merge sub-key",
			key:  toml.Key{"packages", "agent", "toml-merge", "merge", "mcp"},
			want: true,
		},
		{
			name: "top-level toml-merge.merge without packages prefix",
			key:  toml.Key{"toml-merge", "merge"},
			want: false,
		},
		{
			name: "unrelated package key is not suppressed",
			key:  toml.Key{"packages", "muster", "symlink", "from"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFreeform(tc.key); got != tc.want {
				t.Fatalf("isFreeform(%v) = %v, want %v", []string(tc.key), got, tc.want)
			}
		})
	}
}

func TestParseUnknownKeyIsFinding(t *testing.T) {
	src := []byte("[kempt]\nspec = 1\nbogus = true\n")
	_, findings := Parse(src)
	if len(findings) == 0 {
		t.Fatal("want finding for unknown key 'bogus'")
	}
}

func TestParseDoctorConfig(t *testing.T) {
	src := []byte(`
[kempt]
spec = 1
[doctor]
checkNpmOrphans = true
ignore = ["npm:pi-quiet", "glob:pi-*"]
`)
	m, findings := Parse(src)
	if len(findings) != 0 {
		t.Fatalf("unexpected findings (a [doctor] key was flagged unknown?): %v", findings)
	}
	if m.Doctor == nil {
		t.Fatal("m.Doctor is nil, want decoded [doctor] block")
	}
	if !m.Doctor.CheckNpmOrphans {
		t.Error("CheckNpmOrphans = false, want true")
	}
	if len(m.Doctor.Ignore) != 2 || m.Doctor.Ignore[0] != "npm:pi-quiet" || m.Doctor.Ignore[1] != "glob:pi-*" {
		t.Fatalf("Ignore = %v, want [npm:pi-quiet glob:pi-*]", m.Doctor.Ignore)
	}
}

func TestParseNotes(t *testing.T) {
	src := []byte(`
[kempt]
spec = 1
[packages.core]
description = "core tools"
notes = ["run codex login", "grant access"]
`)
	m, findings := Parse(src)
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %v", findings)
	}
	pkg, ok := m.Packages["core"]
	if !ok {
		t.Fatal("missing package core")
	}
	if len(pkg.Notes) != 2 {
		t.Fatalf("notes = %v, want 2 entries", pkg.Notes)
	}
	if pkg.Notes[0] != "run codex login" {
		t.Fatalf("notes[0] = %q, want \"run codex login\"", pkg.Notes[0])
	}
	if pkg.Notes[1] != "grant access" {
		t.Fatalf("notes[1] = %q, want \"grant access\"", pkg.Notes[1])
	}
}

func TestParseDownloadProduct(t *testing.T) {
	src := []byte("[kempt]\nspec = 1\n[packages.tackle]\n[[packages.tackle.download]]\nsite = \"tackle.tools\"\ntool = \"cull\"\nproduct = \"tackle\"\nbin = \"cull\"\n")
	m, findings := Parse(src)
	if len(findings) != 0 {
		t.Fatalf("findings: %v", findings)
	}
	if st := m.Packages["tackle"].Steps[0].(DownloadStep); st.Product != "tackle" {
		t.Fatalf("product = %q, want tackle", st.Product)
	}
}
