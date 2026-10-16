package cli

import (
	"fmt"
	"io"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/layers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/state"
)

// savedSelection reports whether a command runs on the saved selection, the
// only kind layers apply to: no -manifest, -profile or -packages flag. An
// explicit flag is a one-off run on exactly what it names.
func savedSelection(existed bool, manifestFlag, profileFlag, packagesFlag string) bool {
	return existed && manifestFlag == "" && profileFlag == "" && packagesFlag == ""
}

// selectWithLayers is engine.Select for every command that reads the saved
// selection. With withLayers it loads the machine's layers, reports each one
// skipped (source missing) or held (a project-added layer that changed since it
// was applied) on out, composes the rest with the base, and returns the
// selection in layer order. A layer with findings fails the command, as a base
// manifest with findings does.
func selectWithLayers(ctx *machine.Context, m *manifest.Manifest, profile string, packages []string, st *state.State, withLayers bool, out, errw io.Writer) ([]*manifest.Package, error) {
	if !withLayers || st == nil || len(st.Layers) == 0 {
		return engine.Select(m, profile, packages)
	}
	res := layers.Load(st.Layers, ctx.Home)
	for _, s := range res.Skipped {
		_, _ = fmt.Fprintf(out, "skipped layer %s: %s\n", s.Name, s.Reason)
	}
	for _, h := range res.Held {
		_, _ = fmt.Fprintf(out, "held %s (%s): run kempt layer apply %s\n", h.Name, h.Reason, h.Name)
	}
	if len(res.Findings) > 0 {
		for _, l := range st.Layers {
			for _, f := range res.Findings[l.Name] {
				_, _ = fmt.Fprintf(errw, "layer %s: %s: %s: %s\n", l.Name, layers.FilePath(l, ctx.Home), f.Path, f.Msg)
			}
		}
		return nil, fmt.Errorf("a layer has findings; run kempt lint on it")
	}
	composed, sel, err := layers.Compose(m, packages, res.Loaded)
	if err != nil {
		return nil, UsageError{Msg: err.Error()}
	}
	selected, err := engine.Select(composed, "", sel)
	if err != nil {
		return nil, UsageError{Msg: err.Error()}
	}
	return layers.Order(selected, res.Loaded), nil
}
