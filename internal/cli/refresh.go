package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/gitrepo"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/setup"
	"github.com/subaud-tools/kempt/internal/state"
)

func init() {
	Register(Command{
		Name:     "refresh",
		Summary:  "check the repo and update the cached status",
		Synopsis: "refresh [flags]",
		Help:     "Re-reads the manifest and refreshes cached package/tool status.",
		NewFlags: func() *flag.FlagSet { fs, _ := newRefreshFlags(); return fs },
		Run:      runRefresh,
	})
}

// now is a seam so tests can pin the CheckedAt timestamp.
var now = time.Now

// refreshFlags holds the parsed flag values for runRefresh.
type refreshFlags struct {
	manifest *string
}

// newRefreshFlags constructs refresh's FlagSet and the values struct it populates
// on Parse. Side-effect-free: safe to call for -h rendering without running
// runRefresh's body.
func newRefreshFlags() (*flag.FlagSet, *refreshFlags) {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	v := &refreshFlags{
		manifest: fs.String("manifest", "", "path to manifest"),
	}
	return fs, v
}

func runRefresh(args []string, out, errw io.Writer) error {
	fs, v := newRefreshFlags()
	if err := ParseFlags(fs, args, out); err != nil {
		return err
	}

	st, existed, err := loadState()
	if err != nil {
		return err
	}
	if !existed {
		return UsageError{Msg: "no saved selection; run kempt init first"}
	}

	manifestPath := resolveManifest(*v.manifest, st, existed)
	ctx, err := newContext(filepath.Dir(manifestPath))
	if err != nil {
		return err
	}

	// Fetch is best-effort: a failure must not blank the cache. Plan against the
	// current checkout and note the degradation in the summary.
	degraded := false
	if err := gitrepo.Fetch(ctx.Runner, st.RepoDir); err != nil {
		degraded = true
	}
	behind, _ := gitrepo.Behind(ctx.Runner, st.RepoDir)

	src, err := os.ReadFile(manifestPath)
	if err != nil {
		return UsageError{Msg: fmt.Sprintf("cannot read %s: %v", manifestPath, err)}
	}
	m, findings := manifest.Parse(src)
	if m != nil {
		findings = append(findings, manifest.Validate(m)...)
	}
	if len(findings) > 0 {
		for _, f := range findings {
			_, _ = fmt.Fprintf(errw, "%s: %s: %s\n", manifestPath, f.Path, f.Msg)
		}
		return fmt.Errorf("manifest has findings; run kempt lint")
	}

	selected, err := selectWithLayers(ctx, m, "", st.Packages, st, savedSelection(existed, *v.manifest, "", ""), out, errw)
	if err != nil {
		return UsageError{Msg: err.Error()}
	}
	selected, err = setup.Prepare(ctx, selected, false)
	if err != nil {
		return err
	}
	plan, err := engine.BuildPlan(ctx, selected)
	if err != nil {
		return err
	}

	fileChanges, softwareChanges, blocked := countByClass(plan)

	// Split policy: with auto-apply-files enabled we apply ONLY files-class
	// changes, never software. After applying, rebuild the plan so the cached
	// counts reflect post-apply reality (files should now be 0, software
	// unchanged and still pending).
	if st.AutoApplyFiles {
		engine.Execute(ctx, engine.FilterByClass(plan, manifest.ClassFiles), out)
		plan, err = engine.BuildPlan(ctx, selected)
		if err != nil {
			return err
		}
		fileChanges, softwareChanges, blocked = countByClass(plan)
	}

	status := &state.Status{
		CheckedAt:       now(),
		Behind:          behind,
		FileChanges:     fileChanges,
		SoftwareChanges: softwareChanges,
		Blocked:         blocked,
	}
	store, err := statusStore()
	if err != nil {
		return err
	}
	if err := store.SaveStatus(status); err != nil {
		return err
	}

	line := formatStatus(status)
	if degraded {
		line += " (fetch failed; using local checkout)"
	}
	_, _ = fmt.Fprintln(out, line)
	return nil
}

// countByClass tallies OpChange steps by class (files vs software) and OpBlocked
// steps across a plan, skipping filtered-out packages.
func countByClass(p *engine.Plan) (files, software, blocked int) {
	for _, pp := range p.Packages {
		if pp.Skipped {
			continue
		}
		for _, sr := range pp.Steps {
			switch sr.Delta.Op {
			case engine.OpChange:
				switch sr.Step.Class() {
				case manifest.ClassFiles:
					files++
				case manifest.ClassSoftware:
					software++
				}
			case engine.OpBlocked:
				blocked++
			}
		}
	}
	return files, software, blocked
}
