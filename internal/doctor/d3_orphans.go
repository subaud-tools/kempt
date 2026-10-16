package doctor

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/inventory"
	"github.com/subaud-tools/kempt/internal/jsonutil"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// CheckOrphans reports installed software (pi always, global npm only when
// cfg.CheckNpmOrphans) whose identity is not declared by any selected install
// step's Pi/Npm list. pi orphans surface as Warn, npm orphans as Info.
// Identities matching an Ignore entry — exact, npm:-prefixed, or a glob:
// pattern — are suppressed. Output is deterministic (sorted by spec/name).
func CheckOrphans(ctx *machine.Context, pkgs []*manifest.Package, cfg manifest.DoctorConfig) []Finding {
	declaredPi, declaredNpm := map[string]bool{}, map[string]bool{}
	for _, pkg := range pkgs {
		for _, step := range pkg.Steps {
			is, ok := step.(manifest.InstallStep)
			if !ok {
				continue
			}
			for _, s := range is.Pi {
				declaredPi[jsonutil.SpecIdentity(s)] = true
			}
			for _, s := range is.Npm {
				declaredNpm[jsonutil.SpecIdentity("npm:"+s)] = true
			}
		}
	}
	ignored := func(id string) bool {
		for _, ig := range cfg.Ignore {
			if ig == id || ig == "npm:"+id {
				return true
			}
			if strings.HasPrefix(ig, "glob:") {
				if ok, _ := path.Match(ig[len("glob:"):], id); ok {
					return true
				}
			}
		}
		return false
	}

	var out []Finding
	if piInv, err := inventory.Pi(ctx); err == nil {
		specs := make([]string, 0, len(piInv))
		for spec := range piInv {
			specs = append(specs, spec)
		}
		sort.Strings(specs)
		for _, spec := range specs {
			id := jsonutil.SpecIdentity(spec)
			if declaredPi[id] || ignored(id) {
				continue
			}
			out = append(out, Finding{
				Check: "orphan", Package: "pi", Severity: Warn,
				Detail:      fmt.Sprintf("%s is installed but not declared in the manifest", spec),
				Remediation: "adopt it into a package to keep it, or remove it (`pi remove " + spec + "`)",
			})
		}
	}
	if cfg.CheckNpmOrphans {
		if npmInv, err := inventory.Npm(ctx); err == nil {
			names := make([]string, 0, len(npmInv))
			for name := range npmInv {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				id := jsonutil.SpecIdentity("npm:" + name)
				if declaredNpm[id] || ignored(id) {
					continue
				}
				out = append(out, Finding{
					Check: "orphan", Package: "npm", Severity: Info,
					Detail:      fmt.Sprintf("global npm %q is installed but not declared", name),
					Remediation: "add it to an install.npm list, add to [doctor].ignore, or uninstall it",
				})
			}
		}
	}
	return out
}
