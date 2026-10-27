package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() {
	Register(Command{
		Name:     "upgrade",
		Summary:  "upgrade installed tools and extensions to newer releases",
		Synopsis: "upgrade [flags] [tool...]",
		Help: "Upgrades installed tools and extensions that are behind. With no names,\n" +
			"considers all; names limit it to those. Prompts unless -yes.",
		NewFlags: func() *flag.FlagSet { fs, _ := newUpgradeFlags(); return fs },
		Run:      runUpgrade,
	})
}

// upgradeFlags holds the parsed flag values for runUpgrade.
type upgradeFlags struct {
	manifest *string
	profile  *string
	packages *string
	yes      *bool
}

// newUpgradeFlags constructs upgrade's FlagSet and the values struct it populates
// on Parse. Side-effect-free: safe to call for -h rendering without running
// runUpgrade's body.
func newUpgradeFlags() (*flag.FlagSet, *upgradeFlags) {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	v := &upgradeFlags{
		manifest: fs.String("manifest", "", "path to manifest"),
		profile:  fs.String("profile", "", "profile to select"),
		packages: fs.String("packages", "", "comma-separated package names"),
		yes:      YesFlag(fs, "apply without prompting"),
	}
	return fs, v
}

// runUpgrade upgrades installed download tools that are behind their pinned
// or latest-resolved version, reusing the download handler's verified Apply.
// It never edits the manifest.
func runUpgrade(args []string, out, errw io.Writer) error {
	fs, v := newUpgradeFlags()
	if err := ParseFlags(fs, args, out); err != nil {
		return err
	}
	only := map[string]bool{}
	for _, name := range fs.Args() {
		only[name] = true
	}

	selected, ctx, err := loadSelectedContext(*v.manifest, *v.profile, *v.packages, out, errw)
	if err != nil {
		return err
	}
	statuses := scanTools(ctx, selected)

	statuses = append(statuses, scanExtensions(ctx, selected)...)

	var todo []toolStatus
	for _, s := range statuses {
		if len(only) != 0 && !only[s.Tool] {
			continue
		}
		if s.Err != nil {
			_, _ = fmt.Fprintf(out, "skipping %s: could not resolve latest: %v\n", s.Tool, s.Err)
			continue
		}
		if s.Behind {
			todo = append(todo, s)
		}
	}
	if len(todo) == 0 {
		_, _ = fmt.Fprintln(out, "everything up to date")
		return nil
	}
	for _, s := range todo {
		if s.majorBump() {
			_, _ = fmt.Fprintf(out, "%s  %s -> %s  (major release)\n", s.Tool, s.Installed, s.Target)
			continue
		}
		_, _ = fmt.Fprintf(out, "%s  %s -> %s\n", s.Tool, s.Installed, s.Target)
	}
	if !*v.yes {
		_, _ = fmt.Fprint(out, "upgrade these? [y/N] ")
		if !confirm() {
			_, _ = fmt.Fprintln(out, "aborted")
			return nil
		}
	}

	h, _ := engine.HandlerFor("download")
	for _, s := range todo {
		if s.Kind == "download" {
			// Version is the exact resolved target scanTools reported, so a
			// "latest" tool installs the version just resolved (no second
			// pointer read that could race a newer release).
			step := manifest.DownloadStep{Site: s.Site, Tool: s.Tool, Product: s.Product, Version: s.Target, Bin: s.Bin}
			if err := h.Apply(ctx, step); err != nil {
				return fmt.Errorf("upgrade %s: %w", s.Tool, err)
			}
		} else {
			if err := handlers.RollExtensionTo(ctx, s.Ext, s.Target); err != nil {
				return fmt.Errorf("upgrade %s: %w", s.Tool, err)
			}
		}
		_, _ = fmt.Fprintf(out, "upgraded %s to %s\n", s.Tool, s.Target)
	}
	return nil
}
