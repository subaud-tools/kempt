package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/gitrepo"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/picker"
	"github.com/subaud-tools/kempt/internal/setup"
	"github.com/subaud-tools/kempt/internal/state"
)

func init() {
	Register(Command{
		Name:     "init",
		Summary:  "fetch a config repo, choose a profile, and apply",
		Synopsis: "init [flags] [repo-url]",
		Help: "Clones (or fetches) a config repo, selects a profile, and applies it.\n" +
			"repo-url may be a git URL or tarball URL; omit it to use the saved config.\n" +
			"Non-interactively (-yes), -profile is required.",
		NewFlags: func() *flag.FlagSet { fs, _ := newInitFlags(); return fs },
		Run:      runInit,
	})
}

// pickerRun is the interactive picker seam. It is a package var so init_test can
// inject a fixed Result without a TTY.
var pickerRun = picker.Run

// initFlags holds the parsed flag values for the init command.
type initFlags struct {
	dir      *string
	profile  *string
	yes      *bool
	manifest *string
	verbose  *bool
}

// newInitFlags builds the init FlagSet and its bound flag values.
func newInitFlags() (*flag.FlagSet, *initFlags) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	v := &initFlags{
		dir:      fs.String("dir", "", "clone target directory (default ~/.config/kempt/repo)"),
		profile:  fs.String("profile", "", "profile to select (non-interactive)"),
		manifest: fs.String("manifest", "", "path to manifest within the repo (test override)"),
	}
	v.yes = YesFlag(fs, "skip the confirmation prompt")
	v.verbose = verboseFlag(fs)
	return fs, v
}

func runInit(args []string, out, errw io.Writer) error {
	fs, v := newInitFlags()
	flagArgs, positional := SplitArgs(fs, args)
	if err := ParseFlags(fs, flagArgs, out); err != nil {
		return err
	}
	if len(positional) > 1 {
		return UsageError{Msg: "unexpected argument: " + positional[1]}
	}
	var url string
	if len(positional) == 1 {
		url = positional[0]
	}

	dir, err := resolveRepoDir(*v.dir)
	if err != nil {
		return err
	}

	ctx, err := newContext(dir)
	if err != nil {
		return err
	}

	// Repo resolution. A tarball URL is fetched + extracted (no git); anything
	// else is a git repo cloned when the dir is not already the right checkout.
	repoKind := "git"
	if isTarballURL(url) {
		repoKind = "tarball"
		if err := fetchTarball(url, dir); err != nil {
			return fmt.Errorf("fetch %s: %w", url, err)
		}
	} else {
		origin, rerr := gitrepo.RemoteURL(ctx.Runner, dir)
		switch {
		case url != "":
			switch {
			case rerr != nil:
				// Not a repo (or absent): clone it.
				if err := gitrepo.Clone(ctx.Runner, url, dir); err != nil {
					return fmt.Errorf("clone %s: %w", url, err)
				}
			case origin == url:
				// Already the right checkout; reuse.
			default:
				return UsageError{Msg: fmt.Sprintf("%s already has origin %q, not %q", dir, origin, url)}
			}
		default:
			if rerr != nil {
				return UsageError{Msg: "provide a repo URL to clone"}
			}
			url = origin
		}
	}

	manifestPath := *v.manifest
	if manifestPath == "" {
		manifestPath = filepath.Join(dir, "kempt.toml")
	}
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

	// Selection.
	var (
		chosenProfile  string
		chosenPackages []string
		selProfile     string
		selPackages    []string
	)
	switch {
	case *v.profile != "":
		pr, ok := m.Profiles[*v.profile]
		if !ok {
			return UsageError{Msg: fmt.Sprintf("unknown profile %q", *v.profile)}
		}
		chosenProfile = *v.profile
		chosenPackages = pr.Packages
		selProfile = *v.profile
	case *v.yes:
		return UsageError{Msg: "-yes requires -profile for non-interactive init"}
	default:
		profiles, items := buildPickerInputs(m)
		res, err := pickerRun(profiles, items)
		if err != nil {
			return err
		}
		if !res.Confirmed {
			_, _ = fmt.Fprintln(out, "init cancelled")
			return nil
		}
		chosenProfile = res.Profile
		chosenPackages = res.Packages
		selPackages = res.Packages
	}

	if err := saveState(&state.State{
		RepoDir:        dir,
		RepoURL:        url,
		RepoKind:       repoKind,
		Profile:        chosenProfile,
		Packages:       chosenPackages,
		AutoApplyFiles: false,
	}); err != nil {
		return err
	}

	selected, err := engine.Select(m, selProfile, selPackages)
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
	engine.Render(plan, out, *v.verbose)
	if err := blockedReleaseSetup(plan, false); err != nil {
		return err
	}

	changes := countChanges(plan)
	if changes == 0 {
		if err := blockedReleaseSetup(plan, true); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "nothing to do")
		return nil
	}

	if !*v.yes {
		_, _ = fmt.Fprintf(out, "apply %d changes? [y/N] ", changes)
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			// proceed
		default:
			_, _ = fmt.Fprintln(out, "aborted")
			return fmt.Errorf("aborted")
		}
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

// resolveRepoDir returns the clone target: the -dir flag, or the default
// ~/.config/kempt/repo, with a leading ~ expanded via os.UserHomeDir.
func resolveRepoDir(flagVal string) (string, error) {
	if flagVal == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "kempt", "repo"), nil
	}
	if flagVal == "~" || strings.HasPrefix(flagVal, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(flagVal, "~"), "/")), nil
	}
	return flagVal, nil
}

// buildPickerInputs projects the manifest into deterministically-sorted picker
// inputs: profiles by name, then all packages as an unchecked checklist.
func buildPickerInputs(m *manifest.Manifest) ([]picker.Profile, []picker.Item) {
	pnames := make([]string, 0, len(m.Profiles))
	for name := range m.Profiles {
		pnames = append(pnames, name)
	}
	sort.Strings(pnames)
	profiles := make([]picker.Profile, 0, len(pnames))
	for _, n := range pnames {
		p := m.Profiles[n]
		profiles = append(profiles, picker.Profile{Name: p.Name, Description: p.Description, Packages: p.Packages})
	}

	inames := make([]string, 0, len(m.Packages))
	for name := range m.Packages {
		inames = append(inames, name)
	}
	sort.Strings(inames)
	items := make([]picker.Item, 0, len(inames))
	for _, n := range inames {
		pk := m.Packages[n]
		items = append(items, picker.Item{Name: pk.Name, Description: pk.Description})
	}
	return profiles, items
}
