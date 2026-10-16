package cli

import (
	"flag"
	"io"
	"path/filepath"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/run"
)

func init() {
	Register(Command{
		Name:     "plan",
		Summary:  "show what apply would change",
		Synopsis: "plan [flags]",
		Help:     "Shows what apply would change without changing anything. -os/-arch plan for a different target.",
		NewFlags: func() *flag.FlagSet { fs, _ := newPlanFlags(); return fs },
		Run:      runPlan,
	})
}

// newContext builds a machine.Context for a repo directory. It is a package var
// so tests can inject a Context backed by FakeRunner/FakeReleases.
var newContext = func(repoDir string) (*machine.Context, error) {
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return nil, err
	}
	return machine.New(abs, run.RealRunner{})
}

// planFlags holds the parsed flag values for runPlan.
type planFlags struct {
	manifest *string
	profile  *string
	packages *string
	os       *string
	arch     *string
	verbose  *bool
}

// newPlanFlags constructs plan's FlagSet and the values struct it populates
// on Parse. Side-effect-free: safe to call for -h rendering without running
// runPlan's body.
func newPlanFlags() (*flag.FlagSet, *planFlags) {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	v := &planFlags{
		manifest: fs.String("manifest", "", "path to manifest"),
		profile:  fs.String("profile", "", "profile to select"),
		packages: fs.String("packages", "", "comma-separated package names"),
		os:       fs.String("os", "", "override OS for dry-planning (e.g. linux, darwin)"),
		arch:     fs.String("arch", "", "override Arch for dry-planning (e.g. amd64, arm64)"),
		verbose:  verboseFlag(fs),
	}
	return fs, v
}

func runPlan(args []string, out, errw io.Writer) error {
	fs, v := newPlanFlags()
	if err := ParseFlags(fs, args, out); err != nil {
		return err
	}

	selected, ctx, err := loadSelectedContext(*v.manifest, *v.profile, *v.packages, out, errw)
	if err != nil {
		return err
	}
	if *v.os != "" {
		ctx.OS = *v.os
	}
	if *v.arch != "" {
		ctx.Arch = *v.arch
	}
	plan, err := engine.BuildPlan(ctx, selected)
	if err != nil {
		return err
	}
	engine.Render(plan, out, *v.verbose)
	return nil
}
