package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/gitrepo"
	"github.com/subaud-tools/kempt/internal/layers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/setup"
	"github.com/subaud-tools/kempt/internal/state"
	"github.com/subaud-tools/kempt/internal/version"
	tools "github.com/subaud-tools/tools-common"
)

// selfUpdate is a seam so tests can stub the binary-replace step (which
// otherwise reaches the network via the /dl download contract).
var selfUpdate = func(app *tools.App, out, errw io.Writer) (bool, string, error) {
	return app.SelfUpdate(out, errw)
}

// updateReexecEnv marks the process update re-execs into after replacing its
// own binary: that process skips the pull and self-update (already done) and
// only rolls and converges.
const updateReexecEnv = "KEMPT_UPDATE_REEXEC"

// reexecUpdate replaces this process with the freshly installed binary running
// the same command, so the roll and converge run on the new code rather than
// the code that was just replaced. On success it does not return. A seam so
// tests can stand in for the exec.
var reexecUpdate = func() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, append(os.Environ(), updateReexecEnv+"=1"))
}

func runUpdate(app *tools.App, args []string, out, errw io.Writer) error {
	fset := flag.NewFlagSet("update", flag.ContinueOnError)
	verbose := verboseFlag(fset)
	if err := ParseFlags(fset, args, out); err != nil {
		return err
	}

	st, existed, err := loadState()
	if err != nil {
		return err
	}
	if !existed {
		return UsageError{Msg: "no saved selection; run kempt init first"}
	}

	ctx, err := newContext(st.RepoDir)
	if err != nil {
		return err
	}

	// Steps 1 and 2 run once: a process update re-exec'd into has already
	// pulled and replaced the binary.
	if os.Getenv(updateReexecEnv) != "1" {
		handedOff, err := refreshAndSelfUpdate(app, ctx, st, out, errw)
		if err != nil || handedOff {
			return err
		}
	}

	// 3. Load and select from the freshly-pulled repo so the roll step and the
	// converge below share one selection.
	manifestPath := filepath.Join(st.RepoDir, "kempt.toml")
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

	selected, err := selectWithLayers(ctx, m, "", st.Packages, st, true, out, errw)
	if err != nil {
		return UsageError{Msg: err.Error()}
	}

	// 4. Resolve and show the combined plan before any selected tool rolls.
	legacySelected := withoutSetup(selected)
	selected, err = setup.Prepare(ctx, selected, true)
	if err != nil {
		return err
	}
	plan, err := engine.BuildPlan(ctx, selected)
	if err != nil {
		return err
	}
	engine.Render(plan, out, *verbose)
	if err := blockedReleaseSetup(plan, false); err != nil {
		return err
	}

	// Legacy rolling sources keep their update behavior, but run only after
	// the release-owned binary and integration changes have been displayed.
	if err := rollRolling(ctx, legacySelected, out); err != nil {
		return err
	}

	applied, failed := executeAndVerify(ctx, plan, out)
	if err := blockedReleaseSetup(plan, true); err != nil {
		return err
	}
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

// isPermissionErr reports whether err is a permission/not-writable error on the
// exe directory, in which case self-update is skipped rather than fatal.
func isPermissionErr(err error) bool {
	return errors.Is(err, fs.ErrPermission)
}

// refreshAndSelfUpdate pulls (or re-fetches) the config tree and self-updates
// the binary. When the binary was replaced it re-execs into the new one and
// reports handedOff; a failed re-exec warns and lets this process carry on.
func refreshAndSelfUpdate(app *tools.App, ctx *machine.Context, st *state.State, out, errw io.Writer) (handedOff bool, err error) {
	// 1. Refresh the config tree. A tarball-sourced config is re-fetched and
	// re-extracted; a git repo is pulled (a real conflict must surface).
	if st.RepoKind == "tarball" {
		if st.RepoURL == "" {
			return false, UsageError{Msg: "tarball-sourced config has no saved URL to re-fetch"}
		}
		if err := fetchTarball(st.RepoURL, st.RepoDir); err != nil {
			return false, fmt.Errorf("re-fetch %s: %w", st.RepoURL, err)
		}
	} else if err := gitrepo.Pull(ctx.Runner, st.RepoDir); err != nil {
		return false, fmt.Errorf("git pull failed: %w", err)
	}
	// Then each git layer. A project checkout is never pulled: it is yours. A
	// layer that fails to pull is converged from what is already on disk.
	for _, l := range st.Layers {
		if l.Source.Kind != "git" || l.Project != "" {
			continue
		}
		if err := gitrepo.Pull(ctx.Runner, layers.ExpandHome(l.Source.Dir, ctx.Home)); err != nil {
			_, _ = fmt.Fprintf(out, "could not pull layer %s: %v\n", l.Name, err)
		}
	}

	// 2. Self-update the binary via the /dl download contract. A non-writable
	// exe dir is a soft failure: we still converge config. Other errors abort.
	// The binary's standing is always reported, so a current binary is
	// distinguishable from a skipped check.
	updated, newVer, uerr := selfUpdate(app, out, errw)
	switch {
	case uerr != nil && isPermissionErr(uerr):
		_, _ = fmt.Fprintf(out, "binary self-update skipped: %v\n", uerr)
	case uerr != nil:
		return false, uerr
	case updated:
		_, _ = fmt.Fprintf(out, "kempt updated %s -> %s\n", version.Number(), newVer)
		if rerr := reexecUpdate(); rerr != nil {
			_, _ = fmt.Fprintf(out, "could not restart into kempt %s (%v); continuing with this binary\n", newVer, rerr)
			return false, nil
		}
		return true, nil
	default:
		_, _ = fmt.Fprintf(out, "kempt %s (current)\n", newVer)
	}
	return false, nil
}
