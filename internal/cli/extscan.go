package cli

import (
	"fmt"
	"io"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// scanExtensions walks the rolling npm/pi entries in the selected packages and
// reports each entry's version standing as a toolStatus (Kind "pi"/"git"/"npm",
// Mode "latest"). Latest is resolved live via npm, or git ls-remote for git
// entries (an outdated/upgrade/update-roll path). A resolution failure is reported on that entry (Err set,
// Behind false) rather than aborting the scan, mirroring scanTools.
func scanExtensions(ctx *machine.Context, selected []*manifest.Package) []toolStatus {
	var out []toolStatus
	for _, pkg := range selected {
		for _, step := range pkg.Steps {
			is, ok := step.(manifest.InstallStep)
			if !ok {
				continue
			}
			for _, e := range handlers.RollingExtensions(is) {
				installed, known := handlers.ExtInstalledVersion(ctx, e)
				ts := toolStatus{Kind: e.Backend, Tool: e.Pkg, Ext: e, Mode: "latest", Installed: installed, Known: known}
				latest, err := handlers.ExtLatest(ctx, e)
				if err != nil {
					ts.Err = err
				} else {
					ts.Target = latest
					ts.Behind = known && handlers.ExtBehind(e, ts.Target, installed)
				}
				out = append(out, ts)
			}
		}
	}
	return out
}

// rollRolling resolves and rolls every behind rolling entry (download
// version=latest and unversioned npm/pi) to newest. Used by update between
// self-update and converge so `update` lands the machine on latest for rolling
// entries and the pin for pinned ones. Network path; a per-entry resolution or
// roll failure is a warning, not fatal, so one unreachable registry or site
// never aborts the whole update. A major-version bump is held, never rolled:
// it is named with the `kempt upgrade` that takes it, since a major release
// can break a working setup. Pinned entries are handled by the offline
// converge that follows, not here. It always ends with a one-line summary
// (checked/rolled/skipped/held) so a run with nothing behind is still visible.
func rollRolling(ctx *machine.Context, selected []*manifest.Package, out io.Writer) error {
	statuses := scanTools(ctx, selected)
	statuses = append(statuses, scanExtensions(ctx, selected)...)

	h, _ := engine.HandlerFor("download")
	var checked, rolled, skipped, held int
	for _, s := range statuses {
		if s.Mode != "latest" {
			continue
		}
		checked++
		if s.Err != nil {
			_, _ = fmt.Fprintf(out, "skipping %s: could not resolve latest: %v\n", s.Tool, s.Err)
			skipped++
			continue
		}
		if !s.Behind {
			continue
		}
		if s.majorBump() {
			_, _ = fmt.Fprintf(out, "held %s %s -> %s (major release): run kempt upgrade %s\n", s.Tool, s.Installed, s.Target, s.Tool)
			held++
			continue
		}
		var aerr error
		if s.Kind == "download" {
			aerr = h.Apply(ctx, manifest.DownloadStep{Site: s.Site, Tool: s.Tool, Product: s.Product, Version: s.Target, Bin: s.Bin})
		} else {
			aerr = handlers.RollExtensionTo(ctx, s.Ext, s.Target)
		}
		if aerr != nil {
			_, _ = fmt.Fprintf(out, "skipping %s: %v\n", s.Tool, aerr)
			skipped++
			continue
		}
		_, _ = fmt.Fprintf(out, "rolled %s to %s\n", s.Tool, s.Target)
		rolled++
	}
	_, _ = fmt.Fprintf(out, "rolling: %d checked, %d rolled, %d skipped, %d held\n", checked, rolled, skipped, held)
	return nil
}
