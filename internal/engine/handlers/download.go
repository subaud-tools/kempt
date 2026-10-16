package handlers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(downloadHandler{}) }

// downloadHandler installs a single binary from a domain-hosted distribution
// following the kempt download layout:
//
//	pointer: https://<site>/dl/<tool>/latest         -> body is the version
//	asset:   https://<site>/dl/<tool>/<ver>/<tool>_<os>_<arch>.tar.gz
//	sums:    <asset>.sha256
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
	bin := binPath(ctx, st.Bin)

	if _, err := os.Stat(bin); err == nil {
		// Present. For a pinned version, compare the installed binary's
		// reported version against the pin — OFFLINE (a local `<bin> version`
		// probe, no Releases call). "latest" tools stay presence-only so plan
		// never churns when upstream releases.
		if IsPinnedVersion(st.Version) {
			if installed, known := InstalledToolVersion(ctx, st.Bin); known && installed != st.Version {
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

	ver := st.Version
	if !IsPinnedVersion(ver) {
		latest, err := LatestVersion(ctx, st.Site, st.Tool)
		if err != nil {
			return err
		}
		ver = latest
	}

	assetName := st.Tool + "_" + ctx.OS + "_" + ctx.Arch + ".tar.gz"
	assetURL := "https://" + st.Site + "/dl/" + st.Tool + "/" + ver + "/" + assetName
	sumsURL := assetURL + ".sha256"

	assetBytes, err := ctx.Releases.Download(assetURL)
	if err != nil {
		return err
	}
	sumsBytes, err := ctx.Releases.Download(sumsURL)
	if err != nil {
		return err
	}

	return verifyExtractInstall(assetName, assetBytes, sumsBytes, st.Bin, filepath.Dir(binPath(ctx, st.Bin)))
}
