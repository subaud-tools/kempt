package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/subaud-tools/tools-common/hookconfig"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/jsonutil"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func CheckDupIdentity(ctx *machine.Context, pkgs []*manifest.Package) []Finding {
	out := checkDeclaredRemovals(ctx, pkgs)
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
				continue
			}
			var live map[string]any
			if json.Unmarshal(b, &live) != nil {
				continue
			}
			// Normalize the manifest merge (expand ${HOME}, round-trip types)
			// so declared keys align with the live file's expanded paths.
			desiredMerge, _ := jsonutil.ExpandHome(jsonutil.ToAny(jm.Merge), ctx.Home).(map[string]any)
			for key := range desiredMerge {
				current, ok := live[key].([]any)
				if !ok {
					continue
				}
				byID := map[string][]string{}
				for _, e := range current {
					s, ok := e.(string)
					if !ok {
						continue
					}
					id := jsonutil.SpecIdentity(s)
					byID[id] = append(byID[id], s)
				}
				var ids []string
				for id, variants := range byID {
					if len(variants) > 1 {
						ids = append(ids, id)
					}
				}
				sort.Strings(ids)
				for _, id := range ids {
					out = append(out, Finding{
						Check:       "dup-identity",
						Package:     pkg.Name,
						Severity:    Warn,
						Detail:      fmt.Sprintf("%s .%s lists %q %d times: %v", file, key, id, len(byID[id]), byID[id]),
						Remediation: "run `kempt apply` (an arrays=replace merge collapses these) or dedupe the file",
					})
				}
			}
		}
	}
	return out
}

// checkDeclaredRemovals uses the composed declarations, so a removed/re-added
// canonical registration is healthy and superseded aliases cannot reappear.
func checkDeclaredRemovals(ctx *machine.Context, pkgs []*manifest.Package) []Finding {
	plan, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		return nil
	} // rollup reports planning errors
	var out []Finding
	for _, pkg := range plan.Packages {
		pctx := engine.ContextFor(ctx, pkg.Root)
		for _, sr := range pkg.Steps {
			st, ok := sr.Step.(manifest.JSONMergeStep)
			if !ok || sr.Delta.Op != engine.OpChange {
				continue
			}
			file := pctx.Expand(st.File)
			b, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			var live map[string]any
			if json.Unmarshal(b, &live) != nil {
				continue
			}
			desired, _ := jsonutil.ExpandHome(jsonutil.ToAny(st.Merge), ctx.Home).(map[string]any)
			add := func(detail string) {
				out = append(out, Finding{Check: "dup-identity", Package: pkg.Name, Severity: Warn, Detail: file + " " + detail, Remediation: "run `kempt apply` to reconcile declared registrations"})
			}
			for _, rule := range jsonutil.HookRemovals(st.RemoveHooks, ctx.Home) {
				count, want := hookconfig.Count(live, rule), hookconfig.Count(desired, rule)
				if count > want && want > 0 {
					add(fmt.Sprintf("duplicate owned hook registrations for %s matcher %q: %d configured, %d desired", rule.Event, rule.Matcher, count, want))
					continue
				}
				for _, command := range rule.Commands {
					one := hookconfig.Removal{Event: rule.Event, Matcher: rule.Matcher, Commands: []string{command}}
					if hookconfig.Count(live, one) > 0 && hookconfig.Count(desired, one) == 0 {
						add(fmt.Sprintf("retired hook registration for %s matcher %q: %s", rule.Event, rule.Matcher, command))
					}
				}
			}
			for _, path := range st.Remove {
				keys := strings.Split(path, ".")
				if jsonutil.HasPath(live, keys) && !jsonutil.HasPath(desired, keys) {
					add("retired declared registration ." + path + " is still configured")
				}
			}
		}
	}
	return out
}
