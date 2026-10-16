package doctor

import (
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// Run executes every read-only check and returns their combined Report.
func Run(ctx *machine.Context, pkgs []*manifest.Package, cfg manifest.DoctorConfig) Report {
	var f []Finding
	f = append(f, CheckExtraArray(ctx, pkgs)...)
	f = append(f, CheckDupIdentity(ctx, pkgs)...)
	f = append(f, CheckOrphans(ctx, pkgs, cfg)...)
	f = append(f, CheckSymlinks(ctx, pkgs)...)
	f = append(f, CheckRollup(ctx, pkgs)...)
	return Report{Findings: f}
}
