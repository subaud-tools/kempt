package handlers

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/release"
)

func init() { engine.RegisterHandler(downloadHandler{}) }

// downloadHandler installs a single binary from a domain-hosted distribution
// following the kempt download layout:
//
//	pointer: https://<site>/dl/<tool>/latest         -> body is the version
//	asset:   https://<site>/dl/<tool>/<ver>/<tool>_<os>_<arch>.tar.gz
//	sums:    <asset>.sha256
//
// On a gated site (release.GatedSites) each of the three is fetched through a
// signed link instead, with the tool as the product.
//
// The asset is verified sha256 FAIL-CLOSED against its .sha256 sidecar and
// landed atomically at <Home>/.local/bin/<Bin> via verifyExtractInstall.
//
// Inspect is strictly offline: it only checks whether the target binary
// exists, making no Releases calls.
type downloadHandler struct{}

func (downloadHandler) Kind() string { return "download" }

func (downloadHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.DownloadStep)
	if st.Setup && st.Resolved == nil {
		return engine.Delta{Op: engine.OpBlocked, Detail: fmt.Sprintf("setup for %s is not resolved; run kempt apply: %s", st.Bin, st.ResolutionError)}, nil
	}
	bin := binPath(ctx, st.Bin)

	if _, err := os.Stat(bin); err == nil {
		// Present. For a pinned version, compare the installed binary's
		// reported version against the pin — OFFLINE (a local `<bin> version`
		// probe, no Releases call). "latest" tools stay presence-only so plan
		// never churns when upstream releases.
		if IsPinnedVersion(st.Version) {
			installed, known := InstalledToolVersion(ctx, st.Bin)
			if st.Setup && !known {
				// A dev/unknown executable is not proof of the cached identity.
				// Exact verified bytes also establish identity after a fresh install
				// when a local version probe is unavailable.
				name := st.Tool + "_" + ctx.OS + "_" + ctx.Arch + ".tar.gz"
				want, err := extractBinary(name, st.Asset, st.Bin)
				if err != nil {
					return engine.Delta{}, err
				}
				got, readErr := os.ReadFile(bin)
				if readErr != nil {
					return engine.Delta{}, readErr
				}
				if !bytes.Equal(want, got) {
					return engine.Delta{Op: engine.OpChange, Detail: fmt.Sprintf("install %s (%s; installed release unknown)", st.Bin, st.Version)}, nil
				}
			}
			if known && installed != st.Version {
				return engine.Delta{
					Op:     engine.OpChange,
					Detail: fmt.Sprintf("upgrade %s %s -> %s", st.Bin, installed, st.Version),
				}, nil
			}
		}
		return engine.Delta{Op: engine.OpNoop, Detail: fmt.Sprintf("download %s", st.Bin)}, nil
	} else if !os.IsNotExist(err) {
		return engine.Delta{}, err
	}
	ver := st.Version
	if ver == "" {
		ver = "latest"
	}
	return engine.Delta{
		Op:     engine.OpChange,
		Detail: fmt.Sprintf("install %s from %s (%s)", st.Bin, st.Site, ver),
	}, nil
}

func (downloadHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.DownloadStep)

	if st.Setup {
		if st.Resolved == nil {
			return fmt.Errorf("setup for %s is not resolved; run kempt apply", st.Bin)
		}
		name := st.Tool + "_" + ctx.OS + "_" + ctx.Arch + ".tar.gz"
		return verifyExtractInstall(name, st.Asset, st.Checksums, st.Bin, filepath.Dir(binPath(ctx, st.Bin)))
	}
	ver := st.Version
	if !IsPinnedVersion(ver) {
		latest, err := LatestVersion(ctx, st.Site, st.ProductName(), st.Tool)
		if err != nil {
			return err
		}
		ver = latest
	}

	assetName := st.Tool + "_" + ctx.OS + "_" + ctx.Arch + ".tar.gz"
	assetPath := st.Tool + "/" + ver + "/" + assetName

	assetBytes, err := release.Fetch(ctx.Releases, st.Site, st.ProductName(), assetPath)
	if err != nil {
		return err
	}
	sumsBytes, err := release.Fetch(ctx.Releases, st.Site, st.ProductName(), assetPath+".sha256")
	if err != nil {
		return err
	}

	return verifyExtractInstall(assetName, assetBytes, sumsBytes, st.Bin, filepath.Dir(binPath(ctx, st.Bin)))
}
