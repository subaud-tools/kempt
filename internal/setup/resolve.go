// Package setup prepares release-owned declarations before planning. It never
// writes installation targets or executes a release's setup program.
package setup

import (
	"fmt"
	"regexp"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/engine/handlers"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/state"
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// Prepare copies selected packages and adds verified, namespaced fragments.
// Offline misses remain explicit blocked downloads. Online misses are errors:
// an apply must not install half of a selected binary/setup pair.
func Prepare(ctx *machine.Context, selected []*manifest.Package, online bool) ([]*manifest.Package, error) {
	var result []*manifest.Package
	for _, original := range selected {
		p := *original
		p.Steps = append([]manifest.Step(nil), original.Steps...)
		result = append(result, &p)
		if _, skip := engine.OnlySkip(ctx, p.Only); skip {
			continue
		}
		for i, step := range p.Steps {
			d, ok := step.(manifest.DownloadStep)
			if !ok || !d.Setup {
				continue
			}
			if _, skip := engine.OnlySkip(ctx, d.Only); skip {
				continue
			}
			path, err := cachePath(ctx, &p, d)
			if err != nil {
				return nil, err
			}
			var r *record
			if online {
				r, err = resolve(ctx, d)
			} else {
				r, err = readCache(path)
			}
			var fragment *manifest.Package
			if err == nil {
				fragment, err = compose(ctx, &p, d, r)
			}
			if err == nil && online {
				err = writeCache(path, r)
			}
			if err != nil {
				if online {
					return nil, fmt.Errorf("setup for %s is not resolved; run kempt apply: %w", d.Bin, err)
				}
				d.ResolutionError = err.Error()
				p.Steps[i] = d
				continue
			}
			d.Version, d.Resolved = r.Ref.Version, &r.Ref
			d.Held = r.Held
			d.Asset, d.Checksums = r.Asset, r.AssetSums
			p.Steps[i] = d
			if !online {
				for i, step := range fragment.Steps {
					if v, ok := step.(manifest.VerifyStep); ok {
						v.Offline = true
						fragment.Steps[i] = v
					}
				}
			}
			result = append(result, fragment)
		}
	}
	// Callers retain normal engine composition, including conflict reporting.
	return result, nil
}

func resolve(ctx *machine.Context, d manifest.DownloadStep) (*record, error) {
	v := d.Version
	if d.CandidateVersion != "" {
		v = d.CandidateVersion
	}
	held := ""
	if !handlers.IsPinnedVersion(v) {
		var err error
		v, err = handlers.LatestVersion(ctx, d.Site, d.ProductName(), d.Tool)
		if err != nil {
			return nil, err
		}
		if installed, known := handlers.InstalledToolVersion(ctx, d.Bin); known {
			// Rolling applies/updates hold major releases and never downgrade. An
			// explicit upgrade supplies a separate exact candidate instead.
			if handlers.SemverMajorBump(v, installed) {
				held = fmt.Sprintf("held %s %s -> %s (major release): run kempt upgrade %s", d.Bin, installed, v, d.Tool)
			}
			if handlers.SemverMajorBump(v, installed) || !handlers.SemverNewer(v, installed) {
				v = installed
			}
		}
	}
	if !versionPattern.MatchString(v) {
		return nil, fmt.Errorf("invalid release version %q", v)
	}
	r := &record{Held: held, Ref: state.ResolvedRelease{Site: d.Site, Product: d.ProductName(), Tool: d.Tool, Version: v}}
	fetch := func(name string) ([]byte, []byte, error) {
		path := d.Tool + "/" + v + "/" + name
		b, err := release.Fetch(ctx.Releases, d.Site, d.ProductName(), path)
		if err != nil {
			return nil, nil, err
		}
		sums, err := release.Fetch(ctx.Releases, d.Site, d.ProductName(), path+".sha256")
		if err != nil {
			return nil, nil, err
		}
		if err := handlers.VerifyAsset(name, b, sums); err != nil {
			return nil, nil, err
		}
		return b, sums, nil
	}
	var err error
	r.Bundle, r.BundleSums, err = fetch(d.Tool + "_setup.tar.gz")
	if err != nil {
		return nil, err
	}
	r.Ref.BundleDigest = digest(r.Bundle)
	r.Asset, r.AssetSums, err = fetch(d.Tool + "_" + ctx.OS + "_" + ctx.Arch + ".tar.gz")
	if err != nil {
		return nil, err
	}
	return r, nil
}
