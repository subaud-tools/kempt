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
	plan, err := engine.BuildPlan(ctx, selected)
	if err != nil {
		return err
	}

	// Show the same plan `kempt plan` would.
	engine.Render(plan, out, *v.verbose)

	changes := countChanges(plan)
	if changes == 0 {
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

	blocked := countBlocked(plan)
	_, _ = fmt.Fprintf(out, "%d applied, %d failed\n", applied, failed)
	if blocked > 0 {
		_, _ = fmt.Fprintf(out, "%d blocked (unresolved)\n", blocked)
	}

	if failed > 0 {
		return fmt.Errorf("%d step(s) failed", failed)
	}
	return nil
}

// executeAndVerify runs the plan then re-inspects every step Execute claims it
// applied. If any is not now OpNoop, the machine did not actually converge and
// the step is counted as failed. In a package that applied a change it also
// re-inspects that package's pre-apply-blocked verify steps, so a verify of a
// tool installed earlier in the same run is judged against post-apply state
// rather than reported as spuriously blocked. Returns (applied, failed).
func executeAndVerify(ctx *machine.Context, plan *engine.Plan, out io.Writer) (int, int) {
	failed := engine.Execute(ctx, plan, out)
	applied := 0
	for i := range plan.Packages {
		pp := &plan.Packages[i]
		pctx := engine.ContextFor(ctx, pp.Root)
		pkgChanged := false
		for j := range pp.Steps {
			if pp.Steps[j].Applied {
				pkgChanged = true
				break
			}
		}
		for j := range pp.Steps {
			sr := &pp.Steps[j]
			h, ok := engine.HandlerFor(sr.Step.Kind())
			if sr.Applied {
				applied++
				if !ok {
					continue
				}
				delta, err := h.Inspect(pctx, sr.Step)
				if err != nil {
					_, _ = fmt.Fprintf(out, "! %s: re-inspect failed: %v\n", pp.Name, err)
					failed++
					continue
				}
				if delta.Op != engine.OpNoop {
					_, _ = fmt.Fprintf(out, "! %s: not converged after apply: %s\n", pp.Name, delta.Detail)
					failed++
				}
				continue
			}
			// A verify step is inspected up front, against pre-apply state, and is
			// never itself an OpChange the engine applies. When a sibling step in
			// the same package installed the thing being verified this run, that
			// stale OpBlocked is a false positive — re-inspect it against post-apply
			// state so the run's blocked count reflects reality.
			if ok && pkgChanged && sr.Delta.Op == engine.OpBlocked && sr.Step.Kind() == "verify" {
				if delta, err := h.Inspect(pctx, sr.Step); err == nil {
					sr.Delta = delta
				}
			}
		}
	}
	return applied, failed
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

func countBlocked(p *engine.Plan) int {
	n := 0
	for _, pp := range p.Packages {
		if pp.Skipped {
			continue
		}
		for _, sr := range pp.Steps {
			if sr.Delta.Op == engine.OpBlocked {
				n++
			}
		}
	}
	return n
}
