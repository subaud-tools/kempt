package manifest

import (
	"strings"
	"testing"
)

func findingsText(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.Path + ": " + f.Msg + "\n")
	}
	return b.String()
}

func parseValid(t *testing.T, src string) (*Manifest, []Finding) {
	t.Helper()
	m, findings := Parse([]byte(src))
	if len(findings) != 0 {
		t.Fatalf("parse findings: %s", findingsText(findings))
	}
	return m, Validate(m)
}

func TestSpec2AcceptsSetupDownloadAndManagedFile(t *testing.T) {
	m, findings := parseValid(t, `
[kempt]
spec = 2

[[packages.proj.download]]
site = "tackle.tools"
tool = "proj"
bin = "proj"
setup = true

[[packages.proj.managed-file]]
from = "proj.zsh"
to = "~/.config/zsh/proj.zsh"
mode = "0644"
only = { os = "darwin" }
`)
	if len(findings) != 0 {
		t.Fatalf("validate findings: %s", findingsText(findings))
	}
	steps := m.Packages["proj"].Steps
	if d := steps[0].(DownloadStep); !d.Setup {
		t.Fatal("download setup = false, want true")
	}
	mf, ok := steps[1].(ManagedFileStep)
	if !ok {
		t.Fatalf("step 1 = %T, want ManagedFileStep", steps[1])
	}
	if mf.From != "proj.zsh" || mf.To != "~/.config/zsh/proj.zsh" || mf.Mode != "0644" || mf.Only == nil || mf.Only.OS != "darwin" {
		t.Fatalf("managed-file = %+v", mf)
	}
	if mf.Kind() != "managed-file" || mf.Class() != ClassFiles {
		t.Fatalf("kind/class = %s/%v", mf.Kind(), mf.Class())
	}
}

func TestSpec1RejectsSetupSyntax(t *testing.T) {
	_, findings := parseValid(t, `
[kempt]
spec = 1

[[packages.proj.download]]
site = "tackle.tools"
tool = "proj"
bin = "proj"
setup = true

[[packages.proj.managed-file]]
from = "proj.zsh"
to = "~/.config/zsh/proj.zsh"
mode = "0644"
`)
	got := findingsText(findings)
	for _, want := range []string{"packages.proj.download[0]: setup requires spec 2", "packages.proj.managed-file[0]: managed-file requires spec 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("findings missing %q:\n%s", want, got)
		}
	}
}

func TestUnsupportedSpecStillRejected(t *testing.T) {
	_, findings := parseValid(t, "[kempt]\nspec = 3\n")
	if !strings.Contains(findingsText(findings), "unsupported spec version 3") {
		t.Fatalf("findings = %s", findingsText(findings))
	}
}

func TestManagedFileFieldRules(t *testing.T) {
	_, findings := parseValid(t, `
[kempt]
spec = 2

[[packages.p.managed-file]]
from = "../escape"
to = "~/x"
mode = "0600"

[[packages.p.managed-file]]
from = "/abs"
to = "~/y"
mode = "0755"

[[packages.p.managed-file]]
to = "~/z"
`)
	got := findingsText(findings)
	for _, want := range []string{
		`packages.p.managed-file[0]: from "../escape" must be a path inside the bundle's files/`,
		`packages.p.managed-file[0]: mode must be "0644" or "0755"`,
		`packages.p.managed-file[1]: from "/abs" must be a path inside the bundle's files/`,
		`packages.p.managed-file[2]: missing required field "from"`,
		`packages.p.managed-file[2]: missing required field "mode"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("findings missing %q:\n%s", want, got)
		}
	}
}

// A release fragment declares only [packages.integration] in spec 2, may use
// the existing file, service and verify primitives plus managed files, and
// every from names a file the verified bundle carries.
func TestValidateSetupFragment(t *testing.T) {
	src := `
[kempt]
spec = 2

[[packages.integration.managed-file]]
from = "skills/proj/SKILL.md"
to = "~/.claude/skills/proj/SKILL.md"
mode = "0644"

[[packages.integration.json-merge]]
file = "~/.claude/settings.json"
merge = { a = 1 }

[[packages.integration.line-in-file]]
file = "~/.zshrc"
line = "source ~/.config/zsh/proj.zsh"

[[packages.integration.service]]
label = "tools.proj"
program = ["proj", "serve"]

[[packages.integration.verify]]
command-exists = "proj"
`
	m, findings := parseValid(t, src)
	if len(findings) != 0 {
		t.Fatalf("validate findings: %s", findingsText(findings))
	}
	files := map[string][]byte{"skills/proj/SKILL.md": []byte("x")}
	if got := ValidateSetup(m, files); len(got) != 0 {
		t.Fatalf("ValidateSetup = %s", findingsText(got))
	}
	if got := findingsText(ValidateSetup(m, map[string][]byte{})); !strings.Contains(got, `packages.integration.managed-file[0]: from "skills/proj/SKILL.md" is not a file in the setup bundle`) {
		t.Fatalf("missing-file finding absent:\n%s", got)
	}
}

func TestValidateSetupRejectsOtherShapes(t *testing.T) {
	m, findings := parseValid(t, `
[kempt]
spec = 2

[[packages.integration.download]]
site = "tackle.tools"
tool = "proj"
bin = "proj"

[[packages.integration.git-clone]]
repo = "https://example.com/r"
to = "~/r"

[[packages.other.verify]]
command-exists = "proj"
`)
	if len(findings) != 0 {
		t.Fatalf("validate findings: %s", findingsText(findings))
	}
	got := findingsText(ValidateSetup(m, nil))
	for _, want := range []string{
		"packages.other: a setup bundle declares only [packages.integration]",
		"packages.integration.download[0]: a setup bundle may not use download",
		"packages.integration.git-clone[0]: a setup bundle may not use git-clone",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("findings missing %q:\n%s", want, got)
		}
	}
	m1, _ := Parse([]byte("[kempt]\nspec = 1\n[[packages.integration.verify]]\ncommand-exists = \"x\"\n"))
	if got := findingsText(ValidateSetup(m1, nil)); !strings.Contains(got, "kempt.spec: a setup bundle uses spec 2") {
		t.Errorf("spec-1 fragment accepted:\n%s", got)
	}
}

// BindSetup gives each managed-file step its bytes from the verified bundle
// and the release that supplied them; a manifest cannot set them itself.
func TestBindSetupAttachesBundleFiles(t *testing.T) {
	m, findings := parseValid(t, `
[kempt]
spec = 2

[[packages.integration.managed-file]]
from = "bin/proj-wrapper"
to = "~/.local/bin/p"
mode = "0755"
`)
	if len(findings) != 0 {
		t.Fatalf("validate findings: %s", findingsText(findings))
	}
	if got := BindSetup(m, "proj", "1.2.0", map[string][]byte{"bin/proj-wrapper": []byte("#!/bin/sh\n")}); len(got) != 0 {
		t.Fatalf("BindSetup = %s", findingsText(got))
	}
	mf := m.Packages["integration"].Steps[0].(ManagedFileStep)
	if mf.Source == nil || mf.Source.Tool != "proj" || mf.Source.Version != "1.2.0" || string(mf.Source.Data) != "#!/bin/sh\n" {
		t.Fatalf("source = %+v", mf.Source)
	}
	if got := BindSetup(m, "proj", "1.2.0", nil); len(got) == 0 {
		t.Fatal("BindSetup bound a fragment whose file is missing")
	}
}
