package doctor

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/jsonutil"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func CheckExtraArray(ctx *machine.Context, pkgs []*manifest.Package) []Finding {
	var out []Finding
	for _, pkg := range pkgs {
		pctx := engine.ContextFor(ctx, pkg.Root)
		for _, step := range pkg.Steps {
			jm, ok := step.(manifest.JSONMergeStep)
			if !ok {
				continue
			}
			file := pctx.Expand(jm.File)
			b, err := os.ReadFile(file)
			if err != nil {
				continue // missing/unreadable is plan's concern
			}
			var live map[string]any
			if json.Unmarshal(b, &live) != nil {
				continue
			}
			// Normalize the manifest merge (expand ${HOME}, round-trip types)
			// so declared array entries match live absolute paths.
			desiredMerge, _ := jsonutil.ExpandHome(jsonutil.ToAny(jm.Merge), ctx.Home).(map[string]any)
			for key, dv := range desiredMerge {
				desired, ok := toArray(dv)
				if !ok {
					continue
				}
				current, ok := live[key].([]any)
				if !ok {
					continue
				}
				extras := jsonutil.ArrayExtras(desired, current)
				if len(extras) == 0 {
					continue
				}
				sev := Warn
				rem := "set arrays=\"replace\" on this json-merge and run `kempt apply`, or add them to the manifest"
				if jm.Arrays == "replace" {
					sev = Error
					rem = "run `kempt apply` to reconcile (arrays=replace should already remove these)"
				}
				out = append(out, Finding{
					Check:       "extra-array",
					Package:     pkg.Name,
					Severity:    sev,
					Detail:      fmt.Sprintf("%s .%s has %d undeclared entries: %v", file, key, len(extras), extras),
					Remediation: rem,
				})
			}
		}
	}
	return out
}

// toArray coerces a TOML/JSON-decoded value to []any if it is array-shaped.
func toArray(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}
