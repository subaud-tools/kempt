package machine

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/subaud-tools/kempt/internal/release"
	"github.com/subaud-tools/kempt/internal/run"
)

// Context holds the machine-level state for kempt operations.
type Context struct {
	Home     string
	RepoDir  string
	OS       string // runtime.GOOS at construction; overridable in tests
	Arch     string // runtime.GOARCH at construction; overridable in tests
	UID      int    // os.Getuid() at construction; overridable in tests
	Runner   run.Runner
	Releases release.Releases

	// Cache memoizes read-only inventory command output within one
	// plan/apply run. It is keyed by the FakeRunner key string
	// (strings.TrimSpace(name+" "+strings.Join(args," "))) and holds the
	// command's stdout. Handlers use it so repeated inventory probes
	// (e.g. "brew list --formula -1") run at most once per run.
	Cache map[string]string
}

// New constructs a Context for the given repository directory using the
// provided Runner. Home is resolved from os.UserHomeDir; OS and Arch are
// taken from runtime.GOOS and runtime.GOARCH.
func New(repoDir string, r run.Runner) (*Context, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Context{
		Home:     home,
		RepoDir:  repoDir,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		UID:      os.Getuid(),
		Runner:   r,
		Releases: release.RealReleases{Client: http.DefaultClient, Base: "https://github.com"},
		Cache:    map[string]string{},
	}, nil
}

// Expand resolves a path relative to the Context:
//   - "~"        → Home
//   - "~/x"      → Home/x
//   - absolute   → unchanged
//   - anything else → filepath.Join(RepoDir, p)
func (c *Context) Expand(p string) string {
	if p == "~" {
		return c.Home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(c.Home, p[2:])
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.RepoDir, p)
}
