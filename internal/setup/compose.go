package setup

import (
	"fmt"

	"github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/tools-common/setupbundle"
)

func compose(ctx *machine.Context, parent *manifest.Package, d manifest.DownloadStep, r *record) (*manifest.Package, error) {
	if r.Ref.Site != d.Site || r.Ref.Product != d.ProductName() || r.Ref.Tool != d.Tool || !versionPattern.MatchString(r.Ref.Version) ||
		(handlers.IsPinnedVersion(d.Version) && r.Ref.Version != d.Version) || r.Ref.BundleDigest != digest(r.Bundle) {
		return nil, fmt.Errorf("cached release identity does not match selection")
	}
	if err := handlers.VerifyAsset(d.Tool+"_setup.tar.gz", r.Bundle, r.BundleSums); err != nil {
		return nil, err
	}
	if err := handlers.VerifyAsset(d.Tool+"_"+ctx.OS+"_"+ctx.Arch+".tar.gz", r.Asset, r.AssetSums); err != nil {
		return nil, err
	}
	b, err := setupbundle.Read(r.Bundle)
	if err != nil {
		return nil, err
	}
	if b.Tool != d.Tool || b.Version != r.Ref.Version {
		return nil, fmt.Errorf("setup metadata does not match %s %s", d.Tool, r.Ref.Version)
	}
	m, findings := manifest.Parse(b.Manifest)
	if m != nil {
		findings = append(findings, manifest.Validate(m)...)
	}
	if len(findings) > 0 {
		return nil, fmt.Errorf("invalid setup manifest: %v", findings)
	}
	if findings = manifest.BindSetup(m, b.Tool, b.Version, b.Files); len(findings) > 0 {
		return nil, fmt.Errorf("invalid setup: %v", findings)
	}
	p := m.Packages[manifest.SetupPackage]
	if p == nil {
		return nil, fmt.Errorf("setup bundle must declare packages.integration")
	}
	p.Name = parent.Name + "/setup/" + d.Bin
	p.Needs = []string{parent.Name}
	p.SetupParent = parent.Name
	p.Root = parent.Root
	// A release is a distinct contributor, so existing composition checks also
	// catch conflicts with base declarations and other release fragments.
	p.Layer = p.Name
	p.Resolved = &r.Ref
	return p, nil
}
