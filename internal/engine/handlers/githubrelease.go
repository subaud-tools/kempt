package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/subaud-tools/kempt/internal/engine"
	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
)

func init() { engine.RegisterHandler(githubReleaseHandler{}) }

// githubReleaseHandler installs a single binary from a GitHub release asset,
// verified against the release's checksums.txt and landed atomically at
// <Home>/.local/bin/<Bin> via the shared verifyExtractInstall primitive.
//
// Inspect is strictly offline: it only checks whether the target binary
// exists. Version drift is the verify primitive's concern, keeping plan cheap
// and network-free.
type githubReleaseHandler struct{}

func (githubReleaseHandler) Kind() string { return "github-release" }

func (githubReleaseHandler) Inspect(ctx *machine.Context, s manifest.Step) (engine.Delta, error) {
	st := s.(manifest.GithubReleaseStep)
	bin := binPath(ctx, st.Bin)

	if _, err := os.Stat(bin); err == nil {
		return engine.Delta{Op: engine.OpNoop, Detail: fmt.Sprintf("github-release %s", st.Bin)}, nil
	} else if !os.IsNotExist(err) {
		return engine.Delta{}, err
	}
	return engine.Delta{
		Op:     engine.OpChange,
		Detail: fmt.Sprintf("install %s from %s (latest)", st.Bin, st.Repo),
	}, nil
}

func (githubReleaseHandler) Apply(ctx *machine.Context, s manifest.Step) error {
	st := s.(manifest.GithubReleaseStep)

	tag, err := ctx.Releases.LatestTag(st.Repo)
	if err != nil {
		return err
	}

	asset := st.Asset
	asset = strings.ReplaceAll(asset, "{os}", ctx.OS)
	asset = strings.ReplaceAll(asset, "{arch}", ctx.Arch)
	asset = strings.ReplaceAll(asset, "{tag}", tag)

	baseURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/", st.Repo, tag)
	assetBytes, err := ctx.Releases.Download(baseURL + asset)
	if err != nil {
		return err
	}
	sumBytes, err := ctx.Releases.Download(baseURL + "checksums.txt")
	if err != nil {
		return err
	}

	return verifyExtractInstall(asset, assetBytes, sumBytes, st.Bin, filepath.Dir(binPath(ctx, st.Bin)))
}

// binPath returns <Home>/.local/bin/<bin>.
func binPath(ctx *machine.Context, bin string) string {
	return filepath.Join(ctx.Home, ".local", "bin", bin)
}
