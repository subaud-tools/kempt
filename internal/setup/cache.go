package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/subaud-tools/kempt/internal/machine"
	"github.com/subaud-tools/kempt/internal/manifest"
	"github.com/subaud-tools/kempt/internal/state"
	tools "github.com/subaud-tools/tools-common"
)

// A record is published atomically only after both assets have been verified.
// The selector includes the checkout, platform and pin; changing any of them
// cannot silently reuse another selection's candidate.
type record struct {
	Held                                 string
	Ref                                  state.ResolvedRelease
	Bundle, BundleSums, Asset, AssetSums []byte
}

func digest(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func cachePath(ctx *machine.Context, p *manifest.Package, d manifest.DownloadStep) (string, error) {
	s, err := state.DefaultStore()
	if err != nil {
		return "", err
	}
	key, err := json.Marshal([]string{ctx.Home, ctx.RepoDir, p.Root, p.Name, d.Site, d.ProductName(), d.Tool, d.Bin, d.Version, ctx.OS, ctx.Arch})
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, "setup", digest(key)+".json"), nil
}
func readCache(path string) (*record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
func writeCache(path string, r *record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return tools.WriteFileAtomic(path, b, 0600)
}
