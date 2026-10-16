package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	_ "github.com/subaud-tools/kempt/internal/engine/handlers"
)

func init() {
	Register(Command{
		Name:     "outdated",
		Summary:  "list installed tools and extensions with newer releases",
		Synopsis: "outdated [flags]",
		Help:     "Lists installed tools and extensions that are behind their pinned or latest version. -json for machine output.",
		NewFlags: func() *flag.FlagSet { fs, _ := newOutdatedFlags(); return fs },
		Run:      runOutdated,
	})
}

// outdatedFlags holds the parsed flag values for runOutdated.
type outdatedFlags struct {
	manifest *string
	profile  *string
	packages *string
	json     *bool
}

// newOutdatedFlags constructs outdated's FlagSet and the values struct it populates
// on Parse. Side-effect-free: safe to call for -h rendering without running
// runOutdated's body.
func newOutdatedFlags() (*flag.FlagSet, *outdatedFlags) {
	fs := flag.NewFlagSet("outdated", flag.ContinueOnError)
	v := &outdatedFlags{
		manifest: fs.String("manifest", "", "path to manifest"),
		profile:  fs.String("profile", "", "profile to select"),
		packages: fs.String("packages", "", "comma-separated package names"),
		json:     fs.Bool("json", false, "emit machine-readable JSON"),
	}
	return fs, v
}

func runOutdated(args []string, out, errw io.Writer) error {
	fs, v := newOutdatedFlags()
	if err := ParseFlags(fs, args, out); err != nil {
		return err
	}

	selected, ctx, err := loadSelectedContext(*v.manifest, *v.profile, *v.packages, out, errw)
	if err != nil {
		return err
	}

	statuses := scanTools(ctx, selected)

	statuses = append(statuses, scanExtensions(ctx, selected)...)

	if *v.json {
		return printOutdatedJSON(out, statuses)
	}

	behind := 0
	errored := 0
	for _, s := range statuses {
		switch {
		case s.Err != nil:
			_, _ = fmt.Fprintf(out, "%s  ? (could not resolve latest: %v)\n", s.Tool, s.Err)
			errored++
		case s.Behind:
			note := s.Mode
			if s.majorBump() {
				note += ", major release"
			}
			_, _ = fmt.Fprintf(out, "%s  %s -> %s  (%s)\n", s.Tool, s.Installed, s.Target, note)
			behind++
		}
	}
	if behind == 0 && errored == 0 {
		_, _ = fmt.Fprintln(out, "everything up to date")
	}
	return nil
}

// outdatedJSON is the --json shape for one tool's status.
type outdatedJSON struct {
	Tool      string `json:"tool"`
	Installed string `json:"installed"`
	Target    string `json:"target"`
	Mode      string `json:"mode"`
	Behind    bool   `json:"behind"`
	Error     string `json:"error,omitempty"`
}

func printOutdatedJSON(out io.Writer, statuses []toolStatus) error {
	rows := make([]outdatedJSON, 0, len(statuses))
	for _, s := range statuses {
		row := outdatedJSON{Tool: s.Tool, Installed: s.Installed, Target: s.Target, Mode: s.Mode, Behind: s.Behind}
		if s.Err != nil {
			row.Error = s.Err.Error()
		}
		rows = append(rows, row)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}
