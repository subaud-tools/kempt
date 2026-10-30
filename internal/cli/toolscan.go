package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// loadSelectedContext reproduces the manifest-read + parse + validate +
// newContext + engine.Select sequence shared by plan, outdated, and upgrade.
func loadSelectedContext(manifestFlag, profileFlag, packagesFlag string, out, errw io.Writer) ([]*manifest.Package, *machine.Context, error) {
	st, existed, err := loadState()
	if err != nil {
		return nil, nil, err
	}
	manifestPath := resolveManifest(manifestFlag, st, existed)
	profile, packages := resolveSelection(profileFlag, splitPackages(packagesFlag), st, existed)

	src, repoDir, name, err := loadManifestSource(manifestPath, os.Stdin)
	if err != nil {
		return nil, nil, UsageError{Msg: err.Error()}
	}
	m, findings := manifest.Parse(src)
	if m != nil {
		findings = append(findings, manifest.Validate(m)...)
	}
	if len(findings) > 0 {
		for _, f := range findings {
			_, _ = fmt.Fprintf(errw, "%s: %s: %s\n", name, f.Path, f.Msg)
		}
		return nil, nil, fmt.Errorf("manifest has findings; run kempt lint")
	}
	ctx, err := newContext(repoDir)
	if err != nil {
		return nil, nil, err
	}
	selected, err := selectWithLayers(ctx, m, profile, packages, st, savedSelection(existed, manifestFlag, profileFlag, packagesFlag), out, errw)
	if err != nil {
		return nil, nil, UsageError{Msg: err.Error()}
	}
	return selected, ctx, nil
}

// toolStatus is one download tool's version standing.
type toolStatus struct {
	Package         string // declaring package, for post-roll verification
	Tool, Bin, Site string
	Product         string // the download step's product; "" → Tool
	// Kind is "download" | "pi" | "npm". Ext carries the extension identity for
	// pi/npm kinds (zero value for download).
	Kind      string
	Ext       handlers.ExtEntry
	Mode      string // "pinned" | "latest"
	Installed string // "" when unknown
	Target    string // pin, or resolved latest; "" when Err is set
	Behind    bool   // see scanTools for the per-mode rule
	Known     bool   // installed version was parseable
	Err       error  // set when a "latest" tool's pointer could not be resolved
}

// majorBump reports whether taking s crosses a major version (see
// handlers.SemverMajorBump): update holds these, and upgrade and outdated
// mark them.
func (s toolStatus) majorBump() bool {
	return s.Behind && handlers.SemverMajorBump(s.Target, s.Installed)
}

// scanTools walks every download step in the selected packages and reports its
// version standing. Pinned tools compare offline; "latest" tools resolve
// /dl/<tool>/latest over the network (this is an outdated/upgrade path, which
// is allowed to). A tool whose installed version can't be parsed is Known=false
// and never Behind.
//
// A network failure resolving a "latest" tool's pointer is reported on that
// tool's status (Err set, Target empty, Behind false) rather than aborting
// the whole scan — one unreachable site must never hide the standing of
// every other tool.
func scanTools(ctx *machine.Context, selected []*manifest.Package) []toolStatus {
	var out []toolStatus
	for _, pkg := range selected {
		for _, step := range pkg.Steps {
			st, ok := step.(manifest.DownloadStep)
			if !ok {
				continue
			}
			if st.Setup {
				if _, skip := engine.OnlySkip(ctx, pkg.Only); skip {
					continue
				}
				if _, skip := engine.OnlySkip(ctx, st.Only); skip {
					continue
				}
			}
			installed, known := handlers.InstalledToolVersion(ctx, st.Bin)
			ts := toolStatus{Package: pkg.Name, Kind: "download", Tool: st.Tool, Product: st.Product, Bin: st.Bin, Site: st.Site, Installed: installed, Known: known}
			if handlers.IsPinnedVersion(st.Version) {
				ts.Mode = "pinned"
				ts.Target = st.Version
				ts.Behind = known && installed != ts.Target
			} else {
				ts.Mode = "latest"
				latest, err := handlers.LatestVersion(ctx, st.Site, st.ProductName(), st.Tool)
				if err != nil {
					ts.Err = err
				} else {
					ts.Target = latest
					ts.Behind = known && handlers.SemverNewer(ts.Target, installed)
				}
			}
			out = append(out, ts)
		}
	}
	return out
}
