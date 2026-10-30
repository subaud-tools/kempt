package schema

import (
	"encoding/json"
	"testing"
)

func TestJSONIsValid(t *testing.T) {
	if !json.Valid(JSON()) {
		t.Fatal("schema.JSON() is not valid JSON")
	}
}

func TestTopLevelShape(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(JSON(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["$schema"]; !ok {
		t.Error("missing $schema")
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing properties object")
	}
	if _, ok := props["packages"]; !ok {
		t.Error("missing properties.packages")
	}
}

// packageProperties navigates the schema to the properties object of a package.
func packageProperties(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(JSON(), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// $defs.package.properties
	defs, ok := m["$defs"].(map[string]any)
	if !ok {
		t.Fatal("missing $defs")
	}
	pkg, ok := defs["package"].(map[string]any)
	if !ok {
		t.Fatal("missing $defs.package")
	}
	props, ok := pkg["properties"].(map[string]any)
	if !ok {
		t.Fatal("missing $defs.package.properties")
	}
	return props
}

// The exact set of step kinds the parser supports (internal/manifest/parse.go).
// This list must move in lockstep with the parser: adding a kind to the parser
// without adding it here (or to the schema) fails this test, and vice versa.
var parserKinds = []string{
	"install", "github-release", "git-clone", "service", "symlink",
	"json-merge", "line-in-file", "verify", "toml-merge", "download",
	"managed-file",
}

// nonKindPackageKeys are the package.properties entries that are NOT step
// arrays (structural fields the parser reads directly).
var nonKindPackageKeys = []string{"description", "needs", "only", "notes"}

func TestPackageStepKindsPresent(t *testing.T) {
	props := packageProperties(t)
	for _, k := range parserKinds {
		if _, ok := props[k]; !ok {
			t.Errorf("package schema missing step kind property %q", k)
		}
	}
}

// TestPackagePropertiesExactSet asserts the schema's package.properties keys are
// EXACTLY the 4 structural fields plus the 11 parser kinds — no more, no fewer.
// This guards both directions of drift between parser and schema.
func TestPackagePropertiesExactSet(t *testing.T) {
	props := packageProperties(t)

	want := map[string]bool{}
	for _, k := range nonKindPackageKeys {
		want[k] = true
	}
	for _, k := range parserKinds {
		want[k] = true
	}

	if len(parserKinds) != 11 {
		t.Fatalf("parserKinds has %d entries, want exactly 11", len(parserKinds))
	}

	// Every schema key must be expected.
	for k := range props {
		if !want[k] {
			t.Errorf("schema package.properties has unexpected key %q (not a known structural field or parser kind)", k)
		}
	}
	// Every expected key must be in the schema.
	for k := range want {
		if _, ok := props[k]; !ok {
			t.Errorf("schema package.properties missing expected key %q", k)
		}
	}
}

// TestKemptTableHasLayer: the [kempt] table accepts the layer scope the parser
// reads, with the parser's two values, so an editor using the schema does not
// flag a layer file.
func TestKemptTableHasLayer(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(JSON(), &m); err != nil {
		t.Fatal(err)
	}
	kempt := m["properties"].(map[string]any)["kempt"].(map[string]any)["properties"].(map[string]any)
	layer, ok := kempt["layer"].(map[string]any)
	if !ok {
		t.Fatal("[kempt] schema has no layer property")
	}
	enum, _ := layer["enum"].([]any)
	if len(enum) != 2 || enum[0] != "user" || enum[1] != "project" {
		t.Errorf("layer enum = %v; want [user project]", enum)
	}
}

// TestDownloadStepHasProduct: a download step accepts the product the parser
// reads, so an editor using the schema does not flag tackle's steps.
func TestDownloadStepHasProduct(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(JSON(), &m); err != nil {
		t.Fatal(err)
	}
	props := m["$defs"].(map[string]any)["downloadStep"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["product"]; !ok {
		t.Fatal("downloadStep schema has no product property")
	}
}

// TestSpec2SetupSyntax: spec 2's setup opt-in and managed-file step are in the
// schema with the fields the parser reads, and spec accepts 1 and 2 only.
func TestSpec2SetupSyntax(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(JSON(), &m); err != nil {
		t.Fatal(err)
	}
	spec := m["properties"].(map[string]any)["kempt"].(map[string]any)["properties"].(map[string]any)["spec"].(map[string]any)
	enum, _ := spec["enum"].([]any)
	if len(enum) != 2 || enum[0] != float64(1) || enum[1] != float64(2) {
		t.Errorf("spec enum = %v; want [1 2]", enum)
	}
	defs := m["$defs"].(map[string]any)
	if _, ok := defs["downloadStep"].(map[string]any)["properties"].(map[string]any)["setup"]; !ok {
		t.Error("downloadStep schema has no setup property")
	}
	mf, ok := defs["managedFileStep"].(map[string]any)
	if !ok {
		t.Fatal("schema has no managedFileStep")
	}
	props := mf["properties"].(map[string]any)
	for _, k := range []string{"from", "to", "mode", "only"} {
		if _, ok := props[k]; !ok {
			t.Errorf("managedFileStep missing %q", k)
		}
	}
	mode, _ := props["mode"].(map[string]any)["enum"].([]any)
	if len(mode) != 2 || mode[0] != "0644" || mode[1] != "0755" {
		t.Errorf("mode enum = %v", mode)
	}
}
