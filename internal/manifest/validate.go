package manifest

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Validate performs semantic checks over a parsed Manifest, returning findings
// in a deterministic order. Rules run in the documented order (1-6).
func Validate(m *Manifest) []Finding {
	var findings []Finding
	findings = append(findings, validateSpec(m)...)
	findings = append(findings, validateNeeds(m)...)
	findings = append(findings, validateCycles(m)...)
	findings = append(findings, validateOnly(m)...)
	findings = append(findings, validateProfiles(m)...)
	findings = append(findings, validateStepFields(m)...)
	findings = append(findings, validateSpec2Syntax(m)...)
	findings = append(findings, validateInstallSettingsPackages(m)...)
	findings = append(findings, validateLayer(m)...)
	return findings
}

func sortedPackageNames(m *Manifest) []string {
	names := make([]string, 0, len(m.Packages))
	for name := range m.Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Rule 1: spec must be 1 or 2. Spec 2 adds release setup (DownloadStep.Setup
// and managed-file); validateSpec2Syntax rejects them in a spec 1 file.
func validateSpec(m *Manifest) []Finding {
	if m.Spec != 1 && m.Spec != 2 {
		return []Finding{{
			Path: "kempt.spec",
			Msg:  fmt.Sprintf("unsupported spec version %d", m.Spec),
		}}
	}
	return nil
}

// Rule 2: every needs entry names an existing package. A layer's needs may name
// the base's or an earlier layer's packages, which only composition can see,
// so a layer file is checked there instead.
func validateNeeds(m *Manifest) []Finding {
	if m.Layer != "" {
		return nil
	}
	var findings []Finding
	for _, name := range sortedPackageNames(m) {
		pkg := m.Packages[name]
		for _, need := range pkg.Needs {
			if _, ok := m.Packages[need]; !ok {
				findings = append(findings, Finding{
					Path: fmt.Sprintf("packages.%s.needs", name),
					Msg:  fmt.Sprintf("unknown package %q", need),
				})
			}
		}
	}
	return findings
}

// Rule 3: the needs graph is acyclic. Iterative DFS with three-color marking.
func validateCycles(m *Manifest) []Finding {
	const (
		white = 0 // unvisited
		gray  = 1 // on stack
		black = 2 // done
	)
	color := map[string]int{}
	var findings []Finding
	reported := map[string]bool{}

	for _, root := range sortedPackageNames(m) {
		if color[root] != white {
			continue
		}
		// path is the current DFS stack of package names.
		type frame struct {
			name string
			kids []string
			idx  int
		}
		var stack []frame
		pushFrame := func(name string) {
			color[name] = gray
			pkg := m.Packages[name]
			// Only follow needs that reference existing packages, in
			// sorted order for determinism.
			kids := []string{}
			for _, need := range pkg.Needs {
				if _, ok := m.Packages[need]; ok {
					kids = append(kids, need)
				}
			}
			sort.Strings(kids)
			stack = append(stack, frame{name: name, kids: kids})
		}
		pushFrame(root)
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.idx >= len(top.kids) {
				color[top.name] = black
				stack = stack[:len(stack)-1]
				continue
			}
			child := top.kids[top.idx]
			top.idx++
			switch color[child] {
			case white:
				pushFrame(child)
			case gray:
				// Back-edge: build cycle path from child back to top.
				var cycle []string
				start := -1
				for i, fr := range stack {
					if fr.name == child {
						start = i
						break
					}
				}
				if start >= 0 {
					for i := start; i < len(stack); i++ {
						cycle = append(cycle, stack[i].name)
					}
					cycle = append(cycle, child)
				}
				key := strings.Join(cycle, " -> ")
				if !reported[key] {
					reported[key] = true
					findings = append(findings, Finding{
						Path: fmt.Sprintf("packages.%s", cycle[0]),
						Msg:  fmt.Sprintf("dependency cycle: %s", key),
					})
				}
			}
		}
	}
	return findings
}

var validOS = map[string]bool{"darwin": true, "linux": true, "windows": true}
var validArch = map[string]bool{"arm64": true, "amd64": true}

func onlyFindings(path string, only *Only) []Finding {
	if only == nil {
		return nil
	}
	var findings []Finding
	if only.OS != "" && !validOS[only.OS] {
		findings = append(findings, Finding{
			Path: path + ".os",
			Msg:  fmt.Sprintf("unknown os %q", only.OS),
		})
	}
	if only.Arch != "" && !validArch[only.Arch] {
		findings = append(findings, Finding{
			Path: path + ".arch",
			Msg:  fmt.Sprintf("unknown arch %q", only.Arch),
		})
	}
	return findings
}

// stepOnly extracts the Only pointer from a step via type switch.
func stepOnly(s Step) *Only {
	switch v := s.(type) {
	case InstallStep:
		return v.Only
	case GithubReleaseStep:
		return v.Only
	case DownloadStep:
		return v.Only
	case GitCloneStep:
		return v.Only
	case ServiceStep:
		return v.Only
	case SymlinkStep:
		return v.Only
	case ManagedFileStep:
		return v.Only
	case JSONMergeStep:
		return v.Only
	case TomlMergeStep:
		return v.Only
	case LineInFileStep:
		return v.Only
	case VerifyStep:
		return v.Only
	}
	return nil
}

// Rule 4: only.os and only.arch use known values (package- and step-level).
func validateOnly(m *Manifest) []Finding {
	var findings []Finding
	for _, name := range sortedPackageNames(m) {
		pkg := m.Packages[name]
		findings = append(findings, onlyFindings(fmt.Sprintf("packages.%s.only", name), pkg.Only)...)
		kindIdx := map[string]int{}
		for _, step := range pkg.Steps {
			kind := step.Kind()
			idx := kindIdx[kind]
			path := fmt.Sprintf("packages.%s.%s[%d].only", name, kind, idx)
			findings = append(findings, onlyFindings(path, stepOnly(step))...)
			kindIdx[kind]++
		}
	}
	return findings
}

// Rule 5: every profile package exists.
func validateProfiles(m *Manifest) []Finding {
	var findings []Finding
	names := make([]string, 0, len(m.Profiles))
	for name := range m.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pr := m.Profiles[name]
		for _, p := range pr.Packages {
			if _, ok := m.Packages[p]; !ok {
				findings = append(findings, Finding{
					Path: fmt.Sprintf("profiles.%s", name),
					Msg:  fmt.Sprintf("unknown package %q", p),
				})
			}
		}
	}
	return findings
}

// missingFields returns the names of required fields absent from a step.
func missingFields(s Step) []string {
	var missing []string
	req := func(cond bool, field string) {
		if !cond {
			missing = append(missing, field)
		}
	}
	switch v := s.(type) {
	case SymlinkStep:
		req(v.From != "", "from")
		req(v.To != "", "to")
	case ManagedFileStep:
		req(v.From != "", "from")
		req(v.To != "", "to")
		req(v.Mode != "", "mode")
	case GithubReleaseStep:
		req(v.Repo != "", "repo")
		req(v.Asset != "", "asset")
		req(v.Bin != "", "bin")
	case DownloadStep:
		req(v.Site != "", "site")
		req(v.Tool != "", "tool")
		req(v.Bin != "", "bin")
	case GitCloneStep:
		req(v.Repo != "", "repo")
		req(v.To != "", "to")
	case ServiceStep:
		req(v.Label != "", "label")
		req(len(v.Program) > 0, "program")
	case JSONMergeStep:
		req(v.File != "", "file")
		req(len(v.Merge) > 0 || len(v.Remove) > 0 || len(v.RemoveHooks) > 0, "merge")
	case TomlMergeStep:
		req(v.File != "", "file")
		req(len(v.Merge) > 0, "merge")
	case LineInFileStep:
		req(v.File != "", "file")
		req(v.Line != "", "line")
	case InstallStep:
		brewHasContent := v.Brew != nil && (len(v.Brew.Formulas) > 0 || len(v.Brew.Casks) > 0 || len(v.Brew.Taps) > 0)
		hasBackend := brewHasContent || len(v.Winget) > 0 || len(v.Apt) > 0 ||
			len(v.Npm) > 0 || len(v.Pi) > 0
		req(hasBackend, "install")
	case VerifyStep:
		hasCheck := v.CommandExists != "" || len(v.CommandExistsAny) > 0 || v.HTTPOk != "" || v.SymlinkTarget != nil || v.VersionCurrent != nil
		req(hasCheck, "verify")
	}
	return missing
}

// isPiSettingsFile reports whether a json-merge file targets the pi agent
// settings.json.
func isPiSettingsFile(file string) bool {
	// Anchor on the `/.pi/` segment (dot + separator) so an unrelated path that
	// merely embeds the substring (e.g. `.../happi/agent/settings.json`) does not
	// match. Covers the global `~/.pi/agent/settings.json` and a project-local
	// `.pi/agent/settings.json`.
	return file == ".pi/agent/settings.json" || strings.HasSuffix(file, "/.pi/agent/settings.json")
}

// Rule L1: for a package that has both an install step with a non-empty Pi
// list and a json-merge step targeting the pi settings with a packages array,
// the two must be set-equal string-for-string.
func validateInstallSettingsPackages(m *Manifest) []Finding {
	var findings []Finding
	for _, name := range sortedPackageNames(m) {
		pkg := m.Packages[name]
		var installPi []string
		var mergePkgs []string
		hasInstall := false
		hasMerge := false
		for _, step := range pkg.Steps {
			switch v := step.(type) {
			case InstallStep:
				if len(v.Pi) > 0 {
					hasInstall = true
					installPi = append(installPi, v.Pi...)
				}
			case JSONMergeStep:
				if !isPiSettingsFile(v.File) {
					continue
				}
				raw, ok := v.Merge["packages"].([]any)
				if !ok {
					continue
				}
				hasMerge = true
				for _, e := range raw {
					if s, ok := e.(string); ok {
						mergePkgs = append(mergePkgs, s)
					}
				}
			}
		}
		if !hasInstall || !hasMerge {
			continue
		}
		if diff := firstSetDiff(installPi, mergePkgs); diff != "" {
			findings = append(findings, Finding{
				Path: "packages." + name,
				Msg:  fmt.Sprintf("install pi list and settings-merge packages differ string-for-string: %s", diff),
			})
		}
	}
	return findings
}

// firstSetDiff returns a description of the first differing entry between two
// string sets, or "" when the sets are equal (order-insensitive).
func firstSetDiff(a, b []string) string {
	setA := map[string]bool{}
	for _, s := range a {
		setA[s] = true
	}
	setB := map[string]bool{}
	for _, s := range b {
		setB[s] = true
	}
	for _, s := range a {
		if !setB[s] {
			return fmt.Sprintf("%q in install but not in packages", s)
		}
	}
	for _, s := range b {
		if !setA[s] {
			return fmt.Sprintf("%q in packages but not in install", s)
		}
	}
	return ""
}

// Rule 6: per-step required fields.
func validateStepFields(m *Manifest) []Finding {
	var findings []Finding
	for _, name := range sortedPackageNames(m) {
		pkg := m.Packages[name]
		kindIdx := map[string]int{}
		for _, step := range pkg.Steps {
			kind := step.Kind()
			idx := kindIdx[kind]
			for _, field := range missingFields(step) {
				findings = append(findings, Finding{
					Path: fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx),
					Msg:  fmt.Sprintf("missing required field %q", field),
				})
			}
			if js, ok := step.(JSONMergeStep); ok && js.Arrays != "" && js.Arrays != "append" && js.Arrays != "replace" {
				findings = append(findings, Finding{
					Path: fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx),
					Msg:  `arrays must be "append" or "replace"`,
				})
			}
			if js, ok := step.(JSONMergeStep); ok {
				for _, r := range js.RemoveHooks {
					if r.Event == "" || len(r.Commands) == 0 || slices.Contains(r.Commands, "") {
						findings = append(findings, Finding{Path: fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx), Msg: "remove-hooks requires a nonempty event and commands"})
					}
				}
				for _, r := range js.Remove {
					if slices.Contains(strings.Split(r, "."), "") {
						findings = append(findings, Finding{
							Path: fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx),
							Msg:  fmt.Sprintf("remove path %q has an empty key", r),
						})
					}
				}
			}
			if mf, ok := step.(ManagedFileStep); ok {
				path := fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx)
				if len(mf.AdoptMarkers) > 0 && (!strings.HasSuffix(mf.To, "/SKILL.md") && mf.To != "SKILL.md") {
					findings = append(findings, Finding{Path: path, Msg: "adopt-markers requires a managed SKILL.md"})
				}
				if mf.From != "" && !BundlePath(mf.From) {
					findings = append(findings, Finding{Path: path, Msg: fmt.Sprintf("from %q must be a path inside the bundle's files/", mf.From)})
				}
				if mf.Mode != "" && mf.Mode != "0644" && mf.Mode != "0755" {
					findings = append(findings, Finding{Path: path, Msg: `mode must be "0644" or "0755"`})
				}
			}
			if sv, ok := step.(ServiceStep); ok && sv.StartInterval != nil {
				if sv.KeepAlive == nil || *sv.KeepAlive {
					findings = append(findings, Finding{
						Path: fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx),
						Msg:  "start-interval requires keep-alive = false",
					})
				} else if *sv.StartInterval <= 0 {
					findings = append(findings, Finding{
						Path: fmt.Sprintf("packages.%s.%s[%d]", name, kind, idx),
						Msg:  "start-interval must be > 0",
					})
				}
			}
			kindIdx[kind]++
		}
	}
	return findings
}
