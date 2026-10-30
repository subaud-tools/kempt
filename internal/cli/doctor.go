package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	tools "github.com/subaud-tools/tools-common"

	"github.com/subaud-tools/kempt/internal/doctor"
	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/setup"
)

func init() {
	Register(Command{
		Name:     "doctor",
		Summary:  "diagnose machine-vs-manifest drift (read-only)",
		Synopsis: "doctor [flags]",
		Help:     "Reports drift the plan/verify commands miss: undeclared live array entries, duplicate package identities, orphaned installs, broken/foreign managed symlinks, and a plan/verify rollup. Read-only. Exits non-zero when findings reach the failing threshold (error by default; warn under -strict).",
		NewFlags: func() *flag.FlagSet { fs, _ := newDoctorFlags(); return fs },
		Run:      runDoctor,
	})
}

// doctorFlags holds the parsed flag values for runDoctor.
type doctorFlags struct {
	manifest *string
	profile  *string
	packages *string
	json     *bool
	strict   *bool
}

// newDoctorFlags constructs doctor's FlagSet and the values struct it populates
// on Parse. Side-effect-free: safe to call for -h rendering without running
// runDoctor's body.
func newDoctorFlags() (*flag.FlagSet, *doctorFlags) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	v := &doctorFlags{
		manifest: fs.String("manifest", "", "path to manifest"),
		profile:  fs.String("profile", "", "profile to select"),
		packages: fs.String("packages", "", "comma-separated package names"),
		json:     fs.Bool("json", false, "emit machine-readable findings"),
		strict:   fs.Bool("strict", false, "treat warnings as failures for the exit code"),
	}
	return fs, v
}

func runDoctor(args []string, out, errw io.Writer) error {
	fs, v := newDoctorFlags()
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
		return UsageError{Msg: "manifest has findings; run kempt lint"}
	}

	ctx, err := newContext(repoDir)
	if err != nil {
		return err
	}
	selected, err := selectWithLayers(ctx, m, profile, packages, st, savedSelection(existed, *v.manifest, *v.profile, *v.packages), out, errw)
	if err != nil {
		return UsageError{Msg: err.Error()}
	}

	selected, err = setup.Prepare(ctx, selected, false)
	if err != nil {
		return err
	}

	var cfg manifest.DoctorConfig
	if m.Doctor != nil {
		cfg = *m.Doctor
	}
	// Doctor inspects steps directly, so it must see them composed: a layer's
	// entry in a folded replace array is declared, not drift.
	selected, err = engine.Compose(ctx, selected)
	if err != nil {
		return err
	}
	report := doctor.Run(ctx, selected, cfg)
	if *v.json {
		if err := report.WriteJSON(out); err != nil {
			return err
		}
	} else {
		report.WriteHuman(out)
	}
	if report.Failed(*v.strict) {
		return tools.Exitf(1, "%d finding(s)", len(report.Findings))
	}
	return nil
}
