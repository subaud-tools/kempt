package engine

import (
	"fmt"
	"io"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// BuildPlan inspects each selected package against the machine context and
// produces a Plan. A package whose only-clause excludes the context is marked
// Skipped. A step whose only-clause excludes the context yields OpSkip. Steps
// whose kind has no registered handler yield OpBlocked with a placeholder
// detail (handlers are added incrementally during phase 1b); this is not an
// error so plan stays usable mid-phase.
func BuildPlan(ctx *machine.Context, pkgs []*manifest.Package) (*Plan, error) {
	c, err := compose(ctx, pkgs)
	if err != nil {
		return nil, err
	}
	p := &Plan{}
	for pi, pkg := range c.pkgs {
		pp := PackagePlan{Needs: pkg.Needs, Resolved: pkg.Resolved, SetupParent: pkg.SetupParent, Name: pkg.Name, Notes: pkg.Notes, Layer: pkg.Layer, Root: pkg.Root, Overrides: c.overrides[pi]}
		pctx := ContextFor(ctx, pkg.Root)
		if reason, skip := skipReason(ctx, pkg.Only); skip {
			pp.Skipped = true
			pp.Detail = reason
			p.Packages = append(p.Packages, pp)
			continue
		}
		for si, step := range pkg.Steps {
			if d, ok := step.(manifest.DownloadStep); ok && d.Held != "" {
				pp.Overrides = append(pp.Overrides, d.Held)
			}
			if reason, skip := skipReason(ctx, stepOnly(step)); skip {
				pp.Steps = append(pp.Steps, StepResult{
					Step:  step,
					Delta: Delta{Op: OpSkip, Detail: fmt.Sprintf("%s (skipped: %s)", step.Kind(), reason)},
				})
				continue
			}
			h, ok := HandlerFor(step.Kind())
			if !ok {
				pp.Steps = append(pp.Steps, StepResult{
					Step:  step,
					Delta: Delta{Op: OpBlocked, Detail: "handler not implemented yet (phase 1b)"},
				})
				continue
			}
			delta, err := h.Inspect(pctx, step)
			if err != nil {
				return nil, fmt.Errorf("inspect %s in %s: %w", step.Kind(), pkg.Name, err)
			}
			if note := c.notes[pi][si]; note != "" {
				delta.Detail += " " + note
			}
			pp.Steps = append(pp.Steps, StepResult{Step: step, Delta: delta})
		}
		p.Packages = append(p.Packages, pp)
	}
	return p, nil
}

// ContextFor returns the context a package's steps run in: ctx itself for the
// base, or a copy whose RepoDir is the package's root, so relative paths
// resolve against the layer that declared them. The copy shares ctx's runner
// and inventory cache.
func ContextFor(ctx *machine.Context, root string) *machine.Context {
	if root == "" || root == ctx.RepoDir {
		return ctx
	}
	c := *ctx
	c.RepoDir = root
	return &c
}

// Execute applies every OpChange step in plan order. On success it marks the
// StepResult Applied and prints "applied: <detail>". On a step error it records
// the error in the StepResult, prints "! <pkg>: <detail>: <err>", skips the rest
// of that package, and continues with the next package. It returns the number of
// failed steps.
func Execute(ctx *machine.Context, p *Plan, out io.Writer) (failed int) {
	incomplete := map[string]bool{}
	for i := range p.Packages {
		pp := &p.Packages[i]
		if pp.Skipped {
			continue
		}
		blockedBy := ""
		for _, need := range pp.Needs {
			if incomplete[need] {
				blockedBy = need
				break
			}
		}
		if blockedBy != "" {
			incomplete[pp.Name] = true
			for j := range pp.Steps {
				if pp.Steps[j].Delta.Op == OpChange {
					pp.Steps[j].Delta = Delta{Op: OpBlocked, Detail: "dependency " + blockedBy + " is incomplete"}
				}
			}
			_, _ = fmt.Fprintf(out, "! %s: blocked by incomplete dependency %s\n", pp.Name, blockedBy)
			continue
		}
		for _, sr := range pp.Steps {
			if sr.Delta.Op == OpBlocked && sr.Step.Kind() != "verify" {
				incomplete[pp.Name] = true
			}
		}
		pctx := ContextFor(ctx, pp.Root)
		for j := range pp.Steps {
			sr := &pp.Steps[j]
			if sr.Delta.Op != OpChange {
				continue
			}
			h, ok := HandlerFor(sr.Step.Kind())
			if !ok {
				continue
			}
			if err := h.Apply(pctx, sr.Step); err != nil {
				sr.Err = err
				incomplete[pp.Name] = true
				_, _ = fmt.Fprintf(out, "! %s: %s: %v\n", pp.Name, sr.Delta.Detail, err)
				failed++
				break
			}
			sr.Applied = true
			_, _ = fmt.Fprintf(out, "applied: %s\n", sr.Delta.Detail)
		}
	}
	return failed
}

// FilterByClass returns a copy of p containing only OpChange steps whose
// Step.Class() == class. Packages left with zero steps are dropped, and skipped
// packages are omitted. The original plan and its steps are never mutated. It
// is used by refresh to apply files-class changes without touching software.
func FilterByClass(p *Plan, class manifest.Class) *Plan {
	out := &Plan{}
	held := map[string]bool{}
	for _, pp := range p.Packages {
		for _, sr := range pp.Steps {
			if (sr.Delta.Op == OpBlocked && sr.Step.Kind() != "verify") || (sr.Delta.Op == OpChange && sr.Step.Class() != class) {
				held[pp.Name] = true
			}
		}
		for _, need := range pp.Needs {
			if held[need] {
				held[pp.Name] = true
			}
		}
	}
	for i := range p.Packages {
		pp := &p.Packages[i]
		if pp.Skipped {
			continue
		}
		dependencyHeld := false
		for _, need := range pp.Needs {
			if held[need] {
				dependencyHeld = true
			}
		}
		if dependencyHeld {
			continue
		}
		var steps []StepResult
		for _, sr := range pp.Steps {
			if sr.Delta.Op != OpChange {
				continue
			}
			if sr.Step.Class() != class {
				continue
			}
			steps = append(steps, sr)
		}
		if len(steps) == 0 {
			continue
		}
		out.Packages = append(out.Packages, PackagePlan{
			Name:  pp.Name,
			Needs: pp.Needs, Resolved: pp.Resolved, SetupParent: pp.SetupParent,
			Skipped: pp.Skipped,
			Detail:  pp.Detail,
			Steps:   steps,
			Layer:   pp.Layer,
			Root:    pp.Root,
		})
	}
	return out
}

// OnlySkip reports whether an only-clause excludes the machine context.
// It returns a short human-readable reason ("os != windows") and true when
// the step/package should be skipped, or ("", false) when it should run.
// Callers other than BuildPlan (e.g. cli.verify) must use this function
// instead of duplicating the matching logic.
func OnlySkip(ctx *machine.Context, only *manifest.Only) (reason string, skip bool) {
	return skipReason(ctx, only)
}

// StepOnly extracts the only-clause from a step, returning nil when the step
// type has no only-clause or when the type is unknown.
func StepOnly(s manifest.Step) *manifest.Only {
	return stepOnly(s)
}

// skipReason reports whether an only-clause excludes the context, and if so a
// short reason naming the required value that did not match.
func skipReason(ctx *machine.Context, only *manifest.Only) (string, bool) {
	if only == nil {
		return "", false
	}
	if only.OS != "" && only.OS != ctx.OS {
		return "os != " + only.OS, true
	}
	if only.Arch != "" && only.Arch != ctx.Arch {
		return "arch != " + only.Arch, true
	}
	return "", false
}

// stepOnly extracts the only-clause from a step via type switch.
func stepOnly(s manifest.Step) *manifest.Only {
	switch v := s.(type) {
	case manifest.InstallStep:
		return v.Only
	case manifest.GithubReleaseStep:
		return v.Only
	case manifest.DownloadStep:
		return v.Only
	case manifest.TomlMergeStep:
		return v.Only
	case manifest.GitCloneStep:
		return v.Only
	case manifest.ServiceStep:
		return v.Only
	case manifest.SymlinkStep:
		return v.Only
	case manifest.ManagedFileStep:
		return v.Only
	case manifest.JSONMergeStep:
		return v.Only
	case manifest.LineInFileStep:
		return v.Only
	case manifest.VerifyStep:
		return v.Only
	}
	return nil
}
