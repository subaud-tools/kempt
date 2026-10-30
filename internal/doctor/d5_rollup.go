package doctor

import (
	"fmt"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// CheckRollup builds the plan for the selected packages and rolls its outcome
// up into findings: one info when apply has pending changes, one error per
// blocked plan step or pending managed file, and one error per verify step
// that fails or is blocked. Managed files retain their package attribution.
func CheckRollup(ctx *machine.Context, pkgs []*manifest.Package) []Finding {
	var out []Finding
	plan, err := engine.BuildPlan(ctx, pkgs)
	if err != nil {
		return []Finding{{Check: "rollup", Severity: Error, Detail: "plan failed: " + err.Error(), Remediation: "run `kempt plan` to see the error"}}
	}
	pending := 0
	for _, pp := range plan.Packages {
		for _, sr := range pp.Steps {
			switch sr.Delta.Op {
			case engine.OpChange:
				pending++
				if sr.Step.Kind() == "managed-file" {
					out = append(out, Finding{
						Check: "rollup", Package: pp.Name, Severity: Error,
						Detail:      sr.Delta.Detail,
						Remediation: "run `kempt apply` to converge",
					})
				}
			case engine.OpBlocked:
				out = append(out, Finding{
					Check: "rollup", Package: pp.Name, Severity: Error,
					Detail:      "blocked: " + sr.Delta.Detail,
					Remediation: "resolve the blocker; see `kempt plan`",
				})
			}
		}
	}
	if pending > 0 {
		out = append(out, Finding{
			Check: "rollup", Severity: Info,
			Detail:      fmt.Sprintf("%d pending change(s) — machine is behind the manifest", pending),
			Remediation: "run `kempt apply` to converge",
		})
	}

	if h, ok := engine.HandlerFor("verify"); ok {
		for _, pkg := range pkgs {
			if _, skip := engine.OnlySkip(ctx, pkg.Only); skip {
				continue
			}
			for _, step := range pkg.Steps {
				if step.Kind() != "verify" {
					continue
				}
				if _, skip := engine.OnlySkip(ctx, engine.StepOnly(step)); skip {
					continue
				}
				d, err := h.Inspect(ctx, step)
				if err != nil || d.Op == engine.OpBlocked {
					detail := d.Detail
					if err != nil {
						detail = err.Error()
					}
					out = append(out, Finding{
						Check: "rollup", Package: pkg.Name, Severity: Error,
						Detail:      "verify failed: " + detail,
						Remediation: "run `kempt verify` and fix the failing check",
					})
				}
			}
		}
	}
	return out
}
