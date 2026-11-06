package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/setup"
)

func init() {
	Register(Command{
		Name:     "apply",
		Summary:  "apply the plan to converge the machine",
		Synopsis: "apply [flags]",
		Help: "Converges this machine to the manifest: installs/updates packages, writes\n" +
			"symlinks and merged config, and runs verifications. Prompts before applying\n" +
			"unless -yes. With -manifest - (stdin), -yes is required.",
		NewFlags: func() *flag.FlagSet { fs, _ := newApplyFlags(); return fs },
		Run:      runApply,
	})
}

// applyFlags holds the parsed flag values for runApply.
type applyFlags struct {
	manifest *string
	profile  *string
	packages *string
	yes      *bool
	verbose  *bool
}

// newApplyFlags constructs apply's FlagSet and the values struct it populates
// on Parse. Side-effect-free: safe to call for -h rendering without running
// runApply's body.
func newApplyFlags() (*flag.FlagSet, *applyFlags) {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	v := &applyFlags{
		manifest: fs.String("manifest", "", "path to manifest"),
		profile:  fs.String("profile", "", "profile to select"),
		packages: fs.String("packages", "", "comma-separated package names"),
		yes:      YesFlag(fs, "skip the confirmation prompt"),
		verbose:  verboseFlag(fs),
	}
	return fs, v
}

// stdin is the reader used for confirmation prompts (apply, upgrade). It is a
// package var so tests can inject a scripted response.
var stdin io.Reader = os.Stdin

// confirm reads a single line from stdin and reports whether it was an
// affirmative response ("y" or "yes", case-insensitive). Shared by apply's
// and upgrade's confirmation prompts.
func confirm() bool {
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func runApply(args []string, out, errw io.Writer) error {
	fs, v := newApplyFlags()
	if err := ParseFlags(fs, args, out); err != nil {
		return err
	}

	st, existed, err := loadState()
	if err != nil {
		return err
	}
	manifestPath := resolveManifest(*v.manifest, st, existed)
	profile, packages := resolveSelection(*v.profile, splitPackages(*v.packages), st, existed)

	// A stdin manifest consumes the same stream the confirmation prompt reads,
	// so it cannot be applied interactively — require -yes.
	if manifestPath == "-" && !*v.yes {
		return UsageError{Msg: "reading the manifest from stdin (-manifest -) requires -yes"}
	}

	src, repoDir, name, err := loadManifestSource(manifestPath, stdin)
	if err != nil {
		return UsageError{Msg: err.Error()}
	}
	m, findings := manifest.Parse(src)
	if m != nil {
		findings = append(findings, manifest.Validate(m)...)
	}
	if len(findings) > 0 {
		for _, f := range findings {
			_, _ = fmt.Fprintf(errw, "%s: %s: %s\n", name, f.Path, f.Msg)
		}
		return fmt.Errorf("manifest has findings; run kempt lint")
	}

	ctx, err := newContext(repoDir)
	if err != nil {
		return err
	}
	selected, err := selectWithLayers(ctx, m, profile, packages, st, savedSelection(existed, *v.manifest, *v.profile, *v.packages), out, errw)
	if err != nil {
		return UsageError{Msg: err.Error()}
	}
	selected, err = setup.Prepare(ctx, selected, true)
	if err != nil {
		return err
	}
	plan, err := engine.BuildPlan(ctx, selected)
	if err != nil {
		return err
	}

	// Show the same plan `kempt plan` would.
	engine.Render(plan, out, *v.verbose)
	if err := blockedReleaseSetup(plan, false); err != nil {
		return err
	}

	changes := countChanges(plan)
	if changes == 0 {
		if err := blockedReleaseSetup(plan, true); err != nil {
			return err
		}
		if err := unconverged(plan); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "nothing to do")
		return nil
	}

	if !*v.yes {
		_, _ = fmt.Fprintf(out, "apply %d changes? [y/N] ", changes)
		if !confirm() {
			_, _ = fmt.Fprintln(out, "aborted")
			return fmt.Errorf("aborted")
		}
	}

	applied, failed := executeAndVerify(ctx, plan, out)
	if err := blockedReleaseSetup(plan, true); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "%d applied, %d failed\n", applied, failed)
	if err := confirmConverged(ctx, selected); err != nil {
		return err
	}

	if failed > 0 {
		return fmt.Errorf("%d step(s) failed", failed)
	}
	return nil
}

// executeAndVerify runs the plan then re-inspects every step Execute claims it
// applied. If any is not now OpNoop, the machine did not actually converge and
// the step is counted as failed.
//
// kempt plans every step before any runs, so a step whose tool an earlier step
// installs in the same run is planned blocked (pi's extensions before pi's npm
// install, dotfiles#125; a verify of a tool installed this run). After Execute,
// each blocked step in a package where something applied, in a package that
// needs one where something applied, or in a release setup package, is
// re-inspected against the machine as it now is: a step now ready runs (and is
// confirmed like any other), one now in order is updated, and one still
// blocked keeps its blocked delta. Passes repeat until one runs nothing, since
// a step that runs can unblock another. Returns (applied, failed).
func executeAndVerify(ctx *machine.Context, plan *engine.Plan, out io.Writer) (int, int) {
	failed := engine.Execute(ctx, plan, out)
	applied := 0
	touched := map[string]bool{}
	for i := range plan.Packages {
		pp := &plan.Packages[i]
		pctx := engine.ContextFor(ctx, pp.Root)
		for j := range pp.Steps {
			if sr := &pp.Steps[j]; sr.Applied {
				applied++
				touched[pp.Name] = true
				if !confirmApplied(pctx, pp.Name, sr, out) {
					failed++
				}
			}
		}
	}
	for ran := true; ran; {
		ran = false
		incomplete := map[string]bool{}
		for i := range plan.Packages {
			pp := &plan.Packages[i]
			if pp.Skipped {
				continue
			}
			ready, woken := true, touched[pp.Name] || pp.Resolved != nil
			for _, need := range pp.Needs {
				ready = ready && !incomplete[need]
				woken = woken || touched[need]
			}
			for _, sr := range pp.Steps {
				ready = ready && sr.Err == nil
			}
			pctx := engine.ContextFor(ctx, pp.Root)
			for j := range pp.Steps {
				sr := &pp.Steps[j]
				if !woken || sr.Delta.Op != engine.OpBlocked {
					continue
				}
				h, ok := engine.HandlerFor(sr.Step.Kind())
				if !ok {
					continue
				}
				delta, err := h.Inspect(pctx, sr.Step)
				if err != nil || delta.Op == engine.OpBlocked || (delta.Op == engine.OpChange && !ready) {
					continue
				}
				sr.Delta = delta
				if delta.Op != engine.OpChange {
					continue
				}
				ran = true
				if err := h.Apply(pctx, sr.Step); err != nil {
					sr.Err = err
					_, _ = fmt.Fprintf(out, "! %s: %s: %v\n", pp.Name, delta.Detail, err)
					failed++
					break
				}
				sr.Applied = true
				applied++
				touched[pp.Name] = true
				_, _ = fmt.Fprintf(out, "applied: %s\n", delta.Detail)
				if !confirmApplied(pctx, pp.Name, sr, out) {
					failed++
				}
			}
			if !ready {
				incomplete[pp.Name] = true
			}
			for _, sr := range pp.Steps {
				if sr.Err != nil || (sr.Delta.Op == engine.OpBlocked && sr.Step.Kind() != "verify") {
					incomplete[pp.Name] = true
				}
			}
		}
	}
	return applied, failed
}

// confirmApplied re-inspects a step Apply reported done and reports whether
// the machine now agrees, printing why when it does not. A step that does not
// agree carries the reason in its Err, so its package counts as incomplete and
// the packages that need it stay gated.
func confirmApplied(ctx *machine.Context, pkg string, sr *engine.StepResult, out io.Writer) bool {
	h, ok := engine.HandlerFor(sr.Step.Kind())
	if !ok {
		return true
	}
	delta, err := h.Inspect(ctx, sr.Step)
	switch {
	case err != nil:
		sr.Err = fmt.Errorf("re-inspect failed: %w", err)
	case delta.Op != engine.OpNoop:
		sr.Err = fmt.Errorf("not converged after apply: %s", delta.Detail)
	default:
		return true
	}
	_, _ = fmt.Fprintf(out, "! %s: %v\n", pkg, sr.Err)
	return false
}

// confirmConverged plans the already-prepared selection again against the
// machine as it now is, with a fresh inventory cache and no online resolve,
// and fails naming every step that still has a change or is blocked: an
// apply only succeeds when a following kempt plan has nothing left.
func confirmConverged(ctx *machine.Context, selected []*manifest.Package) error {
	fresh := *ctx
	fresh.Cache = map[string]string{}
	plan, err := engine.BuildPlan(&fresh, selected)
	if err != nil {
		return fmt.Errorf("plan after apply: %w", err)
	}
	return unconverged(plan)
}

// unconverged returns an error naming each step of plan that has a change or
// is blocked, or nil when there is none.
func unconverged(plan *engine.Plan) error {
	var left []string
	for _, pp := range plan.Packages {
		if pp.Skipped {
			continue
		}
		for _, sr := range pp.Steps {
			if sr.Delta.Op == engine.OpChange || sr.Delta.Op == engine.OpBlocked {
				left = append(left, pp.Name+": "+sr.Delta.Detail)
			}
		}
	}
	if len(left) == 0 {
		return nil
	}
	return fmt.Errorf("not converged, %d step(s) left:\n  %s", len(left), strings.Join(left, "\n  "))
}

func countChanges(p *engine.Plan) int {
	n := 0
	for _, pp := range p.Packages {
		if pp.Skipped {
			continue
		}
		for _, sr := range pp.Steps {
			if sr.Delta.Op == engine.OpChange {
				n++
			}
		}
	}
	return n
}
