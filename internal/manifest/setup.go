package manifest

import (
	"fmt"
	"io/fs"
	"strings"
)

// SetupPackage is the one package a release setup bundle's fragment declares.
const SetupPackage = "integration"

// BundlePath reports whether p names a file beneath a setup bundle's files/:
// a slash-separated relative path with no "." or ".." element.
func BundlePath(p string) bool {
	return p != "." && fs.ValidPath(p) && !strings.Contains(p, `\`)
}

// validateSpec2Syntax rejects the spec 2 setup syntax in a spec 1 file, so an
// older manifest cannot carry setup that an older engine would ignore.
func validateSpec2Syntax(m *Manifest) []Finding {
	if m.Spec != 1 {
		return nil
	}
	var findings []Finding
	for _, name := range sortedPackageNames(m) {
		idx := map[string]int{}
		for _, step := range m.Packages[name].Steps {
			kind := step.Kind()
			path := fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx[kind])
			idx[kind]++
			switch s := step.(type) {
			case DownloadStep:
				if s.Setup {
					findings = append(findings, Finding{Path: path, Msg: "setup requires spec 2"})
				}
			case JSONMergeStep:
				if len(s.RemoveHooks) > 0 {
					findings = append(findings, Finding{Path: path, Msg: "remove-hooks requires spec 2"})
				}
			case ManagedFileStep:
				findings = append(findings, Finding{Path: path, Msg: "managed-file requires spec 2"})
			}
		}
	}
	return findings
}

// ValidateSetup checks a release setup bundle's fragment, already Parsed and
// Validated, against the bundle's files (path beneath files/ -> bytes, as
// setupbundle.Read returns them): spec 2, exactly [packages.integration], no
// software source of its own (the parent download supplies the release), and
// every managed-file from naming one of files.
func ValidateSetup(m *Manifest, files map[string][]byte) []Finding {
	var findings []Finding
	if m.Spec != 2 {
		findings = append(findings, Finding{Path: "kempt.spec", Msg: "a setup bundle uses spec 2"})
	}
	if m.Layer != "" {
		findings = append(findings, Finding{Path: "kempt.layer", Msg: "a setup bundle is not a layer"})
	}
	if len(m.Profiles) > 0 {
		findings = append(findings, Finding{Path: "profiles", Msg: "a setup bundle declares no profiles"})
	}
	for _, name := range sortedPackageNames(m) {
		if name != SetupPackage {
			findings = append(findings, Finding{Path: "packages." + name, Msg: "a setup bundle declares only [packages." + SetupPackage + "]"})
			continue
		}
		idx := map[string]int{}
		for _, step := range m.Packages[name].Steps {
			kind := step.Kind()
			path := fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx[kind])
			idx[kind]++
			switch s := step.(type) {
			case DownloadStep, GithubReleaseStep, GitCloneStep:
				findings = append(findings, Finding{Path: path, Msg: "a setup bundle may not use " + kind})
			case ManagedFileStep:
				if _, ok := files[s.From]; !ok {
					findings = append(findings, Finding{Path: path, Msg: fmt.Sprintf("from %q is not a file in the setup bundle", s.From)})
				}
			}
		}
	}
	return findings
}

// BindSetup validates a setup bundle's fragment (see ValidateSetup) and, when
// it is valid, gives each managed-file step its bytes from files and the
// release (tool, version) that supplied them. The resolver calls it after
// setupbundle.Read, Parse and Validate; with findings it binds nothing.
func BindSetup(m *Manifest, tool, version string, files map[string][]byte) []Finding {
	if findings := ValidateSetup(m, files); len(findings) > 0 {
		return findings
	}
	for _, pkg := range m.Packages {
		for i, step := range pkg.Steps {
			if mf, ok := step.(ManagedFileStep); ok {
				mf.Source = &SetupSource{Tool: tool, Version: version, Data: files[mf.From]}
				pkg.Steps[i] = mf
			}
		}
	}
	return nil
}
