package manifest

import (
	"strings"
	"testing"
)

func findingsFor(t *testing.T, src string) []Finding {
	t.Helper()
	m, pf := Parse([]byte(src))
	if len(pf) != 0 {
		t.Fatalf("parse findings: %v", pf)
	}
	return Validate(m)
}

func TestValidateCycle(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
needs = ["b"]
[packages.b]
description = "b"
needs = ["a"]
`)
	if len(f) != 1 || !strings.Contains(f[0].Msg, "cycle") {
		t.Fatalf("want cycle finding, got %v", f)
	}
}

func TestValidateUnknownNeed(t *testing.T) {
	f := findingsFor(t, "[kempt]\nspec = 1\n[packages.a]\ndescription = \"a\"\nneeds = [\"ghost\"]\n")
	if len(f) != 1 || !strings.Contains(f[0].Msg, `unknown package "ghost"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateBadOS(t *testing.T) {
	f := findingsFor(t, "[kempt]\nspec = 1\n[packages.a]\ndescription = \"a\"\nonly = { os = \"beos\" }\n")
	if len(f) != 1 {
		t.Fatalf("got %v", f)
	}
}

func TestValidateSpecVersion(t *testing.T) {
	// Spec 2 (release setup) is supported; the next version is not.
	f := findingsFor(t, "[kempt]\nspec = 3\n")
	if len(f) != 1 || !strings.Contains(f[0].Msg, "unsupported spec") {
		t.Fatalf("got %v", f)
	}
}

func TestValidateProfileRef(t *testing.T) {
	f := findingsFor(t, "[kempt]\nspec = 1\n[profiles.dev]\ndescription = \"d\"\npackages = [\"nope\"]\n")
	if len(f) != 1 {
		t.Fatalf("got %v", f)
	}
}

func TestValidateStepRequiredFields(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.symlink]]
from = "x"
`)
	if len(f) != 1 || !strings.Contains(f[0].Msg, `missing required field "to"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateStepFindingUsesPerKindIndex(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.install]]
brew = { formulas = ["jq"] }
[[packages.a.symlink]]
from = "one"
to = "~/one"
[[packages.a.symlink]]
from = "two"
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "symlink[1]") {
		t.Fatalf("want path containing symlink[1] (per-kind index), got %v", f)
	}
}

func TestValidateEmptyBrewTableIsNotABackend(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.install]]
brew = {}
`)
	if len(f) != 1 {
		t.Fatalf("brew={} must fail the >=1-backend check, got %v", f)
	}
}

func TestValidateNpmOnlyIsABackend(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.install]]
npm = ["typescript"]
`)
	if len(f) != 0 {
		t.Fatalf("npm-only install must be a valid backend, got %v", f)
	}
}

func TestValidatePiOnlyIsABackend(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.install]]
pi = ["npm:typescript"]
`)
	if len(f) != 0 {
		t.Fatalf("pi-only install must be a valid backend, got %v", f)
	}
}

func TestValidateStepLevelOnlyBadArch(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.symlink]]
from = "x"
to = "~/x"
only = { arch = "sparc" }
`)
	if len(f) != 1 || !strings.Contains(f[0].Msg, `unknown arch "sparc"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateMissingFieldGithubReleaseBin(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.github-release]]
repo = "owner/repo"
asset = "asset.tar.gz"
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "github-release[0]") || !strings.Contains(f[0].Msg, `missing required field "bin"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateMissingFieldGitCloneTo(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.git-clone]]
repo = "https://example.com/repo"
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "git-clone[0]") || !strings.Contains(f[0].Msg, `missing required field "to"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateMissingFieldServiceProgram(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.service]]
label = "com.example.svc"
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "service[0]") || !strings.Contains(f[0].Msg, `missing required field "program"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateMissingFieldJSONMergeMerge(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.json-merge]]
file = "/tmp/foo.json"
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "json-merge[0]") || !strings.Contains(f[0].Msg, `missing required field "merge"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateMissingFieldTomlMergeMerge(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.toml-merge]]
file = "/tmp/foo.toml"
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "toml-merge[0]") || !strings.Contains(f[0].Msg, `missing required field "merge"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateTomlMergeFreeformClean(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.toml-merge]]
file = "/tmp/foo.toml"
merge = { model = "gpt-5", nested = { arbitrary = true } }
`)
	if len(f) != 0 {
		t.Fatalf("want zero findings for free-form toml-merge subtree, got %v", f)
	}
}

func TestValidateJSONMergeArraysBogus(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.json-merge]]
file = "/tmp/foo.json"
arrays = "bogus"
merge = { x = 1 }
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "json-merge[0]") || !strings.Contains(f[0].Msg, `arrays must be "append" or "replace"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateMissingFieldLineInFileLine(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.line-in-file]]
file = "/etc/hosts"
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "line-in-file[0]") || !strings.Contains(f[0].Msg, `missing required field "line"`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateMissingFieldVerifyNoChecks(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.verify]]
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "verify[0]") || !strings.Contains(f[0].Msg, `missing required field`) {
		t.Fatalf("got %v", f)
	}
}

func TestValidateInstallSettingsPackagesMismatch(t *testing.T) {
	src := []byte(`
spec = 1
[packages.pi]
[[packages.pi.install]]
pi = ["npm:a", "npm:b"]
[[packages.pi.json-merge]]
file = "~/.pi/agent/settings.json"
merge = { packages = ["npm:a"] }
`)
	m, f := Parse(src)
	f = append(f, Validate(m)...)
	found := false
	for _, x := range f {
		if strings.Contains(x.Msg, "install") && strings.Contains(x.Msg, "packages") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a string-for-string mismatch finding, got %+v", f)
	}
}

func TestValidateInstallSettingsPackagesMatchOrderInsensitive(t *testing.T) {
	src := []byte(`
spec = 1
[packages.pi]
[[packages.pi.install]]
pi = ["npm:a", "npm:b"]
[[packages.pi.json-merge]]
file = "~/.pi/agent/settings.json"
merge = { packages = ["npm:b", "npm:a"] }
`)
	m, f := Parse(src)
	f = append(f, Validate(m)...)
	for _, x := range f {
		if strings.Contains(x.Msg, "string-for-string") {
			t.Fatalf("reordered lists are set-equal; no finding expected: %+v", f)
		}
	}
}

func TestValidateReferenceIsClean(t *testing.T) {
	m := mustParse(t, "testdata/reference.toml")
	if f := Validate(m); len(f) != 0 {
		t.Fatalf("reference should validate clean: %v", f)
	}
}

func TestValidateNonPiSettingsMergeNotChecked(t *testing.T) {
	// A path that merely embeds "pi/agent/settings.json" (via "happi") must NOT
	// be treated as the pi settings file, so the install<->packages
	// string-for-string rule does not fire on it.
	src := []byte(`
spec = 1
[packages.x]
[[packages.x.install]]
pi = ["npm:a"]
[[packages.x.json-merge]]
file = "/opt/happi/agent/settings.json"
merge = { packages = ["npm:b"] }
`)
	m, f := Parse(src)
	f = append(f, Validate(m)...)
	for _, x := range f {
		if strings.Contains(x.Msg, "string-for-string") {
			t.Fatalf("non-pi settings file must be ignored by L1; got %+v", f)
		}
	}
}

func TestValidateServiceStartIntervalRequiresKeepAliveFalse(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.service]]
label = "com.example.every"
program = ["/usr/bin/foo"]
start-interval = 1800
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "service[0]") || !strings.Contains(f[0].Msg, "start-interval requires keep-alive = false") {
		t.Fatalf("want one finding about keep-alive, got %v", f)
	}
}

func TestValidateServiceStartIntervalZeroInvalid(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.service]]
label = "com.example.every"
program = ["/usr/bin/foo"]
keep-alive = false
start-interval = 0
`)
	if len(f) != 1 || !strings.Contains(f[0].Msg, "start-interval must be > 0") {
		t.Fatalf("want one finding about start-interval > 0, got %v", f)
	}
}

func TestValidateServiceStartIntervalValid(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.service]]
label = "com.example.every"
program = ["/usr/bin/foo"]
keep-alive = false
start-interval = 1800
`)
	if len(f) != 0 {
		t.Fatalf("want no findings, got %v", f)
	}
}

// remove parses, satisfies the merge requirement on its own, and rejects an
// empty key.
func TestValidateJSONMergeRemove(t *testing.T) {
	f := findingsFor(t, `
[kempt]
spec = 1
[packages.a]
description = "a"
[[packages.a.json-merge]]
file = "/tmp/foo.json"
remove = ["mcpServers.daybook.command"]
[[packages.a.json-merge]]
file = "/tmp/foo.json"
remove = ["mcpServers..command"]
merge = { x = 1 }
`)
	if len(f) != 1 || !strings.Contains(f[0].Path, "json-merge[1]") || !strings.Contains(f[0].Msg, "empty key") {
		t.Fatalf("want one empty-key finding on json-merge[1], got %v", f)
	}
}
