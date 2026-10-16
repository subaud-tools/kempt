package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() {
	Register(Command{
		Name:     "verify",
		Summary:  "run read-only verify checks",
		Synopsis: "verify [flags]",
		Help:     "Runs each package's verify checks (command-exists, symlink targets, ...) read-only.",
		NewFlags: func() *flag.FlagSet { fs, _ := newVerifyFlags(); return fs },
		Run:      runVerify,
	})
}

// verifyFlags holds the parsed flag values for runVerify.
type verifyFlags struct {
	manifest *string
	profile  *string
	packages *string
}

// newVerifyFlags constructs verify's FlagSet and the values struct it populates
// on Parse. Side-effect-free: safe to call for -h rendering without running
// runVerify's body.
func newVerifyFlags() (*flag.FlagSet, *verifyFlags) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	v := &verifyFlags{
		manifest: fs.String("manifest", "", "path to manifest"),
		profile:  fs.String("profile", "", "profile to select"),
		packages: fs.String("packages", "", "comma-separated package names"),
	}
	return fs, v
}

func runVerify(args []string, out, errw io.Writer) error {
	fs, v := newVerifyFlags()
	if err := ParseFlags(fs, args, out); err != nil {
		return err
	}

	st, existed, err := loadState()
	if err != nil {
		return err
	}
	manifestPath := resolveManifest(*v.manifest, st, existed)
	profile, packages := resolveSelection(*v.profile, splitPackages(*v.packages), st, existed)

	src, repoDir, name, err := loadManifestSource(manifestPath, os.Stdin)
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

	h, ok := engine.HandlerFor("verify")
	if !ok {
		return fmt.Errorf("verify handler not registered")
	}

	var passed, failed, total int
	for _, pkg := range selected {
		pctx := engine.ContextFor(ctx, pkg.Root)
		// Package-level only filtering: skip if the machine context doesn't match.
		if _, skip := engine.OnlySkip(ctx, pkg.Only); skip {
			continue
		}
		for _, step := range pkg.Steps {
			if step.Kind() != "verify" {
				continue
			}
			// Step-level only filtering.
			if _, skip := engine.OnlySkip(ctx, engine.StepOnly(step)); skip {
				continue
			}
			total++
			delta, err := h.Inspect(pctx, step)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, delta.Detail)
			if delta.Op == engine.OpBlocked {
				failed++
			} else {
				passed++
			}
		}
	}

	if total == 0 {
		_, _ = fmt.Fprintln(out, "no verify steps")
		return nil
	}
	_, _ = fmt.Fprintf(out, "%d passed, %d failed\n", passed, failed)
	if failed > 0 {
		return fmt.Errorf("%d verify check(s) failed", failed)
	}
	return nil
}
