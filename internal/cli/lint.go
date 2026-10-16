package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/subaud-tools/kempt/internal/manifest"
)

// runLint validates a manifest from a path, an http(s) URL, or "-" (stdin).

func init() {
	Register(Command{
		Name:     "lint",
		Summary:  "validate a kempt.toml",
		Synopsis: "lint [path]",
		Help:     "Validates a kempt.toml (defaults to the current manifest) and reports findings.",
		Run:      runLint,
	})
}

func runLint(args []string, out, errw io.Writer) error {
	path := "kempt.toml"
	if len(args) > 1 {
		return UsageError{Msg: "usage: kempt lint [path|url|-]"}
	}
	if len(args) == 1 {
		path = args[0]
	}
	src, _, name, err := loadManifestSource(path, os.Stdin)
	if err != nil {
		return UsageError{Msg: err.Error()}
	}
	m, findings := manifest.Parse(src)
	if m != nil {
		findings = append(findings, manifest.Validate(m)...)
	}
	for _, f := range findings {
		_, _ = fmt.Fprintf(out, "%s: %s: %s\n", name, f.Path, f.Msg)
	}
	if len(findings) > 0 {
		return fmt.Errorf("%d finding(s)", len(findings))
	}
	return nil
}
