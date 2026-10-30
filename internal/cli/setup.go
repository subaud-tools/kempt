package cli

import (
	"fmt"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

// withoutSetup leaves legacy rolling behavior intact; opted-in downloads
// must run only through the combined binary/setup plan.
func withoutSetup(selected []*manifest.Package) []*manifest.Package {
	var result []*manifest.Package
	for _, pkg := range selected {
		p := *pkg
		p.Steps = nil
		for _, s := range pkg.Steps {
			if d, ok := s.(manifest.DownloadStep); ok && d.Setup {
				continue
			}
			p.Steps = append(p.Steps, s)
		}
		result = append(result, &p)
	}
	return result
}
func unresolvedSetup(selected []*manifest.Package) error {
	for _, p := range selected {
		for _, s := range p.Steps {
			if d, ok := s.(manifest.DownloadStep); ok && d.ResolutionError != "" {
				return fmt.Errorf("setup for %s is not resolved; run kempt apply: %s", d.Bin, d.ResolutionError)
			}
		}
	}
	return nil
}

// A selected release integration that cannot converge is never a successful
// apply, including a plan with no executable changes left.
func blockedReleaseSetup(plan *engine.Plan, afterApply bool) error {
	for _, p := range plan.Packages {
		if p.Skipped {
			continue
		}
		for _, s := range p.Steps {
			if s.Step.Kind() == "verify" && !afterApply {
				continue
			}
			d, isDownload := s.Step.(manifest.DownloadStep)
			if s.Delta.Op == engine.OpBlocked && (p.Resolved != nil || (isDownload && d.Setup)) {
				return fmt.Errorf("release setup incomplete in %s: %s", p.Name, s.Delta.Detail)
			}
		}
	}
	return nil
}
